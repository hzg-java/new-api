package channel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequestPreservesTransportFailure(t *testing.T) {
	service.InitHttpClient()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "https://upstream.invalid/images?key=secret-value", bytes.NewReader(nil))
	require.NoError(t, err)
	resp, err := doRequest(ctx, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.False(t, errors.Is(err, context.Canceled))
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "upstream error: do request failed", apiErr.ToOpenAIError().Message)
	out := apiErr.ToOpenAIRelayError()
	assert.Contains(t, out.Message, "context canceled")
	assert.NotContains(t, out.Message, "secret-value")
	assert.NotContains(t, out.Message, "upstream.invalid")
}

func TestDoRequestReturnsUpstreamRedirectWithoutFollowing(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	sharedClient := service.GetHttpClient()
	require.NotNil(t, sharedClient)
	require.NotNil(t, sharedClient.CheckRedirect)
	originalRedirectPolicy := reflect.ValueOf(sharedClient.CheckRedirect).Pointer()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	const responseBody = "redirect response"
	tests := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, statusCode := range tests {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			targetRequests.Store(0)
			var sourceRequests atomic.Int32
			type sourceResult struct {
				body []byte
				err  error
			}
			sourceResultCh := make(chan sourceResult, 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceRequests.Add(1)
				body, err := io.ReadAll(r.Body)
				sourceResultCh <- sourceResult{body: body, err: err}
				w.Header().Set("Location", target.URL+"/redirect-target")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, responseBody)
			}))
			defer source.Close()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

			req, err := http.NewRequest(http.MethodPost, source.URL, bytes.NewReader([]byte("request body")))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

			resp, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			gotSource := <-sourceResultCh
			require.NoError(t, gotSource.err)

			assert.Equal(t, statusCode, resp.StatusCode)
			assert.Equal(t, target.URL+"/redirect-target", resp.Header.Get("Location"))
			assert.Equal(t, responseBody, string(body))
			assert.Equal(t, []byte("request body"), gotSource.body)
			assert.EqualValues(t, 1, sourceRequests.Load())
			assert.Zero(t, targetRequests.Load())
		})
	}

	assert.Equal(t, originalRedirectPolicy, reflect.ValueOf(sharedClient.CheckRedirect).Pointer(), "the cached client must not be mutated")
}
