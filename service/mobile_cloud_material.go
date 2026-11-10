package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const mobileCloudMaterialGateway = "/api/openapi-maas"

// The official signer formats Shanghai local time with a literal Z suffix.
var mobileCloudMaterialTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type MobileCloudMaterialRejection struct{ Message string }

func (e *MobileCloudMaterialRejection) Error() string { return e.Message }

type mobileCloudMaterialProxyError struct{ statusCode int }

func (e *mobileCloudMaterialProxyError) Error() string {
	return fmt.Sprintf("proxy connection failed (HTTP %d)", e.statusCode)
}

func mobileCloudMaterialConnectionError(err error, tlsHandshakeFailed bool) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("mobile cloud material request canceled: %w", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("mobile cloud material request timed out: %w", context.DeadlineExceeded)
	}
	var proxyErr *mobileCloudMaterialProxyError
	var opErr *net.OpError
	prefix := "mobile cloud material upstream connection failed"
	if errors.As(err, &proxyErr) {
		return errors.New(prefix + ": " + proxyErr.Error())
	}
	if errors.As(err, &opErr) && opErr.Op == "proxyconnect" {
		prefix += ": proxy connection failed"
	}
	var dnsErr *net.DNSError
	var certificateErr *tls.CertificateVerificationError
	var authorityErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidCertificateErr x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	var alertErr tls.AlertError
	switch {
	case errors.As(err, &dnsErr):
		return errors.New(prefix + ": DNS resolution failed")
	case errors.As(err, &certificateErr), errors.As(err, &authorityErr), errors.As(err, &hostnameErr), errors.As(err, &invalidCertificateErr):
		return errors.New(prefix + ": TLS certificate verification failed")
	case errors.As(err, &recordErr), errors.As(err, &alertErr):
		return errors.New(prefix + ": TLS handshake failed")
	case errors.Is(err, syscall.ECONNRESET), runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(10054)):
		return errors.New(prefix + ": connection reset by peer")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New(prefix + ": connection closed before an HTTP response (EOF)")
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if timeoutErr, ok := cause.(net.Error); ok && timeoutErr.Timeout() {
			return errors.New(prefix + ": connection timed out")
		}
	}
	if tlsHandshakeFailed {
		return errors.New(prefix + ": TLS handshake failed")
	}
	return errors.New(prefix)
}

type MobileCloudMaterialClient struct {
	endpoint   url.URL
	accessKey  string
	secretKey  string
	poolID     string
	httpClient *http.Client
	now        func() time.Time
	nonce      func() string
}

type mobileCloudMaterialTraceKey struct{}

type mobileCloudMaterialTrace struct {
	mu                                                    sync.Mutex
	started                                               time.Time
	credentialInfo                                        string
	diagnosticID                                          string
	proxy, dns, tcp, peerIP, tls, requestWrite            string
	proxyStatus                                           int
	httpsProxy                                            bool
	proxyTLS                                              string
	tlsVersion, proxyTLSVersion                           uint16
	dnsMS, tcpMS, tlsMS, proxyTLSMS, writeMS, firstByteMS int64
}

func (d *mobileCloudMaterialTrace) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.dns = "started"
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.dns, d.dnsMS = "ok", time.Since(d.started).Milliseconds()
			if info.Err != nil {
				d.dns = "error"
			}
		},
		ConnectStart: func(_, _ string) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.tcp == "not_started" {
				d.tcp = "started"
			}
		},
		ConnectDone: func(_, addr string, err error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.tcp == "ok" {
				return
			}
			d.tcp, d.tcpMS = "error", time.Since(d.started).Milliseconds()
			if err == nil {
				d.tcp = "ok"
				if peer, err := netip.ParseAddrPort(addr); err == nil {
					d.peerIP = peer.Addr().WithZone("").String()
				}
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if peer, err := netip.ParseAddrPort(info.Conn.RemoteAddr().String()); err == nil {
				d.peerIP = peer.Addr().WithZone("").String()
			}
		},
		TLSHandshakeStart: func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.httpsProxy && d.proxyStatus == 0 {
				d.proxyTLS = "started"
				return
			}
			d.tls = "started"
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			result := "ok"
			if err != nil {
				result = "error"
			}
			if d.httpsProxy && d.proxyStatus == 0 {
				d.proxyTLS, d.proxyTLSMS, d.proxyTLSVersion = result, time.Since(d.started).Milliseconds(), state.Version
				return
			}
			d.tls, d.tlsMS, d.tlsVersion = result, time.Since(d.started).Milliseconds(), state.Version
		},
		WroteHeaders: func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.requestWrite = "started"
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.requestWrite, d.writeMS = "ok", time.Since(d.started).Milliseconds()
			if info.Err != nil {
				d.requestWrite = "error"
			}
		},
		GotFirstResponseByte: func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.firstByteMS = time.Since(d.started).Milliseconds()
		},
	}
}

