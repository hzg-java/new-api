package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Trust the fixture certificate without replacing production transport behavior.
func newTopenRouterMaterialTestClient(t *testing.T, server *httptest.Server) *TopenRouterMaterialClient {
	t.Helper()
	client, err := NewTopenRouterMaterialClient(server.URL, "test-platform-token")
	require.NoError(t, err)
	transport := client.httpClient.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	t.Cleanup(transport.CloseIdleConnections)
	return client
}

func TestTopenRouterMaterialEndpoint(t *testing.T) {
	for _, path := range []string{"", "/", "/v1", "/v1/", "/v1/api/assets", "/v1/api/assets/"} {
		t.Run("accept "+path, func(t *testing.T) {
			client, err := NewTopenRouterMaterialClient("https://example.com:8000"+path, "platform-token")
			require.NoError(t, err)
			assert.Equal(t, "https://example.com:8000/v1/api/assets/", client.endpoint.String())
			assert.GreaterOrEqual(t, client.httpClient.Transport.(*http.Transport).TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
		})
	}
	for _, endpoint := range []string{
		"", "http://example.com", "https://", "https://user:password@example.com", "https://example.com/v2",
		"https://example.com/v1/chat/completions", "https://example.com/v1//", "https://example.com/v1/api/assets/extra",
		"https://example.com?", "https://example.com?token=secret", "https://example.com/#fragment", "https://example.com/#",
		"https://example.com/%76%31", "https://example.com/v1/../v1", "https://example.com:99999", "https://example.com:",
	} {
		t.Run("reject "+endpoint, func(t *testing.T) {
			client, err := NewTopenRouterMaterialClient(endpoint, "platform-token")
			require.Error(t, err)
			assert.Nil(t, client)
			assert.NotContains(t, err.Error(), "example.com")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
	for _, token := range []string{"", " ", " leading", "trailing ", "a b", "a\tb", "a\rb", "a\nb", "a\x00b", "a\x7fb", "a\u0085b", "a\u00a0b"} {
		t.Run(fmt.Sprintf("token %q", token), func(t *testing.T) {
			client, err := NewTopenRouterMaterialClient("https://example.com", token)
			require.Error(t, err)
			assert.Nil(t, client)
		})
	}
}

func TestTopenRouterMaterialActions(t *testing.T) {
	ctx := context.Background()
	group := `{"Id":"group-1","Name":"portraits","GroupType":"AIGC","Description":"ownership","CreateTime":"created","UpdateTime":"updated"}`
	asset := `{"Id":"asset-1","GroupId":"group-1","Name":"portrait","AssetType":"Image","URL":"https://source.example/current?token=temporary","Status":"Active","CreateTime":"created","UpdateTime":"updated"}`
	for _, tc := range []struct {
		action, input, result, followAction, followResult string
		call                                              func(*TopenRouterMaterialClient) (any, error)
		want                                              any
	}{
		{"CreateAssetGroup", `{"Name":"portraits","GroupType":"AIGC","Description":""}`, group, "", "", func(c *TopenRouterMaterialClient) (any, error) {
			return c.CreateAssetGroup(ctx, "portraits", common.GetPointer(""))
		}, &MobileCloudAssetGroup{ID: "group-1", Name: "portraits", Type: "AIGC", Description: "ownership", CreatedTime: "created", UpdatedTime: "updated"}},
		{"ListAssetGroups", `{"Page":2,"PageSize":20}`, `{"Items":[` + group + `],"TotalCount":21}`, "", "", func(c *TopenRouterMaterialClient) (any, error) {
			return c.ListAssetGroups(ctx, MobileCloudGroupQuery{PageNo: common.GetPointer(int32(2)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC"), Name: common.GetPointer("does-not-match"), GroupIDs: []string{"different-group"}})
		}, &MobileCloudMaterialPage[MobileCloudAssetGroup]{Data: []MobileCloudAssetGroup{{ID: "group-1", Name: "portraits", Type: "AIGC", Description: "ownership", CreatedTime: "created", UpdatedTime: "updated"}}, Total: 21}},
		{"GetAssetGroup", `{"Id":"group-1"}`, group, "", "", func(c *TopenRouterMaterialClient) (any, error) { return c.GetAssetGroup(ctx, "group-1") }, &MobileCloudAssetGroup{ID: "group-1", Name: "portraits", Type: "AIGC", Description: "ownership", CreatedTime: "created", UpdatedTime: "updated"}},
		{"UpdateAssetGroup", `{"Id":"group-1","Name":"portraits","Description":""}`, `{}`, "GetAssetGroup", group, func(c *TopenRouterMaterialClient) (any, error) {
			return c.UpdateAssetGroup(ctx, "group-1", MobileCloudGroupUpdate{Name: common.GetPointer("portraits"), Description: common.GetPointer("")})
		}, &MobileCloudAssetGroup{ID: "group-1", Name: "portraits", Type: "AIGC", Description: "ownership", CreatedTime: "created", UpdatedTime: "updated"}},
		{"DeleteAssetGroup", `{"Id":"group-1"}`, `{}`, "", "", func(c *TopenRouterMaterialClient) (any, error) { return c.DeleteAssetGroup(ctx, "group-1") }, true},
		{"CreateAsset", `{"URL":"https://source.example/input.png","AssetType":"Image","Name":"portrait","GroupId":"group-1"}`, `{"Id":"asset-1","Status":"Processing"}`, "", "", func(c *TopenRouterMaterialClient) (any, error) {
			return c.CreateAsset(ctx, MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", Type: "Image", URL: "https://source.example/input.png"})
		}, "asset-1"},
		{"ListAssets", `{"Page":2,"PageSize":20,"GroupIds":["group-1"]}`, `{"Items":[` + asset + `],"TotalCount":21}`, "", "", func(c *TopenRouterMaterialClient) (any, error) {
			return c.ListAssets(ctx, MobileCloudAssetQuery{PageNo: common.GetPointer(int32(2)), PageSize: common.GetPointer(int32(20)), GroupType: common.GetPointer("AIGC"), GroupIDs: []string{"group-1"}, Name: common.GetPointer("does-not-match"), Statuses: []string{"FAILED"}})
		}, &MobileCloudMaterialPage[MobileCloudAsset]{Data: []MobileCloudAsset{{ID: "asset-1", GroupID: "group-1", Name: "portrait", Type: "Image", URL: "https://source.example/current?token=temporary", Status: "ACTIVE", CreatedTime: "created", UpdatedTime: "updated"}}, Total: 21}},
		{"GetAsset", `{"Id":"asset-1"}`, asset, "", "", func(c *TopenRouterMaterialClient) (any, error) { return c.GetAsset(ctx, "asset-1") }, &MobileCloudAsset{ID: "asset-1", GroupID: "group-1", Name: "portrait", Type: "Image", URL: "https://source.example/current?token=temporary", Status: "ACTIVE", CreatedTime: "created", UpdatedTime: "updated"}},
		{"UpdateAsset", `{"Id":"asset-1","Name":"portrait"}`, `{}`, "GetAsset", asset, func(c *TopenRouterMaterialClient) (any, error) { return c.UpdateAsset(ctx, "asset-1", "portrait") }, &MobileCloudAsset{ID: "asset-1", GroupID: "group-1", Name: "portrait", Type: "Image", URL: "https://source.example/current?token=temporary", Status: "ACTIVE", CreatedTime: "created", UpdatedTime: "updated"}},
		{"DeleteAsset", `{"Id":"asset-1"}`, `{}`, "", "", func(c *TopenRouterMaterialClient) (any, error) { return c.DeleteAsset(ctx, "asset-1") }, true},
	} {
		t.Run(tc.action, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				action, input, result := tc.action, tc.input, tc.result
				if call == 2 && tc.followAction != "" {
					action, result = tc.followAction, tc.followResult
					input = `{"Id":"group-1"}`
					if action == "GetAsset" {
						input = `{"Id":"asset-1"}`
					}
				}
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/v1/api/assets/", r.URL.Path)
				assert.Equal(t, "HTTP/1.1", r.Proto)
				assert.Equal(t, "Bearer test-platform-token", r.Header.Get("Authorization"))
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Len(t, r.URL.Query(), 2)
				assert.Equal(t, action, r.URL.Query().Get("Action"))
				assert.Equal(t, "2024-01-01", r.URL.Query().Get("Version"))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.JSONEq(t, input, string(body))
				_, _ = fmt.Fprintf(w, `{"ResponseMetadata":{"Action":%q},"Result":%s}`, action, result)
			}))
			defer server.Close()
			client := newTopenRouterMaterialTestClient(t, server)
			var _ MaterialManagementProvider = client
			result, err := tc.call(client)
			require.NoError(t, err)
			assert.Equal(t, tc.want, result)
			wantCalls := int32(1)
			if tc.followAction != "" {
				wantCalls++
			}
			assert.Equal(t, wantCalls, calls.Load())
		})
	}
}

func TestTopenRouterMaterialGroupRecoveryDescription(t *testing.T) {
	for _, tc := range []struct {
		name, description, queryName, detail string
		wantCalls                            int32
		wantDescription                      string
		wantError                            bool
	}{
		{"unfiltered list stays untouched", "", "", "", 1, "", false},
		{"recover matching name from detail", "", "portraits", `{"Id":"group-1","Name":"portraits","GroupType":"AIGC","Description":"ownership"}`, 2, "ownership", false},
		{"never copy requested name to description", "", "portraits", `{"Id":"group-1","Name":"portraits","GroupType":"AIGC"}`, 2, "", false},
		{"explicit empty description needs no lookup", `,"Description":""`, "portraits", "", 1, "", false},
		{"unrelated name needs no lookup", "", "different", "", 1, "", false},
		{"unconfirmed lookup blocks recovery", "", "portraits", `null`, 2, "", true},
		{"mismatched detail blocks recovery", "", "portraits", `{"Id":"other","Name":"portraits","GroupType":"AIGC","Description":"ownership"}`, 2, "", true},
		{"changed detail cannot supply ownership", "", "portraits", `{"Id":"group-1","Name":"renamed","GroupType":"AIGC","Description":"ownership"}`, 2, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("Action") == "GetAssetGroup" {
					_, _ = fmt.Fprintf(w, `{"Result":%s}`, tc.detail)
					return
				}
				_, _ = fmt.Fprintf(w, `{"Result":{"TotalCount":5,"Items":[{"Id":"group-1","Name":"portraits","GroupType":"AIGC"%s}]}}`, tc.description)
			}))
			defer server.Close()
			client := newTopenRouterMaterialTestClient(t, server)
			query := MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(1))}
			if tc.queryName != "" {
				query.Name = &tc.queryName
			}
			page, err := client.ListAssetGroups(context.Background(), query)
			if tc.wantError {
				require.Error(t, err)
				var rejection *TopenRouterMaterialRejection
				assert.False(t, errors.As(err, &rejection))
			} else {
				require.NoError(t, err)
				require.Len(t, page.Data, 1)
				assert.Equal(t, int64(5), page.Total)
				assert.Equal(t, tc.wantDescription, page.Data[0].Description)
			}
			assert.Equal(t, tc.wantCalls, calls.Load())
		})
	}
}

