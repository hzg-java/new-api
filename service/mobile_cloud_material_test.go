package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMobileCloudMaterialSignedCreate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/openapi-maas/exp/aicc/v2/asset", r.URL.Path)
		assert.Equal(t, "CIDC-CORE-00", r.Header.Get("Pool-Id"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Empty(t, r.Header.Get("Authorization"))
		query := r.URL.Query()
		assert.Len(t, query, 6)
		assert.Equal(t, "test-ak", query.Get("AccessKey"))
		assert.Equal(t, "HmacSHA256", query.Get("SignatureMethod"))
		assert.Equal(t, "V2.0", query.Get("SignatureVersion"))
		assert.Equal(t, "2026-11-01T10:30:00Z", query.Get("Timestamp"))
		assert.Equal(t, "00112233445566778899aabbccddeeff", query.Get("SignatureNonce"))
		assert.Equal(t, "150b823f1e63d01174326e0ac28444833ada325a6bcbebffe94338d7f7bb80c8", query.Get("Signature"))
		var body map[string]any
		if assert.NoError(t, common.DecodeJson(r.Body, &body)) {
			assert.Equal(t, map[string]any{"groupId": "group-1", "assetName": "portrait", "assetUrl": "https://ecloud.10086.cn/input.png", "assetType": "Image"}, body)
		}
		_, _ = io.WriteString(w, `{"state":"OK","requestId":"req-1","body":"asset-1"}`)
	}))
	defer server.Close()
	client, err := NewMobileCloudMaterialClient(server.URL+"/api/openapi-maas/", "test-ak", "test-sk", "CIDC-CORE-00")
	require.NoError(t, err)
	client.httpClient = server.Client()
	client.now = func() time.Time { return time.Date(2026, 11, 1, 2, 30, 0, 0, time.UTC) }
	client.nonce = func() string { return "00112233445566778899aabbccddeeff" }
	id, err := client.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", URL: "https://ecloud.10086.cn/input.png", Type: "Image"})
	require.NoError(t, err)
	assert.Equal(t, "asset-1", id)
}

