package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type flowQuotaResponse struct {
	Success bool                  `json:"success"`
	Message string                `json:"message"`
	Data    []model.FlowQuotaData `json:"data"`
}

func setupFlowControllerTestDB(t *testing.T) {
	t.Helper()
	db := model.DB
	if os.Getenv("MATERIAL_TEST_POSTGRES") == "1" {
		db = setupMaterialConcurrentPostgres(t)
	} else {
		db = setupModelListControllerTestDB(t)
	}
	previousLogDB := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Log{}, &model.Token{}, &model.QuotaData{}, &model.Channel{}))
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Name: "east"}).Error)
	require.NoError(t, model.DB.Create(&model.Token{Id: 11, UserId: 1, Key: "sk-primary", Name: "primary"}).Error)
	require.NoError(t, model.DB.Create(&model.Token{Id: 22, UserId: 2, Key: "sk-backup", Name: "backup"}).Error)
	require.NoError(t, model.DB.Create(&model.QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "default",
		ChannelID: 1,
		ModelName: "gpt-a",
		CreatedAt: 1100,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	}).Error)
	require.NoError(t, model.DB.Create(&model.QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		UseGroup:  "vip",
		ChannelID: 1,
		ModelName: "gpt-b",
		CreatedAt: 1200,
		Count:     1,
		Quota:     70,
		TokenUsed: 30,
	}).Error)
}

func decodeFlowQuotaResponse(t *testing.T, recorder *httptest.ResponseRecorder) flowQuotaResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload flowQuotaResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success, payload.Message)
	return payload
}

func TestGetAllFlowQuotaDatesUsesAdminDimensions(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("role", common.RoleAdminUser)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow?start_timestamp=1000&end_timestamp=2000&username=bob", nil)

	GetAllFlowQuotaDates(ctx)

	payload := decodeFlowQuotaResponse(t, recorder)
	require.Len(t, payload.Data, 1)
	require.Equal(t, "bob", payload.Data[0].Username)
	require.Equal(t, "vip", payload.Data[0].UseGroup)
	require.Equal(t, "east", payload.Data[0].ChannelName)
	require.Empty(t, payload.Data[0].TokenName)
	require.Empty(t, payload.Data[0].NodeName)
}

func TestGetAllFlowQuotaDatesUsesRootDimensions(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("role", common.RoleRootUser)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow?start_timestamp=1000&end_timestamp=2000&username=alice", nil)

	GetAllFlowQuotaDates(ctx)

	payload := decodeFlowQuotaResponse(t, recorder)
	require.Len(t, payload.Data, 1)
	require.Equal(t, "alice", payload.Data[0].Username)
	require.Equal(t, "node-a", payload.Data[0].NodeName)
	require.Equal(t, "primary", payload.Data[0].TokenName)
	require.Equal(t, "default", payload.Data[0].UseGroup)
	require.Equal(t, "east", payload.Data[0].ChannelName)
}

func TestGetUserFlowQuotaDatesRestrictsToAuthenticatedUser(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow/self?start_timestamp=1000&end_timestamp=2000", nil)

	GetUserFlowQuotaDates(ctx)

	payload := decodeFlowQuotaResponse(t, recorder)
	require.Len(t, payload.Data, 1)
	require.Empty(t, payload.Data[0].Username)
	require.Equal(t, "primary", payload.Data[0].TokenName)
	require.Equal(t, "default", payload.Data[0].UseGroup)
	require.Empty(t, payload.Data[0].ChannelName)
}

