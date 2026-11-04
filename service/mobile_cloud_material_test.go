package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
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
