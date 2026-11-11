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

// Only an explicit upstream refusal is safe to classify as rejection.
type TopenRouterMaterialRejection struct{ Message string }

func (e *TopenRouterMaterialRejection) Error() string { return e.Message }

type TopenRouterMaterialClient struct {
	endpoint   url.URL
	token      string
	httpClient *http.Client
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
	}, nil
}

func (c *TopenRouterMaterialClient) safeReason(reason string) string {
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
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("topenrouter material request canceled: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("topenrouter material request timed out: %w", context.DeadlineExceeded)
		}
		return errors.New("topenrouter material upstream connection failed; result is unknown")
	}
	defer resp.Body.Close()
	const maxResponseBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("topenrouter material response canceled: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("topenrouter material response timed out: %w", context.DeadlineExceeded)
		}
		return errors.New("topenrouter material upstream response could not be read; result is unknown")
	}
	if len(data) > maxResponseBytes {
		return errors.New("topenrouter material upstream response exceeds size limit; result is unknown")
	}
	var envelope struct {
		ResponseMetadata struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result  common.RawMessage `json:"Result"`
		Code    *int              `json:"code"`
		Message string            `json:"message"`
	}
	message := "topenrouter material upstream rejected the request"
	httpFailed := resp.StatusCode < 200 || resp.StatusCode >= 300
	if httpFailed {
		message = fmt.Sprintf("topenrouter material upstream HTTP status %d", resp.StatusCode)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || common.Unmarshal(data, &envelope) != nil {
		return errors.New(message + ": invalid JSON response; result is unknown")
	}
	official := envelope.ResponseMetadata.Error
	platformError := envelope.Code != nil && *envelope.Code == 1
	if httpFailed || official != nil || platformError {
		rejected := resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests
		if !httpFailed {
			// Internal, throttling, timeout and unknown errors cannot prove refusal.
			rejected = platformError
			if official != nil {
				code, _, _ := strings.Cut(official.Code, ".")
				rejected = slices.Contains([]string{"InvalidParameter", "InvalidParameterValue", "MissingParameter", "NotFound", "Unauthorized", "AccessDenied", "Forbidden", "AuthenticationFailed", "InvalidAction", "InvalidVersion"}, code)
			}
		}
		reason := envelope.Message
		if official != nil {
			reason = official.Message
		}
		if strings.TrimSpace(reason) != "" {
			message += ": " + c.safeReason(reason)
		}
		if rejected {
			return &TopenRouterMaterialRejection{Message: message}
		}
		return errors.New(message + "; result is unknown")
	}
	result := bytes.TrimSpace(envelope.Result)
	if envelope.Code != nil || len(result) == 0 || result[0] != '{' || common.Unmarshal(result, output) != nil {
		return errors.New("topenrouter material upstream result is missing or invalid; result is unknown")
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
	var result topenRouterMaterialAsset
	if err := c.request(ctx, "CreateAsset", input, &result); err != nil {
		return "", err
	}
	if err := validateTopenRouterMaterialID(result.ID); err != nil {
		return "", err
	}
	return result.ID, nil
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
