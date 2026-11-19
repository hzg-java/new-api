package controller

import (
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogStatsTotalTokensFollowFilters(t *testing.T) {
	setupManageUserTestDB(t)
	now := time.Now().Unix()
	logs := []model.Log{
		{Type: model.LogTypeConsume, CreatedAt: now - 3600, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", RequestId: "request-old", PromptTokens: 100, CompletionTokens: 50, Quota: 10},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", RequestId: "request-recent", PromptTokens: 20, CompletionTokens: 30, Quota: 5},
		{Type: model.LogTypeConsume, CreatedAt: now - 7200, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 900, Quota: 90},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "another", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 800, Quota: 80},
		{Type: model.LogTypeTopup, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 700, Quota: 70},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-b", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 600, Quota: 60},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-b", ChannelId: 2, Group: "default", PromptTokens: 500, Quota: 50},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 3, Group: "default", PromptTokens: 400, Quota: 40},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	for _, self := range []bool{false, true} {
		for _, tc := range []struct {
			name, model, query     string
			quota, total, rpm, tpm int
		}{
			{"matched", "model-a", "", 15, 200, 1, 50},
			{"all_types", "model-a", "&type=0", 15, 200, 1, 50},
			{"consume", "model-a", "&type=2", 15, 200, 1, 50},
			{"topup", "model-a", "&type=1", 0, 0, 0, 0},
			{"error", "model-a", "&type=5", 0, 0, 0, 0},
			{"refund", "model-a", "&type=6", 0, 0, 0, 0},
			{"old_request", "model-a", "&request_id=request-old", 10, 150, 0, 0},
			{"recent_request", "model-a", "&request_id=request-recent", 5, 50, 1, 50},
			{"missing_request", "model-a", "&request_id=missing", 0, 0, 0, 0},
			{"empty", "missing", "", 0, 0, 0, 0},
		} {
			t.Run(tc.name+map[bool]string{false: "/admin", true: "/self"}[self], func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("GET", "/?start_timestamp="+strconv.FormatInt(now-4000, 10)+"&end_timestamp="+strconv.FormatInt(now+1, 10)+"&username=owner&model_name="+tc.model+"&token_name=key-a&channel=2&p=2&page_size=1"+tc.query, nil)
				c.Set("username", "owner")
				if self {
					GetLogsSelfStat(c)
				} else {
					GetLogsStat(c)
				}
				var response struct {
					Success bool           `json:"success"`
					Data    map[string]int `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.True(t, response.Success)
				assert.Equal(t, tc.quota, response.Data["quota"])
				assert.Equal(t, tc.total, response.Data["total_tokens"])
				assert.Contains(t, response.Data, "total_tokens")
				assert.Equal(t, tc.rpm, response.Data["rpm"])
				assert.Equal(t, tc.tpm, response.Data["tpm"])
			})
		}
	}
}

func TestLogStatsUseFinalVideoCosts(t *testing.T) {
	if os.Getenv("MATERIAL_TEST_POSTGRES") == "1" {
		db := setupMaterialConcurrentPostgres(t)
		previousLogDB := model.LOG_DB
		model.LOG_DB = db
		t.Cleanup(func() { model.LOG_DB = previousLogDB })
		require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Log{}))
	} else {
		db := setupManageUserTestDB(t)
		require.NoError(t, db.AutoMigrate(&model.Task{}))
	}
	db := model.DB
	now := time.Now().Unix()
	for _, tc := range []struct {
		id                 string
		status             model.TaskStatus
		quota              int
		partial, confirmed bool
	}{
		{"lower", model.TaskStatusSuccess, 60, false, true},
		{"higher", model.TaskStatusSuccess, 140, false, true},
		{"pending", model.TaskStatusInProgress, 100, false, false},
		{"refunded", model.TaskStatusFailure, 0, false, true},
		{"refund-pending", model.TaskStatusFailure, 100, false, false},
		{"unconfirmed", model.TaskStatusSuccess, 100, true, false},
		{"success-before-settlement", model.TaskStatusSuccess, 100, false, false},
		{"legacy-lower", model.TaskStatusSuccess, 70, false, false},
		{"legacy-higher", model.TaskStatusSuccess, 130, false, false},
		{"legacy-free", model.TaskStatusSuccess, 0, false, false},
		{"legacy-partial", model.TaskStatusSuccess, 70, true, false},
		{"legacy-invalid", model.TaskStatusSuccess, 100, false, false},
		{"legacy-mismatch", model.TaskStatusSuccess, 100, false, false},
	} {
		task := model.Task{TaskID: tc.id, UserId: 7, Action: constant.TaskActionTextToVideo, Status: tc.status, Quota: tc.quota, PrivateData: model.TaskPrivateData{BillingAdjustmentFailed: tc.partial}}
		if tc.confirmed {
			task.PrivateData.SettledQuota = &tc.quota
		}
		require.NoError(t, db.Create(&task).Error)
		other, err := common.Marshal(map[string]any{"task_id": tc.id})
		require.NoError(t, err)
		require.NoError(t, model.LOG_DB.Create(&model.Log{
			UserId: 7, Username: "owner", CreatedAt: now - 3600, Type: model.LogTypeConsume,
			ModelName: "video-model", TokenName: "video-key", RequestId: tc.id, Quota: 100, Other: string(other),
		}).Error)
	}
	logs := []model.Log{
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeRefund, ModelName: "video-model", TokenName: "video-key", Quota: 40, Other: `{"task_id":"lower","actual_quota":60,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "video-model", TokenName: "video-key", Quota: 40, Other: `{"task_id":"higher","actual_quota":140,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeRefund, ModelName: "video-model", TokenName: "video-key", Quota: 100, Other: `{"task_id":"refunded"}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "text-model", Quota: 25, PromptTokens: 10, CompletionTokens: 5},
		{UserId: 8, Username: "another", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "video-model", Quota: 300, Other: `{"task_id":"lower"}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeRefund, ModelName: "video-model", TokenName: "video-key", Quota: 30, Other: `{"task_id":"legacy-lower","actual_quota":70,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "video-model", TokenName: "video-key", Quota: 30, Other: `{"task_id":"legacy-higher","actual_quota":130,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeRefund, ModelName: "video-model", TokenName: "video-key", Quota: 100, Other: `{"task_id":"legacy-free","actual_quota":0,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeRefund, ModelName: "video-model", TokenName: "video-key", Quota: 30, Other: `{"task_id":"legacy-partial","actual_quota":70,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "video-model", TokenName: "video-key", Quota: 1, Other: `{"task_id":"legacy-invalid","actual_quota":-1,"pre_consumed_quota":100}`},
		{UserId: 7, Username: "owner", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "video-model", TokenName: "video-key", Quota: 30, Other: `{"task_id":"legacy-mismatch","actual_quota":130,"pre_consumed_quota":100}`},
		{UserId: 8, Username: "another", CreatedAt: now - 10, Type: model.LogTypeConsume, ModelName: "video-model", Quota: 100, Other: `{"task_id":"success-before-settlement","actual_quota":100}`},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	var before []model.Log
	require.NoError(t, model.LOG_DB.Order("id").Find(&before).Error)
	for _, self := range []bool{false, true} {
		for _, tc := range []struct {
			name, query string
			quota       int
		}{
			{"all", "", 425},
			{"consume", "&type=2", 425},
			{"recent_settlement", "&start_timestamp=" + strconv.FormatInt(now-50, 10), 425},
			{"model", "&model_name=video-model", 400},
			{"token", "&token_name=video-key", 400},
			{"legacy-lower", "&request_id=legacy-lower", 70},
			{"legacy-higher", "&request_id=legacy-higher", 130},
			{"legacy-free", "&request_id=legacy-free", 0},
			{"legacy-partial", "&request_id=legacy-partial", 0},
			{"legacy-invalid", "&request_id=legacy-invalid", 0},
			{"legacy-mismatch", "&request_id=legacy-mismatch", 0},
			{"legacy-creation-window", "&request_id=legacy-lower&end_timestamp=" + strconv.FormatInt(now-100, 10), 70},
			{"lower", "&request_id=lower", 60},
			{"higher", "&request_id=higher", 140},
			{"pending", "&request_id=pending", 0},
			{"refunded", "&request_id=refunded", 0},
			{"refund-pending", "&request_id=refund-pending", 0},
			{"unconfirmed", "&request_id=unconfirmed", 0},
			{"success-before-settlement", "&request_id=success-before-settlement", 0},
			{"missing", "&request_id=missing", 0},
		} {
			t.Run(tc.name+map[bool]string{false: "/admin", true: "/self"}[self], func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				query, err := url.ParseQuery(tc.query)
				require.NoError(t, err)
				query.Set("username", "owner")
				if !query.Has("start_timestamp") {
					query.Set("start_timestamp", strconv.FormatInt(now-4000, 10))
				}
				if !query.Has("end_timestamp") {
					query.Set("end_timestamp", strconv.FormatInt(now+1, 10))
				}
				query.Set("p", "3")
				query.Set("page_size", "1")
				c.Request = httptest.NewRequest("GET", "/?"+query.Encode(), nil)
				c.Set("id", 7)
				c.Set("username", "owner")
				if self {
					GetLogsSelfStat(c)
				} else {
					GetLogsStat(c)
				}
				var response struct {
					Success bool       `json:"success"`
					Data    model.Stat `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.True(t, response.Success, recorder.Body.String())
				assert.Equal(t, tc.quota, response.Data.Quota)
			})
		}
	}
	var after []model.Log
	require.NoError(t, model.LOG_DB.Order("id").Find(&after).Error)
	assert.Equal(t, before, after)
}