func TestMobileCloudMaterialOperations(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, input, response, expected string
		call                                          func(*MobileCloudMaterialClient) (any, error)
	}{
		{"create group", "POST", "/asset-group", `{"groupType":"AIGC","groupName":"portraits","description":""}`, `{"groupId":"group-1","groupType":"AIGC","groupName":"portraits"}`, `{"groupId":"group-1","groupType":"AIGC","groupName":"portraits","description":"","createdTime":"","updatedTime":""}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAssetGroup(context.Background(), "portraits", common.GetPointer(""))
		}},
		{"list groups", "POST", "/asset-group/query", `{"pageNo":1,"pageSize":20,"groupType":"AIGC","groupIds":["group-1"]}`, `{"data":[],"total":0}`, `{"data":[],"total":0}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC"), GroupIDs: []string{"group-1"}})
		}},
		{"omitted optional group filters", "POST", "/asset-group/query", `{"pageNo":1,"pageSize":100}`, `{"data":[],"total":0}`, `{"data":[],"total":0}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(100))})
		}},
		{"asset page size without documented upper limit", "POST", "/asset/query", `{"pageNo":1,"pageSize":101,"groupType":"AIGC"}`, `{"data":[],"total":0}`, `{"data":[],"total":0}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(101)), GroupType: common.GetPointer("AIGC")})
		}},
		{"get group", "GET", "/asset-group/group-1", "", `{"groupId":"group-1","groupType":"AIGC","groupName":"portraits"}`, `{"groupId":"group-1","groupType":"AIGC","groupName":"portraits","description":"","createdTime":"","updatedTime":""}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAssetGroup(context.Background(), "group-1")
		}},
		{"update group", "PUT", "/asset-group/group-1", `{"description":""}`, `{"groupId":"group-1","groupName":"portraits"}`, `{"groupId":"group-1","groupType":"","groupName":"portraits","description":"","createdTime":"","updatedTime":""}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.UpdateAssetGroup(context.Background(), "group-1", MobileCloudGroupUpdate{Description: common.GetPointer("")})
		}},
		{"delete group", "DELETE", "/asset-group/group-1", "", `true`, `true`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.DeleteAssetGroup(context.Background(), "group-1")
		}},
		{"list assets", "POST", "/asset/query", `{"pageNo":2,"pageSize":10,"groupType":"AIGC","groupIds":["group-1"],"statuses":["ACTIVE"]}`, `{"data":[],"total":0}`, `{"data":[],"total":0}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(2)), PageSize: common.GetPointer(int32(10)), GroupType: common.GetPointer("AIGC"), GroupIDs: []string{"group-1"}, Statuses: []string{"ACTIVE"}})
		}},
		{"get asset", "GET", "/asset/asset-1", "", `{"assetId":"asset-1","groupId":"group-1","assetName":"portrait","assetType":"Image","assetUrl":"https://ecloud.10086.cn/signed.png?token=temporary","status":"ACTIVE"}`, `{"assetId":"asset-1","groupId":"group-1","assetName":"portrait","assetType":"Image","assetUrl":"https://ecloud.10086.cn/signed.png?token=temporary","status":"ACTIVE","errorMessage":"","createdTime":"","updatedTime":""}`, func(c *MobileCloudMaterialClient) (any, error) { return c.GetAsset(context.Background(), "asset-1") }},
		{"update asset", "PUT", "/asset/asset-1", `{"assetName":"renamed"}`, `{"assetId":"asset-1","assetName":"renamed","status":"ACTIVE"}`, `{"assetId":"asset-1","groupId":"","assetName":"renamed","assetType":"","assetUrl":"","status":"ACTIVE","errorMessage":"","createdTime":"","updatedTime":""}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.UpdateAsset(context.Background(), "asset-1", "renamed")
		}},
		{"delete asset", "DELETE", "/asset/asset-1", "", `true`, `true`, func(c *MobileCloudMaterialClient) (any, error) { return c.DeleteAsset(context.Background(), "asset-1") }},
		{"default callback session", "POST", "/real-person-auth/sessions", `{}`, `{"bytedToken":"private-token","h5Link":"https://ecloud.10086.cn/verify","expiresIn":120}`, `{"bytedToken":"private-token","h5Link":"https://ecloud.10086.cn/verify","expiresIn":120}`, func(c *MobileCloudMaterialClient) (any, error) { return c.CreateSession(context.Background(), nil) }},
		{"custom callback session", "POST", "/real-person-auth/sessions", `{"callbackUrl":"https://ecloud.10086.cn/callback"}`, `{"bytedToken":"private-token","h5Link":"https://ecloud.10086.cn/verify","expiresIn":90}`, `{"bytedToken":"private-token","h5Link":"https://ecloud.10086.cn/verify","expiresIn":90}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateSession(context.Background(), common.GetPointer("https://ecloud.10086.cn/callback"))
		}},
		{"lookup verification", "POST", "/real-person-auth/asset-group/by-byted-token", `{"bytedToken":"private-token"}`, `"real-group"`, `"real-group"`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAssetGroupByBytedToken(context.Background(), "private-token")
		}},
		{"confirm verification", "POST", "/real-person-auth/verification-result", `{"bytedToken":"private-token"}`, `{"groupId":"real-group"}`, `"real-group"`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ConfirmVerificationResult(context.Background(), "private-token")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.method, r.Method)
				assert.Equal(t, "/api/openapi-maas/exp/aicc/v2"+tc.path, r.URL.Path)
				assert.Equal(t, "pool-1", r.Header.Get("Pool-Id"))
				assert.Equal(t, "HmacSHA256", r.URL.Query().Get("SignatureMethod"))
				assert.Regexp(t, `^[a-f0-9]{32}$`, r.URL.Query().Get("SignatureNonce"))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				if tc.input == "" {
					assert.Empty(t, body)
				} else {
					assert.JSONEq(t, tc.input, string(body))
				}
				_, _ = fmt.Fprintf(w, `{"state":"OK","body":%s}`, tc.response)
			}))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL+"/api/openapi-maas", "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient = server.Client()
			result, err := tc.call(client)
			require.NoError(t, err)
			body, err := common.Marshal(result)
			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(body))
		})
	}
}

func TestMobileCloudMaterialFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"business error on 200", 200, `{"state":"ERROR","errorCode":"InvalidSignature","requestId":"req-1","errorMessage":"test-ak test-sk private-token"}`},
		{"HTTP error despite OK envelope", 403, `{"state":"OK","body":"asset-1","errorMessage":"test-ak test-sk"}`},
		{"HTML error", 502, `<html>test-ak test-sk private-token</html>`},
		{"invalid JSON", 200, `{"state":"OK"`},
		{"missing state", 200, `{"body":"asset-1"}`},
		{"missing body", 200, `{"state":"OK"}`},
		{"null body", 200, `{"state":"OK","body":null}`},
		{"wrong body type", 200, `{"state":"OK","body":{"assetId":"asset-1"}}`},
		{"empty identifier", 200, `{"state":"OK","body":""}`},
		{"trailing response JSON", 200, `{"state":"OK","body":"asset-1"}{"state":"ERROR"}`},
		{"oversized body", 200, `{"state":"OK","body":"asset-1","padding":"` + strings.Repeat("x", 4<<20) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient = server.Client()
			id, err := client.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", URL: "https://ecloud.10086.cn/input.png", Type: "Image"})
			require.Error(t, err)
			assert.Empty(t, id)
			if tc.name == "oversized body" {
				assert.ErrorContains(t, err, "exceeds size limit")
			}
			assert.Equal(t, 1, calls, "mutating requests must not be automatically retried")
			for _, secret := range []string{"test-ak", "test-sk", "private-token", "Signature=", server.URL} {
				assert.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestMobileCloudMaterialConnectionFailures(t *testing.T) {
	const private = "test-ak test-sk test-pool proxy-password https://private.example/path?Signature=private-signature"
	for _, tc := range []struct {
		name, expected string
		cause          error
	}{
		{"DNS", "DNS resolution failed", &net.DNSError{Name: private, Server: private, Err: "no such host"}},
		{"certificate authority", "TLS certificate verification failed", &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{{DNSNames: []string{private}}}, Err: x509.UnknownAuthorityError{}}},
		{"certificate hostname", "TLS certificate verification failed", x509.HostnameError{Certificate: &x509.Certificate{}, Host: private}},
		{"certificate expiry", "TLS certificate verification failed", x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired, Detail: private}},
		{"TLS protocol", "TLS handshake failed", tls.RecordHeaderError{Msg: private}},
		{"TLS alert", "TLS handshake failed", tls.AlertError(40)},
		{"proxy", "proxy connection failed", &net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New(private)}},
		{"proxy DNS", "proxy connection failed: DNS resolution failed", &net.OpError{Op: "proxyconnect", Net: "tcp", Err: &net.DNSError{Name: private, Err: "no such host"}}},
		{"proxy timeout", "proxy connection failed: connection timed out", &net.OpError{Op: "proxyconnect", Net: "tcp", Err: os.ErrDeadlineExceeded}},
		{"connection reset", "connection reset by peer", &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}},
		{"EOF", "connection closed before an HTTP response (EOF)", io.EOF},
		{"unexpected EOF", "connection closed before an HTTP response (EOF)", io.ErrUnexpectedEOF},
		{"transport timeout", "connection timed out", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}},
		{"unknown", "mobile cloud material upstream connection failed", errors.New(private)},
		{"canceled", "mobile cloud material request canceled: context canceled", context.Canceled},
		{"deadline", "mobile cloud material request timed out: context deadline exceeded", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
			require.NoError(t, err)
			calls := 0
			client.httpClient.Transport = mobileCloudMaterialRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				return nil, fmt.Errorf("%s %s: %w", r.URL.String(), private, tc.cause)
			})
			group, err := client.CreateAssetGroup(t.Context(), "group", nil)
			require.Error(t, err)
			assert.Nil(t, group)
			expected := tc.expected
			if !strings.HasPrefix(expected, "mobile cloud material") {
				expected = "mobile cloud material upstream connection failed: " + expected
			}
			assert.EqualError(t, err, expected)
			assert.Equal(t, 1, calls, "failed mutations must not be replayed")
			var rejection *MobileCloudMaterialRejection
			assert.NotErrorAs(t, err, &rejection, "transport failures do not confirm an upstream rejection")
			for _, secret := range []string{"test-ak", "test-sk", "test-pool", "proxy-password", "private.example", "private-signature", "Signature="} {
				assert.NotContains(t, err.Error(), secret)
			}
			if tc.cause == context.Canceled || tc.cause == context.DeadlineExceeded {
				assert.ErrorIs(t, err, tc.cause)
			} else {
				assert.Nil(t, errors.Unwrap(err), "returned errors must not retain credential-bearing causes")
			}
		})
	}
	if runtime.GOOS == "windows" {
		t.Run("Windows connection reset", func(t *testing.T) {
			client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
			require.NoError(t, err)
			client.httpClient.Transport = mobileCloudMaterialRoundTripper(func(*http.Request) (*http.Response, error) {
				return nil, &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("wsarecv", syscall.Errno(10054))}
			})
			_, err = client.GetAsset(t.Context(), "asset-1")
			assert.EqualError(t, err, "mobile cloud material upstream connection failed: connection reset by peer")
		})
	}
}

type mobileCloudMaterialLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *mobileCloudMaterialLogBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *mobileCloudMaterialLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func captureMobileCloudMaterialLogs(t *testing.T) *mobileCloudMaterialLogBuffer {
	t.Helper()
	buffer := &mobileCloudMaterialLogBuffer{}
	common.LogWriterMu.Lock()
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = buffer, buffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	return buffer
}

func TestMobileCloudMaterialStageDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stage    string
		cause    error
		response *http.Response
		expected []string
	}{
		{
			name: "DNS failure", stage: "dns", cause: &net.DNSError{Err: "test-ak Signature=secret-signature", Name: "private.example"},
			expected: []string{"outcome=transport_error", "dns=error", "upstream_tls=not_started", "request_write=not_started", "first_byte_after_ms=-1", "http_status=0"},
		},
		{
			name: "TCP failure", stage: "tcp", cause: &net.OpError{Op: "dial", Err: errors.New("proxy-password private.example")},
			expected: []string{"outcome=transport_error", "dns=ok", "tcp=error", "peer_ip=unknown", "upstream_tls=not_started", "request_write=not_started"},
		},
		{
			name: "TLS EOF", stage: "tls", cause: io.EOF,
			expected: []string{"outcome=transport_error", "dns=ok", "tcp=ok", "peer_ip=192.0.2.10", "upstream_tls=error", "request_write=not_started", "first_byte_after_ms=-1"},
		},
		{
			name: "EOF after request write", stage: "write", cause: io.EOF,
			expected: []string{"outcome=transport_error", "upstream_tls=ok", "upstream_tls_version=TLS1.3", "request_write=ok", "first_byte_after_ms=-1"},
		},
		{
			name: "write failure", stage: "write", cause: errors.New("test-ak test-sk private-token"),
			expected: []string{"outcome=transport_error", "upstream_tls=ok", "request_write=error", "first_byte_after_ms=-1"},
		},
		{
			name: "body failure", stage: "response",
			response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(mobileCloudMaterialReadError{errors.New("test-sk private-token")})},
			expected: []string{"outcome=response_read_error", "upstream_tls=ok", "request_write=ok", "http_status=200"},
		},
		{
			name: "upstream rejection", stage: "response",
			response: &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"state":"ERROR","errorMessage":"test-ak test-sk private-token"}`))},
			expected: []string{"outcome=upstream_rejected", "http_status=403"},
		},
		{
			name: "canceled", cause: context.Canceled,
			expected: []string{"outcome=transport_error", "dns=not_started", "tcp=not_started", "upstream_tls=not_started", "request_write=not_started"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buffer := captureMobileCloudMaterialLogs(t)
			client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
			require.NoError(t, err)
			calls := 0
			client.httpClient.Transport = mobileCloudMaterialRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				trace := httptrace.ContextClientTrace(r.Context())
				require.NotNil(t, trace)
				if trace.DNSStart != nil && tc.stage != "" {
					trace.DNSStart(httptrace.DNSStartInfo{Host: "private.example"})
				}
				if trace.DNSDone != nil && tc.stage != "" {
					var dnsErr error
					if tc.stage == "dns" {
						dnsErr = tc.cause
					}
					trace.DNSDone(httptrace.DNSDoneInfo{Addrs: []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}, Err: dnsErr})
				}
				if tc.stage == "tcp" || tc.stage == "tls" || tc.stage == "write" || tc.stage == "response" {
					if trace.ConnectStart != nil {
						trace.ConnectStart("tcp", "192.0.2.10:443")
					}
					if trace.ConnectDone != nil {
						var connectErr error
						if tc.stage == "tcp" {
							connectErr = tc.cause
						}
						trace.ConnectDone("tcp", "192.0.2.10:443", connectErr)
					}
				}
				if tc.stage == "tls" || tc.stage == "write" || tc.stage == "response" {
					if trace.TLSHandshakeStart != nil {
						trace.TLSHandshakeStart()
					}
					var tlsErr error
					if tc.stage == "tls" {
						tlsErr = tc.cause
					}
					trace.TLSHandshakeDone(tls.ConnectionState{Version: tls.VersionTLS13}, tlsErr)
				}
				if trace.WroteRequest != nil && (tc.stage == "write" || tc.stage == "response") {
					var writeErr error
					if tc.name == "write failure" {
						writeErr = tc.cause
					}
					trace.WroteRequest(httptrace.WroteRequestInfo{Err: writeErr})
				}
				if trace.GotFirstResponseByte != nil && tc.stage == "response" {
					trace.GotFirstResponseByte()
				}
				return tc.response, tc.cause
			})
			_, err = client.CreateAsset(t.Context(), MobileCloudCreateAssetInput{GroupID: "private-group-id", Name: "private-asset-name", URL: "https://source.example/private.png?token=private-token", Type: "Image"})
			require.Error(t, err)
			assert.Equal(t, 1, calls, "diagnostics must not replay requests")
			logs := buffer.String()
			assert.Equal(t, 1, strings.Count(logs, "mobile_cloud_material_request"))
			assert.Regexp(t, `diagnostic_id=[a-f0-9]{32}`, logs)
			assert.Contains(t, logs, `method=POST route="/asset"`)
			assert.Regexp(t, `elapsed_ms=\d+`, logs)
			for _, expected := range tc.expected {
				assert.Contains(t, logs, expected)
			}
			if tc.stage == "response" {
				assert.Regexp(t, `first_byte_after_ms=\d+`, logs)
			}
			for _, private := range []string{"test-ak", "test-sk", "test-pool", "Signature", "secret-signature", "private-token", "private.example", "source.example", "private-group-id", "private-asset-name"} {
				assert.NotContains(t, logs, private)
			}
		})
	}
}

func TestMobileCloudMaterialCredentialDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, accessKey, secretKey, poolID, masked string
		trimmed                                    bool
	}{
		{"configured credentials", "private-access-key-4321", "private-secret-key-8765", "private-pool", "********4321", true},
		{"short credentials", "ak-1", "sk-2", "pl-3", "********", true},
		{"outer whitespace", " private-access-key-4321 ", " private-secret-key-8765 ", " private-pool ", "********321 ", false},
		{"control characters", "private-access-key-\r\n\t", "private-secret-key-\r\n\t", "private-pool", "********-\r\n\t", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buffer := captureMobileCloudMaterialLogs(t)
			t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "private-encryption-master-key")
			client, err := NewMobileCloudMaterialClient("https://private.example", tc.accessKey, tc.secretKey, tc.poolID)
			require.NoError(t, err)
			client.now = func() time.Time { return time.Date(2026, 11, 10, 3, 4, 5, 0, time.UTC) }
			client.httpClient.Transport = mobileCloudMaterialRoundTripper(func(*http.Request) (*http.Response, error) { return nil, io.EOF })
			_, err = client.CreateAssetGroup(t.Context(), "private-group", nil)
			require.Error(t, err)
			logs := buffer.String()
			assert.Contains(t, logs, fmt.Sprintf("ak_masked=%q", tc.masked))
			assert.Contains(t, logs, fmt.Sprintf("ak_bytes=%d sk_bytes=%d", len(tc.accessKey), len(tc.secretKey)))
			assert.Contains(t, logs, fmt.Sprintf("ak_trimmed=%t sk_trimmed=%t", tc.trimmed, tc.trimmed))
			assert.Contains(t, logs, fmt.Sprintf("pool_bytes=%d pool_trimmed=%t", len(tc.poolID), tc.poolID == strings.TrimSpace(tc.poolID)))
			assert.Contains(t, logs, "signed_at=2026-11-10T11:04:05Z signature_method=HmacSHA256 signature_version=V2.0")
			assert.Equal(t, 1, strings.Count(logs, "\n"), "credential fragments must not inject log entries")
			for _, secret := range []string{tc.accessKey, tc.secretKey, tc.poolID, "private-encryption-master-key", "private.example", "private-group", "Signature="} {
				assert.NotContains(t, logs, secret)
			}
		})
	}
}

func TestMobileCloudMaterialStageDiagnosticsSuccess(t *testing.T) {
	buffer := captureMobileCloudMaterialLogs(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"state":"OK","body":{"assetId":"private-asset-id","assetUrl":"https://private.example/result?token=response-secret"}}`)
	}))
	defer server.Close()
	client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "test-pool")
	require.NoError(t, err)
	transport := client.httpClient.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	for range 2 {
		asset, err := client.GetAsset(t.Context(), "private-asset-id")
		require.NoError(t, err)
		assert.Equal(t, "private-asset-id", asset.ID)
	}
	logs := buffer.String()
	assert.Equal(t, 2, strings.Count(logs, "mobile_cloud_material_request"))
	assert.Contains(t, logs, `method=GET route="/asset/:id"`)
	assert.Contains(t, logs, "proxy=direct")
	assert.Contains(t, logs, "tcp=ok")
	assert.Contains(t, logs, "peer_ip=127.0.0.1")
	assert.Contains(t, logs, "upstream_tls=ok")
	assert.Contains(t, logs, "request_write=ok")
	assert.Contains(t, logs, "http_status=200")
	assert.Contains(t, logs, "outcome=completed")
	assert.Regexp(t, `upstream_tls_done_after_ms=\d+`, logs)
	assert.Regexp(t, `write_done_after_ms=\d+`, logs)
	assert.Regexp(t, `first_byte_after_ms=\d+`, logs)
	ids := make(map[string]bool)
	for _, field := range strings.Fields(logs) {
		if strings.HasPrefix(field, "diagnostic_id=") {
			assert.False(t, ids[field], "each request must have a distinct diagnostic ID")
			ids[field] = true
		}
	}
	assert.Len(t, ids, 2)
	for _, private := range []string{"test-ak", "test-sk", "test-pool", "Signature", "private-asset-id", "private.example", "response-secret"} {
		assert.NotContains(t, logs, private)
	}
}