func TestTopenRouterMaterialStatusAndProcessingPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name, detail, reason string
	}{
		{"nested review failure", `"Error":{"Code":"InputImageSensitiveContentDetected","Message":"The request failed because the input image may contain sensitive information; https://private.example/input?token=private-token; Authorization: Bearer test-platform-token"}`, "InputImageSensitiveContentDetected: The request failed because the input image may contain sensitive information"},
		{"nested reason overrides legacy reason", `"Error":{"Code":"InputImageSensitiveContentDetected","Message":"Image rejected"},"ErrorMessage":"Legacy reason"`, "InputImageSensitiveContentDetected: Image rejected"},
		{"code without message", `"Error":{"Code":"InputImageSensitiveContentDetected"}`, "InputImageSensitiveContentDetected"},
		{"message without code", `"Error":{"Message":"Image rejected"}`, "Image rejected"},
		{"empty error preserves legacy reason", `"Error":{},"ErrorMessage":"Unsupported image format"`, "Unsupported image format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"Result":{"Id":"asset-1","GroupId":"group-1","AssetType":"Image","Status":"Failed",%s}}`, tc.detail)
			}))
			defer server.Close()
			asset, err := newTopenRouterMaterialTestClient(t, server).GetAsset(t.Context(), "asset-1")
			require.NoError(t, err, "a rejected image is a material state, not a failed GetAsset call")
			assert.Equal(t, "FAILED", asset.Status)
			assert.Contains(t, asset.ErrorMessage, tc.reason)
			for _, private := range []string{"test-platform-token", "private-token", "private.example", "/input", "Legacy reason"} {
				assert.NotContains(t, asset.ErrorMessage, private)
			}
		})
	}
	for _, tc := range []struct{ native, normalized string }{{"Processing", "PROCESSING"}, {"Active", "ACTIVE"}, {"Failed", "FAILED"}, {"FutureState", "FUTURESTATE"}} {
		t.Run(tc.native, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"Result":{"Id":"asset-1","GroupId":"group-1","AssetType":"Image","Status":%q,"ErrorMessage":"Unsupported image format; https://private.example/input?token=private-token; Authorization: Bearer test-platform-token"}}`, tc.native)
			}))
			defer server.Close()
			asset, err := newTopenRouterMaterialTestClient(t, server).GetAsset(context.Background(), "asset-1")
			require.NoError(t, err)
			assert.Equal(t, tc.normalized, asset.Status)
			assert.Contains(t, asset.ErrorMessage, "Unsupported image format")
			for _, private := range []string{"test-platform-token", "private-token", "private.example", "/input"} {
				assert.NotContains(t, asset.ErrorMessage, private)
			}
		})
	}
}

func TestTopenRouterMaterialFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		rejection bool
		reason    string
	}{
		{"official validation on 200", 200, `{"ResponseMetadata":{"Error":{"Code":"InvalidParameter.URL","Message":"Unsupported image format"}}}`, true, "Unsupported image format"},
		{"official not found", 404, `{"ResponseMetadata":{"Error":{"Code":"NotFound.asset_id","Message":"Asset not found"}}}`, true, "Asset not found"},
		{"platform error on 200", 200, `{"code":1,"message":"asset url not allowed"}`, true, "asset url not allowed"},
		{"platform rejection", 403, `{"code":1,"message":"upstream not configured"}`, true, "not configured"},
		{"explicit HTTP rejection", 401, `{}`, true, "401"},
		{"server error overrides business rejection", 502, `{"ResponseMetadata":{"Error":{"Code":"InvalidParameter.URL","Message":"Unsupported image format"}}}`, false, "502"},
		{"rate limit", 429, `{"code":1,"message":"rate limited"}`, false, "429"},
		{"request timeout", 408, `{"code":1,"message":"timeout"}`, false, "408"},
		{"official server error on 200", 200, `{"ResponseMetadata":{"Error":{"Code":"InternalError","Message":"internal failure"}}}`, false, "internal failure"},
		{"official throttle on 200", 200, `{"ResponseMetadata":{"Error":{"Code":"Throttling","Message":"rate limited"}}}`, false, "rate limited"},
		{"unknown official code", 200, `{"ResponseMetadata":{"Error":{"Code":"UnrecognizedError","Message":"unknown failure"}}}`, false, "unknown failure"},
		{"empty error object", 200, `{"ResponseMetadata":{"Error":{}},"Result":{"Id":"asset-1","Status":"Processing"}}`, false, ""},
		{"empty response", 200, ``, false, ""},
		{"HTML", 502, `<html>test-platform-token https://private.example/input</html>`, false, ""},
		{"broken JSON", 200, `{"Result":`, false, ""},
		{"invalid JSON despite 400", 400, `bad JSON`, false, ""},
		{"JSON null", 200, `null`, false, ""},
		{"missing result", 200, `{}`, false, ""},
		{"null result", 200, `{"Result":null}`, false, ""},
		{"string result", 200, `{"Result":"asset-1"}`, false, ""},
		{"array result", 200, `{"Result":[]}`, false, ""},
		{"empty result", 200, `{"Result":{}}`, false, ""},
		{"missing create status", 200, `{"Result":{"Id":"asset-1"}}`, false, ""},
		{"trailing JSON", 200, `{"Result":{"Id":"asset-1","Status":"Processing"}}{}`, false, ""},
		{"oversized", 200, `{"padding":"` + strings.Repeat("x", 4<<20) + `"}`, false, "size limit"},
		{"private reason", 400, `{"code":1,"message":"Unsupported image format; test-platform-token; https://private.example/raw/path?token=private-token; Authorization: Bearer other-private-token"}`, true, "Unsupported image format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := newTopenRouterMaterialTestClient(t, server)
			id, err := client.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", Type: "Image", URL: "https://source.example/input.png"})
			require.Error(t, err)
			assert.Empty(t, id)
			var rejection *TopenRouterMaterialRejection
			assert.Equal(t, tc.rejection, errors.As(err, &rejection))
			if tc.reason != "" {
				assert.Contains(t, err.Error(), tc.reason)
			}
			for _, private := range []string{"test-platform-token", "private-token", "private.example", "/raw/path", server.URL} {
				assert.NotContains(t, err.Error(), private)
			}
			assert.Equal(t, int32(1), calls.Load(), "mutations must never be retried")
		})
	}
}