func (d *mobileCloudMaterialTrace) log(method, path, outcome string, status int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	route := "unknown"
	for _, prefix := range []string{"/asset-group", "/asset", "/real-person-auth"} {
		if path == prefix || path == prefix+"/query" {
			route = path
			break
		}
		if strings.HasPrefix(path, prefix+"/") {
			route = prefix + "/:id"
			break
		}
	}
	peerRole := "unknown"
	if d.proxy == "direct" {
		peerRole = "upstream"
	} else if d.proxy == "enabled" {
		peerRole = "proxy"
	}
	common.SysLog(fmt.Sprintf("mobile_cloud_material_request diagnostic_id=%s method=%s route=%q outcome=%s proxy=%s proxy_connect_status=%d dns=%s dns_done_after_ms=%d tcp=%s tcp_done_after_ms=%d peer_ip=%s peer_role=%s proxy_tls=%s proxy_tls_version=%s proxy_tls_done_after_ms=%d upstream_tls=%s upstream_tls_version=%s upstream_tls_done_after_ms=%d request_write=%s write_done_after_ms=%d first_byte_after_ms=%d http_status=%d elapsed_ms=%d %s",
		d.diagnosticID, method, route, outcome, d.proxy, d.proxyStatus, d.dns, d.dnsMS, d.tcp, d.tcpMS, d.peerIP, peerRole,
		d.proxyTLS, strings.ReplaceAll(tls.VersionName(d.proxyTLSVersion), " ", ""), d.proxyTLSMS,
		d.tls, strings.ReplaceAll(tls.VersionName(d.tlsVersion), " ", ""), d.tlsMS, d.requestWrite, d.writeMS, d.firstByteMS, status, time.Since(d.started).Milliseconds(), d.credentialInfo))
}

type MobileCloudCreateAssetInput struct {
	GroupID string `json:"groupId"`
	Name    string `json:"assetName"`
	URL     string `json:"assetUrl"`
	Type    string `json:"assetType"`
}

type MobileCloudGroupQuery struct {
	GroupType *string  `json:"groupType,omitempty"`
	Name      *string  `json:"groupName,omitempty"`
	PageNo    *int32   `json:"pageNo,omitempty"`
	PageSize  *int32   `json:"pageSize,omitempty"`
	GroupIDs  []string `json:"groupIds,omitempty"`
}

type MobileCloudGroupUpdate struct {
	Name        *string `json:"groupName,omitempty"`
	Description *string `json:"description,omitempty"`
}

type MobileCloudAssetQuery struct {
	GroupType *string  `json:"groupType,omitempty"`
	PageNo    *int32   `json:"pageNo,omitempty"`
	PageSize  *int32   `json:"pageSize,omitempty"`
	GroupIDs  []string `json:"groupIds,omitempty"`
	Name      *string  `json:"assetName,omitempty"`
	Statuses  []string `json:"statuses,omitempty"`
}

type MobileCloudAssetGroup struct {
	ID          string `json:"groupId"`
	Type        string `json:"groupType"`
	Name        string `json:"groupName"`
	Description string `json:"description"`
	CreatedTime string `json:"createdTime"`
	UpdatedTime string `json:"updatedTime"`
}

