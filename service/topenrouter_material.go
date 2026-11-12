package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const topenRouterMaterialPath = "/v1/api/assets/"

// Also remove malformed URLs that the shared URL parser cannot redact.
var topenRouterMaterialReasonURL = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
var topenRouterMaterialErrorCode = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*(?:\.[A-Za-z0-9_]+)*$`)

// Only an explicit upstream refusal is safe to classify as rejection.
type TopenRouterMaterialRejection struct {
	Message    string
	Code       string
	StatusCode int
	Action     string
	RequestID  string
}

func (e *TopenRouterMaterialRejection) Error() string { return e.Message }

type topenRouterMaterialRetryableError struct{ error }

func (e *topenRouterMaterialRetryableError) Unwrap() error { return e.error }

type topenRouterMaterialAttemptsError struct {
	error
	attempts int
}

func (e *topenRouterMaterialAttemptsError) Error() string {
	return fmt.Sprintf("%s; attempts=%d", e.error, e.attempts)
}

func (e *topenRouterMaterialAttemptsError) Unwrap() error { return e.error }

func topenRouterMaterialRetryable(status int, code, reason string) bool {
	if slices.Contains([]int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}, status) {
		return true
	}
	code = strings.ToLower(code)
	if slices.Contains([]string{"internalerror", "unavailable", "throttling"}, code) {
		return true
	}
	reason = strings.ToLower(reason)
	return slices.ContainsFunc([]string{"internal", "eof", "connection reset", "connection", "network", "timeout", "timed out", "deadline", "rate limit", "too many requests", "throttl", "unavailable"}, func(fragment string) bool {
		return strings.Contains(reason, fragment)
	})
}

type TopenRouterMaterialClient struct {
	endpoint   url.URL
	token      string
	httpClient *http.Client
	retryWait  func(context.Context, time.Duration) error
}

var _ MaterialManagementProvider = (*TopenRouterMaterialClient)(nil)

type topenRouterMaterialID struct {
	ID string `json:"Id"`
}

type topenRouterMaterialPageQuery struct {
	Page     int32    `json:"Page"`
	PageSize int32    `json:"PageSize"`
	GroupIDs []string `json:"GroupIds,omitempty"`
}

type topenRouterMaterialPage[T any] struct {
	Items      []T    `json:"Items"`
	TotalCount *int64 `json:"TotalCount"`
}

type topenRouterMaterialGroup struct {
	ID          string  `json:"Id"`
	Name        string  `json:"Name"`
	Type        string  `json:"GroupType"`
	Description *string `json:"Description"`
	CreateTime  string  `json:"CreateTime"`
	UpdateTime  string  `json:"UpdateTime"`
}

func (g topenRouterMaterialGroup) materialGroup() MobileCloudAssetGroup {
	group := MobileCloudAssetGroup{ID: g.ID, Name: g.Name, Type: g.Type, CreatedTime: g.CreateTime, UpdatedTime: g.UpdateTime}
	if g.Description != nil {
		group.Description = *g.Description
	}
	return group
}

type topenRouterMaterialAsset struct {
	ID           string `json:"Id"`
	GroupID      string `json:"GroupId"`
	Name         string `json:"Name"`
	Type         string `json:"AssetType"`
	URL          string `json:"URL"`
	Status       string `json:"Status"`
	ErrorMessage string `json:"ErrorMessage"`
	Error        *struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	} `json:"Error"`
	CreateTime string `json:"CreateTime"`
	UpdateTime string `json:"UpdateTime"`
}

func (c *TopenRouterMaterialClient) materialAsset(a topenRouterMaterialAsset) MobileCloudAsset {
	asset := MobileCloudAsset{
		ID: a.ID, GroupID: a.GroupID, Name: a.Name, Type: a.Type, URL: a.URL,
		Status: strings.ToUpper(a.Status), CreatedTime: a.CreateTime, UpdatedTime: a.UpdateTime,
	}
	reason := a.ErrorMessage
	if a.Error != nil {
		code, message := strings.TrimSpace(a.Error.Code), strings.TrimSpace(a.Error.Message)
		switch {
		case code != "" && message != "":
			reason = code + ": " + message
		case code != "":
			reason = code
		case message != "":
			reason = message
		}
	}
	if reason != "" {
		asset.ErrorMessage = c.safeReason(reason)
	}
	return asset
}

func waitTopenRouterMaterialRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func NewTopenRouterMaterialClient(endpoint, token string) (*TopenRouterMaterialClient, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.Opaque != "" || base.RawPath != "" || strings.ContainsAny(endpoint, "?#") {
		return nil, errors.New("topenrouter material endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	if strings.HasSuffix(base.Host, ":") {
		return nil, errors.New("topenrouter material endpoint port is invalid")
	}
	if port := base.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("topenrouter material endpoint port is invalid")
		}
	}
	if !slices.Contains([]string{"", "/", "/v1", "/v1/", "/v1/api/assets", topenRouterMaterialPath}, base.Path) {
		return nil, errors.New("topenrouter material endpoint must use the origin, /v1 or assets path")
	}
	if token == "" || !utf8.ValidString(token) || strings.ContainsFunc(token, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return nil, errors.New("topenrouter material token is required and must not contain whitespace or control characters")
	}
	base.Path = topenRouterMaterialPath
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 30 * time.Second
	// Fresh HTTP/1 connections and non-rewindable bodies prevent mutation replays.
	transport.DisableKeepAlives = true
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	return &TopenRouterMaterialClient{
		endpoint: *base, token: token,
		httpClient: &http.Client{
			Transport: transport, Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		retryWait: waitTopenRouterMaterialRetry,
	}, nil
}

func (c *TopenRouterMaterialClient) safeReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	reason = strings.ReplaceAll(reason, c.token, "***")
	reason = types.NewError(errors.New("Material request failed"), types.ErrorCodeBadResponse,
		types.ErrOptionWithUpstreamResponse(types.OpenAIError{Message: reason})).ToOpenAIRelayError().Message
	reason = topenRouterMaterialReasonURL.ReplaceAllString(reason, "***")
	reason = strings.Join(strings.Fields(reason), " ")
	return string([]rune(reason)[:min(512, utf8.RuneCountInString(reason))])
}

func (c *TopenRouterMaterialClient) request(ctx context.Context, action string, input, output any) error {
	body, err := common.Marshal(input)
	if err != nil {
		return errors.New("topenrouter material request could not be encoded")
	}
	target := c.endpoint
	target.RawQuery = url.Values{"Action": {action}, "Version": {"2024-01-01"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return errors.New("topenrouter material request could not be created")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	message := "topenrouter material upstream action=" + c.safeReason(action)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// url.Error includes the endpoint; retain its cause, never its URL.
		var urlErr *url.Error
		cause := err
		if errors.As(err, &urlErr) {
			cause = urlErr.Err
		}
		message += ": connection failed: " + c.safeReason(cause.Error()) + "; result is unknown"
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("%s: %w", message, context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%s: %w", message, context.DeadlineExceeded)
		}
		return &topenRouterMaterialRetryableError{error: errors.New(message)}
	}
	defer resp.Body.Close()
	message += fmt.Sprintf(" HTTP status %d", resp.StatusCode)
	requestID := c.safeReason(resp.Header.Get("X-Request-Id"))
	if requestID != "" {
		message += " requestID=" + requestID
	}
	const maxResponseBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		message += ": response could not be read: " + c.safeReason(err.Error()) + "; result is unknown"
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("%s: %w", message, context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%s: %w", message, context.DeadlineExceeded)
		}
		return &topenRouterMaterialRetryableError{error: errors.New(message)}
	}
	if len(data) > maxResponseBytes {
		return errors.New(message + ": response exceeds size limit; result is unknown")
	}
	var envelope struct {
		ResponseMetadata struct {
			RequestID string `json:"RequestId"`
			Error     *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result  common.RawMessage `json:"Result"`
		Code    *int              `json:"code"`
		Message string            `json:"message"`
	}
	httpFailed := resp.StatusCode < 200 || resp.StatusCode >= 300
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || common.Unmarshal(data, &envelope) != nil {
		unknown := errors.New(message + ": invalid JSON response; result is unknown")
		if httpFailed && topenRouterMaterialRetryable(resp.StatusCode, "", "") {
			return &topenRouterMaterialRetryableError{error: unknown}
		}
		return unknown
	}
	if envelope.ResponseMetadata.RequestID != "" {
		requestID = c.safeReason(envelope.ResponseMetadata.RequestID)
		message += " requestID=" + requestID
	}
	official := envelope.ResponseMetadata.Error
	platformError := envelope.Code != nil && *envelope.Code == 1
	if httpFailed || official != nil || envelope.Code != nil {
		code, reason := "", envelope.Message
		rejected := false
		if official != nil {
			code, reason = official.Code, official.Message
			baseCode, _, _ := strings.Cut(code, ".")
			// Official codes take precedence over HTTP and platform envelopes.
			rejected = slices.Contains([]string{"InvalidParameter", "InvalidParameterValue", "MissingParameter", "NotFound", "Unauthorized", "AccessDenied", "Forbidden", "AuthenticationFailed", "InvalidAction", "InvalidVersion"}, baseCode)
		} else {
			if envelope.Code != nil {
				code = strconv.Itoa(*envelope.Code)
			}
			lowerReason := strings.ToLower(reason)
			// Code 1 alone is not evidence: gateways use it for transport failures too.
			transient := slices.ContainsFunc([]string{"internal", "eof", "timeout", "timed out", "deadline", "connection", "connect", "network", "tls", "rate limit", "too many requests", "throttl", "unavailable"}, func(fragment string) bool {
				return strings.Contains(lowerReason, fragment)
			})
			validation := slices.ContainsFunc([]string{"validation failed", "validation error", "invalid parameter", "invalid url", "invalid image", "invalid asset", "invalid group", "unsupported image", "unsupported asset", "url not allowed", "missing parameter", "required parameter", "is required"}, func(fragment string) bool {
				return strings.Contains(lowerReason, fragment)
			})
			rejected = !transient && (resp.StatusCode == http.StatusUnauthorized ||
				resp.StatusCode == http.StatusForbidden && strings.Contains(lowerReason, "not configured") ||
				platformError && validation)
		}
		// A timeout, throttle, server failure or redirect never proves refusal.
		rejected = rejected && (resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode >= 400 && resp.StatusCode < 500) &&
			resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests
		// The shared reason sanitizer mistakes dotted official codes for domains.
		// Preserve only code-shaped metadata and still redact credentials in it.
		if official != nil && topenRouterMaterialErrorCode.MatchString(code) {
			codeParts := strings.Split(strings.ReplaceAll(code, c.token, "***"), ".")
			for i := range codeParts {
				codeParts[i] = c.safeReason(codeParts[i])
			}
			code = strings.Join(codeParts, ".")
			code = string([]rune(code)[:min(512, utf8.RuneCountInString(code))])
		} else {
			code = c.safeReason(code)
		}
		if code != "" {
			message += " code=" + code
		}
		if strings.TrimSpace(reason) != "" {
			message += ": " + c.safeReason(reason)
		}
		if rejected {
			return &TopenRouterMaterialRejection{Message: message, Code: code, StatusCode: resp.StatusCode, Action: c.safeReason(action), RequestID: requestID}
		}
		unknown := errors.New(message + "; result is unknown")
		if topenRouterMaterialRetryable(resp.StatusCode, code, reason) {
			return &topenRouterMaterialRetryableError{error: unknown}
		}
		return unknown
	}
	result := bytes.TrimSpace(envelope.Result)
	if len(result) == 0 || result[0] != '{' || common.Unmarshal(result, output) != nil {
		return errors.New(message + ": result is missing or invalid; result is unknown")
	}
	return nil
}

func validateTopenRouterMaterialID(id string) error {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#%") || strings.ContainsFunc(id, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return errors.New("topenrouter material identifier is invalid")
	}
	return nil
}

func validateTopenRouterMaterialName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 64 {
		return errors.New("topenrouter material name must contain 1 to 64 characters")
	}
	return nil
}

func topenRouterMaterialPagination(page, size *int32) (topenRouterMaterialPageQuery, error) {
	query := topenRouterMaterialPageQuery{Page: 1, PageSize: 20}
	if page != nil {
		query.Page = *page
	}
	if size != nil {
		query.PageSize = *size
	}
	if query.Page < 1 || query.PageSize < 1 || query.PageSize > 200 {
		return query, errors.New("topenrouter material page must be positive and page size must be between 1 and 200")
	}
	return query, nil
}

func (c *TopenRouterMaterialClient) CreateAssetGroup(ctx context.Context, name string, description *string) (*MobileCloudAssetGroup, error) {
	if err := validateTopenRouterMaterialName(name); err != nil {
		return nil, err
	}
	if description != nil && utf8.RuneCountInString(*description) > 300 {
		return nil, errors.New("topenrouter material group description exceeds 300 characters")
	}
	input := struct {
		Name        string  `json:"Name"`
		GroupType   string  `json:"GroupType"`
		Description *string `json:"Description,omitempty"`
	}{Name: name, GroupType: "AIGC", Description: description}
	var result topenRouterMaterialGroup
	if err := c.request(ctx, "CreateAssetGroup", input, &result); err != nil {
		return nil, err
	}
	if err := validateTopenRouterMaterialID(result.ID); err != nil {
		return nil, err
	}
	group := result.materialGroup()
	return &group, nil
}

func (c *TopenRouterMaterialClient) ListAssetGroups(ctx context.Context, input MobileCloudGroupQuery) (*MobileCloudMaterialPage[MobileCloudAssetGroup], error) {
	query, err := topenRouterMaterialPagination(input.PageNo, input.PageSize)
	if err != nil {
		return nil, err
	}
	// Unsupported filters must not change page totals or hide recovery candidates.
	var result topenRouterMaterialPage[topenRouterMaterialGroup]
	if err := c.request(ctx, "ListAssetGroups", query, &result); err != nil {
		return nil, err
	}
	if result.Items == nil || result.TotalCount == nil || *result.TotalCount < 0 {
		return nil, errors.New("topenrouter material group list is invalid")
	}
	page := &MobileCloudMaterialPage[MobileCloudAssetGroup]{Data: make([]MobileCloudAssetGroup, 0, len(result.Items)), Total: *result.TotalCount}
	for _, item := range result.Items {
		if err := validateTopenRouterMaterialID(item.ID); err != nil {
			return nil, err
		}
		group := item.materialGroup()
		// Recovery needs the real ownership marker omitted by list responses.
		if item.Description == nil && input.Name != nil && item.Name == *input.Name {
			detail, err := c.GetAssetGroup(ctx, item.ID)
			if err != nil || detail.Name != item.Name || detail.Type != item.Type {
				return nil, errors.New("topenrouter material group recovery detail could not be confirmed")
			}
			group.Description = detail.Description
		}
		page.Data = append(page.Data, group)
	}
	return page, nil
}

func (c *TopenRouterMaterialClient) GetAssetGroup(ctx context.Context, id string) (*MobileCloudAssetGroup, error) {
	if err := validateTopenRouterMaterialID(id); err != nil {
		return nil, err
	}
	var result topenRouterMaterialGroup
	if err := c.request(ctx, "GetAssetGroup", topenRouterMaterialID{ID: id}, &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, errors.New("topenrouter material group response does not match the requested group")
	}
	group := result.materialGroup()
	return &group, nil
}

func (c *TopenRouterMaterialClient) UpdateAssetGroup(ctx context.Context, id string, update MobileCloudGroupUpdate) (*MobileCloudAssetGroup, error) {
	if err := validateTopenRouterMaterialID(id); err != nil {
		return nil, err
	}
	if update.Name != nil {
		if err := validateTopenRouterMaterialName(*update.Name); err != nil {
			return nil, err
		}
	}
	if update.Description != nil && utf8.RuneCountInString(*update.Description) > 300 {
		return nil, errors.New("topenrouter material group description exceeds 300 characters")
	}
	input := struct {
		ID          string  `json:"Id"`
		Name        *string `json:"Name,omitempty"`
		Description *string `json:"Description,omitempty"`
	}{ID: id, Name: update.Name, Description: update.Description}
	if err := c.request(ctx, "UpdateAssetGroup", input, &struct{}{}); err != nil {
		return nil, err
	}
	group, err := c.GetAssetGroup(ctx, id)
	if err != nil {
		// The mutation succeeded; a rejected follow-up read must not reclassify it.
		return nil, errors.New("topenrouter material group update confirmation failed; result is unknown")
	}
	return group, nil
}

func (c *TopenRouterMaterialClient) DeleteAssetGroup(ctx context.Context, id string) (bool, error) {
	if err := validateTopenRouterMaterialID(id); err != nil {
		return false, err
	}
	if err := c.request(ctx, "DeleteAssetGroup", topenRouterMaterialID{ID: id}, &struct{}{}); err != nil {
		return false, err
	}
	return true, nil
}

func (c *TopenRouterMaterialClient) CreateAsset(ctx context.Context, asset MobileCloudCreateAssetInput) (string, error) {
	if asset.GroupID != "" {
		if err := validateTopenRouterMaterialID(asset.GroupID); err != nil {
			return "", err
		}
	}
	if asset.Name != "" {
		if err := validateTopenRouterMaterialName(asset.Name); err != nil {
			return "", err
		}
	}
	if !slices.Contains([]string{"Image", "Video", "Audio"}, asset.Type) {
		return "", errors.New("topenrouter material asset type is invalid")
	}
	source, err := url.Parse(asset.URL)
	if err != nil || (source.Scheme != "http" && source.Scheme != "https") || source.Hostname() == "" || source.User != nil || source.Opaque != "" || strings.Contains(asset.URL, "#") {
		return "", errors.New("topenrouter material source must be an HTTP or HTTPS URL without credentials or fragment")
	}
	input := struct {
		URL       string  `json:"URL"`
		AssetType string  `json:"AssetType"`
		Name      *string `json:"Name,omitempty"`
		GroupID   *string `json:"GroupId,omitempty"`
	}{URL: asset.URL, AssetType: asset.Type}
	if asset.Name != "" {
		input.Name = &asset.Name
	}
	if asset.GroupID != "" {
		input.GroupID = &asset.GroupID
	}
	const maxAttempts = 3
	delays := [...]time.Duration{0, 2 * time.Second, 5 * time.Second}
	for attempt := range maxAttempts {
		if attempt > 0 {
			if err := c.retryWait(ctx, delays[attempt]); err != nil {
				return "", err
			}
		}
		var result topenRouterMaterialAsset
		err := c.request(ctx, "CreateAsset", input, &result)
		if err == nil {
			if err := validateTopenRouterMaterialID(result.ID); err != nil {
				return "", err
			}
			return result.ID, nil
		}
		if ctx.Err() != nil {
			return "", err
		}
		var retryable *topenRouterMaterialRetryableError
		if !errors.As(err, &retryable) || attempt == maxAttempts-1 {
			if errors.As(err, &retryable) {
				return "", &topenRouterMaterialAttemptsError{error: err, attempts: attempt + 1}
			}
			return "", err
		}
	}
	return "", errors.New("topenrouter material create asset retry loop ended unexpectedly")
}

func (c *TopenRouterMaterialClient) ListAssets(ctx context.Context, input MobileCloudAssetQuery) (*MobileCloudMaterialPage[MobileCloudAsset], error) {
	query, err := topenRouterMaterialPagination(input.PageNo, input.PageSize)
	if err != nil {
		return nil, err
	}
	for _, id := range input.GroupIDs {
		if err := validateTopenRouterMaterialID(id); err != nil {
			return nil, err
		}
	}
	query.GroupIDs = input.GroupIDs
	var result topenRouterMaterialPage[topenRouterMaterialAsset]
	if err := c.request(ctx, "ListAssets", query, &result); err != nil {
		return nil, err
	}
	if result.Items == nil || result.TotalCount == nil || *result.TotalCount < 0 {
		return nil, errors.New("topenrouter material asset list is invalid")
	}
	page := &MobileCloudMaterialPage[MobileCloudAsset]{Data: make([]MobileCloudAsset, 0, len(result.Items)), Total: *result.TotalCount}
	for _, item := range result.Items {
		if err := validateTopenRouterMaterialID(item.ID); err != nil {
			return nil, err
		}
		page.Data = append(page.Data, c.materialAsset(item))
	}
	return page, nil
}

func (c *TopenRouterMaterialClient) GetAsset(ctx context.Context, id string) (*MobileCloudAsset, error) {
	if err := validateTopenRouterMaterialID(id); err != nil {
		return nil, err
	}
	var result topenRouterMaterialAsset
	if err := c.request(ctx, "GetAsset", topenRouterMaterialID{ID: id}, &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, errors.New("topenrouter material asset response does not match the requested asset")
	}
	asset := c.materialAsset(result)
	return &asset, nil
}

func (c *TopenRouterMaterialClient) UpdateAsset(ctx context.Context, id, name string) (*MobileCloudAsset, error) {
	if err := validateTopenRouterMaterialID(id); err != nil {
		return nil, err
	}
	if err := validateTopenRouterMaterialName(name); err != nil {
		return nil, err
	}
	input := struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}{ID: id, Name: name}
	if err := c.request(ctx, "UpdateAsset", input, &struct{}{}); err != nil {
		return nil, err
	}
	asset, err := c.GetAsset(ctx, id)
	if err != nil {
		return nil, errors.New("topenrouter material asset update confirmation failed; result is unknown")
	}
	return asset, nil
}

func (c *TopenRouterMaterialClient) DeleteAsset(ctx context.Context, id string) (bool, error) {
	if err := validateTopenRouterMaterialID(id); err != nil {
		return false, err
	}
	if err := c.request(ctx, "DeleteAsset", topenRouterMaterialID{ID: id}, &struct{}{}); err != nil {
		return false, err
	}
	return true, nil
}
