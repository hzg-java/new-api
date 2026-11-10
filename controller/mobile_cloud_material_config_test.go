package controller

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func TestTopenRouterMaterialConfiguration(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	const optionKey = "TopenRouterMaterialConfigSecret"
	const endpoint = "https://tp-api.chinadatapay.com:8000"
	const path = "/api/material/topenrouter/config"
	require.NoError(t, db.Where("key IN ?", []string{optionKey, model.MobileCloudMaterialConfigOption}).Delete(&model.Option{}).Error)
	router := newAdminMaterialRouter(t)
	admin := &users[0]
	requestAdminMaterial(t, router, nil, http.MethodGet, path, "", http.StatusUnauthorized, false)
	requestAdminMaterial(t, router, &users[1], http.MethodPut, path, `{}`, http.StatusForbidden, false)
	response := requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusOK, true)
	var view struct {
		Available       bool   `json:"available"`
		Configured      bool   `json:"configured"`
		Endpoint        string `json:"endpoint"`
		TokenConfigured bool   `json:"token_configured"`
		ChannelID       int    `json:"channel_id"`
		ChannelName     string `json:"channel_name"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.True(t, view.Available)
	assert.False(t, view.Configured)
	assert.Equal(t, endpoint, view.Endpoint)
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","token":""}`, http.StatusBadRequest, false)
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","token":"test-topenrouter-secret"}`, http.StatusBadRequest, false)
	channel := model.Channel{Name: "TopenRouter video account", Key: "test-topenrouter-secret", BaseURL: common.GetPointer(endpoint), Models: "seedance-2.0", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	response = requestAdminMaterial(t, router, admin, http.MethodPut, path, fmt.Sprintf(`{"channel_id":%d,"endpoint":%q,"token":"test-topenrouter-secret"}`, channel.Id, endpoint), http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.True(t, view.Configured)
	assert.True(t, view.TokenConfigured)
	require.Positive(t, view.ChannelID)
	assert.NotContains(t, string(response.Data), "test-topenrouter-secret")
	var stored model.Option
	require.NoError(t, db.Where("key = ?", optionKey).First(&stored).Error)
	assert.NotContains(t, stored.Value, "test-topenrouter-secret")
	require.Equal(t, channel.Id, view.ChannelID)
	var unchanged model.Channel
	require.NoError(t, db.First(&unchanged, view.ChannelID).Error)
	assert.Equal(t, channel.Name, view.ChannelName)
	assert.Equal(t, channel.Key, unchanged.Key)
	assert.Equal(t, channel.Models, unchanged.Models)
	assert.Equal(t, channel.GetBaseURL(), unchanged.GetBaseURL(), "saving material configuration must not change the video destination")
	for _, invalid := range []model.Channel{
		{Name: "disabled video", Key: "key", Status: common.ChannelStatusManuallyDisabled},
		{Name: "multi-key video", Key: "key", Status: common.ChannelStatusEnabled, ChannelInfo: model.ChannelInfo{IsMultiKey: true}},
		{Name: "material-only", Type: 8, Key: "material-managed:test", Status: common.ChannelStatusEnabled},
	} {
		require.NoError(t, db.Create(&invalid).Error)
		requestAdminMaterial(t, router, admin, http.MethodPut, path, fmt.Sprintf(`{"channel_id":%d,"endpoint":%q,"token":"test-token"}`, invalid.Id, endpoint), http.StatusBadRequest, false)
	}
	materialOnly := model.Channel{Type: 8, Key: "material-managed:test"}
	assert.Empty(t, selectChannelsForAutomaticTest([]*model.Channel{&materialOnly}, "scheduled_all"))
	result := testChannel(context.Background(), &materialOnly, 0, "", "", false)
	require.ErrorContains(t, result.localErr, "material-only")
	channels, err := service.LoadMaterialChannels()
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Equal(t, "topenrouter", channels[0].Provider)
	assert.Equal(t, channel.Id, channels[0].ChannelID)
	scope := channels[0].Scope
	response = requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","token":""}`, http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.Equal(t, channel.Id, view.ChannelID)
	channels, err = service.LoadMaterialChannels()
	require.NoError(t, err)
	assert.Equal(t, scope, channels[0].Scope)
	response = requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`/v1/api/assets/","token":""}`, http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.Equal(t, endpoint, view.Endpoint)
	channels, err = service.LoadMaterialChannels()
	require.NoError(t, err)
	assert.Equal(t, scope, channels[0].Scope, "equivalent endpoint paths must preserve existing resource mappings")
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","token":"replacement-topenrouter-secret"}`, http.StatusOK, true)
	channels, err = service.LoadMaterialChannels()
	require.NoError(t, err)
	assert.NotEqual(t, scope, channels[0].Scope, "a different token must not reuse another account's resource mappings")
	scope = channels[0].Scope
	require.NoError(t, db.Model(&channel).Update("key", "replacement-video-account").Error)
	_, err = service.LoadMaterialChannels()
	require.Error(t, err, "changed video credentials require explicit rebinding before material operations")
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","token":""}`, http.StatusOK, true)
	channels, err = service.LoadMaterialChannels()
	require.NoError(t, err)
	assert.NotEqual(t, scope, channels[0].Scope, "rebinding a changed video account must not reuse its old resource mappings")
	for _, invalid := range []string{
		`{"endpoint":"http://localhost","token":"test-token"}`,
		`{"endpoint":"` + endpoint + `/?token=secret","token":"test-token"}`,
		`{"endpoint":"` + endpoint + `","token":"bad\r\ntoken"}`,
		`{"endpoint":"` + endpoint + `","token":"test-token"} {}`,
	} {
		requestAdminMaterial(t, router, admin, http.MethodPut, path, invalid, http.StatusBadRequest, false)
	}
	require.Error(t, model.UpdateOption(optionKey, "plaintext"))
	require.Error(t, model.UpdateOptionsBulk(map[string]string{optionKey: "plaintext"}))
	mobileChannel := model.Channel{Name: "Mobile Cloud coexistence", Key: "unused-key", Status: 1}
	require.NoError(t, db.Create(&mobileChannel).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{
		ChannelID: mobileChannel.Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint,
		AccessKey: "test-coexistence-ak", SecretKey: "test-coexistence-sk", PoolID: "test-coexistence-pool",
	}))
	channels, err = service.LoadMaterialChannels()
	require.NoError(t, err)
	require.Len(t, channels, 2)
	assert.Equal(t, "mobile_cloud", channels[0].Provider)
	assert.Equal(t, mobileChannel.Id, channels[0].ChannelID)
	assert.Equal(t, "topenrouter", channels[1].Provider)
	assert.Equal(t, channel.Id, channels[1].ChannelID)
	assert.NotEqual(t, channels[0].Scope, channels[1].Scope)
	t.Run("one video channel cannot use two material suppliers", func(t *testing.T) {
		requestAdminMaterial(t, router, admin, http.MethodPut, path, fmt.Sprintf(`{"channel_id":%d,"endpoint":%q,"token":"test-token"}`, mobileChannel.Id, endpoint), http.StatusBadRequest, false)
		err := service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{
			ChannelID: channel.Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint,
			AccessKey: "test-ak", SecretKey: "test-sk", PoolID: "test-pool",
		})
		assert.ErrorIs(t, err, service.ErrMobileCloudMaterialInput)
	})
	t.Run("healthy in-flight bindings remain queued after aggregate failure", func(t *testing.T) {
		provider := &materialPreparationProvider{status: "PROCESSING"}
		material := model.Material{UserId: users[1].Id, MaterialID: model.NewPlatformMaterialID(), Name: "in-flight supplier isolation", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?inflight", Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		targets := []service.MaterialChannel{
			{ChannelID: mobileChannel.Id, Provider: "mobile_cloud", Scope: channels[0].Scope},
			{ChannelID: channel.Id, Provider: "topenrouter", Scope: channels[1].Scope, Client: provider},
		}
		for _, target := range targets {
			require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: target.Scope, ChannelID: target.ChannelID, Provider: target.Provider, Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: common.GetTimestamp()}).Error)
		}
		require.Error(t, service.SyncMaterial(t.Context(), &material, targets))
		require.NoError(t, db.First(&material, material.Id).Error)
		require.Equal(t, "failed", material.Status)
		var queued []model.Material
		require.NoError(t, service.MaterialPreparationQueue(db, []string{targets[1].Scope}).Where("user_id = ?", users[1].Id).Find(&queued).Error)
		require.Contains(t, queued, material, "a healthy processing binding must remain pollable")
		provider.status = "ACTIVE"
		require.Error(t, service.SyncMaterial(t.Context(), &material, targets))
		var binding model.MaterialBinding
		require.NoError(t, db.Where("material_id = ? AND scope = ?", material.Id, targets[1].Scope).First(&binding).Error)
		assert.Equal(t, "ready", binding.State)
		assert.Equal(t, 1, provider.assets, "continued polling must not repeat an upload")
		queued = nil
		require.NoError(t, service.MaterialPreparationQueue(db, []string{targets[1].Scope}).Where("id = ?", material.Id).Find(&queued).Error)
		assert.Empty(t, queued, "a completed healthy binding must not auto-retry the unavailable supplier")
	})
	for _, unavailableID := range []int{channel.Id, mobileChannel.Id} {
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", unavailableID).Update("status", common.ChannelStatusManuallyDisabled).Error)
		available, err := service.LoadMaterialChannels()
		require.NoError(t, err, "one supplier must not stop another supplier")
		require.Len(t, available, 2)
		material := model.Material{UserId: users[1].Id, MaterialID: model.NewPlatformMaterialID(), Name: "supplier isolation", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?case=" + common.GetUUID(), Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		provider := &materialPreparationProvider{status: "ACTIVE"}
		for i := range available {
			if available[i].Client != nil {
				available[i].Client = provider
			}
			target := available[i]
			require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: target.Scope, ChannelID: target.ChannelID, ChannelName: target.ChannelName, Provider: target.Provider, Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: common.GetTimestamp()}).Error)
		}
		require.Error(t, service.SyncMaterial(context.Background(), &material, available))
		assert.Equal(t, 1, provider.assets, "the healthy supplier still uploads")
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "failed", material.Status)
		assert.True(t, material.CanRetry)
		require.NoError(t, db.Model(&material).Update("deleting", true).Error)
		require.NoError(t, service.SyncMaterial(context.Background(), &material, available))
		assert.Equal(t, 1, provider.assetDeletes, "the healthy supplier still deletes")
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "failed", material.Status, "unavailable mappings remain recoverable")
		for _, target := range available {
			if target.ChannelID == unavailableID {
				assert.Nil(t, target.Client, "the failed scope remains visible but cannot make requests")
			} else {
				assert.NotNil(t, target.Client, "the healthy supplier remains usable")
			}
		}
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", unavailableID).Update("status", common.ChannelStatusEnabled).Error)
	}
	for _, brokenKey := range []string{model.MobileCloudMaterialConfigOption, optionKey} {
		var original model.Option
		require.NoError(t, db.Where("key = ?", brokenKey).First(&original).Error)
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", brokenKey).Update("value", "corrupted-encrypted-config").Error)
		available, err := service.LoadMaterialChannels()
		require.Error(t, err, "configuration damage must remain visible")
		require.Len(t, available, 1, "the other supplier must remain available")
		assert.NotNil(t, available[0].Client)
		assert.NotContains(t, err.Error(), "corrupted-encrypted-config")
		assert.NotContains(t, err.Error(), "test-topenrouter-secret")
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", brokenKey).Update("value", original.Value).Error)
	}
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "invalid")
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","token":"test-token"}`, http.StatusServiceUnavailable, false)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
	missingKey := filepath.Join(t.TempDir(), "missing-material.key")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE", missingKey)
	require.NoError(t, db.Where("key = ?", optionKey).Delete(&model.Option{}).Error)
	requestAdminMaterial(t, router, admin, http.MethodPut, path, fmt.Sprintf(`{"channel_id":%d,"endpoint":%q,"token":"test-token"}`, channel.Id, endpoint), http.StatusServiceUnavailable, false)
	_, err = os.Stat(missingKey)
	assert.ErrorIs(t, err, os.ErrNotExist, "missing encryption keys must not be regenerated while another supplier has encrypted credentials")
	require.NoError(t, db.Create(&stored).Error)
	require.NoError(t, db.Where("key = ?", model.MobileCloudMaterialConfigOption).Delete(&model.Option{}).Error)
	err = service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{
		ChannelID: mobileChannel.Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint,
		AccessKey: "test-coexistence-ak", SecretKey: "test-coexistence-sk", PoolID: "test-coexistence-pool",
	})
	assert.ErrorIs(t, err, service.ErrMobileCloudMaterialEncryption)
	_, err = os.Stat(missingKey)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMaterialIntegrationWithoutTestEnvironment(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	router := newAdminMaterialRouter(t)
	for _, enabled := range []string{"", "false"} {
		for _, provider := range []string{"mobile-cloud", "topenrouter"} {
			t.Run(provider+"/test-enabled="+enabled, func(t *testing.T) {
				t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", enabled)
				require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
				path := "/api/material/" + provider + "/config"
				response := requestAdminMaterial(t, router, &users[0], http.MethodGet, path, "", http.StatusOK, true)
				var view map[string]any
				require.NoError(t, common.Unmarshal(response.Data, &view))
				require.Equal(t, true, view["available"])
				assert.Equal(t, false, view["configured"])

				channel := model.Channel{Name: provider + " configuration", Key: "video-key", Status: common.ChannelStatusEnabled}
				require.NoError(t, db.Create(&channel).Error)
				optionKey := model.MobileCloudMaterialConfigOption
				input := map[string]any{"channel_id": channel.Id, "endpoint": service.MobileCloudMaterialDefaultEndpoint, "access_key": "config-access", "secret_key": "config-secret", "pool_id": "config-pool"}
				if provider == "topenrouter" {
					optionKey = model.TopenRouterMaterialConfigOption
					input = map[string]any{"channel_id": channel.Id, "endpoint": service.TopenRouterMaterialDefaultEndpoint, "token": "config-secret"}
				}
				body, err := common.Marshal(input)
				require.NoError(t, err)
				requestAdminMaterial(t, router, nil, http.MethodPut, path, string(body), http.StatusUnauthorized, false)
				requestAdminMaterial(t, router, &users[1], http.MethodPut, path, string(body), http.StatusForbidden, false)
				response = requestAdminMaterial(t, router, &users[0], http.MethodPut, path, string(body), http.StatusOK, true)
				require.NoError(t, common.Unmarshal(response.Data, &view))
				assert.Equal(t, true, view["available"])
				assert.Equal(t, true, view["configured"])
				assert.NotContains(t, string(response.Data), "config-secret")
				var stored model.Option
				require.NoError(t, db.Where("key = ?", optionKey).First(&stored).Error)
				assert.NotContains(t, stored.Value, "config-secret")
				require.Error(t, model.UpdateOption(optionKey, "plaintext"))
				require.Error(t, model.UpdateOptionsBulk(map[string]string{optionKey: "plaintext"}))
				channels, err := service.LoadMaterialChannels()
				require.NoError(t, err)
				require.Len(t, channels, 1)
				assert.Equal(t, channel.Id, channels[0].ChannelID)
				assert.NotNil(t, channels[0].Client)
				requestBody := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://unknown-reference"}}}}
				_, issues, reservation, err := service.PrepareVideoMaterialRequest(t.Context(), users[1].Id, &channel, requestBody)
				require.NoError(t, err)
				assert.NotEmpty(t, issues, "configured channels must enforce material ownership without a test flag")
				assert.Nil(t, reservation)
			})
		}
	}
}