type MobileCloudAsset struct {
	ID           string `json:"assetId"`
	GroupID      string `json:"groupId"`
	Name         string `json:"assetName"`
	Type         string `json:"assetType"`
	URL          string `json:"assetUrl"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage"`
	CreatedTime  string `json:"createdTime"`
	UpdatedTime  string `json:"updatedTime"`
}

type MobileCloudMaterialPage[T any] struct {
	Data  []T   `json:"data"`
	Total int64 `json:"total"`
}

type MobileCloudVerificationSession struct {
	BytedToken string `json:"bytedToken"`
	H5Link     string `json:"h5Link"`
	ExpiresIn  int32  `json:"expiresIn"`
}

func NewMobileCloudMaterialClient(endpoint, accessKey, secretKey, poolID string) (*MobileCloudMaterialClient, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" {
		return nil, errors.New("mobile cloud material endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" || strings.TrimSpace(poolID) == "" || strings.ContainsAny(poolID, "\r\n") {
		return nil, errors.New("mobile cloud material credentials and pool are required")
	}
	path := strings.TrimRight(base.Path, "/")
	if path != "" && path != mobileCloudMaterialGateway {
		return nil, errors.New("mobile cloud material endpoint must use the MaaS gateway path")
	}
	base.Path = mobileCloudMaterialGateway
	base.RawPath = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 30 * time.Second
	// Fresh HTTP/1 connections prevent the transport from replaying mutations.
	transport.DisableKeepAlives = true
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	proxy := transport.Proxy
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		var proxyURL *url.URL
		var err error
		if proxy != nil {
			proxyURL, err = proxy(req)
		}
		if trace, ok := req.Context().Value(mobileCloudMaterialTraceKey{}).(*mobileCloudMaterialTrace); ok {
			trace.mu.Lock()
			trace.proxy = "direct"
			if proxyURL != nil {
				trace.proxy = "enabled"
				trace.httpsProxy = proxyURL.Scheme == "https"
			}
			if err != nil {
				trace.proxy = "error"
			}
			trace.mu.Unlock()
		}
		return proxyURL, err
	}
	transport.OnProxyConnectResponse = func(ctx context.Context, proxyURL *url.URL, req *http.Request, resp *http.Response) error {
		if trace, ok := ctx.Value(mobileCloudMaterialTraceKey{}).(*mobileCloudMaterialTrace); ok {
			trace.mu.Lock()
			trace.proxy, trace.proxyStatus = "enabled", resp.StatusCode
			trace.mu.Unlock()
		}
		if resp.StatusCode != http.StatusOK {
			return &mobileCloudMaterialProxyError{statusCode: resp.StatusCode}
		}
		return nil
	}
	return &MobileCloudMaterialClient{
		endpoint:  *base,
		accessKey: accessKey,
		secretKey: secretKey,
		poolID:    poolID,
		httpClient: &http.Client{
			Transport:     transport,
			Timeout:       60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   time.Now,
		nonce: common.GetUUID,
	}, nil
}

func (c *MobileCloudMaterialClient) request(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	if input != nil {
		var err error
		body, err = common.Marshal(input)
		if err != nil {
			return errors.New("mobile cloud material request could not be encoded")
		}
	}
	target := c.endpoint
	target.Path += "/exp/aicc/v2" + path
	query := url.Values{
		"AccessKey":        {c.accessKey},
		"Timestamp":        {c.now().In(mobileCloudMaterialTimeZone).Format("2006-01-02T15:04:05Z")},
		"SignatureMethod":  {"HmacSHA256"},
		"SignatureVersion": {"V2.0"},
		"SignatureNonce":   {c.nonce()},
	}
	canonical := strings.ReplaceAll(query.Encode(), "+", "%20")
	hash := sha256.Sum256([]byte(canonical))
	pathToSign := strings.ReplaceAll(url.QueryEscape(target.Path), "+", "%20")
	stringToSign := method + "\n" + pathToSign + "\n" + hex.EncodeToString(hash[:])
	signer := hmac.New(sha256.New, []byte("BC_SIGNATURE&"+c.secretKey))
	_, _ = signer.Write([]byte(stringToSign))
	target.RawQuery = canonical + "&Signature=" + hex.EncodeToString(signer.Sum(nil))
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("mobile cloud material request could not be created")
	}
	req.Header.Set("Pool-Id", c.poolID)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	maskedAK := "********"
	if len(c.accessKey) > 8 {
		maskedAK += c.accessKey[len(c.accessKey)-4:]
	}
	diagnosticID, _ := ctx.Value(materialDiagnosticIDKey{}).(string)
	if diagnosticID == "" {
		diagnosticID = common.GetUUID()
	}
	trace := &mobileCloudMaterialTrace{
		diagnosticID: diagnosticID,
		started:      time.Now(), proxy: "unknown", dns: "not_started", tcp: "not_started", peerIP: "unknown", tls: "not_started", proxyTLS: "not_started", requestWrite: "not_started",
		dnsMS: -1, tcpMS: -1, tlsMS: -1, proxyTLSMS: -1, writeMS: -1, firstByteMS: -1,
		credentialInfo: fmt.Sprintf("ak_masked=%q ak_bytes=%d sk_bytes=%d ak_trimmed=%t sk_trimmed=%t pool_bytes=%d pool_trimmed=%t signed_at=%s signature_method=HmacSHA256 signature_version=V2.0",
			maskedAK, len(c.accessKey), len(c.secretKey), c.accessKey == strings.TrimSpace(c.accessKey), c.secretKey == strings.TrimSpace(c.secretKey), len(c.poolID), c.poolID == strings.TrimSpace(c.poolID), query.Get("Timestamp")),
	}
	outcome, status := "transport_error", 0
	defer func() { trace.log(method, path, outcome, status) }()
	ctx = context.WithValue(req.Context(), mobileCloudMaterialTraceKey{}, trace)
	req = req.WithContext(httptrace.WithClientTrace(ctx, trace.clientTrace()))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		trace.mu.Lock()
		tlsHandshakeFailed := trace.tls == "error" || trace.proxyTLS == "error"
		trace.mu.Unlock()
		return mobileCloudMaterialConnectionError(err, tlsHandshakeFailed)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	const maxResponseBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		outcome = "response_read_error"
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("mobile cloud material response canceled: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("mobile cloud material response timed out: %w", context.DeadlineExceeded)
		}
		return errors.New("mobile cloud material upstream response could not be read")
	}
	outcome = "invalid_response"
	if len(data) > maxResponseBytes {
		return errors.New("mobile cloud material upstream response exceeds size limit")
	}
	var envelope struct {
		State        string            `json:"state"`
		Body         common.RawMessage `json:"body"`
		ErrorMessage string            `json:"errorMessage"`
	}
	decodeErr := common.Unmarshal(data, &envelope)
	httpFailed := resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices
	if httpFailed || decodeErr == nil && envelope.State != "OK" {
		outcome = "upstream_rejected"
		message := "mobile cloud material upstream rejected the request"
		if httpFailed {
			message = fmt.Sprintf("mobile cloud material upstream HTTP status %d", resp.StatusCode)
		}
		if decodeErr == nil && envelope.State != "OK" && !strings.HasPrefix(path, "/real-person-auth/") && strings.TrimSpace(envelope.ErrorMessage) != "" {
			reason := envelope.ErrorMessage
			for _, secret := range []string{c.accessKey, c.secretKey, c.poolID} {
				if strings.Contains(reason, secret) {
					// Credential echoes may contain other secrets that cannot be identified safely.
					return errors.New(message)
				}
			}
			upstream := types.OpenAIError{Message: reason}
			reason = types.NewError(errors.New(message), types.ErrorCodeBadResponse,
				types.ErrOptionWithUpstreamResponse(upstream)).ToOpenAIRelayError().Message
			reason = strings.Join(strings.Fields(reason), " ")
			message += ": " + string([]rune(reason)[:min(512, utf8.RuneCountInString(reason))])
		}
		if decodeErr == nil && envelope.State == "ERROR" && (resp.StatusCode == http.StatusOK || resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests) {
			return &MobileCloudMaterialRejection{Message: message}
		}
		return errors.New(message)
	}
	if decodeErr != nil {
		return errors.New("mobile cloud material upstream response is invalid JSON")
	}
	if len(envelope.Body) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Body), []byte("null")) {
		return errors.New("mobile cloud material upstream response body is missing")
	}
	if err := common.Unmarshal(envelope.Body, output); err != nil {
		return errors.New("mobile cloud material upstream response body is invalid")
	}
	outcome = "completed"
	return nil
}