func TestTopenRouterMaterialReasonPrivacy(t *testing.T) {
	for _, reason := range []string{
		"Unsupported image format https://private.example/raw/path?token=private-token",
		"Unsupported image format https://private.example/raw/path?token=private-token%zz",
		"Unsupported image format https://[invalid-host/raw/path?token=private-token",
		"Unsupported image format test-platform-token Authorization: Bearer other-private-token",
	} {
		t.Run(reason, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"code": 1, "message": reason})
			require.NoError(t, err)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			_, err = newTopenRouterMaterialTestClient(t, server).DeleteAsset(context.Background(), "asset-1")
			require.Error(t, err)
			assert.ErrorContains(t, err, "Unsupported image format")
			for _, private := range []string{"test-platform-token", "other-private-token", "private-token", "private.example", "invalid-host", "/raw/path"} {
				assert.NotContains(t, err.Error(), private)
			}
		})
	}
}

func TestTopenRouterMaterialListValidation(t *testing.T) {
	for _, action := range []string{"ListAssets", "ListAssetGroups"} {
		for _, result := range []string{`{}`, `{"Items":[],"TotalCount":-1}`, `{"Items":[],"TotalCount":null}`, `{"Items":null,"TotalCount":0}`, `{"Items":{},"TotalCount":0}`, `{"Items":[{}],"TotalCount":1}`} {
			t.Run(action+result, func(t *testing.T) {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = fmt.Fprintf(w, `{"Result":%s}`, result)
				}))
				defer server.Close()
				client := newTopenRouterMaterialTestClient(t, server)
				var err error
				if action == "ListAssets" {
					_, err = client.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20))})
				} else {
					_, err = client.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(20))})
				}
				require.Error(t, err)
				var rejection *TopenRouterMaterialRejection
				assert.False(t, errors.As(err, &rejection))
			})
		}
	}
}