func TestMobileCloudMaterialStageDiagnosticsProxySelection(t *testing.T) {
	for _, tc := range []struct {
		name, expectedProxy, expectedAddress string
		useProxy, proxyError                 bool
	}{
		{name: "direct", expectedProxy: "direct", expectedAddress: "private.example:443"},
		{name: "proxy", expectedProxy: "enabled", expectedAddress: "proxy.example:8080", useProxy: true},
		{name: "proxy selection error", expectedProxy: "error", proxyError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buffer := captureMobileCloudMaterialLogs(t)
			previous := http.DefaultTransport
			base := previous.(*http.Transport).Clone()
			base.Proxy = func(*http.Request) (*url.URL, error) {
				if tc.proxyError {
					return nil, errors.New("proxy-password Signature=proxy-secret")
				}
				if tc.useProxy {
					return &url.URL{Scheme: "http", Host: "proxy.example:8080", User: url.UserPassword("proxy-user", "proxy-password")}, nil
				}
				return nil, nil
			}
			http.DefaultTransport = base
			t.Cleanup(func() { http.DefaultTransport = previous })
			client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
			require.NoError(t, err)
			var dialAddress string
			client.httpClient.Transport.(*http.Transport).DialContext = func(_ context.Context, _, addr string) (net.Conn, error) {
				dialAddress = addr
				return nil, errors.New("test-ak test-sk proxy-password")
			}
			_, err = client.CreateAssetGroup(t.Context(), "private-group-name", nil)
			require.Error(t, err)
			assert.Equal(t, tc.expectedAddress, dialAddress)
			logs := buffer.String()
			assert.Contains(t, logs, "proxy="+tc.expectedProxy+" ")
			for _, private := range []string{"private.example", "proxy.example", "proxy-user", "proxy-password", "proxy-secret", "test-ak", "test-sk", "test-pool", "Signature", "private-group-name"} {
				assert.NotContains(t, logs, private)
			}
		})
	}
}