func validateMobileCloudMaterialID(id string) error {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#%\r\n\t ") {
		return errors.New("mobile cloud material identifier is invalid")
	}
	return nil
}

func validateMobileCloudMaterialName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 64 {
		return errors.New("mobile cloud material name must contain 1 to 64 characters")
	}
	return nil
}

func validateMobileCloudMaterialQuery(groupType, name *string, pageNo, pageSize *int32) error {
	if groupType != nil && *groupType != "AIGC" && *groupType != "LivenessFace" {
		return errors.New("mobile cloud material group type is invalid")
	}
	if name != nil && utf8.RuneCountInString(*name) > 64 {
		return errors.New("mobile cloud material search name exceeds 64 characters")
	}
	if pageNo == nil || pageSize == nil || *pageNo < 1 || *pageSize < 1 {
		return errors.New("mobile cloud material pagination is required and must be positive")
	}
	return nil
}

func (c *MobileCloudMaterialClient) CreateAssetGroup(ctx context.Context, name string, description *string) (*MobileCloudAssetGroup, error) {
	if err := validateMobileCloudMaterialName(name); err != nil {
		return nil, err
	}
	if description != nil && utf8.RuneCountInString(*description) > 300 {
		return nil, errors.New("mobile cloud material group description exceeds 300 characters")
	}
	input := struct {
		Type        string  `json:"groupType"`
		Name        string  `json:"groupName"`
		Description *string `json:"description,omitempty"`
	}{Type: "AIGC", Name: name, Description: description}
	var group MobileCloudAssetGroup
	if err := c.request(ctx, http.MethodPost, "/asset-group", input, &group); err != nil {
		return nil, err
	}
	if err := validateMobileCloudMaterialID(group.ID); err != nil {
		return nil, err
	}
	return &group, nil
}