func TestMaterialIntegrationEnablement(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "")
	const masterKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", masterKey)
	router := newAdminMaterialRouter(t)
	for _, provider := range []string{"mobile-cloud", "topenrouter"} {
		t.Run(provider, func(t *testing.T) {
			require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
			channel := model.Channel{Name: provider + " enablement", Key: "video-key", Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			optionKey := model.MobileCloudMaterialConfigOption
			input := map[string]any{"channel_id": channel.Id, "endpoint": service.MobileCloudMaterialDefaultEndpoint, "access_key": "toggle-ak", "secret_key": "toggle-sk", "pool_id": "toggle-pool"}
			if provider == "topenrouter" {
				optionKey = model.TopenRouterMaterialConfigOption
				input = map[string]any{"channel_id": channel.Id, "endpoint": service.TopenRouterMaterialDefaultEndpoint, "token": "toggle-token"}
			}
			encoded, err := common.Marshal(input)
			require.NoError(t, err)
			path := "/api/material/" + provider + "/config"
			response := requestAdminMaterial(t, router, &users[0], http.MethodPut, path, string(encoded), http.StatusOK, true)
			var view map[string]any
			require.NoError(t, common.Unmarshal(response.Data, &view))
			require.Equal(t, true, view["enabled"], "existing setup without an enabled field remains enabled")
			channels, err := service.LoadMaterialChannels()
			require.NoError(t, err)
			require.Len(t, channels, 1)
			scope := channels[0].Scope
			for _, key := range []string{"access_key", "secret_key", "token"} {
				if _, present := input[key]; present {
					input[key] = ""
				}
			}
			input["enabled"] = false
			encoded, err = common.Marshal(input)
			require.NoError(t, err)
			requestAdminMaterial(t, router, &users[1], http.MethodPut, path, string(encoded), http.StatusForbidden, false)
			requestAdminMaterial(t, router, &users[0], http.MethodPut, path, string(encoded), http.StatusOK, true)
			response = requestAdminMaterial(t, router, &users[0], http.MethodGet, path, "", http.StatusOK, true)
			require.NoError(t, common.Unmarshal(response.Data, &view))
			assert.Equal(t, false, view["enabled"])
			assert.Equal(t, true, view["configured"])
			channels, err = service.LoadMaterialChannels()
			require.NoError(t, err, "manual material management remains available")
			require.Len(t, channels, 1)
			assert.Equal(t, scope, channels[0].Scope)
			assert.NotNil(t, channels[0].Client)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
			body := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://unknown-reference"}}}}
			prepared, issues, reservation, err := service.PrepareVideoMaterialRequest(t.Context(), users[1].Id, &channel, body)
			require.NoError(t, err)
			assert.Equal(t, body, prepared)
			assert.Empty(t, issues)
			assert.Nil(t, reservation)
			delete(input, "enabled")
			encoded, err = common.Marshal(input)
			require.NoError(t, err)
			response = requestAdminMaterial(t, router, &users[0], http.MethodPut, path, string(encoded), http.StatusOK, true)
			require.NoError(t, common.Unmarshal(response.Data, &view))
			assert.Equal(t, false, view["enabled"], "omitting enabled must not silently re-enable a disabled integration")
			input["enabled"] = true
			encoded, err = common.Marshal(input)
			require.NoError(t, err)
			requestAdminMaterial(t, router, &users[0], http.MethodPut, path, string(encoded), http.StatusOK, true)
			_, issues, _, err = service.PrepareVideoMaterialRequest(t.Context(), users[1].Id, &channel, body)
			require.NoError(t, err)
			assert.NotEmpty(t, issues, "re-enabling restores material validation")
			var stored model.Option
			require.NoError(t, db.Where("key = ?", optionKey).First(&stored).Error)
			assert.NotContains(t, stored.Value, "toggle-")
			key, err := base64.StdEncoding.DecodeString(masterKey)
			require.NoError(t, err)
			block, err := aes.NewCipher(key)
			require.NoError(t, err)
			aead, err := cipher.NewGCM(block)
			require.NoError(t, err)
			parts := strings.SplitN(stored.Value, ".", 3)
			require.Len(t, parts, 3)
			sealed, err := base64.StdEncoding.DecodeString(parts[2])
			require.NoError(t, err)
			associatedData := optionKey + ":v2:" + parts[1]
			plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(associatedData))
			require.NoError(t, err)
			var legacy map[string]any
			require.NoError(t, common.Unmarshal(plain, &legacy))
			delete(legacy, "enabled")
			plain, err = common.Marshal(legacy)
			require.NoError(t, err)
			nonce := make([]byte, aead.NonceSize())
			sealed = aead.Seal(nonce, nonce, plain, []byte(associatedData))
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", optionKey).Update("value", "v2."+parts[1]+"."+base64.StdEncoding.EncodeToString(sealed)).Error)
			response = requestAdminMaterial(t, router, &users[0], http.MethodGet, path, "", http.StatusOK, true)
			require.NoError(t, common.Unmarshal(response.Data, &view))
			assert.Equal(t, true, view["enabled"], "legacy encrypted configurations default to enabled")
		})
	}
}