func TestGetUserFlowQuotaDatesRejectsInvalidTimeRange(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow/self?start_timestamp=bad&end_timestamp=2000", nil)

	GetUserFlowQuotaDates(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload flowQuotaResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.False(t, payload.Success)
	require.Equal(t, "invalid start_timestamp", payload.Message)
}

func TestDashboardReconcilesVideoUsageWithoutChangingHistory(t *testing.T) {
	if os.Getenv("MATERIAL_TEST_POSTGRES") != "1" {
		t.Skip("requires the isolated new-api-test PostgreSQL fixture")
	}
	db := setupMaterialConcurrentPostgres(t)
	previousLogDB := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Log{}, &model.QuotaData{}, &model.Token{}, &model.Channel{}))
	model.CacheQuotaDataLock.Lock()
	previousCache := model.CacheQuotaData
	model.CacheQuotaData = make(map[string]*model.QuotaData)
	model.CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		model.CacheQuotaDataLock.Lock()
		model.CacheQuotaData = previousCache
		model.CacheQuotaDataLock.Unlock()
	})
	require.NoError(t, db.Create(&model.Channel{Id: 1, Name: "video-channel"}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 11, UserId: 7, Key: "dashboard-test-key", Name: "primary"}).Error)
	const hour int64 = 1795017600
	for _, tc := range []struct {
		id     string
		quota  int
		status model.TaskStatus
		data   string
	}{
		{"lower", 60, model.TaskStatusSuccess, `{"usage":{"total_tokens":120}}`},
		{"higher", 140, model.TaskStatusSuccess, `{"usage":{"total_tokens":80}}`},
		{"failed", 0, model.TaskStatusFailure, `{}`},
		{"pending", 100, model.TaskStatusInProgress, `{"usage":{"total_tokens":900}}`},
		{"legacy", 70, model.TaskStatusSuccess, `{"usage":{"total_tokens":50}}`},
	} {
		task := model.Task{TaskID: tc.id, UserId: 7, Action: constant.TaskActionTextToVideo, Status: tc.status, SubmitTime: hour + 10, Quota: tc.quota, Data: []byte(tc.data), PrivateData: model.TaskPrivateData{NodeName: "node-a", TokenId: 11}}
		if tc.id != "pending" && tc.id != "legacy" {
			task.PrivateData.SettledQuota = &tc.quota
		}
		require.NoError(t, db.Create(&task).Error)
		other, err := common.Marshal(map[string]any{"task_id": tc.id})
		require.NoError(t, err)
		require.NoError(t, db.Create(&model.Log{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 10, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 100, Other: string(other)}).Error)
	}
	logs := []model.Log{
		{UserId: 7, Username: "owner", Type: model.LogTypeRefund, CreatedAt: hour + 3700, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 40, Other: `{"task_id":"lower","actual_quota":60,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 3700, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 40, Other: `{"task_id":"higher","actual_quota":140,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeRefund, CreatedAt: hour + 3700, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 100, Other: `{"task_id":"failed"}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeRefund, CreatedAt: hour + 3700, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 30, Other: `{"task_id":"legacy","actual_quota":70,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 20, ModelName: "text", TokenId: 11, ChannelId: 1, Group: "default", Quota: 25, PromptTokens: 10, CompletionTokens: 5},
	}
	require.NoError(t, db.Create(&logs).Error)
	buckets := []model.QuotaData{
		{UserID: 7, Username: "owner", ModelName: "video", CreatedAt: hour, UseGroup: "default", TokenID: 11, ChannelID: 1, NodeName: "node-a", Count: 5, Quota: 500},
		{UserID: 7, Username: "owner", ModelName: "video", CreatedAt: hour + 3600, UseGroup: "default", TokenID: 11, ChannelID: 1, NodeName: "node-a", Count: 1, Quota: 40},
		{UserID: 7, Username: "owner", ModelName: "text", CreatedAt: hour, UseGroup: "default", TokenID: 11, ChannelID: 1, NodeName: "node-a", Count: 1, Quota: 25, TokenUsed: 15},
		{UserID: 7, Username: "owner", ModelName: "purged-history", CreatedAt: hour, UseGroup: "default", TokenID: 11, ChannelID: 1, NodeName: "node-a", Count: 2, Quota: 90, TokenUsed: 30},
		{UserID: 8, Username: "another", ModelName: "other", CreatedAt: hour, UseGroup: "vip", Count: 1, Quota: 300, TokenUsed: 600},
	}
	require.NoError(t, db.Create(&buckets).Error)
	var beforeLogs []model.Log
	var beforeTasks []model.Task
	var beforeBuckets []model.QuotaData
	require.NoError(t, db.Order("id").Find(&beforeLogs).Error)
	require.NoError(t, db.Order("id").Find(&beforeTasks).Error)
	require.NoError(t, db.Order("id").Find(&beforeBuckets).Error)
	for _, tc := range []struct {
		name     string
		handler  gin.HandlerFunc
		role, id int
		query    string
		quota    int
		tokens   int
		count    int
	}{
		{"all", GetAllQuotaDates, common.RoleAdminUser, 0, "", 685, 895, 9},
		{"username", GetAllQuotaDates, common.RoleAdminUser, 0, "&username=owner", 385, 295, 8},
		{"self", GetUserQuotaDates, common.RoleCommonUser, 7, "&username=another", 385, 295, 8},
		{"users", GetQuotaDatesByUser, common.RoleAdminUser, 0, "", 685, 895, 9},
		{"flow-admin", GetAllFlowQuotaDates, common.RoleAdminUser, 0, "&username=owner", 385, 295, 8},
		{"flow-root", GetAllFlowQuotaDates, common.RoleRootUser, 0, "&username=owner", 385, 295, 8},
		{"flow-self", GetUserFlowQuotaDates, common.RoleCommonUser, 7, "&username=another", 385, 295, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("role", tc.role)
			c.Set("id", tc.id)
			c.Request = httptest.NewRequest(http.MethodGet, "/?start_timestamp="+strconv.FormatInt(hour, 10)+"&end_timestamp="+strconv.FormatInt(hour+7199, 10)+tc.query, nil)
			tc.handler(c)
			var response struct {
				Success bool `json:"success"`
				Data    []struct {
					ModelName    string `json:"model_name"`
					CreatedAt    int64  `json:"created_at"`
					Quota        int    `json:"quota"`
					Tokens       int    `json:"token_used"`
					Count        int    `json:"count"`
					Unreconciled bool   `json:"unreconciled"`
					NodeName     string `json:"node_name"`
					Username     string `json:"username"`
					TokenName    string `json:"token_name"`
					ChannelName  string `json:"channel_name"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success, recorder.Body.String())
			var quota, tokens, count int
			var warning bool
			for _, row := range response.Data {
				quota += row.Quota
				tokens += row.Tokens
				count += row.Count
				warning = warning || row.Unreconciled
				if row.ModelName == "video" {
					assert.Equal(t, 270, row.Quota)
					assert.Equal(t, 250, row.Tokens)
					assert.Equal(t, 5, row.Count)
					assert.False(t, row.Unreconciled)
					if tc.name == "username" || tc.name == "self" {
						assert.Equal(t, hour, row.CreatedAt)
					}
					if tc.name == "flow-root" {
						assert.Equal(t, "node-a", row.NodeName)
						assert.Equal(t, "primary", row.TokenName)
					}
				}
				if tc.name == "flow-self" {
					assert.Empty(t, row.Username)
					assert.Empty(t, row.ChannelName)
				}
				if tc.role < common.RoleRootUser {
					assert.Empty(t, row.NodeName)
				}
			}
			assert.Equal(t, tc.quota, quota)
			assert.Equal(t, tc.tokens, tokens)
			assert.Equal(t, tc.count, count)
			assert.True(t, warning)
		})
	}
	var afterLogs []model.Log
	var afterTasks []model.Task
	var afterBuckets []model.QuotaData
	require.NoError(t, db.Order("id").Find(&afterLogs).Error)
	require.NoError(t, db.Order("id").Find(&afterTasks).Error)
	require.NoError(t, db.Order("id").Find(&afterBuckets).Error)
	assert.Equal(t, beforeLogs, afterLogs)
	assert.Equal(t, beforeTasks, afterTasks)
	assert.Equal(t, beforeBuckets, afterBuckets)

	t.Run("mixed-ordinary-and-video-bucket", func(t *testing.T) {
		ordinary := model.Log{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 30, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 10, PromptTokens: 3, CompletionTokens: 4}
		require.NoError(t, db.Create(&ordinary).Error)
		t.Cleanup(func() {
			assert.NoError(t, db.Delete(&ordinary).Error)
			assert.NoError(t, db.Model(&model.QuotaData{}).Where("id = ?", buckets[0].Id).Updates(map[string]any{"count": 5, "quota": 500, "token_used": 0}).Error)
		})
		require.NoError(t, db.Model(&model.QuotaData{}).Where("id = ?", buckets[0].Id).Updates(map[string]any{"count": 6, "quota": 510, "token_used": 7}).Error)
		rows, err := getDashboardQuotaDates(hour, hour+7199, "owner", 0, false)
		require.NoError(t, err)
		var videoRows int
		for _, row := range rows {
			if row.ModelName != "video" {
				continue
			}
			videoRows++
			assert.Equal(t, 280, row.Quota)
			assert.Equal(t, 257, row.TokenUsed)
			assert.Equal(t, 6, row.Count)
			assert.False(t, row.Unreconciled)
		}
		assert.Equal(t, 1, videoRows)
	})
	t.Run("missing-details-preserve-whole-bucket", func(t *testing.T) {
		require.NoError(t, db.Model(&model.QuotaData{}).Where("id = ?", buckets[0].Id).Update("count", 6).Error)
		t.Cleanup(func() {
			assert.NoError(t, db.Model(&model.QuotaData{}).Where("id = ?", buckets[0].Id).Update("count", 5).Error)
		})
		rows, err := getDashboardQuotaDates(hour, hour+7199, "owner", 0, false)
		require.NoError(t, err)
		var quota, tokens, count int
		for _, row := range rows {
			if row.ModelName != "video" {
				continue
			}
			quota += row.Quota
			tokens += row.TokenUsed
			count += row.Count
			assert.True(t, row.Unreconciled)
		}
		assert.Equal(t, 540, quota)
		assert.Equal(t, 0, tokens)
		assert.Equal(t, 7, count)
	})
	t.Run("duplicate-task-owner-preserves-originals", func(t *testing.T) {
		duplicate := model.Task{TaskID: "higher", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess}
		require.NoError(t, db.Create(&duplicate).Error)
		t.Cleanup(func() { assert.NoError(t, db.Delete(&duplicate).Error) })
		rows, err := getDashboardQuotaDates(hour, hour+7199, "owner", 0, false)
		require.NoError(t, err)
		var quota, count int
		for _, row := range rows {
			if row.ModelName != "video" {
				continue
			}
			quota += row.Quota
			count += row.Count
			assert.True(t, row.Unreconciled)
		}
		assert.Equal(t, 540, quota)
		assert.Equal(t, 6, count)
	})
	t.Run("malformed-task-details-preserve-matching-bucket", func(t *testing.T) {
		log := model.Log{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 10, ModelName: "unknown-task", TokenId: 11, ChannelId: 1, Group: "default", Quota: 5, Other: `{"task_id":"image","actual_quota":"5"}`}
		bucket := model.QuotaData{UserID: 7, Username: "owner", ModelName: "unknown-task", CreatedAt: hour, UseGroup: "default", TokenID: 11, ChannelID: 1, NodeName: "node-a", Count: 1, Quota: 5}
		require.NoError(t, db.Create(&log).Error)
		require.NoError(t, db.Create(&bucket).Error)
		t.Cleanup(func() {
			assert.NoError(t, db.Delete(&log).Error)
			assert.NoError(t, db.Delete(&bucket).Error)
		})
		rows, err := getDashboardQuotaDates(hour, hour+7199, "owner", 0, false)
		require.NoError(t, err)
		var retained int
		for _, row := range rows {
			if row.ModelName != "unknown-task" {
				continue
			}
			retained++
			assert.Equal(t, 5, row.Quota)
			assert.Equal(t, 1, row.Count)
			assert.True(t, row.Unreconciled)
		}
		assert.Equal(t, 1, retained)
	})
	t.Run("unflushed-log-does-not-reduce-persisted-history", func(t *testing.T) {
		unflushed := model.Log{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 30, ModelName: "video", TokenId: 11, ChannelId: 1, Group: "default", Quota: 10}
		require.NoError(t, db.Create(&unflushed).Error)
		t.Cleanup(func() { assert.NoError(t, db.Delete(&unflushed).Error) })
		rows, err := getDashboardQuotaDates(hour, hour+7199, "owner", 0, false)
		require.NoError(t, err)
		var quota, count int
		for _, row := range rows {
			if row.ModelName != "video" {
				continue
			}
			quota += row.Quota
			count += row.Count
			assert.True(t, row.Unreconciled)
		}
		assert.Equal(t, 540, quota)
		assert.Equal(t, 6, count)
	})
	t.Run("multiple-node-buckets-keep-original-attribution", func(t *testing.T) {
		require.NoError(t, db.Model(&model.QuotaData{}).Where("id = ?", buckets[0].Id).Updates(map[string]any{"count": 2, "quota": 200}).Error)
		second := buckets[0]
		second.Id = 0
		second.NodeName = "node-b"
		second.Count = 3
		second.Quota = 300
		require.NoError(t, db.Create(&second).Error)
		t.Cleanup(func() {
			assert.NoError(t, db.Delete(&second).Error)
			assert.NoError(t, db.Model(&model.QuotaData{}).Where("id = ?", buckets[0].Id).Updates(map[string]any{"count": 5, "quota": 500}).Error)
		})
		rows, err := getDashboardFlowDates(hour, hour+7199, "owner", 0, common.RoleRootUser)
		require.NoError(t, err)
		totals := make(map[string]int)
		for _, row := range rows {
			if row.ModelName != "video" {
				continue
			}
			totals[row.NodeName] += row.Quota
			assert.True(t, row.Unreconciled)
		}
		assert.Equal(t, map[string]int{"node-a": 240, "node-b": 300}, totals)
	})
	t.Run("original-hour-and-settlement-hour-do-not-double-count", func(t *testing.T) {
		for _, tc := range []struct {
			start, end           int64
			quota, tokens, count int
		}{
			{hour, hour + 3599, 385, 295, 8},
			{hour + 3600, hour + 7199, 0, 0, 0},
		} {
			rows, err := getDashboardQuotaDates(tc.start, tc.end, "owner", 0, false)
			require.NoError(t, err)
			var quota, tokens, count int
			for _, row := range rows {
				quota += row.Quota
				tokens += row.TokenUsed
				count += row.Count
			}
			assert.Equal(t, tc.quota, quota)
			assert.Equal(t, tc.tokens, tokens)
			assert.Equal(t, tc.count, count)
		}
	})
}