func (c *MobileCloudMaterialClient) ListAssetGroups(ctx context.Context, input MobileCloudGroupQuery) (*MobileCloudMaterialPage[MobileCloudAssetGroup], error) {
	if err := validateMobileCloudMaterialQuery(input.GroupType, input.Name, input.PageNo, input.PageSize); err != nil {
		return nil, err
	}
	if *input.PageSize > 100 {
		return nil, errors.New("mobile cloud material group page size must not exceed 100")
	}
	var page struct {
		Data  []MobileCloudAssetGroup `json:"data"`
		Total *int64                  `json:"total"`
	}
	if err := c.request(ctx, http.MethodPost, "/asset-group/query", input, &page); err != nil {
		return nil, err
	}
	if page.Data == nil || page.Total == nil || *page.Total < 0 {
		return nil, errors.New("mobile cloud material group list is invalid")
	}
	return &MobileCloudMaterialPage[MobileCloudAssetGroup]{Data: page.Data, Total: *page.Total}, nil
}

func (c *MobileCloudMaterialClient) GetAssetGroup(ctx context.Context, id string) (*MobileCloudAssetGroup, error) {
	if err := validateMobileCloudMaterialID(id); err != nil {
		return nil, err
	}
	var group MobileCloudAssetGroup
	if err := c.request(ctx, http.MethodGet, "/asset-group/"+id, nil, &group); err != nil {
		return nil, err
	}
	if err := validateMobileCloudMaterialID(group.ID); err != nil {
		return nil, err
	}
	return &group, nil
}