func TestMaterialIntegrationConfigurationSerializesSuppliers(t *testing.T) {
	db, _ := setupAdminMaterialPostgres(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	isolated, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "material_config_" + strings.ReplaceAll(common.GetUUID(), "-", "") + "_"},
		Logger:         db.Logger,
	})
	require.NoError(t, err)
	require.NoError(t, isolated.AutoMigrate(&model.Option{}))
	t.Cleanup(func() { require.NoError(t, isolated.Migrator().DropTable(&model.Option{})) })
	keys := []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}
	require.NoError(t, isolated.Create(&[]model.Option{{Key: keys[0]}, {Key: keys[1]}}).Error)
	previousDB := model.DB
	t.Cleanup(func() { model.DB = previousDB })
	for i, key := range keys {
		t.Run(key, func(t *testing.T) {
			writer := isolated.Begin()
			require.NoError(t, writer.Error)
			t.Cleanup(func() { require.NoError(t, writer.Rollback().Error) })
			model.DB = writer
			err := model.UpdateMaterialIntegrationConfig(key, func(_ *gorm.DB, _ string) (string, error) {
				contender := isolated.Begin()
				require.NoError(t, contender.Error)
				defer contender.Rollback()
				var other model.Option
				err := contender.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).Where("key = ?", keys[1-i]).First(&other).Error
				var pgErr *pgconn.PgError
				if assert.ErrorAs(t, err, &pgErr, "the other supplier must be locked before binding validation") {
					assert.Equal(t, "55P03", pgErr.Code)
				}
				return "test-config", nil
			})
			require.NoError(t, err)
		})
	}
}