func TestTopenRouterMaterialNoReplayOrRedirect(t *testing.T) {
	for _, mode := range []string{"disconnect", "truncated body", "redirect 301", "redirect 302", "redirect 307", "redirect 308", "deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			releaseHandler := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if strings.HasPrefix(mode, "redirect") {
					status := 0
					_, _ = fmt.Sscanf(mode, "redirect %d", &status)
					w.Header().Set("Location", "/credential-leak")
					w.WriteHeader(status)
					return
				}
				if mode == "deadline" || mode == "cancel" {
					if mode == "cancel" {
						cancel()
					}
					select {
					case <-r.Context().Done():
					case <-releaseHandler:
					}
					return
				}
				if mode == "truncated body" {
					w.Header().Set("Content-Length", "1000")
					_, _ = io.WriteString(w, `{"Result":`)
					return
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if assert.NoError(t, err) {
					_ = conn.Close()
				}
			}))
			defer server.Close()
			defer close(releaseHandler)
			client := newTopenRouterMaterialTestClient(t, server)
			if mode == "deadline" {
				client.httpClient.Timeout = 100 * time.Millisecond
			}
			_, err := client.CreateAssetGroup(ctx, "portraits", nil)
			require.Error(t, err)
			var rejection *TopenRouterMaterialRejection
			assert.False(t, errors.As(err, &rejection))
			assert.Equal(t, int32(1), calls.Load())
			assert.NotContains(t, err.Error(), server.URL)
			assert.NotContains(t, err.Error(), "test-platform-token")
			if mode == "cancel" {
				assert.ErrorIs(t, err, context.Canceled)
			}
			if mode == "deadline" {
				assert.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
}