func (c *MobileCloudMaterialClient) UpdateAssetGroup(ctx context.Context, id string, input MobileCloudGroupUpdate) (*MobileCloudAssetGroup, error) {
	if err := validateMobileCloudMaterialID(id); err != nil {
		return nil, err
	}
	if input.Name != nil {
		if err := validateMobileCloudMaterialName(*input.Name); err != nil {
			return nil, err
		}
	}
	if input.Description != nil && utf8.RuneCountInString(*input.Description) > 300 {
		return nil, errors.New("mobile cloud material group description exceeds 300 characters")
	}
	var group MobileCloudAssetGroup
	if err := c.request(ctx, http.MethodPut, "/asset-group/"+id, input, &group); err != nil {
		return nil, err
	}
	if err := validateMobileCloudMaterialID(group.ID); err != nil {
		return nil, err
	}
	return &group, nil
}

func (c *MobileCloudMaterialClient) DeleteAssetGroup(ctx context.Context, id string) (bool, error) {
	if err := validateMobileCloudMaterialID(id); err != nil {
		return false, err
	}
	var deleted bool
	err := c.request(ctx, http.MethodDelete, "/asset-group/"+id, nil, &deleted)
	return deleted, err
}

func (c *MobileCloudMaterialClient) CreateAsset(ctx context.Context, input MobileCloudCreateAssetInput) (string, error) {
	if err := validateMobileCloudMaterialID(input.GroupID); err != nil {
		return "", err
	}
	if err := validateMobileCloudMaterialName(input.Name); err != nil {
		return "", err
	}
	if input.Type != "Image" && input.Type != "Video" && input.Type != "Audio" {
		return "", errors.New("mobile cloud material asset type is invalid")
	}
	source, err := url.Parse(input.URL)
	if err != nil || (source.Scheme != "http" && source.Scheme != "https") || source.Hostname() == "" || source.User != nil || source.Fragment != "" {
		return "", errors.New("mobile cloud material source must be an HTTP or HTTPS URL without credentials or fragment")
	}
	var id string
	if err := c.request(ctx, http.MethodPost, "/asset", input, &id); err != nil {
		return "", err
	}
	if err := validateMobileCloudMaterialID(id); err != nil {
		return "", err
	}
	return id, nil
}

func (c *MobileCloudMaterialClient) ListAssets(ctx context.Context, input MobileCloudAssetQuery) (*MobileCloudMaterialPage[MobileCloudAsset], error) {
	if err := validateMobileCloudMaterialQuery(input.GroupType, input.Name, input.PageNo, input.PageSize); err != nil {
		return nil, err
	}
	if input.GroupType == nil {
		return nil, errors.New("mobile cloud material asset group type is required")
	}
	for _, status := range input.Statuses {
		if !slices.Contains([]string{"PROCESSING", "ACTIVE", "FAILED"}, status) {
			return nil, errors.New("mobile cloud material asset status is invalid")
		}
	}
	var page struct {
		Data  []MobileCloudAsset `json:"data"`
		Total *int64             `json:"total"`
	}
	if err := c.request(ctx, http.MethodPost, "/asset/query", input, &page); err != nil {
		return nil, err
	}
	if page.Data == nil || page.Total == nil || *page.Total < 0 {
		return nil, errors.New("mobile cloud material asset list is invalid")
	}
	return &MobileCloudMaterialPage[MobileCloudAsset]{Data: page.Data, Total: *page.Total}, nil
}