func TestMobileCloudMaterialConfig(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE", filepath.Join(t.TempDir(), "material.key"))
	const key = "MobileCloudMaterialConfigSecret"
	const endpoint = "https://ecloud.10086.cn/api/openapi-maas"
	require.NoError(t, db.Where("key IN ?", []string{key, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	router := newAdminMaterialRouter(t)
	const path = "/api/material/mobile-cloud/config"
	admin := &users[0]
	requestAdminMaterial(t, router, nil, http.MethodGet, path, "", http.StatusUnauthorized, false)
	requestAdminMaterial(t, router, &users[1], http.MethodGet, path, "", http.StatusForbidden, false)
	requestAdminMaterial(t, router, &users[1], http.MethodPut, path, `{}`, http.StatusForbidden, false)
	response := requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusOK, true)
	var view struct {
		Available           bool   `json:"available"`
		Configured          bool   `json:"configured"`
		EncryptionReady     bool   `json:"encryption_ready"`
		Endpoint            string `json:"endpoint"`
		AccessKeyMasked     string `json:"access_key_masked"`
		SecretKeyConfigured bool   `json:"secret_key_configured"`
		PoolID              string `json:"pool_id"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.True(t, view.Available)
	assert.True(t, view.EncryptionReady)
	assert.False(t, view.Configured)
	assert.Equal(t, endpoint, view.Endpoint)
	channel := model.Channel{Name: "Material test channel", Key: "unused-test-key", Status: 1}
	require.NoError(t, db.Create(&channel).Error)
	body := fmt.Sprintf(`{"channel_id":%d,"endpoint":%q,"access_key":"test-access-123456","secret_key":"test-secret-123456","pool_id":"test-pool"}`, channel.Id, endpoint)
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","pool_id":"test-pool"}`, http.StatusBadRequest, false)
	for _, id := range []int{0, -1, channel.Id + 1000000} {
		invalid := fmt.Sprintf(`{"channel_id":%d,"endpoint":%q,"access_key":"test-access-123456","secret_key":"test-secret-123456","pool_id":"test-pool"}`, id, endpoint)
		requestAdminMaterial(t, router, admin, http.MethodPut, path, invalid, http.StatusBadRequest, false)
	}
	t.Run("channel validation is bounded by the save deadline", func(t *testing.T) {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("material_config_test_deadline", func(tx *gorm.DB) {
			if tx.Statement.Table == "channels" {
				if _, bounded := tx.Statement.Context.Deadline(); !bounded {
					tx.AddError(fmt.Errorf("channel validation has no save deadline"))
				}
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("material_config_test_deadline")) })
		require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{
			ChannelID: channel.Id, Endpoint: endpoint, AccessKey: "test-access-123456", SecretKey: "test-secret-123456", PoolID: "test-pool",
		}))
	})
	response = requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusOK, true)
	assert.NotContains(t, string(response.Data), "test-access-123456")
	assert.NotContains(t, string(response.Data), "test-secret-123456")
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.True(t, view.Configured)
	assert.True(t, view.SecretKeyConfigured)
	assert.NotEmpty(t, view.AccessKeyMasked)
	var stored model.Option
	require.NoError(t, db.Where("key = ?", key).First(&stored).Error)
	assert.NotContains(t, stored.Value, "test-access-123456")
	assert.NotContains(t, stored.Value, "test-secret-123456")
	assert.NotContains(t, stored.Value, "test-pool")
	config, err := service.LoadMobileCloudMaterialConfiguration()
	require.NoError(t, err)
	assert.Equal(t, "test-access-123456", config.AccessKey)
	assert.Equal(t, "test-secret-123456", config.SecretKey)
	assert.Equal(t, channel.Id, config.ChannelID)
	configuredView, err := service.GetMobileCloudMaterialConfigView()
	require.NoError(t, err)
	assert.Equal(t, channel.Id, configuredView.ChannelID)
	assert.Equal(t, channel.Name, configuredView.ChannelName)
	client, err := service.LoadMobileCloudMaterialClient()
	require.NoError(t, err)
	require.NotNil(t, client)
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","access_key":"","secret_key":"","pool_id":"test-pool-2"}`, http.StatusOK, true)
	config, err = service.LoadMobileCloudMaterialConfiguration()
	require.NoError(t, err)
	assert.Equal(t, "test-access-123456", config.AccessKey)
	assert.Equal(t, "test-secret-123456", config.SecretKey)
	assert.Equal(t, "test-pool-2", config.PoolID)
	for _, invalid := range []string{
		`{"endpoint":"http://localhost","pool_id":"test-pool"}`,
		`{"endpoint":"` + endpoint + `","secret_key":"   ","pool_id":"test-pool"}`,
		`{"endpoint":"` + endpoint + `","pool_id":"bad\r\nPool"}`,
		`{"endpoint":"` + endpoint + `","access_key":"` + strings.Repeat("a", 1025) + `","pool_id":"test-pool"}`,
		`{"endpoint":"` + endpoint + `","pool_id":"test-pool"} {}`,
	} {
		requestAdminMaterial(t, router, admin, http.MethodPut, path, invalid, http.StatusBadRequest, false)
	}
	require.Error(t, model.UpdateOption(key, "plaintext"))
	require.Error(t, model.UpdateOptionsBulk(map[string]string{key: "plaintext"}))
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"`+endpoint+`","access_key":"replacement-access","secret_key":"replacement-secret","pool_id":"test-pool-2"}`, http.StatusOK, true)
	config, err = service.LoadMobileCloudMaterialConfiguration()
	require.NoError(t, err)
	assert.Equal(t, "replacement-access", config.AccessKey)
	assert.Equal(t, "replacement-secret", config.SecretKey)
	var replaced model.Option
	require.NoError(t, db.Where("key = ?", key).First(&replaced).Error)
	assert.NotEqual(t, stored.Value, replaced.Value)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
	requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusServiceUnavailable, false)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "invalid")
	requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusServiceUnavailable, false)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODk=")
	requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusServiceUnavailable, false)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", key).Update("value", replaced.Value[:len(replaced.Value)-8]+"AAAAAAAA").Error)
	requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusServiceUnavailable, false)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", key).Update("value", replaced.Value).Error)

	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+admin.GetAccessToken())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
}

func TestMaterialIntegrationQueryTests(t *testing.T) {
	var logs bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &logs
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	mode, calls := "success", 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, http.MethodPost, r.Method)
		if mode == "EOF" {
			conn, _, err := w.(http.Hijacker).Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if mode == "failure" {
			w.WriteHeader(http.StatusForbidden)
			if r.URL.Path == "/v1/api/assets/" {
				_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"Unauthorized","Message":"Access denied test-token https://private.example/query?Signature=private-signature"}}}`))
			} else {
				_, _ = w.Write([]byte(`{"state":"ERROR","errorMessage":"Invalid signature https://private.example/query?Signature=private-signature Authorization: Bearer private-token"}`))
			}
			return
		}
		if mode == "secret" {
			_, _ = w.Write([]byte(`{"state":"ERROR","errorMessage":"test-ak test-sk test-pool private-signature"}`))
			return
		}
		if mode == "malformed-url" {
			_, _ = w.Write([]byte(`{"state":"ERROR","errorMessage":"Invalid signature https://[bad/p?Signature=private-signature"}`))
			return
		}
		var input map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &input))
		if strings.HasPrefix(r.URL.Path, "/api/openapi-maas/") {
			assert.Equal(t, "test-ak", r.URL.Query().Get("AccessKey"))
			assert.NotEmpty(t, r.URL.Query().Get("Signature"))
			assert.Equal(t, "test-pool", r.Header.Get("Pool-Id"))
			assert.EqualValues(t, 1, input["pageNo"])
			assert.EqualValues(t, 1, input["pageSize"])
			assert.Equal(t, "AIGC", input["groupType"])
			assert.Contains(t, []string{"/api/openapi-maas/exp/aicc/v2/asset-group/query", "/api/openapi-maas/exp/aicc/v2/asset/query"}, r.URL.Path)
			_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[],"total":0}}`))
			return
		}
		assert.Equal(t, "/v1/api/assets/", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Contains(t, []string{"ListAssetGroups", "ListAssets"}, r.URL.Query().Get("Action"))
		assert.EqualValues(t, 1, input["Page"])
		assert.EqualValues(t, 1, input["PageSize"])
		_, _ = w.Write([]byte(`{"Result":{"Items":[],"TotalCount":0}}`))
	}))
	defer server.Close()
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = (&tls.Dialer{Config: &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}).DialContext
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	channels := []model.Channel{{Name: "Mobile test", Key: "model-only", Status: 1}, {Name: "Topen test", Key: "other-model", Status: 1}}
	require.NoError(t, db.Create(&channels).Error)
	router := newAdminMaterialRouter(t)
	for _, provider := range []string{"mobile-cloud", "topenrouter"} {
		path := "/api/material/" + provider + "/test"
		requestAdminMaterial(t, router, nil, http.MethodPost, path, `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusUnauthorized, false)
		requestAdminMaterial(t, router, &users[1], http.MethodPost, path, `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusForbidden, false)
		requestAdminMaterial(t, router, &users[0], http.MethodPost, path, `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusServiceUnavailable, false)
	}
	assert.Zero(t, calls)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{ChannelID: channels[0].Id, Endpoint: server.URL, AccessKey: "test-ak", SecretKey: "test-sk", PoolID: "test-pool"}))
	require.NoError(t, service.SaveTopenRouterMaterialConfiguration(service.TopenRouterMaterialConfiguration{ChannelID: channels[1].Id, Endpoint: server.URL, Token: "test-token"}))
	var before []model.Option
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Order("key").Find(&before).Error)
	diagnosticIDs := make(map[string]bool)
	for index, provider := range []string{"mobile-cloud", "topenrouter"} {
		path := "/api/material/" + provider + "/test"
		for _, operation := range []string{"list_groups", "list_assets"} {
			previousCalls := calls
			logs.Reset()
			response := requestAdminMaterial(t, router, &users[0], http.MethodPost, path, fmt.Sprintf(`{"operation":%q,"parameters":{"pageNo":1,"pageSize":1%s}}`, operation, map[string]string{"mobile-cloud": `,"groupType":"AIGC"`}[provider]), http.StatusOK, true)
			var result struct {
				DiagnosticID string `json:"diagnostic_id"`
				ChannelID    int    `json:"channel_id"`
				Success      bool   `json:"success"`
				Operation    string `json:"operation"`
				Total        *int64 `json:"total"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &result))
			require.Regexp(t, `^[a-f0-9]{32}$`, result.DiagnosticID)
			assert.False(t, diagnosticIDs[result.DiagnosticID])
			diagnosticIDs[result.DiagnosticID] = true
			assert.Contains(t, logs.String(), "material_integration_test diagnostic_id="+result.DiagnosticID+" ")
			if provider == "mobile-cloud" {
				assert.Contains(t, logs.String(), "mobile_cloud_material_request diagnostic_id="+result.DiagnosticID+" ")
			}
			assert.True(t, result.Success)
			assert.Equal(t, channels[index].Id, result.ChannelID)
			assert.Equal(t, operation, result.Operation)
			require.NotNil(t, result.Total)
			assert.Zero(t, *result.Total)
			assert.Equal(t, previousCalls+1, calls)
			for _, secret := range []string{"test-ak", "test-sk", "test-pool", "test-token", "model-only", "Signature="} {
				assert.NotContains(t, string(response.Data), secret)
			}
		}
		previousCalls := calls
		for _, body := range []string{`{}`, `{"operation":"create_group"}`, `{"operation":"delete_asset"}`, `{"operation":"list_groups"} {}`, `{"operation":"list_groups","endpoint":"https://other.example","token":"injected"}`, `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1},"diagnostic_id":"client-supplied"}`} {
			requestAdminMaterial(t, router, &users[0], http.MethodPost, path, body, http.StatusBadRequest, false)
		}
		assert.Equal(t, previousCalls, calls)
	}
	mode = "failure"
	logs.Reset()
	response := requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusOK, true)
	var failedResult struct {
		DiagnosticID string `json:"diagnostic_id"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &failedResult))
	require.Regexp(t, `^[a-f0-9]{32}$`, failedResult.DiagnosticID)
	assert.Contains(t, logs.String(), "mobile_cloud_material_request diagnostic_id="+failedResult.DiagnosticID+" ")
	assert.Contains(t, logs.String(), "material_integration_test diagnostic_id="+failedResult.DiagnosticID+" ")
	assert.Contains(t, string(response.Data), "Invalid signature")
	assert.Contains(t, string(response.Data), `"success":false`)
	for _, secret := range []string{"private.example", "private-signature", "private-token", "test-ak", "test-sk"} {
		assert.NotContains(t, string(response.Data), secret)
	}
	response = requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/topenrouter/test", `{"operation":"list_assets","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusOK, true)
	assert.Contains(t, string(response.Data), "Access denied")
	assert.Contains(t, string(response.Data), `"success":false`)
	for _, secret := range []string{"test-token", "private.example", "private-signature"} {
		assert.NotContains(t, string(response.Data), secret)
	}
	mode = "EOF"
	logs.Reset()
	previousCalls := calls
	response = requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &failedResult))
	require.Regexp(t, `^[a-f0-9]{32}$`, failedResult.DiagnosticID)
	assert.Contains(t, string(response.Data), `"success":false`)
	assert.Contains(t, logs.String(), "mobile_cloud_material_request diagnostic_id="+failedResult.DiagnosticID+" ")
	assert.Contains(t, logs.String(), "material_integration_test diagnostic_id="+failedResult.DiagnosticID+" ")
	assert.Equal(t, previousCalls+1, calls, "diagnostic IDs must not cause retries")
	mode = "secret"
	response = requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"list_assets","parameters":{"pageNo":1,"pageSize":1,"groupType":"AIGC"}}`, http.StatusOK, true)
	for _, secret := range []string{"private-signature", "test-ak", "test-sk", "test-pool"} {
		assert.NotContains(t, string(response.Data), secret)
	}
	mode = "malformed-url"
	response = requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusOK, true)
	assert.Contains(t, string(response.Data), "Invalid signature")
	assert.NotContains(t, string(response.Data), "private-signature")
	assert.NotContains(t, string(response.Data), "https://[bad")
	previousCalls = calls
	for _, provider := range []string{"mobile-cloud", "topenrouter"} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result, err := service.TestMaterialIntegration(ctx, provider, service.MaterialIntegrationTestRequest{Operation: "list_groups", Parameters: service.MaterialIntegrationTestParameters{PageNo: common.GetPointer(int32(1)), PageSize: common.GetPointer(int32(1))}})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, previousCalls, calls, "canceled tests must not reach the upstream")
	}
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusServiceUnavailable, false)
	assert.Equal(t, previousCalls, calls)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Model(&channels[0]).Update("key", "changed-video-key").Error)
	requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1}}`, http.StatusServiceUnavailable, false)
	assert.Equal(t, previousCalls, calls)
	var after []model.Option
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Order("key").Find(&after).Error)
	assert.Equal(t, before, after, "testing must never save credentials or capability state")
}