func TestMobileCloudMaterialStageDiagnosticsHTTPSProxy(t *testing.T) {
	buffer := captureMobileCloudMaterialLogs(t)
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodConnect, r.Method)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("proxy-user", "proxy-password")
	previous := http.DefaultTransport
	base := previous.(*http.Transport).Clone()
	base.Proxy = http.ProxyURL(proxyURL)
	http.DefaultTransport = base
	t.Cleanup(func() { http.DefaultTransport = previous })
	client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
	require.NoError(t, err)
	client.httpClient.Transport.(*http.Transport).TLSClientConfig.RootCAs = proxy.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	_, err = client.CreateAssetGroup(t.Context(), "private-name", nil)
	require.Error(t, err)
	logs := buffer.String()
	assert.Contains(t, logs, "proxy=enabled proxy_connect_status=407")
	assert.Contains(t, logs, "peer_role=proxy")
	assert.Contains(t, logs, "proxy_tls=ok")
	assert.Contains(t, logs, "upstream_tls=not_started")
	assert.Contains(t, logs, "request_write=not_started")
	for _, private := range []string{"proxy-user", "proxy-password", "private.example", "private-name", "test-ak", "test-sk", "test-pool", "Signature"} {
		assert.NotContains(t, logs, private)
	}
}

func TestMobileCloudMaterialStageDiagnosticsLateCallbacks(t *testing.T) {
	buffer := captureMobileCloudMaterialLogs(t)
	client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
	require.NoError(t, err)
	release, finished := make(chan struct{}), make(chan struct{})
	client.httpClient.Transport = mobileCloudMaterialRoundTripper(func(r *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(r.Context())
		trace.DNSStart(httptrace.DNSStartInfo{Host: "private.example"})
		go func() {
			defer close(finished)
			<-release
			trace.DNSDone(httptrace.DNSDoneInfo{Err: errors.New("test-ak test-sk private-token")})
			trace.ConnectDone("tcp", "192.0.2.10:443", nil)
			trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("Signature=private-signature")})
		}()
		return nil, context.Canceled
	})
	_, err = client.GetAsset(t.Context(), "private-asset-id")
	assert.ErrorIs(t, err, context.Canceled)
	before := buffer.String()
	close(release)
	<-finished
	assert.Equal(t, before, buffer.String(), "late callbacks must not emit another or credential-bearing log")
	assert.Equal(t, 1, strings.Count(before, "mobile_cloud_material_request"))
	assert.Contains(t, before, "dns=started")
	assert.Contains(t, before, "dns_done_after_ms=-1")
	for _, private := range []string{"test-ak", "test-sk", "test-pool", "private-token", "Signature", "private-signature", "private.example", "private-asset-id"} {
		assert.NotContains(t, before, private)
	}
}

func TestMobileCloudMaterialTLSHandshakeFailure(t *testing.T) {
	buffer := captureMobileCloudMaterialLogs(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an incompatible TLS handshake must not send the material request")
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "test-pool")
	require.NoError(t, err)
	client.httpClient.Transport.(*http.Transport).TLSClientConfig.MaxVersion = tls.VersionTLS12
	_, err = client.CreateAssetGroup(t.Context(), "group", nil)
	assert.EqualError(t, err, "mobile cloud material upstream connection failed: TLS handshake failed")
	assert.Contains(t, buffer.String(), "upstream_tls=error")
	assert.Contains(t, buffer.String(), "request_write=not_started")
	assert.Contains(t, buffer.String(), "first_byte_after_ms=-1")
}

