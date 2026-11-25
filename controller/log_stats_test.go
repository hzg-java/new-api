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
	if os.Getenv("MATERIAL_TEST_POSTGRES") == "1" {
		db := setupMaterialConcurrentPostgres(t)
		previousLogDB := model.LOG_DB
		model.LOG_DB = db
		t.Cleanup(func() { model.LOG_DB = previousLogDB })
		require.NoError(t, db.AutoMigrate(&model.Log{}))
	} else {
		setupManageUserTestDB(t)
	}
	now := time.Now().Unix()
	logs := []model.Log{
		{UserId: 7, Type: model.LogTypeConsume, CreatedAt: now - 3600, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", RequestId: "request-old", PromptTokens: 100, CompletionTokens: 50, Quota: 10},
		{UserId: 7, Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", RequestId: "request-recent", PromptTokens: 20, CompletionTokens: 30, Quota: 5},
		{UserId: 7, Type: model.LogTypeConsume, CreatedAt: now - 7200, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 900, Quota: 90},
		{UserId: 8, Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "another", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 800, Quota: 80},
		{UserId: 7, Type: model.LogTypeTopup, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 700, Quota: 70},
		{UserId: 7, Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-b", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 600, Quota: 60},
		{UserId: 7, Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-b", ChannelId: 2, Group: "default", PromptTokens: 500, Quota: 50},
		{UserId: 7, Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 3, Group: "default", PromptTokens: 400, Quota: 40},
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
				c.Set("id", 7)
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

func TestLogStatsIncludeActualVideoTokensOnce(t *testing.T) {
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
	now := time.Now().Unix()
	for _, tc := range []struct {
		id, data string
		status   model.TaskStatus
	}{
		{"total", `{"usage":{"total_tokens":120,"completion_tokens":90}}`, model.TaskStatusSuccess},
		{"refund", `{"usage":{"total_tokens":80,"completion_tokens":60}}`, model.TaskStatusSuccess},
		{"completion-only", `{"usage":{"completion_tokens":50}}`, model.TaskStatusSuccess},
		{"zero", `{"usage":{"total_tokens":0,"completion_tokens":0}}`, model.TaskStatusSuccess},
		{"unsettled", `{"usage":{"total_tokens":30}}`, model.TaskStatusSuccess},
		{"adjustment-failed", `{"usage":{"total_tokens":40}}`, model.TaskStatusSuccess},
		{"legacy", `{"usage":{"total_tokens":60}}`, model.TaskStatusSuccess},
		{"running", `{"usage":{"total_tokens":900}}`, model.TaskStatusInProgress},
		{"failed", `{"usage":{"total_tokens":800}}`, model.TaskStatusFailure},
		{"missing-usage", `{}`, model.TaskStatusSuccess},
		{"invalid-usage", `{"usage":{"total_tokens":-1}}`, model.TaskStatusSuccess},
	} {
		task := model.Task{
			TaskID: tc.id, UserId: 7, Action: constant.TaskActionTextToVideo, Status: tc.status,
			Quota: 10, Data: []byte(tc.data),
			PrivateData: model.TaskPrivateData{BillingAdjustmentFailed: tc.id == "adjustment-failed"},
		}
		if tc.id != "unsettled" && tc.id != "legacy" {
			task.PrivateData.SettledQuota = &task.Quota
		}
		require.NoError(t, model.DB.Create(&task).Error)
		other, err := common.Marshal(map[string]any{"task_id": tc.id, "usage_facts": map[string]int{"tokens": 999}})
		require.NoError(t, err)
		log := model.Log{
			UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: now - 3600,
			ModelName: "video-model", TokenName: "video-key", RequestId: tc.id, Quota: 10, Other: string(other),
		}
		if tc.id == "total" || tc.id == "zero" {
			log.PromptTokens, log.CompletionTokens = 5, 6
		}
		require.NoError(t, model.LOG_DB.Create(&log).Error)
	}
	quota := 10
	require.NoError(t, model.DB.Create(&model.Task{
		TaskID: "total", UserId: 8, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess,
		Quota: quota, PrivateData: model.TaskPrivateData{SettledQuota: &quota}, Data: []byte(`{"usage":{"total_tokens":300}}`),
	}).Error)
	require.NoError(t, model.DB.Create(&model.Task{
		TaskID: "image", UserId: 7, Action: "text_to_image", Status: model.TaskStatusSuccess,
		Data: []byte(`{"usage":{"total_tokens":999}}`),
	}).Error)
	logs := []model.Log{
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: now - 10, ModelName: "video-model", TokenName: "video-key", PromptTokens: 5, CompletionTokens: 6, Other: `{"task_id":"total","actual_quota":10,"pre_consumed_quota":5}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeRefund, CreatedAt: now - 10, ModelName: "video-model", TokenName: "video-key", Other: `{"task_id":"refund","actual_quota":10,"pre_consumed_quota":20}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: now - 10, ModelName: "video-model", TokenName: "video-key", Other: `{"task_id":"completion-only","actual_quota":10,"pre_consumed_quota":5}`},
		{UserId: 8, Username: "another", Type: model.LogTypeConsume, CreatedAt: now - 10, ModelName: "video-model", TokenName: "video-key", RequestId: "total", Other: `{"task_id":"total"}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: now - 10, ModelName: "text-model", PromptTokens: 10, CompletionTokens: 5},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: now - 10, ModelName: "image-model", RequestId: "image", PromptTokens: 7, CompletionTokens: 3, Other: `{"task_id":"image"}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: now - 10, ModelName: "text-model", RequestId: "orphan", PromptTokens: 3, CompletionTokens: 2, Other: `{"task_id":"orphan"}`},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	var beforeLogs []model.Log
	var beforeTasks []model.Task
	require.NoError(t, model.LOG_DB.Order("id").Find(&beforeLogs).Error)
	require.NoError(t, model.DB.Order("id").Find(&beforeTasks).Error)
	for _, self := range []bool{false, true} {
		for _, tc := range []struct {
			name, query string
			tokens      int64
		}{
			{"all", "", 410},
			{"consume", "&type=2", 410},
			{"refund-type", "&type=6", 0},
			{"model", "&model_name=video-model", 380},
			{"token", "&token_name=video-key", 380},
			{"total", "&request_id=total", 120},
			{"refund", "&request_id=refund", 80},
			{"completion-only", "&request_id=completion-only", 50},
			{"zero", "&request_id=zero", 0},
			{"unsettled", "&request_id=unsettled", 30},
			{"adjustment-failed", "&request_id=adjustment-failed", 40},
			{"legacy", "&request_id=legacy", 60},
			{"running", "&request_id=running", 0},
			{"failed", "&request_id=failed", 0},
			{"missing-usage", "&request_id=missing-usage", 0},
			{"invalid-usage", "&request_id=invalid-usage", 0},
			{"image", "&request_id=image", 10},
			{"orphan", "&request_id=orphan", 5},
			{"missing", "&request_id=missing", 0},
			{"recent-settlement", "&model_name=video-model&start_timestamp=" + strconv.FormatInt(now-50, 10), 250},
			{"recent-consume", "&model_name=video-model&type=2&start_timestamp=" + strconv.FormatInt(now-50, 10), 170},
			{"creation-window", "&request_id=total&end_timestamp=" + strconv.FormatInt(now-100, 10), 120},
			{"other-owner", "&username=another&model_name=video-model", 300},
		} {
			t.Run(tc.name+map[bool]string{false: "/admin", true: "/self"}[self], func(t *testing.T) {
				query, err := url.ParseQuery(tc.query)
				require.NoError(t, err)
				if !query.Has("username") {
					query.Set("username", "owner")
				}
				if !query.Has("start_timestamp") {
					query.Set("start_timestamp", strconv.FormatInt(now-4000, 10))
				}
				if !query.Has("end_timestamp") {
					query.Set("end_timestamp", strconv.FormatInt(now+1, 10))
				}
				query.Set("p", "3")
				query.Set("page_size", "1")
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("GET", "/?"+query.Encode(), nil)
				if self {
					userID := 7
					if tc.name == "other-owner" {
						userID = 8
					}
					c.Set("id", userID)
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
				assert.Equal(t, tc.tokens, response.Data.TotalTokens)
			})
		}
	}
	stat, err := getLogStatistics(model.UsageLogFilter{Start: now - 4000, End: now + 1})
	require.NoError(t, err)
	assert.EqualValues(t, 710, stat.TotalTokens)
	var afterLogs []model.Log
	var afterTasks []model.Task
	require.NoError(t, model.LOG_DB.Order("id").Find(&afterLogs).Error)
	require.NoError(t, model.DB.Order("id").Find(&afterTasks).Error)
	assert.Equal(t, beforeLogs, afterLogs)
	assert.Equal(t, beforeTasks, afterTasks)
}

func TestDashboardTimeRangeMatchesUsageLogSummary(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	previousLogDB := model.LOG_DB
	previousLogType := common.LogDatabaseType()
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, previousLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Log{}, &model.QuotaData{}))
	const hour int64 = 1795536000
	quota := 60
	require.NoError(t, db.Create(&model.Task{TaskID: "video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Quota: quota, Data: []byte(`{"usage":{"total_tokens":120}}`), PrivateData: model.TaskPrivateData{SettledQuota: &quota}}).Error)
	logs := []model.Log{
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour - 1, ModelName: "video", Quota: 100, Other: `{"task_id":"video"}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 9, ModelName: "text", Quota: 80},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 10, ModelName: "text", Quota: 25, PromptTokens: 10},
		{UserId: 7, Username: "owner", Type: model.LogTypeRefund, CreatedAt: hour + 15, ModelName: "video", Quota: 40, Other: `{"task_id":"video","actual_quota":60}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 20, ModelName: "image", Quota: 5, CompletionTokens: 2, Other: `{"task_id":"image"}`},
		{UserId: 7, Username: "owner", Type: model.LogTypeConsume, CreatedAt: hour + 21, ModelName: "text", Quota: 90},
		{UserId: 8, Username: "another", Type: model.LogTypeConsume, CreatedAt: hour + 20, ModelName: "text", Quota: 70, CompletionTokens: 3},
	}
	require.NoError(t, db.Create(&logs).Error)
	legacy := model.QuotaData{UserID: 7, Username: "owner", ModelName: "legacy", CreatedAt: hour, Count: 1, Quota: 99, TokenUsed: 90}
	require.NoError(t, db.Create(&legacy).Error)
	for _, tc := range []struct {
		name                 string
		start, end           int64
		username             string
		self                 bool
		quota, tokens, count int
	}{
		{"partial hour, all users", hour + 10, hour + 20, "", false, 160, 135, 4},
		{"partial hour, filtered user", hour + 10, hour + 20, "owner", false, 90, 132, 3},
		{"partial hour, own user", hour + 10, hour + 20, "", true, 90, 132, 3},
		{"whole range, video counted once", hour - 1, hour + 21, "owner", false, 260, 132, 5},
		{"no matching logs", hour + 22, hour + 30, "owner", false, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"start_timestamp": {strconv.FormatInt(tc.start, 10)}, "end_timestamp": {strconv.FormatInt(tc.end, 10)}}
			if tc.username != "" {
				query.Set("username", tc.username)
			}
			call := func(handler gin.HandlerFunc, into any) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Set("id", 7)
				c.Set("username", "owner")
				c.Request = httptest.NewRequest("GET", "/?"+query.Encode(), nil)
				handler(c)
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), into))
			}
			statHandler, dashboardHandler := GetLogsStat, GetAllQuotaDates
			if tc.self {
				query.Set("username", "another")
				statHandler, dashboardHandler = GetLogsSelfStat, GetUserQuotaDates
			}
			var logsResponse struct {
				Success bool       `json:"success"`
				Data    model.Stat `json:"data"`
			}
			call(statHandler, &logsResponse)
			require.True(t, logsResponse.Success)
			assert.Equal(t, tc.quota, logsResponse.Data.Quota)
			assert.EqualValues(t, tc.tokens, logsResponse.Data.TotalTokens)
			var dashboard struct {
				Success bool              `json:"success"`
				Data    []model.QuotaData `json:"data"`
			}
			call(dashboardHandler, &dashboard)
			require.True(t, dashboard.Success)
			var gotQuota, gotTokens, gotCount int
			for _, row := range dashboard.Data {
				gotQuota += row.Quota
				gotTokens += row.TokenUsed
				gotCount += row.Count
			}
			assert.Equal(t, logsResponse.Data.Quota, gotQuota)
			assert.EqualValues(t, logsResponse.Data.TotalTokens, gotTokens)
			assert.Equal(t, tc.count, gotCount)
		})
	}
	var after model.QuotaData
	require.NoError(t, db.First(&after, legacy.Id).Error)
	assert.Equal(t, legacy, after)
}
