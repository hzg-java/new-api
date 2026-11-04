package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMaterialRouteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialRoute{}, &model.MaterialCapability{}))
	return db
}

type routeEnvelope struct {
	Success bool                `json:"success"`
	Message string              `json:"message"`
	Data    model.MaterialRoute `json:"data"`
}

type routeListEnvelope struct {
	Success bool                  `json:"success"`
	Total   int64                 `json:"total"`
	Data    []model.MaterialRoute `json:"data"`
}

type routeBoolEnvelope struct {
	Success bool `json:"success"`
	Data    bool `json:"data"`
}

type capabilityGridEnvelope struct {
	Success      bool             `json:"success"`
	Data         []capabilityCell `json:"data"`
	Boundaries   []string         `json:"boundaries"`
	Capabilities []string         `json:"capabilities"`
}

type capabilityEnvelope struct {
	Success bool                     `json:"success"`
	Message string                   `json:"message"`
	Data    model.MaterialCapability `json:"data"`
}

// verifyAIGCCapabilities marks the capabilities an AIGC route needs as verified on
// the Millu gateway boundary so the enable gate can pass.
func verifyAIGCCapabilities(t *testing.T, boundary string) {
	t.Helper()
	now := common.GetTimestamp()
	for _, capability := range model.RequiredCapabilitiesForScope(model.MaterialLibraryAIGC) {
		record := model.MaterialCapability{
			AccessBoundary: boundary,
			Capability:     capability,
			Status:         model.CapabilityStatusVerified,
			DocBasis:       "millu api docs",
			VerifiedTime:   now,
			CreatedTime:    now,
			UpdatedTime:    now,
		}
		require.NoError(t, model.ValidateMaterialCapabilityCell(&record))
		require.NoError(t, model.UpsertMaterialCapability(&record))
	}
}

func TestMobileCloudAIGCRouteWithoutCapabilityConfiguration(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.MaterialRoute{}, &model.MaterialCapability{}))
	for _, boundary := range model.AllAccessBoundaries() {
		for _, capability := range model.AllMaterialCapabilities() {
			require.NoError(t, model.UpsertMaterialCapability(&model.MaterialCapability{
				AccessBoundary: boundary, Capability: capability, Status: model.CapabilityStatusPending,
			}))
		}
	}
	for _, tc := range []struct {
		name     string
		boundary string
		scope    string
		allowed  bool
	}{
		{"mobile cloud AIGC", model.AccessBoundaryMobileCloudDir, model.MaterialLibraryAIGC, true},
		{"mobile cloud real person", model.AccessBoundaryMobileCloudDir, model.MaterialLibraryLivenessFace, false},
		{"millu AIGC", model.AccessBoundaryMilluGateway, model.MaterialLibraryAIGC, false},
		{"volcano AIGC", model.AccessBoundaryVolcanoDirect, model.MaterialLibraryAIGC, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := materialRouteRequest{
				Model: "route-" + common.GetUUID(), LibraryScope: tc.scope, AccessBoundary: tc.boundary,
				ChannelIds: []int{3}, AuthPolicy: model.MaterialAuthPolicyOnDemand, Enabled: true,
			}
			body, err := common.Marshal(request)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			CreateMaterialRoute(newMaterialContext(recorder, http.MethodPost, "/api/material/route/", string(body), users[0].Id))
			var created routeEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &created))
			assert.Equal(t, tc.allowed, created.Success, created.Message)

			route := request.toRoute()
			route.Enabled = false
			require.NoError(t, model.CreateMaterialRoute(route))
			recorder = httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodPut, "/api/material/route/"+strconv.Itoa(route.Id), string(body), users[0].Id)
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(route.Id)}}
			UpdateMaterialRoute(c)
			var updated routeEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &updated))
			assert.Equal(t, tc.allowed, updated.Success, updated.Message)
			stored, err := model.GetMaterialRouteById(route.Id)
			require.NoError(t, err)
			require.NotNil(t, stored)
			assert.Equal(t, tc.allowed, stored.Enabled)
		})
	}
}