func TestMobileCloudMaterialProxyRejection(t *testing.T) {
	buffer := captureMobileCloudMaterialLogs(t)
	calls := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, http.MethodConnect, r.Method)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	client, err := NewMobileCloudMaterialClient("https://private.example", "test-ak", "test-sk", "test-pool")
	require.NoError(t, err)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("proxy-user", "proxy-password")
	client.httpClient.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
	_, err = client.CreateAssetGroup(t.Context(), "group", nil)
	assert.EqualError(t, err, "mobile cloud material upstream connection failed: proxy connection failed (HTTP 407)")
	assert.Equal(t, 1, calls)
	assert.NotContains(t, err.Error(), proxy.URL)
	assert.NotContains(t, err.Error(), "proxy-password")
	logs := buffer.String()
	assert.Contains(t, logs, "proxy=enabled proxy_connect_status=407")
	assert.Contains(t, logs, "upstream_tls=not_started")
	assert.Contains(t, logs, "request_write=not_started")
	for _, private := range []string{"proxy-user", "proxy-password", proxy.URL, "test-ak", "test-sk", "test-pool", "Signature", "private.example"} {
		assert.NotContains(t, logs, private)
	}
}

func TestMobileCloudMaterialProxyTunnel(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			buffer := captureMobileCloudMaterialLogs(t)
			upstreamCalls := 0
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls++
				assert.Empty(t, r.Header.Get("Proxy-Authorization"))
				assert.Equal(t, "test-ak", r.URL.Query().Get("AccessKey"))
				_, _ = io.WriteString(w, `{"state":"OK","body":{"groupId":"group-1","groupName":"group","groupType":"AIGC"}}`)
			}))
			upstream.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
			upstream.StartTLS()
			defer upstream.Close()
			proxyCalls := 0
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxyCalls++
				assert.Equal(t, http.MethodConnect, r.Method)
				target, err := net.Dial("tcp", upstream.Listener.Addr().String())
				require.NoError(t, err)
				defer target.Close()
				conn, _, err := w.(http.Hijacker).Hijack()
				require.NoError(t, err)
				defer conn.Close()
				_, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				require.NoError(t, err)
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(target, conn)
					_ = target.Close()
					close(done)
				}()
				_, _ = io.Copy(conn, target)
				_ = conn.Close()
				<-done
			}))
			if scheme == "https" {
				proxy.TLS = &tls.Config{MaxVersion: tls.VersionTLS12}
				proxy.StartTLS()
			} else {
				proxy.Start()
			}
			defer proxy.Close()
			proxyURL, err := url.Parse(proxy.URL)
			require.NoError(t, err)
			proxyURL.User = url.UserPassword("proxy-user", "proxy-password")
			previous := http.DefaultTransport
			base := previous.(*http.Transport).Clone()
			base.Proxy = http.ProxyURL(proxyURL)
			http.DefaultTransport = base
			t.Cleanup(func() { http.DefaultTransport = previous })
			client, err := NewMobileCloudMaterialClient(upstream.URL, "test-ak", "test-sk", "test-pool")
			require.NoError(t, err)
			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			if scheme == "https" {
				roots.AddCert(proxy.Certificate())
			}
			client.httpClient.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
			group, err := client.CreateAssetGroup(t.Context(), "group", nil)
			require.NoError(t, err)
			assert.Equal(t, "group-1", group.ID)
			assert.Equal(t, 1, proxyCalls)
			assert.Equal(t, 1, upstreamCalls)
			logs := buffer.String()
			assert.Contains(t, logs, "proxy=enabled proxy_connect_status=200")
			assert.Contains(t, logs, "peer_role=proxy")
			assert.Contains(t, logs, "upstream_tls=ok")
			assert.Contains(t, logs, "upstream_tls_version=TLS1.3")
			if scheme == "https" {
				assert.Contains(t, logs, "proxy_tls=ok")
				assert.Contains(t, logs, "proxy_tls_version=TLS1.2")
			} else {
				assert.Contains(t, logs, "proxy_tls=not_started")
			}
			assert.Contains(t, logs, "outcome=completed")
			for _, private := range []string{"proxy-user", "proxy-password", "test-ak", "test-sk", "test-pool", "Signature", "group-1"} {
				assert.NotContains(t, logs, private)
			}
		})
	}
}