func TestMaterialIntegrationManualOperations(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	var requests []string
	mode := "success"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil && r.ContentLength != 0 {
			require.NoError(t, common.DecodeJson(r.Body, &body))
		}
		requests = append(requests, r.Method+" "+r.URL.Path+" "+r.URL.Query().Get("Action"))
		w.Header().Set("Content-Type", "application/json")
		if mode == "unknown" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`not-json`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/openapi-maas/") {
			assert.Equal(t, "test-ak", r.URL.Query().Get("AccessKey"))
			assert.NotEmpty(t, r.URL.Query().Get("Signature"))
			switch r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/openapi-maas/exp/aicc/v2") {
			case "POST /asset-group":
				assert.Equal(t, map[string]any{"groupName": "manual group", "groupType": "AIGC", "description": ""}, body)
				_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"group-1","groupType":"AIGC","groupName":"manual group"}}`))
			case "POST /asset":
				assert.Equal(t, "group-1", body["groupId"])
				assert.Equal(t, "https://public.example/input.png", body["assetUrl"])
				_, _ = w.Write([]byte(`{"state":"OK","body":"asset-1"}`))
			case "GET /asset/asset-1", "PUT /asset/asset-1":
				_, _ = w.Write([]byte(`{"state":"OK","body":{"assetId":"asset-1","groupId":"group-1","assetName":"test-sk","assetType":"Image","assetUrl":"https://private.example/secret?token=private-token","status":"ACTIVE"}}`))
			case "DELETE /asset/asset-1", "DELETE /asset-group/group-1":
				_, _ = w.Write([]byte(`{"state":"OK","body":true}`))
			case "GET /asset-group/group-1", "PUT /asset-group/group-1":
				_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"group-1","groupType":"AIGC","groupName":"manual group","description":"test-ak test-sk"}}`))
			case "POST /asset-group/query":
				assert.EqualValues(t, 2, body["pageNo"])
				assert.EqualValues(t, 7, body["pageSize"])
				assert.Equal(t, "manual", body["groupName"])
				_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[],"total":0}}`))
			case "POST /asset/query":
				assert.EqualValues(t, 3, body["pageNo"])
				assert.EqualValues(t, 9, body["pageSize"])
				assert.Equal(t, []any{"group-1"}, body["groupIds"])
				_, _ = w.Write([]byte(`{"state":"OK","body":{"data":[],"total":0}}`))
			default:
				assert.Fail(t, "unexpected mutation or extra request", "%s %s", r.Method, r.URL.Path)
			}
			return
		}
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		switch r.URL.Query().Get("Action") {
		case "CreateAssetGroup":
			assert.Equal(t, map[string]any{"Name": "manual group", "GroupType": "AIGC", "Description": ""}, body)
			_, _ = w.Write([]byte(`{"Result":{"Id":"group-1","GroupType":"AIGC","Name":"manual group"}}`))
		case "CreateAsset":
			assert.Equal(t, "https://public.example/input.png", body["URL"])
			assert.NotContains(t, body, "GroupId")
			assert.NotContains(t, body, "Name")
			_, _ = w.Write([]byte(`{"Result":{"Id":"asset-1","Status":"Active"}}`))
		case "ListAssetGroups", "ListAssets":
			assert.Positive(t, body["Page"])
			assert.Positive(t, body["PageSize"])
			_, _ = w.Write([]byte(`{"Result":{"Items":[],"TotalCount":0}}`))
		case "GetAssetGroup":
			_, _ = w.Write([]byte(`{"Result":{"Id":"group-1","GroupType":"AIGC","Name":"manual group"}}`))
		case "GetAsset":
			_, _ = w.Write([]byte(`{"Result":{"Id":"asset-1","GroupId":"group-1","AssetType":"Image","Status":"Active","URL":"https://private.example/secret?token=private-token"}}`))
		case "UpdateAssetGroup", "UpdateAsset", "DeleteAssetGroup", "DeleteAsset":
			_, _ = w.Write([]byte(`{"Result":{}}`))
		default:
			assert.Fail(t, "unexpected action", "%s", r.URL.Query().Get("Action"))
		}
	}))
	defer server.Close()
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = (&tls.Dialer{Config: &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}).DialContext
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	channels := []model.Channel{{Name: "Mobile manual", Key: "model-key", Status: 1}, {Name: "Topen manual", Key: "other-model-key", Status: 1}}
	require.NoError(t, db.Create(&channels).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{ChannelID: channels[0].Id, Endpoint: server.URL, AccessKey: "test-ak", SecretKey: "test-sk", PoolID: "test-pool"}))
	require.NoError(t, service.SaveTopenRouterMaterialConfiguration(service.TopenRouterMaterialConfiguration{ChannelID: channels[1].Id, Endpoint: server.URL, Token: "test-token"}))
	router := newAdminMaterialRouter(t)
	for _, provider := range []string{"mobile-cloud", "topenrouter"} {
		for _, tc := range []struct{ operation, parameters string }{
			{"create_group", `{"groupName":"manual group","description":""}`},
			{"list_groups", `{"pageNo":2,"pageSize":7}`},
			{"get_group", `{"groupId":"group-1"}`},
			{"update_group", `{"groupId":"group-1","description":""}`},
			{"create_asset", `{"assetUrl":"https://public.example/input.png","assetType":"Image"}`},
			{"list_assets", `{"pageNo":3,"pageSize":9,"groupIds":["group-1"]}`},
			{"get_asset", `{"assetId":"asset-1"}`},
			{"update_asset", `{"assetId":"asset-1","assetName":"renamed"}`},
			{"delete_asset", `{"assetId":"asset-1"}`},
			{"delete_group", `{"groupId":"group-1"}`},
		} {
			t.Run(provider+"/"+tc.operation, func(t *testing.T) {
				parameters := tc.parameters
				if provider == "mobile-cloud" {
					switch tc.operation {
					case "list_groups":
						parameters = `{"pageNo":2,"pageSize":7,"groupType":"AIGC","groupName":"manual"}`
					case "list_assets":
						parameters = `{"pageNo":3,"pageSize":9,"groupType":"AIGC","groupIds":["group-1"]}`
					case "create_asset":
						parameters = `{"groupId":"group-1","assetName":"image","assetUrl":"https://public.example/input.png","assetType":"Image"}`
					}
				}
				count := len(requests)
				body := fmt.Sprintf(`{"operation":%q,"parameters":%s,"confirmed":true}`, tc.operation, parameters)
				response := requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/"+provider+"/test", body, http.StatusOK, true)
				var result struct {
					Success  bool `json:"success"`
					Response any  `json:"response"`
				}
				require.NoError(t, common.Unmarshal(response.Data, &result))
				assert.True(t, result.Success)
				assert.NotNil(t, result.Response)
				expectedCalls := 1
				if provider == "topenrouter" && (tc.operation == "update_group" || tc.operation == "update_asset") {
					expectedCalls = 2
				}
				assert.Len(t, requests, count+expectedCalls)
				for _, secret := range []string{"test-ak", "test-sk", "test-token", "test-pool", "private.example", "private-token"} {
					assert.NotContains(t, string(response.Data), secret)
				}
			})
		}
	}
	count := len(requests)
	for _, body := range []string{
		`{"operation":"delete_asset","parameters":{"assetId":"asset-1"}}`,
		`{"operation":"delete_group","parameters":{"groupId":"group-1"},"confirmed":false}`,
		`{"operation":"create_group","parameters":{"groupName":""}}`,
		`{"operation":"create_asset","parameters":{"assetUrl":"https://public.example/input.png","assetType":"Image"}}`,
		`{"operation":"get_asset","parameters":{"assetId":"../escape"}}`,
		`{"operation":"get_group","parameters":{"groupId":"group-1","endpoint":"https://other.example"}}`,
		`{"operation":"list_groups","parameters":{"pageNo":0,"pageSize":1}}`,
		`{"operation":"list_groups","parameters":{"pageNo":1,"pageSize":1,"token":"injected"}}`,
	} {
		requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", body, http.StatusBadRequest, false)
	}
	assert.Len(t, requests, count)
	for _, provider := range []string{"mobile-cloud", "topenrouter"} {
		for _, operation := range []string{"create_group", "update_asset", "delete_asset", "delete_group"} {
			t.Run(provider+"/reject-invalid-direct-"+operation, func(t *testing.T) {
				assert.NotPanics(t, func() {
					result, err := service.TestMaterialIntegration(t.Context(), provider, service.MaterialIntegrationTestRequest{
						Operation:  operation,
						Parameters: service.MaterialIntegrationTestParameters{GroupID: "group-1", AssetID: "asset-1"},
					})
					require.Error(t, err)
					assert.Nil(t, result)
				})
			})
		}
	}
	assert.Len(t, requests, count, "invalid direct calls must not execute upstream mutations")
	for _, operation := range []string{"list_groups", "list_assets"} {
		requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/topenrouter/test", fmt.Sprintf(`{"operation":%q,"parameters":{}}`, operation), http.StatusOK, true)
	}
	count = len(requests)
	mode = "unknown"
	response := requestAdminMaterial(t, router, &users[0], http.MethodPost, "/api/material/mobile-cloud/test", `{"operation":"create_group","parameters":{"groupName":"manual group"}}`, http.StatusOK, true)
	assert.Contains(t, string(response.Data), `"success":false`)
	assert.Contains(t, string(response.Data), "check")
	assert.Len(t, requests, count+1, "unknown writes must never be replayed")
}

func TestMobileCloudMaterialConfigAutomaticEncryption(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
	keyPath := filepath.Join(t.TempDir(), "keys", "material.key")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE", keyPath)
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	router := newAdminMaterialRouter(t)
	const path = "/api/material/mobile-cloud/config"
	channel := model.Channel{Name: "Automatic material test channel", Key: "unused-test-key", Status: 1}
	require.NoError(t, db.Create(&channel).Error)
	body := fmt.Sprintf(`{"channel_id":%d,"endpoint":"https://ecloud.10086.cn/api/openapi-maas","access_key":"test-automatic-ak","secret_key":"test-automatic-sk","pool_id":"test-pool"}`, channel.Id)
	admin := &users[0]
	response := requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusOK, true)
	var view service.MobileCloudMaterialConfigView
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.True(t, view.Available)
	assert.False(t, view.Configured)
	_, err := os.Stat(keyPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	requestAdminMaterial(t, router, &users[1], http.MethodPut, path, body, http.StatusForbidden, false)
	response = requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &view))
	assert.True(t, view.EncryptionReady)
	assert.True(t, view.Configured)
	key, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	require.Len(t, key, 44)
	assert.NotContains(t, string(response.Data), string(key))
	assert.NotContains(t, string(response.Data), "test-automatic-sk")
	config, err := service.LoadMobileCloudMaterialConfiguration()
	require.NoError(t, err)
	assert.Equal(t, "test-automatic-ak", config.AccessKey)
	assert.Equal(t, "test-automatic-sk", config.SecretKey)
	requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"https://ecloud.10086.cn/api/openapi-maas","pool_id":"updated-pool"}`, http.StatusOK, true)
	persistedKey, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, key, persistedKey)
	config, err = service.LoadMobileCloudMaterialConfiguration()
	require.NoError(t, err)
	assert.Equal(t, "test-automatic-sk", config.SecretKey)
	assert.Equal(t, "updated-pool", config.PoolID)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "invalid")
	requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusServiceUnavailable, false)
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
	require.NoError(t, os.WriteFile(keyPath, []byte("invalid"), 0600))
	requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusServiceUnavailable, false)
	persistedKey, err = os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, "invalid", string(persistedKey))
	require.NoError(t, os.Remove(keyPath))
	requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusServiceUnavailable, false)
	_, err = os.Stat(keyPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	t.Run("default key survives with application data", func(t *testing.T) {
		workDir := t.TempDir()
		t.Chdir(workDir)
		t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
		t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE", "")
		t.Setenv("APPDATA", filepath.Join(workDir, "config"))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(workDir, "config"))
		require.NoError(t, db.Where("key = ?", model.MobileCloudMaterialConfigOption).Delete(&model.Option{}).Error)
		requestAdminMaterial(t, router, admin, http.MethodPut, path, body, http.StatusOK, true)
		defaultKeyPath := filepath.Join(workDir, "data", "mobile-cloud-material.key")
		key, err := os.ReadFile(defaultKeyPath)
		require.NoError(t, err)
		require.Len(t, key, 44)
		config, err := service.LoadMobileCloudMaterialConfiguration()
		require.NoError(t, err)
		assert.Equal(t, "test-automatic-sk", config.SecretKey)
		requestAdminMaterial(t, router, admin, http.MethodPut, path, `{"endpoint":"https://ecloud.10086.cn/api/openapi-maas","pool_id":"restored-pool"}`, http.StatusOK, true)
		persistedKey, err := os.ReadFile(defaultKeyPath)
		require.NoError(t, err)
		assert.Equal(t, key, persistedKey)
	})
}