func (c *MobileCloudMaterialClient) GetAsset(ctx context.Context, id string) (*MobileCloudAsset, error) {
	if err := validateMobileCloudMaterialID(id); err != nil {
		return nil, err
	}
	var asset MobileCloudAsset
	if err := c.request(ctx, http.MethodGet, "/asset/"+id, nil, &asset); err != nil {
		return nil, err
	}
	if err := validateMobileCloudMaterialID(asset.ID); err != nil {
		return nil, err
	}
	if asset.ErrorMessage != "" {
		reason := asset.ErrorMessage
		for _, secret := range []string{c.accessKey, c.secretKey, c.poolID} {
			if strings.Contains(reason, secret) {
				reason = "Upstream material processing failed"
				break
			}
		}
		reason = types.NewError(errors.New("Upstream material processing failed"), types.ErrorCodeBadResponse,
			types.ErrOptionWithUpstreamResponse(types.OpenAIError{Message: reason})).ToOpenAIRelayError().Message
		reason = strings.Join(strings.Fields(reason), " ")
		asset.ErrorMessage = string([]rune(reason)[:min(512, utf8.RuneCountInString(reason))])
	}
	return &asset, nil
}

func (c *MobileCloudMaterialClient) UpdateAsset(ctx context.Context, id, name string) (*MobileCloudAsset, error) {
	if err := validateMobileCloudMaterialID(id); err != nil {
		return nil, err
	}
	if err := validateMobileCloudMaterialName(name); err != nil {
		return nil, err
	}
	input := struct {
		Name string `json:"assetName"`
	}{Name: name}
	var asset MobileCloudAsset
	if err := c.request(ctx, http.MethodPut, "/asset/"+id, input, &asset); err != nil {
		return nil, err
	}
	if err := validateMobileCloudMaterialID(asset.ID); err != nil {
		return nil, err
	}
	return &asset, nil
}

func (c *MobileCloudMaterialClient) DeleteAsset(ctx context.Context, id string) (bool, error) {
	if err := validateMobileCloudMaterialID(id); err != nil {
		return false, err
	}
	var deleted bool
	err := c.request(ctx, http.MethodDelete, "/asset/"+id, nil, &deleted)
	return deleted, err
}

func (c *MobileCloudMaterialClient) CreateSession(ctx context.Context, callbackURL *string) (*MobileCloudVerificationSession, error) {
	if callbackURL != nil && *callbackURL != "" {
		callback, err := url.Parse(*callbackURL)
		if err != nil || callback.Scheme != "https" || callback.Hostname() == "" || callback.User != nil || callback.RawQuery != "" || callback.ForceQuery || callback.Fragment != "" {
			return nil, errors.New("mobile cloud material callback must be an HTTPS URL without credentials, query or fragment")
		}
	}
	input := struct {
		CallbackURL *string `json:"callbackUrl,omitempty"`
	}{CallbackURL: callbackURL}
	var session MobileCloudVerificationSession
	if err := c.request(ctx, http.MethodPost, "/real-person-auth/sessions", input, &session); err != nil {
		return nil, err
	}
	h5, err := url.Parse(session.H5Link)
	if strings.TrimSpace(session.BytedToken) == "" || session.ExpiresIn <= 0 || err != nil || h5.Scheme != "https" || h5.Hostname() == "" || h5.User != nil {
		return nil, errors.New("mobile cloud material verification session is invalid")
	}
	return &session, nil
}

func (c *MobileCloudMaterialClient) GetAssetGroupByBytedToken(ctx context.Context, token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", errors.New("mobile cloud material verification token is required")
	}
	input := struct {
		BytedToken string `json:"bytedToken"`
	}{BytedToken: token}
	var id string
	if err := c.request(ctx, http.MethodPost, "/real-person-auth/asset-group/by-byted-token", input, &id); err != nil {
		return "", err
	}
	if err := validateMobileCloudMaterialID(id); err != nil {
		return "", err
	}
	return id, nil
}

func (c *MobileCloudMaterialClient) ConfirmVerificationResult(ctx context.Context, token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", errors.New("mobile cloud material verification token is required")
	}
	input := struct {
		BytedToken string `json:"bytedToken"`
	}{BytedToken: token}
	var result struct {
		GroupID string `json:"groupId"`
	}
	if err := c.request(ctx, http.MethodPost, "/real-person-auth/verification-result", input, &result); err != nil {
		return "", err
	}
	if err := validateMobileCloudMaterialID(result.GroupID); err != nil {
		return "", err
	}
	return result.GroupID, nil
}