func TestTopenRouterMaterialOptionalFieldsAndPagination(t *testing.T) {
	for _, tc := range []struct {
		action, input, result string
		call                  func(*TopenRouterMaterialClient) error
	}{
		{"CreateAssetGroup", `{"Name":"portraits","GroupType":"AIGC"}`, `{"Id":"group-1","Name":"portraits","GroupType":"AIGC"}`, func(c *TopenRouterMaterialClient) error {
			_, err := c.CreateAssetGroup(context.Background(), "portraits", nil)
			return err
		}},
		{"CreateAsset", `{"URL":"https://source.example/input.png","AssetType":"Image"}`, `{"Id":"asset-1","Status":"Processing"}`, func(c *TopenRouterMaterialClient) error {
			_, err := c.CreateAsset(context.Background(), MobileCloudCreateAssetInput{URL: "https://source.example/input.png", Type: "Image"})
			return err
		}},
		{"ListAssets", `{"Page":1,"PageSize":20}`, `{"Items":[],"TotalCount":0}`, func(c *TopenRouterMaterialClient) error {
			page, err := c.ListAssets(context.Background(), MobileCloudAssetQuery{})
			if assert.NoError(t, err) {
				assert.Equal(t, &MobileCloudMaterialPage[MobileCloudAsset]{Data: []MobileCloudAsset{}, Total: 0}, page)
			}
			return err
		}},
		{"ListAssetGroups", `{"Page":1,"PageSize":200}`, `{"Items":[],"TotalCount":0}`, func(c *TopenRouterMaterialClient) error {
			page, err := c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageSize: common.GetPointer(int32(200))})
			if assert.NoError(t, err) {
				assert.Equal(t, &MobileCloudMaterialPage[MobileCloudAssetGroup]{Data: []MobileCloudAssetGroup{}, Total: 0}, page)
			}
			return err
		}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.action, r.URL.Query().Get("Action"))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.JSONEq(t, tc.input, string(body))
				_, _ = fmt.Fprintf(w, `{"Result":%s}`, tc.result)
			}))
			defer server.Close()
			require.NoError(t, tc.call(newTopenRouterMaterialTestClient(t, server)))
		})
	}
}