func TestCreateMaterialRouteDisabledPersists(t *testing.T) {
	setupMaterialRouteTestDB(t)
	recorder := httptest.NewRecorder()
	body := `{"model":"seedance-2.0","library_scope":"AIGC","access_boundary":"millu_gateway","channel_ids":[3,7],"failover_allowed":true,"auth_policy":"on_demand","enabled":false}`
	c := newMaterialContext(recorder, http.MethodPost, "/api/material/route/", body, 99)

	CreateMaterialRoute(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope routeEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success, recorder.Body.String())
	assert.Equal(t, "seedance-2.0", envelope.Data.Model)
	assert.Equal(t, []int{3, 7}, envelope.Data.ChannelIds)
	assert.True(t, envelope.Data.FailoverAllowed)
	assert.False(t, envelope.Data.Enabled)

	var count int64
	require.NoError(t, model.DB.Model(&model.MaterialRoute{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestCreateMaterialRouteEnableBlockedByPendingCapability(t *testing.T) {
	setupMaterialRouteTestDB(t)
	recorder := httptest.NewRecorder()
	body := `{"model":"seedance-2.5","library_scope":"AIGC","access_boundary":"millu_gateway","channel_ids":[3],"auth_policy":"on_demand","enabled":true}`
	c := newMaterialContext(recorder, http.MethodPost, "/api/material/route/", body, 99)

	CreateMaterialRoute(c)

	var envelope routeEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.False(t, envelope.Success, "enable must be blocked while capabilities are pending")
	assert.Contains(t, envelope.Message, "not verified")

	var count int64
	require.NoError(t, model.DB.Model(&model.MaterialRoute{}).Count(&count).Error)
	assert.EqualValues(t, 0, count)
}

func TestCreateMaterialRouteEnableAllowedAfterVerification(t *testing.T) {
	setupMaterialRouteTestDB(t)
	verifyAIGCCapabilities(t, model.AccessBoundaryMilluGateway)

	recorder := httptest.NewRecorder()
	body := `{"model":"seedance-2.0","library_scope":"AIGC","access_boundary":"millu_gateway","channel_ids":[3],"auth_policy":"pre_auth","enabled":true}`
	c := newMaterialContext(recorder, http.MethodPost, "/api/material/route/", body, 99)

	CreateMaterialRoute(c)

	var envelope routeEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success, recorder.Body.String())
	assert.True(t, envelope.Data.Enabled)
}

func TestCreateMaterialRouteRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing model", `{"library_scope":"AIGC","access_boundary":"millu_gateway","auth_policy":"on_demand","enabled":false}`},
		{"invalid scope", `{"model":"m","library_scope":"Unknown","access_boundary":"millu_gateway","auth_policy":"on_demand","enabled":false}`},
		{"invalid boundary", `{"model":"m","library_scope":"AIGC","access_boundary":"Unknown","auth_policy":"on_demand","enabled":false}`},
		{"invalid policy", `{"model":"m","library_scope":"AIGC","access_boundary":"millu_gateway","auth_policy":"Unknown","enabled":false}`},
		{"failover needs two channels", `{"model":"m","library_scope":"AIGC","access_boundary":"millu_gateway","channel_ids":[3],"failover_allowed":true,"auth_policy":"on_demand","enabled":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupMaterialRouteTestDB(t)
			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodPost, "/api/material/route/", tc.body, 99)

			CreateMaterialRoute(c)

			var envelope routeEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			assert.False(t, envelope.Success, recorder.Body.String())
			var count int64
			require.NoError(t, model.DB.Model(&model.MaterialRoute{}).Count(&count).Error)
			assert.EqualValues(t, 0, count)
		})
	}
}

func TestUpdateMaterialRouteRechecksEnableGate(t *testing.T) {
	db := setupMaterialRouteTestDB(t)
	now := common.GetTimestamp()
	route := model.MaterialRoute{
		Model: "seedance-2.0", LibraryScope: model.MaterialLibraryAIGC,
		AccessBoundary: model.AccessBoundaryMilluGateway, AuthPolicy: model.MaterialAuthPolicyOnDemand,
		CreatedTime: now, UpdatedTime: now,
	}
	route.SetChannelIDs([]int{3})
	require.NoError(t, db.Create(&route).Error)

	recorder := httptest.NewRecorder()
	body := `{"model":"seedance-2.0","library_scope":"AIGC","access_boundary":"millu_gateway","channel_ids":[3],"auth_policy":"on_demand","enabled":true}`
	c := newMaterialContext(recorder, http.MethodPut, "/api/material/route/"+strconv.Itoa(route.Id), body, 99)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(route.Id)}}

	UpdateMaterialRoute(c)

	var envelope routeEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.False(t, envelope.Success, "enabling on update must be blocked while capabilities are pending")

	var stored model.MaterialRoute
	require.NoError(t, db.Where("id = ?", route.Id).First(&stored).Error)
	assert.False(t, stored.Enabled, "route must remain disabled after a blocked enable")
}

func TestDeleteMaterialRoute(t *testing.T) {
	db := setupMaterialRouteTestDB(t)
	now := common.GetTimestamp()
	route := model.MaterialRoute{
		Model: "m", LibraryScope: model.MaterialLibraryAIGC,
		AccessBoundary: model.AccessBoundaryMilluGateway, AuthPolicy: model.MaterialAuthPolicyOnDemand,
		CreatedTime: now, UpdatedTime: now,
	}
	require.NoError(t, db.Create(&route).Error)

	recorder := httptest.NewRecorder()
	c := newMaterialContext(recorder, http.MethodDelete, "/api/material/route/"+strconv.Itoa(route.Id), "", 99)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(route.Id)}}
	DeleteMaterialRoute(c)
	var envelope routeBoolEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success, recorder.Body.String())

	recorder = httptest.NewRecorder()
	c = newMaterialContext(recorder, http.MethodDelete, "/api/material/route/9999", "", 99)
	c.Params = gin.Params{{Key: "id", Value: "9999"}}
	DeleteMaterialRoute(c)
	var missing routeBoolEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &missing))
	assert.False(t, missing.Success)
}

func TestListMaterialCapabilitiesGridDefaultsPending(t *testing.T) {
	setupMaterialRouteTestDB(t)
	recorder := httptest.NewRecorder()
	c := newMaterialContext(recorder, http.MethodGet, "/api/material/capability/all", "", 99)

	ListMaterialCapabilities(c)

	var envelope capabilityGridEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success)
	assert.Len(t, envelope.Boundaries, 3)
	assert.Len(t, envelope.Capabilities, 6)
	assert.Len(t, envelope.Data, 18)
	for _, cell := range envelope.Data {
		assert.Equal(t, model.CapabilityStatusPending, cell.Status)
		assert.Zero(t, cell.Id)
	}
}

func TestUpsertCapabilityVerifiedRequiresEvidence(t *testing.T) {
	setupMaterialRouteTestDB(t)
	recorder := httptest.NewRecorder()
	body := `{"access_boundary":"millu_gateway","capability":"virtual_material_ops","status":"verified"}`
	c := newMaterialContext(recorder, http.MethodPut, "/api/material/capability/", body, 99)

	UpsertMaterialCapability(c)

	var envelope capabilityEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.False(t, envelope.Success, "verified without evidence must be rejected")
	assert.Contains(t, envelope.Message, "documentation basis")
}

func TestUpsertCapabilityPersistsAndReflectsInGrid(t *testing.T) {
	setupMaterialRouteTestDB(t)
	now := common.GetTimestamp()
	recorder := httptest.NewRecorder()
	body := `{"access_boundary":"volcano_direct","capability":"video_reference_format","status":"verified","doc_basis":"volcano docs","verified_time":` + strconv.FormatInt(now, 10) + `,"verification_note":"checked"}`
	c := newMaterialContext(recorder, http.MethodPut, "/api/material/capability/", body, 99)
	UpsertMaterialCapability(c)
	var saved capabilityEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &saved))
	assert.True(t, saved.Success, recorder.Body.String())
	assert.Equal(t, model.CapabilityStatusVerified, saved.Data.Status)

	recorder = httptest.NewRecorder()
	c = newMaterialContext(recorder, http.MethodGet, "/api/material/capability/all", "", 99)
	ListMaterialCapabilities(c)
	var grid capabilityGridEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &grid))
	var match *capabilityCell
	for i := range grid.Data {
		if grid.Data[i].AccessBoundary == "volcano_direct" && grid.Data[i].Capability == "video_reference_format" {
			match = &grid.Data[i]
		}
	}
	require.NotNil(t, match)
	assert.Equal(t, model.CapabilityStatusVerified, match.Status)
	assert.Equal(t, "volcano docs", match.DocBasis)
	assert.NotZero(t, match.Id)
}