func TestMobileCloudMaterialFailureReason(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"state":"ERROR","errorCode":"InvalidAsset","errorMessage":"Unsupported image format; https://ecloud.10086.cn/private/image.png?token=private-token; Authorization: Bearer private-token"}`)
			}))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient = server.Client()
			_, err = client.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", URL: "https://ecloud.10086.cn/input.png", Type: "Image"})
			require.Error(t, err)
			assert.ErrorContains(t, err, "Unsupported image format")
			var rejection *MobileCloudMaterialRejection
			assert.ErrorAs(t, err, &rejection)
			for _, secret := range []string{"test-ak", "test-sk", "pool-1", "private-token", "/private/image.png", "ecloud.10086.cn"} {
				assert.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestMobileCloudMaterialProcessingReasonPrivacy(t *testing.T) {
	for _, reason := range []string{
		"Unsupported image format https://private.example/image?token=private-token",
		"test-ak test-sk private-token",
	} {
		t.Run(reason[:min(10, len(reason))], func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := common.Marshal(map[string]any{"state": "OK", "body": MobileCloudAsset{ID: "asset-1", GroupID: "group-1", Type: "Image", Status: "FAILED", ErrorMessage: reason}})
				require.NoError(t, err)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "test-pool")
			require.NoError(t, err)
			client.httpClient = server.Client()
			asset, err := client.GetAsset(context.Background(), "asset-1")
			require.NoError(t, err)
			assert.NotContains(t, asset.ErrorMessage, "private-token")
			assert.NotContains(t, asset.ErrorMessage, "private.example")
			assert.NotContains(t, asset.ErrorMessage, "test-ak")
			assert.NotContains(t, asset.ErrorMessage, "test-sk")
			if strings.HasPrefix(reason, "Unsupported") {
				assert.Contains(t, asset.ErrorMessage, "Unsupported image format")
			}
		})
	}
}

func TestMobileCloudMaterialVerificationErrorsRemainPrivate(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"state":"ERROR","errorMessage":"Invalid bytedToken: private-token"}`)
			}))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient = server.Client()
			_, err = client.GetAssetGroupByBytedToken(context.Background(), "private-token")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
			_, err = client.ConfirmVerificationResult(context.Background(), "private-token")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestMobileCloudMaterialResponseSizeBoundary(t *testing.T) {
	body := `{"state":"OK","body":"asset-1","padding":""}`
	body = strings.Replace(body, `"padding":""`, `"padding":"`+strings.Repeat("x", (4<<20)-len(body))+`"`, 1)
	require.Len(t, body, 4<<20)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
	defer server.Close()
	client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
	require.NoError(t, err)
	client.httpClient = server.Client()
	id, err := client.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", URL: "https://ecloud.10086.cn/input.png", Type: "Image"})
	require.NoError(t, err)
	assert.Equal(t, "asset-1", id)
}

func TestMobileCloudMaterialMutationTransport(t *testing.T) {
	buffer := captureMobileCloudMaterialLogs(t)
	var protocols []string
	var connections []string
	calls := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		protocols = append(protocols, r.Proto)
		connections = append(connections, r.RemoteAddr)
		_, _ = io.Copy(io.Discard, r.Body)
		if calls == 1 {
			_, _ = io.WriteString(w, `{"state":"OK","body":"asset-1"}`)
			return
		}
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if assert.NoError(t, err) {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
	require.NoError(t, err)
	transport := client.httpClient.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	input := MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", URL: "https://ecloud.10086.cn/input.png", Type: "Image"}
	_, err = client.CreateAsset(context.Background(), input)
	require.NoError(t, err)
	_, err = client.CreateAsset(context.Background(), input)
	require.Error(t, err)
	assert.Equal(t, 2, calls)
	require.Len(t, connections, 2)
	assert.NotEqual(t, connections[0], connections[1], "mutations must not reuse a connection that can be replayed")
	assert.Equal(t, []string{"HTTP/1.1", "HTTP/1.1"}, protocols)
	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[1], "outcome=transport_error")
	assert.Contains(t, lines[1], "upstream_tls=ok")
	assert.Contains(t, lines[1], "request_write=ok")
	assert.Contains(t, lines[1], "first_byte_after_ms=-1")
	assert.Contains(t, lines[1], "http_status=0")
}

func TestMobileCloudMaterialMalformedResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		call       func(*MobileCloudMaterialClient) (any, error)
	}{
		{"missing group ID", `{}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAssetGroup(context.Background(), "portraits", nil)
		}},
		{"invalid group result", `true`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAssetGroup(context.Background(), "group-1")
		}},
		{"missing asset ID", `{}`, func(c *MobileCloudMaterialClient) (any, error) { return c.GetAsset(context.Background(), "asset-1") }},
		{"invalid deletion result", `"true"`, func(c *MobileCloudMaterialClient) (any, error) { return c.DeleteAsset(context.Background(), "asset-1") }},
		{"missing list data", `{"total":0}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC")})
		}},
		{"missing list total", `{"data":[]}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20))})
		}},
		{"null list data", `{"data":null,"total":0}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC")})
		}},
		{"negative list total", `{"data":[],"total":-1}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20))})
		}},
		{"missing session token", `{"h5Link":"https://ecloud.10086.cn/verify","expiresIn":120}`, func(c *MobileCloudMaterialClient) (any, error) { return c.CreateSession(context.Background(), nil) }},
		{"unsafe H5 link", `{"bytedToken":"private-token","h5Link":"javascript:alert(1)","expiresIn":120}`, func(c *MobileCloudMaterialClient) (any, error) { return c.CreateSession(context.Background(), nil) }},
		{"missing session expiry", `{"bytedToken":"private-token","h5Link":"https://ecloud.10086.cn/verify"}`, func(c *MobileCloudMaterialClient) (any, error) { return c.CreateSession(context.Background(), nil) }},
		{"missing confirmed group", `{}`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.ConfirmVerificationResult(context.Background(), "private-token")
		}},
		{"empty lookup result", `""`, func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAssetGroupByBytedToken(context.Background(), "private-token")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"state":"OK","body":%s}`, tc.body)
			}))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient = server.Client()
			_, err = tc.call(client)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestMobileCloudMaterialTransportSecurity(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"state":"OK","body":{"assetId":"asset-1"}}`)
	}))
	defer server.Close()
	previous := common.TLSInsecureSkipVerify
	common.TLSInsecureSkipVerify = true
	t.Cleanup(func() { common.TLSInsecureSkipVerify = previous })
	client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
	require.NoError(t, err)
	_, err = client.GetAsset(context.Background(), "asset-1")
	require.Error(t, err, "material credentials must not be sent when the certificate is untrusted")
	assert.EqualError(t, err, "mobile cloud material upstream connection failed: TLS certificate verification failed")
	assert.Zero(t, calls)
	assert.NotContains(t, err.Error(), "test-ak")
	assert.NotContains(t, err.Error(), "Signature=")

	t.Run("redirect is not followed", func(t *testing.T) {
		targetCalls := 0
		target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
		defer target.Close()
		redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
		}))
		defer redirect.Close()
		client, err := NewMobileCloudMaterialClient(redirect.URL, "test-ak", "test-sk", "pool-1")
		require.NoError(t, err)
		client.httpClient.Transport = redirect.Client().Transport
		_, err = client.GetAsset(context.Background(), "asset-1")
		require.Error(t, err)
		assert.Zero(t, targetCalls)
	})

	t.Run("canceled request keeps cancellation without signed URL", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.GetAsset(ctx, "asset-1")
		require.ErrorIs(t, err, context.Canceled)
		assert.NotContains(t, err.Error(), "test-ak")
		assert.False(t, errors.Is(err, context.DeadlineExceeded))
	})

	t.Run("expired deadline before headers", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
		defer cancel()
		_, err := client.GetAsset(ctx, "asset-1")
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotContains(t, err.Error(), "test-ak")
		assert.NotContains(t, err.Error(), "Signature=")
	})

	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run("body read "+cause.Error(), func(t *testing.T) {
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient.Transport = mobileCloudMaterialRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(mobileCloudMaterialReadError{fmt.Errorf("test-ak Signature=private: %w", cause)})}, nil
			})
			_, err = client.GetAsset(context.Background(), "asset-1")
			require.ErrorIs(t, err, cause)
			assert.NotContains(t, err.Error(), "test-ak")
			assert.NotContains(t, err.Error(), "Signature=")
		})
	}
}

type mobileCloudMaterialRoundTripper func(*http.Request) (*http.Response, error)

func (f mobileCloudMaterialRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type mobileCloudMaterialReadError struct{ err error }

func (r mobileCloudMaterialReadError) Read([]byte) (int, error) { return 0, r.err }

func TestMobileCloudMaterialValidation(t *testing.T) {
	for _, credentials := range []struct{ accessKey, secretKey, poolID string }{
		{"", "test-sk", "pool-1"},
		{"test-ak", " ", "pool-1"},
		{"test-ak", "test-sk", ""},
		{"test-ak", "test-sk", "pool-1\r\nInjected: value"},
	} {
		_, err := NewMobileCloudMaterialClient("https://ecloud.10086.cn", credentials.accessKey, credentials.secretKey, credentials.poolID)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "test-sk")
	}
	for _, endpoint := range []string{"http://ecloud.10086.cn", "https://user:password@ecloud.10086.cn", "https://ecloud.10086.cn?token=private", "https://ecloud.10086.cn/#fragment", "https://ecloud.10086.cn?", "https://ecloud.10086.cn/api/openapi-maas/exp/aicc/v2/asset"} {
		_, err := NewMobileCloudMaterialClient(endpoint, "test-ak", "test-sk", "pool-1")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "password")
	}
	for _, tc := range []struct {
		name string
		call func(*MobileCloudMaterialClient) (any, error)
	}{
		{"empty group name", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAssetGroup(context.Background(), " ", nil)
		}},
		{"long group name", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAssetGroup(context.Background(), strings.Repeat("字", 65), nil)
		}},
		{"long group description", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAssetGroup(context.Background(), "portraits", common.GetPointer(strings.Repeat("字", 301)))
		}},
		{"missing asset group", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAsset(context.Background(), MobileCloudCreateAssetInput{Name: "portrait", Type: "Image", URL: "https://ecloud.10086.cn/input.png"})
		}},
		{"invalid asset type", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "g", Name: "portrait", Type: "Document", URL: "https://ecloud.10086.cn/input.png"})
		}},
		{"invalid asset URL", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "g", Name: "portrait", Type: "Image", URL: "file:///private/input.png"})
		}},
		{"identifier traversal", func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAsset(context.Background(), "../asset-group")
		}},
		{"identifier query injection", func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAssetGroup(context.Background(), "g?Signature=injected")
		}},
		{"missing group page", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageSize: common.GetPointer(int32(20))})
		}},
		{"missing group page size", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1))})
		}},
		{"oversized group page", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(101))})
		}},
		{"missing asset group type", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20))})
		}},
		{"missing asset page", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC")})
		}},
		{"missing asset page size", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), GroupType: common.GetPointer("AIGC")})
		}},
		{"invalid group page", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(0)), PageSize: common.GetPointer(int32(20))})
		}},
		{"invalid group type", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("Unknown")})
		}},
		{"invalid asset status", func(c *MobileCloudMaterialClient) (any, error) {
			return c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC"), Statuses: []string{"READY"}})
		}},
		{"insecure callback", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateSession(context.Background(), common.GetPointer("http://ecloud.10086.cn/callback"))
		}},
		{"callback query", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateSession(context.Background(), common.GetPointer("https://ecloud.10086.cn/callback?user=1"))
		}},
		{"callback empty query", func(c *MobileCloudMaterialClient) (any, error) {
			return c.CreateSession(context.Background(), common.GetPointer("https://ecloud.10086.cn/callback?"))
		}},
		{"empty verification token", func(c *MobileCloudMaterialClient) (any, error) {
			return c.GetAssetGroupByBytedToken(context.Background(), "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
			defer server.Close()
			client, err := NewMobileCloudMaterialClient(server.URL, "test-ak", "test-sk", "pool-1")
			require.NoError(t, err)
			client.httpClient = server.Client()
			_, err = tc.call(client)
			require.Error(t, err)
			if tc.name == "invalid group page" {
				assert.ErrorContains(t, err, "pagination")
			}
			if tc.name == "invalid asset status" {
				assert.ErrorContains(t, err, "asset status is invalid")
			}
			assert.Zero(t, calls)
		})
	}
}
