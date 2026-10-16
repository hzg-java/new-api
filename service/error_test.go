package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "skip status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     200,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
		})
	}
}

func TestRelayErrorHandlerTruncatesInvalidJSONBodyInLog(t *testing.T) {
	withDebugEnabled(t, false)

	body := strings.Repeat("b", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, body, newAPIError.ToOpenAIRelayError().Message)
	require.Contains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), fmt.Sprintf("original_length=%d", len(body)))
	require.NotContains(t, logBuffer.String(), strings.Repeat("b", common.LocalLogContentLimit+1))
}

func TestRelayErrorHandlerKeepsStructuredErrorMessage(t *testing.T) {
	message := strings.Repeat("c", common.LocalLogContentLimit+256)
	body := `{"message":"` + message + `"}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsOpenAIErrorMessage(t *testing.T) {
	message := strings.Repeat("d", common.LocalLogContentLimit+256)
	body := `{"error":{"message":"` + message + `","type":"server_error","code":"server_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsInvalidJSONBodyInDebugLog(t *testing.T) {
	withDebugEnabled(t, true)

	body := strings.Repeat("e", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.NotContains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), body)
}

func TestOpenAIRelayErrorBoundaries(t *testing.T) {
	local := types.NewError(errors.New("invalid model"), types.ErrorCodeInvalidRequest)
	assert.Equal(t, types.ErrorCodeInvalidRequest, local.ToOpenAIRelayError().Code)
	assert.Equal(t, local.ToOpenAIError(), local.ToOpenAIRelayError())
	for _, body := range []string{
		`{"error":{"message":"","code":"provider_busy","type":"server_error"}}`,
		`{"error":{"message":"Upstream","code":"provider_busy","type":"server_error"}}`,
	} {
		apiErr := RelayErrorHandler(context.Background(), &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(body))}, false)
		require.NotNil(t, apiErr)
		apiErr.SetMessage(apiErr.Error() + " (request id: rid-123)")
		out := apiErr.ToOpenAIRelayError()
		assert.Equal(t, "provider_busy", out.Code)
		assert.Equal(t, "Request failed (request id: rid-123)", out.Message)
	}
	for _, message := range []string{
		`authentication failed api_key="sk-secret"`,
		`authentication failed Authorization: Bearer secret`,
		`authentication failed Cookie: session=secret`,
	} {
		apiErr := types.NewError(errors.New(message), types.ErrorCodeDoRequestFailed, types.ErrOptionWithTransportError(errors.New(message)))
		assert.NotContains(t, apiErr.ToOpenAIRelayError().Message, "secret")
	}
	body := `{"reason":"backend unavailable","api_key":"sk-secret","Authorization":"Bearer secret"}`
	apiErr := RelayErrorHandler(context.Background(), &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(body))}, false)
	out := apiErr.ToOpenAIRelayError()
	assert.Equal(t, "backend unavailable", out.Message)
	assert.NotContains(t, out.Message, "secret")
	assert.NotEqual(t, out, apiErr.ToOpenAIError())
}

func TestRelayErrorMessageOmitsUpstream(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    string
	}{
		{"Upstream request failed", "request failed"},
		{"UPSTREAM request timed out", "request timed out"},
		{"The upstream connection was reset", "The connection was reset"},
		{"request failed", "request failed"},
		{"Upstream error: bad response status code 502, body: connection refused", "connection refused"},
		{"Upstream: connection refused", "connection refused"},
		{"Incorrect API key provided: sk-proj-123456", "Incorrect API key provided: ***"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			apiErr := types.WithOpenAIError(types.OpenAIError{Message: tc.message, Type: "server_error", Code: "provider_error"}, 502, types.ErrOptionWithUpstreamResponse(types.OpenAIError{Message: tc.message, Type: "server_error", Code: "provider_error"}))
			out := apiErr.ToOpenAIRelayError()
			assert.Equal(t, tc.want, out.Message)
			assert.Equal(t, "provider_error", out.Code)
			assert.Equal(t, "server_error", out.Type)
			assert.Equal(t, tc.message, apiErr.Error())
		})
	}
}

func TestRelayErrorResponseCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code any
	}{
		{"upstream string code", `{"error":{"message":"failed","code":"provider_error"}}`, "provider_error"},
		{"upstream numeric code", `{"error":{"message":"failed","code":123}}`, float64(123)},
		{"upstream zero code", `{"error":{"message":"failed","code":0}}`, float64(0)},
		{"missing code", `{"error":{"message":"failed"}}`, 503},
		{"null code", `{"error":{"message":"failed","code":null}}`, 503},
		{"empty code", `{"error":{"message":"failed","code":""}}`, 503},
		{"top-level message", `{"message":"failed"}`, 503},
		{"plain text", "gateway failed", 503},
		{"top-level code", `{"message":"failed","code":"provider_busy"}`, "provider_busy"},
		{"empty message code", `{"error":{"message":"","code":"provider_busy"}}`, "provider_busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := RelayErrorHandler(context.Background(), &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(tc.body))}, false)
			require.NotNil(t, apiErr)
			ResetStatusCode(apiErr, `{"502":503}`)
			assert.Equal(t, tc.code, apiErr.ToOpenAIRelayError().Code)
		})
	}
	apiErr := types.NewError(errors.New("context deadline exceeded"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithTransportError(errors.New("context deadline exceeded")))
	assert.Equal(t, 500, apiErr.ToOpenAIRelayError().Code)
	assert.Equal(t, "api_error", apiErr.ToOpenAIRelayError().Type)
	assert.Equal(t, types.ErrorTypeNewAPIError, apiErr.GetErrorType())
	assert.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
}

func TestRelayErrorHandlerPreservesFailureDetails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		message string
	}{
		{"plain text", "gateway connection timed out", "gateway connection timed out"},
		{"unrecognized JSON", `{"reason":"backend unavailable"}`, "backend unavailable"},
		{"empty body", "", "Response is empty"},
		{"structured error", `{"error":{"message":"quota exceeded","type":"quota_error","param":"model","code":"quota_exceeded"}}`, "quota exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(tc.body))}
			apiErr := RelayErrorHandler(context.Background(), resp, false)
			require.NotNil(t, apiErr)
			out := apiErr.ToOpenAIRelayError()
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.Equal(t, tc.message, out.Message)
			if tc.name == "structured error" {
				assert.Equal(t, "quota_error", out.Type)
				assert.Equal(t, "model", out.Param)
				assert.Equal(t, "quota_exceeded", out.Code)
			}
		})
	}
}

type failingUpstreamBody struct {
	closed bool
}

func (*failingUpstreamBody) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func (b *failingUpstreamBody) Close() error {
	b.closed = true
	return nil
}

func TestRelayErrorHandlerPreservesReadFailure(t *testing.T) {
	body := &failingUpstreamBody{}
	apiErr := RelayErrorHandler(context.Background(), &http.Response{StatusCode: http.StatusBadGateway, Body: body}, false)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.ToOpenAIRelayError().Message, "failed to read response: unexpected EOF")
	assert.Equal(t, "bad_response_status_code", apiErr.ToClaudeError().Type)
	assert.True(t, body.closed)
}

func withDebugEnabled(t *testing.T, enabled bool) {
	t.Helper()

	oldDebug := common.DebugEnabled
	common.DebugEnabled = enabled
	t.Cleanup(func() {
		common.DebugEnabled = oldDebug
	})
}