func TestTopenRouterMaterialUpdateConfirmationFailureIsUnknown(t *testing.T) {
	for _, group := range []bool{true, false} {
		t.Run(fmt.Sprintf("group %t", group), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if strings.HasPrefix(r.URL.Query().Get("Action"), "Get") {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"ResponseMetadata":{"Error":{"Code":"NotFound","Message":"not found"}}}`)
					return
				}
				_, _ = io.WriteString(w, `{"Result":{}}`)
			}))
			defer server.Close()
			client := newTopenRouterMaterialTestClient(t, server)
			var err error
			if group {
				_, err = client.UpdateAssetGroup(context.Background(), "group-1", MobileCloudGroupUpdate{Name: common.GetPointer("renamed")})
			} else {
				_, err = client.UpdateAsset(context.Background(), "asset-1", "renamed")
			}
			require.Error(t, err)
			var rejection *TopenRouterMaterialRejection
			assert.False(t, errors.As(err, &rejection), "a failed confirmation does not reject the already applied mutation")
			assert.Equal(t, int32(2), calls.Load())
		})
	}
}

func TestTopenRouterMaterialInputValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*TopenRouterMaterialClient) error
	}{
		{"invalid id", func(c *TopenRouterMaterialClient) error {
			_, err := c.DeleteAsset(context.Background(), "../asset")
			return err
		}},
		{"empty group name", func(c *TopenRouterMaterialClient) error {
			_, err := c.CreateAssetGroup(context.Background(), "", nil)
			return err
		}},
		{"long description", func(c *TopenRouterMaterialClient) error {
			_, err := c.CreateAssetGroup(context.Background(), "group", common.GetPointer(strings.Repeat("x", 301)))
			return err
		}},
		{"invalid source", func(c *TopenRouterMaterialClient) error {
			_, err := c.CreateAsset(context.Background(), MobileCloudCreateAssetInput{URL: "https://user:password@source.example/image", Type: "Image"})
			return err
		}},
		{"invalid type", func(c *TopenRouterMaterialClient) error {
			_, err := c.CreateAsset(context.Background(), MobileCloudCreateAssetInput{URL: "https://source.example/image", Type: "Document"})
			return err
		}},
		{"zero page", func(c *TopenRouterMaterialClient) error {
			_, err := c.ListAssets(context.Background(), MobileCloudAssetQuery{PageNo: common.GetPointer(int32(0))})
			return err
		}},
		{"oversized page", func(c *TopenRouterMaterialClient) error {
			_, err := c.ListAssetGroups(context.Background(), MobileCloudGroupQuery{PageSize: common.GetPointer(int32(201))})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer server.Close()
			err := tc.call(newTopenRouterMaterialTestClient(t, server))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "password")
			assert.Equal(t, int32(0), calls.Load())
		})
	}
}

func TestTopenRouterMaterialResponseSizeBoundary(t *testing.T) {
	body := `{"Result":{"Id":"asset-1","Status":"Processing"},"padding":""}`
	body = strings.Replace(body, `"padding":""`, `"padding":"`+strings.Repeat("x", (4<<20)-len(body))+`"`, 1)
	require.Len(t, body, 4<<20)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
	defer server.Close()
	client := newTopenRouterMaterialTestClient(t, server)
	id, err := client.CreateAsset(context.Background(), MobileCloudCreateAssetInput{GroupID: "group-1", Name: "portrait", Type: "Image", URL: "https://source.example/input.png"})
	require.NoError(t, err)
	assert.Equal(t, "asset-1", id)
}
