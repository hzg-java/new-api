package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVideoTaskDisplayBillingProjection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      model.TaskStatus
		quota, want int
		pending     bool
	}{
		{"pending", model.TaskStatusInProgress, 100, 0, true},
		{"success increased", model.TaskStatusSuccess, 140, 140, false},
		{"success decreased", model.TaskStatusSuccess, 60, 60, false},
		{"failed refunded", model.TaskStatusFailure, 0, 0, false},
		{"failed refund incomplete", model.TaskStatusFailure, 100, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := openTaskDialectDatabase(t, &model.Task{})
			oldDB := model.DB
			model.DB = db
			t.Cleanup(func() { model.DB = oldDB })
			require.NoError(t, db.Create(&model.Task{TaskID: "video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: tc.status, Quota: tc.quota}).Error)
			logs := []*model.Log{{UserId: 7, Type: model.LogTypeConsume, Quota: 100, Other: `{"task_id":"video","billing_source":"subscription","subscription_consumed":100}`}}
			require.NoError(t, enrichVideoTaskLogs(logs))
			assert.Equal(t, tc.want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Equal(t, string(tc.status), other["task_status"])
			assert.Equal(t, tc.pending, other["task_billing_pending"])
			if tc.status == model.TaskStatusSuccess {
				assert.Equal(t, float64(tc.quota), other["actual_quota"])
				assert.Equal(t, float64(tc.quota), other["subscription_consumed"])
			}
			grouped, err := projectVideoTaskLogs([]*model.Log{
				{UserId: 7, Type: model.LogTypeRefund, Quota: 40, Other: `{"task_id":"video","pre_consumed_quota":100,"actual_quota":60,"usage_facts":{"tokens":60},"matched_tier":"actual"}`},
				{UserId: 7, Type: model.LogTypeConsume, Quota: 100, Other: `{"task_id":"video","billing_source":"subscription","subscription_consumed":100,"usage_facts":{"tokens":100},"matched_tier":"estimated"}`},
			}, true)
			require.NoError(t, err)
			require.Len(t, grouped, 1)
			assert.Equal(t, model.LogTypeConsume, grouped[0].Type)
			assert.Equal(t, tc.want, grouped[0].Quota)
			var merged map[string]any
			require.NoError(t, common.UnmarshalJsonStr(grouped[0].Other, &merged))
			assert.Equal(t, "actual", merged["matched_tier"])
			assert.Equal(t, map[string]any{"tokens": float64(60)}, merged["usage_facts"])
		})
	}
}

func TestVideoTaskDisplayGroupsBeforeEndpointPagination(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB = db
	// Keep the isolated fixture tables while satisfying the model's logs qualifiers.
	model.LOG_DB = db.Table(db.NamingStrategy.TableName("logs") + " AS logs")
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
	})
	for _, task := range []model.Task{
		{TaskID: "video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Quota: 60},
		{TaskID: "video", UserId: 8, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Quota: 30},
		{TaskID: "image", UserId: 7, Action: "text_to_image"},
		{TaskID: "ambiguous", UserId: 7, Action: constant.TaskActionTextToVideo},
		{TaskID: "ambiguous", UserId: 7, Action: constant.TaskActionTextToVideo},
	} {
		require.NoError(t, db.Create(&task).Error)
	}
	stored := []model.Log{
		{UserId: 7, CreatedAt: 1, Type: model.LogTypeConsume, Quota: 100, RequestId: "initial-request", TokenId: 12, TokenName: "initial-token", Group: "initial-group", Other: `{"task_id":"video","billing_source":"subscription","subscription_consumed":100,"admin_info":{"initial":true},"root_info":{"secret":"canary"}}`},
		{UserId: 7, CreatedAt: 2, Type: model.LogTypeRefund, Quota: 40, Other: `{"task_id":"video","pre_consumed_quota":100,"actual_quota":60}`},
		{UserId: 8, CreatedAt: 3, Type: model.LogTypeConsume, Quota: 30, Other: `{"task_id":"video"}`},
		{UserId: 7, CreatedAt: 4, Type: model.LogTypeConsume, Quota: 20, Other: `{"task_id":"image"}`},
		{UserId: 7, CreatedAt: 5, Type: model.LogTypeRefund, Quota: 5, Other: `{"task_id":"image"}`},
		{UserId: 9, CreatedAt: 6, Type: model.LogTypeConsume, Quota: 11, Other: `{"task_id":"video"}`},
		{UserId: 7, CreatedAt: 7, Type: model.LogTypeConsume, Quota: 12, Other: `{"task_id":"missing"}`},
		{UserId: 7, CreatedAt: 8, Type: model.LogTypeConsume, Quota: 13, Other: `{"task_id":"ambiguous"}`},
		{UserId: 7, CreatedAt: 9, Type: model.LogTypeRefund, Quota: 2, Other: `{"task_id":"ambiguous"}`},
	}
	require.NoError(t, db.Create(&stored).Error)
	for _, tc := range []struct {
		name        string
		handler     gin.HandlerFunc
		role, total int
	}{
		{"user", GetUserLogs, common.RoleCommonUser, 6},
		{"admin", GetAllLogs, common.RoleAdminUser, 8},
		{"root", GetAllLogs, common.RoleRootUser, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows []model.Log
			for page := 1; page <= tc.total+1; page++ {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/log/?p=%d&page_size=1", page), nil)
				c.Set("id", 7)
				c.Set("role", tc.role)
				model.LOG_DB = db.Session(&gorm.Session{NewDB: true}).Table(db.NamingStrategy.TableName("logs") + " AS logs")
				tc.handler(c)
				var response struct {
					Success bool
					Data    struct {
						Total int
						Items []model.Log
					}
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.True(t, response.Success, recorder.Body.String())
				assert.Equal(t, tc.total, response.Data.Total)
				rows = append(rows, response.Data.Items...)
			}
			require.Len(t, rows, tc.total)
			row := rows[len(rows)-1]
			assert.Equal(t, model.LogTypeConsume, row.Type)
			assert.Equal(t, 60, row.Quota)
			assert.Equal(t, int64(2), row.CreatedAt)
			assert.Equal(t, "initial-request", row.RequestId)
			assert.Equal(t, "initial-token", row.TokenName)
			assert.Equal(t, 12, row.TokenId)
			assert.Equal(t, "initial-group", row.Group)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(row.Other, &other))
			assert.Equal(t, float64(100), other["pre_consumed_quota"])
			assert.Equal(t, float64(60), other["actual_quota"])
			assert.Equal(t, float64(60), other["subscription_consumed"])
			if tc.role == common.RoleCommonUser {
				assert.NotContains(t, row.Other, "admin_info")
			} else {
				assert.Contains(t, row.Other, "initial")
			}
			if tc.role != common.RoleRootUser {
				assert.NotContains(t, row.Other, "canary")
			}
		})
	}
	var after []model.Log
	require.NoError(t, db.Order("id").Find(&after).Error)
	assert.Equal(t, stored, after)
}

func TestVideoUsageLogsDisplayActualTaskMetrics(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	for _, task := range []*model.Task{
		{TaskID: "completed-video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, SubmitTime: 1700000000, FinishTime: 1700000176, Data: []byte(`{"usage":{"total_tokens":50638,"completion_tokens":50638}}`)},
		{TaskID: "pending-video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusInProgress, SubmitTime: 1700000000, Data: []byte(`{"usage":{"total_tokens":48038}}`)},
		{TaskID: "no-usage-video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, SubmitTime: 1700000000, FinishTime: 1700000010, Data: []byte(`{}`)},
		{TaskID: "split-video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Data: []byte(`{"usage":{"total_tokens":100,"completion_tokens":80}}`)},
		{TaskID: "duplicate-video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Data: []byte(`{"usage":{"total_tokens":10}}`)},
		{TaskID: "duplicate-video", UserId: 7, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Data: []byte(`{"usage":{"total_tokens":20}}`)},
	} {
		require.NoError(t, db.Create(task).Error)
	}
	logs := []*model.Log{
		{UserId: 7, Type: model.LogTypeConsume, Other: `{"is_task":true,"task_id":"completed-video","usage_facts":{"tokens":48038}}`},
		{UserId: 7, Type: model.LogTypeConsume, Other: `{"task_id":"completed-video"}`},
		{UserId: 8, Type: model.LogTypeConsume, Other: `{"task_id":"completed-video"}`},
		{UserId: 7, Type: model.LogTypeConsume, Other: `{"task_id":"pending-video"}`},
		{UserId: 7, Type: model.LogTypeConsume, Other: `{"task_id":"no-usage-video"}`},
		{UserId: 7, Type: model.LogTypeConsume, PromptTokens: 1, CompletionTokens: 2, UseTime: 3, Other: `{"task_id":"completed-video"}`},
		{UserId: 7, Type: model.LogTypeConsume, Other: `{"task_id":"split-video"}`},
		{UserId: 7, Type: model.LogTypeConsume, Other: `{"task_id":"duplicate-video"}`},
	}
	require.NoError(t, enrichVideoTaskLogs(logs))
	for _, log := range logs[:2] {
		assert.Equal(t, 50638, log.CompletionTokens)
		assert.Zero(t, log.PromptTokens)
		assert.Equal(t, 176, log.UseTime)
	}
	assert.Zero(t, logs[2].CompletionTokens)
	assert.Zero(t, logs[2].UseTime)
	assert.Zero(t, logs[3].CompletionTokens)
	assert.Zero(t, logs[3].UseTime)
	assert.Zero(t, logs[4].CompletionTokens)
	assert.Equal(t, 10, logs[4].UseTime)
	assert.Equal(t, 1, logs[5].PromptTokens)
	assert.Equal(t, 2, logs[5].CompletionTokens)
	assert.Equal(t, 3, logs[5].UseTime)
	assert.Equal(t, 20, logs[6].PromptTokens)
	assert.Equal(t, 80, logs[6].CompletionTokens)
	assert.Zero(t, logs[7].CompletionTokens)
	var stored model.Task
	require.NoError(t, db.Where("task_id = ?", "completed-video").First(&stored).Error)
	assert.JSONEq(t, `{"usage":{"total_tokens":50638,"completion_tokens":50638}}`, string(stored.Data))
}

func TestVideoTaskInsertionRejectsOversizedEventBeforePersisting(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := &model.Task{
		TaskID: "public-id", UserId: 7, SubmitTime: 1700000000,
		PrivateData: model.TaskPrivateData{VideoInfo: &dto.TaskVideoInfo{Resolution: strings.Repeat("x", 4097)}},
	}
	require.Error(t, task.Insert())
	var count int64
	require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestTaskEventsMigrateTwiceWithoutLosingHistoricalTasks(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{})
	task := &model.Task{TaskID: "historical", UserId: 7}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskEvent{}))
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskEvent{}))
	var loaded model.Task
	require.NoError(t, db.First(&loaded, task.ID).Error)
	assert.Equal(t, "historical", loaded.TaskID)
}

func TestTaskEventTimelinePreservesSafeSnapshotsAndOwnership(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := &model.Task{TaskID: "public-id", UserId: 7, Status: model.TaskStatusSuccess}
	require.NoError(t, db.Create(task).Error)
	other := &model.Task{TaskID: "public-id", UserId: 8, Status: model.TaskStatusSuccess}
	require.NoError(t, db.Create(other).Error)
	require.NoError(t, model.RecordTaskEvent(other.ID, "request", 1700000000, map[string]any{"model": "foreign-model"}))
	require.NoError(t, model.RecordTaskEvent(task.ID, "request", 1700000000, map[string]any{"model": "public-model", "prompt": "hello"}))
	require.NoError(t, model.RecordTaskEvent(task.ID, "accepted", 1700000001, map[string]any{"id": task.TaskID, "status": "queued"}))
	require.NoError(t, model.RecordTaskEvent(task.ID, "result", 1700000010, map[string]any{"status": "SUCCESS", "total_tokens": 12}))
	require.NoError(t, model.RecordTaskEvent(task.ID, "result", 1700000011, map[string]any{"status": "FAILURE"}))

	for _, tc := range []struct {
		name                 string
		userID, role, status int
		wantCount            int
	}{
		{"owner", 7, common.RoleCommonUser, 200, 3},
		{"same public ID, different owner", 8, common.RoleCommonUser, 404, 0},
		{"foreign user", 9, common.RoleCommonUser, 404, 0},
		{"admin", 8, common.RoleAdminUser, 200, 3},
		{"api token", 9, common.RoleRootUser, 404, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/task/%d/events", task.ID), nil)
			c.Params = gin.Params{{Key: "task_id", Value: strconv.FormatInt(task.ID, 10)}}
			c.Set("id", tc.userID)
			c.Set("role", tc.role)
			if tc.name == "api token" {
				c.Set("token_id", 1)
			}
			GetDashboardTaskEvents(c)
			assert.Equal(t, tc.status, recorder.Code)
			if tc.wantCount == 0 {
				return
			}
			var response struct {
				Data []model.TaskEvent `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.Len(t, response.Data, tc.wantCount)
			assert.Equal(t, []string{"request", "accepted", "result"}, []string{response.Data[0].Kind, response.Data[1].Kind, response.Data[2].Kind})
			assert.Contains(t, response.Data[2].Payload, "SUCCESS")
			assert.NotContains(t, recorder.Body.String(), "FAILURE")
		})
	}
}

func TestTaskBillingModeProjectionFromStoredContext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		context *model.TaskBillingContext
		want    string
	}{
		{"historical unknown", nil, ""},
		{"per call", &model.TaskBillingContext{PerCallBilling: true}, "per_call"},
		{"per token", &model.TaskBillingContext{}, "per_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := tasksToDto([]*model.Task{{PrivateData: model.TaskPrivateData{BillingContext: tc.context}}}, false, common.RoleCommonUser)[0]
			assert.Equal(t, tc.want, view.BillingMode)
		})
	}
}

func TestTaskEventStoreRejectsOversizedPayloadAndUnknownFields(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	require.Error(t, model.RecordTaskEvent(1, "request", 1700000000, map[string]any{"model": strings.Repeat("m", 4097)}))
	require.Error(t, model.RecordTaskEvent(1, "request", 1700000000, map[string]any{"model": "public-model", "video_info": &dto.TaskVideoInfo{Resolution: strings.Repeat("s", 4097)}}))
	require.Error(t, model.RecordTaskEvent(1, "unknown", 1700000000, map[string]any{"model": "public-model"}))
	require.NoError(t, model.RecordTaskEvent(1, "request", 1700000000, map[string]any{"model": "public-model", "prompt": "secret-canary"}))
	events, err := model.GetTaskEvents(1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.JSONEq(t, `{"model":"public-model"}`, events[0].Payload)
}

func TestTaskEventResultSnapshotOmitsUntrustedResponseData(t *testing.T) {
	task := &model.Task{TaskID: "public-id", Status: model.TaskStatusSuccess, Data: []byte(`{"usage":{"total_tokens":12,"completion_tokens":9,"key":"secret-canary"},"url":"https://secret.invalid/video?key=secret-canary"}`)}
	payload := model.TaskResultEventPayload(task)
	encoded, err := common.Marshal(payload)
	require.NoError(t, err)
	assert.JSONEq(t, `{"task_id":"public-id","status":"SUCCESS","usage":{"total_tokens":12,"completion_tokens":9}}`, string(encoded))
	assert.NotContains(t, string(encoded), "secret-canary")
	assert.NotContains(t, string(encoded), "https://")
}

func TestImmediateTaskSubmissionRecordsTerminalAcceptedState(t *testing.T) {
	var billingEvents []string
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{}, &model.User{}, &model.Channel{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	oldConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldConsume })
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &billingEvents})
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "video", TaskData: []byte(`{"usage":{"total_tokens":12}}`), Immediate: &relaycommon.TaskInfo{Status: string(model.TaskStatusSuccess)}}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	events, err := model.GetTaskEvents(outcome.Task.ID)
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Contains(t, events[1].Payload, `"status":"SUCCESS"`)
	assert.Contains(t, events[2].Payload, `"total_tokens":12`)
	assert.LessOrEqual(t, events[1].Timestamp, events[2].Timestamp)
}

func TestTaskSubmissionRecordsTimelineWithoutRawPayloads(t *testing.T) {
	var billingEvents []string
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{}, &model.User{}, &model.Channel{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	oldConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldConsume })

	c := taskSubmissionTestContext()
	c.Set("task_request", map[string]any{"model": "private-model", "prompt": "public prompt", "api_key": "secret-canary"})
	info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &billingEvents})
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "video", UpstreamTaskID: "private-id", TaskData: []byte(`{"url":"https://secret.invalid/video?key=secret-canary"}`)}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	events, err := model.GetTaskEvents(outcome.Task.ID)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "request", events[0].Kind)
	assert.Equal(t, "accepted", events[1].Kind)
	assert.JSONEq(t, `{"model":"plugin-model"}`, events[0].Payload)
	assert.JSONEq(t, `{"id":"task_public","status":"queued","model":"plugin-model"}`, events[1].Payload)
	for _, event := range events {
		assert.NotContains(t, event.Payload, "secret-canary")
		assert.NotContains(t, event.Payload, "private-id")
	}
}

func TestBulkTaskFailureRecordsResultEvent(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := &model.Task{TaskID: "public-id", UserId: 7, Status: model.TaskStatusSubmitted}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, model.TaskBulkUpdateByID([]int64{task.ID}, map[string]any{"status": "FAILURE", "progress": "100%"}))
	events, err := model.GetTaskEvents(task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "result", events[0].Kind)
	assert.Contains(t, events[0].Payload, `"status":"FAILURE"`)
}

func TestTerminalTaskTransitionRecordsResultOnce(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := &model.Task{TaskID: "public-id", UserId: 7, Status: model.TaskStatusInProgress, Data: []byte(`{"usage":{"total_tokens":12},"api_key":"secret-canary"}`)}
	require.NoError(t, db.Create(task).Error)
	task.Status = model.TaskStatusSuccess
	task.FinishTime = 1700000030
	won, err := task.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	events, err := model.GetTaskEvents(task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "result", events[0].Kind)
	assert.Equal(t, task.FinishTime, events[0].Timestamp)
	assert.NotContains(t, events[0].Payload, "secret-canary")

	won, err = task.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	assert.False(t, won)
	events, err = model.GetTaskEvents(task.ID)
	require.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestTaskLogDTOSeparatesUserAdminAndRootDetails(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		Platform:   "document-parser",
		Properties: model.Properties{OriginModelName: "public-model", UpstreamModelName: "private-model"},
		Data:       []byte(`{"model":"private-model","plugin":"document-parser"}`),
		PrivateData: model.TaskPrivateData{
			Key:            "channel-secret-canary",
			UpstreamTaskID: "upstream-private",
			NodeName:       "node-a",
			Execution: &model.TaskExecutionSnapshot{
				RequestID:   "request-public",
				RequestPath: "/v1/documents",
				TaskPlugin: &model.TaskPluginSnapshot{
					Key:     "document-parser",
					Name:    "Document Parser",
					Version: "1.2.3",
					Author: &model.TaskPluginAuthorSnapshot{
						Name: "Community Author",
						URL:  "https://plugins.example/author",
					},
					APIVersion: 1,
					Generation: 42,
				},
			},
		},
	}

	userView := tasksToDto([]*model.Task{task}, false, common.RoleCommonUser)[0]
	assert.Nil(t, userView.AdminInfo)
	assert.Nil(t, userView.RootInfo)
	userJSON, err := common.Marshal(userView)
	require.NoError(t, err)
	assert.NotContains(t, string(userJSON), "private-model")
	assert.NotContains(t, string(userJSON), "document-parser")
	assert.Contains(t, string(userJSON), "public-model")
	assert.Equal(t, "private-model", task.Properties.UpstreamModelName)

	adminView := tasksToDto([]*model.Task{task}, false, common.RoleAdminUser)[0]
	require.NotNil(t, adminView.AdminInfo)
	require.NotNil(t, adminView.AdminInfo.TaskPlugin)
	assert.Equal(t, "document-parser", adminView.AdminInfo.TaskPlugin.Key)
	assert.Equal(t, "Document Parser", adminView.AdminInfo.TaskPlugin.Name)
	assert.Equal(t, "1.2.3", adminView.AdminInfo.TaskPlugin.Version)
	require.NotNil(t, adminView.AdminInfo.TaskPlugin.Author)
	assert.Equal(t, "Community Author", adminView.AdminInfo.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", adminView.AdminInfo.TaskPlugin.Author.URL)
	assert.Equal(t, "request-public", adminView.AdminInfo.RequestID)
	assert.Equal(t, "/v1/documents", adminView.AdminInfo.RequestPath)
	assert.Nil(t, adminView.RootInfo)
	assert.Equal(t, task.Properties, adminView.Properties)
	assert.Equal(t, "document-parser", adminView.Platform)

	rootView := tasksToDto([]*model.Task{task}, false, common.RoleRootUser)[0]
	require.NotNil(t, rootView.AdminInfo)
	require.NotNil(t, rootView.RootInfo)
	require.NotNil(t, rootView.RootInfo.TaskPlugin)
	assert.Equal(t, 1, rootView.RootInfo.TaskPlugin.APIVersion)
	assert.Equal(t, uint64(42), rootView.RootInfo.TaskPlugin.Generation)
	assert.Equal(t, "upstream-private", rootView.RootInfo.UpstreamTaskID)
	assert.Equal(t, "node-a", rootView.RootInfo.NodeName)

	adminJSON, err := common.Marshal(adminView)
	require.NoError(t, err)
	assert.NotContains(t, string(adminJSON), "channel-secret-canary")
	assert.NotContains(t, string(adminJSON), "upstream-private")

	rootJSON, err := common.Marshal(rootView)
	require.NoError(t, err)
	assert.NotContains(t, string(rootJSON), "channel-secret-canary")
	assert.Contains(t, string(rootJSON), "upstream-private")
}

func TestTaskVideoRequestSnapshot(t *testing.T) {
	for _, tc := range []struct{ name, action, body, want string }{
		{"native doubao", "text_to_video", `{"metadata":{"resolution":"1080p","duration":8,"content":[{"type":"video_url","video_url":{"url":"https://secret.invalid/?key=canary"}}]}}`, `{"resolution":"1080p","duration_seconds":8,"has_reference_video":true}`},
		{"native ali", "reference_to_video", `{"metadata":{"parameters":{"size":"1280*720","duration":"5"},"input":{"media":[{"type":"reference_video","url":"secret-canary"}]}}}`, `{"resolution":"1280x720","duration_seconds":5,"has_reference_video":true}`},
		{"image is not video", "image_to_video", `{"seconds":0,"size":"720p","content":[{"type":"image_url","image_url":{"url":"secret-canary"}}]}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false}`},
		{"wrapped video urls", "text_to_video", `{"parameters":{"durationSeconds":4,"width":1920,"height":1080},"input":{"video_urls":["secret-canary"]}}`, `{"resolution":"1920x1080","duration_seconds":4,"has_reference_video":true}`},
		{"invalid", "text_to_video", `{"seconds":-1,"resolution":"https://secret.invalid","reference_video":42}`, `{}`},
		{"too large", "text_to_video", `{"duration":3601,"size":"999999x720"}`, `{}`},
		{"unknown", "text_to_video", `{}`, `{}`},
		{"empty known content", "text_to_video", `{"content":[]}`, `{"has_reference_video":false}`},
		{"nonvideo", "text_to_image", `{"size":"720p","seconds":5,"video_urls":["secret"]}`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &body))
			info := taskVideoRequestSnapshot(body, tc.action)
			encoded, err := common.Marshal(info)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(encoded))
			assert.NotContains(t, string(encoded), "canary")
		})
	}
}

func TestVideoRequestSnapshotInsertRollsBackWhenEventCannotPersist(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{})
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	db.Callback().Create().Before("gorm:create").Register("reject_request_snapshot", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "TaskEvent" {
			tx.AddError(fmt.Errorf("request snapshot unavailable"))
		}
	})
	task := &model.Task{TaskID: "rollback-video", SubmitTime: 1700000000, PrivateData: model.TaskPrivateData{VideoInfo: &dto.TaskVideoInfo{Resolution: "480p"}}}
	require.Error(t, task.Insert())
	var count int64
	require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestVideoRequestSnapshotSurvivesPrivateDataReplacement(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{}, &model.User{}, &model.Channel{})
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	oldConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldConsume })
	c := taskSubmissionTestContext()
	c.Set("task_request", map[string]any{"resolution": "480p", "duration": 5, "content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "secret-canary"}}}})
	events := []string{}
	info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
	info.Action = constant.TaskActionTextToVideo
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "video", TaskData: []byte(`{"resolution":"1080p","duration":99,"usage":{"total_tokens":12}}`)}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NoError(t, db.Model(&model.Task{}).Where("id = ?", outcome.Task.ID).Update("private_data", "{}").Error)
	for _, status := range []model.TaskStatus{model.TaskStatusInProgress, model.TaskStatusSuccess, model.TaskStatusFailure} {
		require.NoError(t, db.Model(&model.Task{}).Where("id = ?", outcome.Task.ID).Update("status", status).Error)
		for _, tasks := range [][]*model.Task{model.TaskGetAllUserTask(info.UserId, 0, 10, model.SyncTaskQueryParams{}), model.TaskGetAllTasks(0, 10, model.SyncTaskQueryParams{})} {
			require.Len(t, tasks, 1)
			view := tasksToDto(tasks, false, common.RoleCommonUser)[0]
			require.NotNil(t, view.VideoInfo)
			assert.Equal(t, "480p", view.VideoInfo.Resolution)
			require.NotNil(t, view.VideoInfo.DurationSeconds)
			assert.Equal(t, float64(5), *view.VideoInfo.DurationSeconds)
			require.NotNil(t, view.VideoInfo.HasReferenceVideo)
			assert.True(t, *view.VideoInfo.HasReferenceVideo)
			if status == model.TaskStatusSuccess {
				require.NotNil(t, view.VideoInfo.ConsumedTokens)
				assert.Equal(t, 12, *view.VideoInfo.ConsumedTokens)
			} else {
				assert.Nil(t, view.VideoInfo.ConsumedTokens)
			}
		}
	}
	storedEvents, err := model.GetTaskEvents(outcome.Task.ID)
	require.NoError(t, err)
	require.NotEmpty(t, storedEvents)
	assert.Contains(t, storedEvents[0].Payload, `"video_info"`)
	assert.NotContains(t, storedEvents[0].Payload, "secret-canary")
}

func TestTaskSubmissionCapturesSafeVideoRequest(t *testing.T) {
	events := []string{}
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{}, &model.User{}, &model.Channel{})
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	oldConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldConsume })
	c := taskSubmissionTestContext()
	c.Set("task_request", &dto.VideoRequest{Duration: 5, Width: 1280, Height: 720, Image: "secret-canary"})
	info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
	info.Action = constant.TaskActionImageToVideo
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "video", UpstreamTaskID: "vendor-private", TaskData: []byte(`{"resolution":"4k","duration":99}`)}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	var stored model.Task
	require.NoError(t, db.First(&stored, outcome.Task.ID).Error)
	encoded, err := common.Marshal(stored.PrivateData.VideoInfo)
	require.NoError(t, err)
	assert.JSONEq(t, `{"resolution":"1280x720","duration_seconds":5,"has_reference_video":false}`, string(encoded))
	assert.NotContains(t, string(encoded), "canary")
}

func TestCompletedVideoLogFallsBackToResultMetadata(t *testing.T) {
	for _, tc := range []struct{ name, snapshot, data, want string }{
		{"historical result", `{}`, `{"resolution":"480p","duration":5,"usage":{"total_tokens":48437},"content":{"video_url":"private-canary"}}`, `{"resolution":"480p","duration_seconds":5,"consumed_tokens":48437}`},
		{"request takes precedence", `{"video_info":{"resolution":"720p","duration_seconds":0,"has_reference_video":false}}`, `{"resolution":"480p","duration":5,"usage":{"total_tokens":12}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false,"consumed_tokens":12}`},
		{"partial request", `{"video_info":{"has_reference_video":true}}`, `{"resolution":"480p","duration":5}`, `{"resolution":"480p","duration_seconds":5,"has_reference_video":true}`},
		{"invalid result", `{}`, `{"resolution":"https://secret.invalid","duration":-1,"content":{"video_url":"private-canary"}}`, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Data: []byte(tc.data)}
			require.NoError(t, task.PrivateData.Scan(tc.snapshot))
			for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
				view := tasksToDto([]*model.Task{task}, false, role)[0]
				encoded, err := common.Marshal(view.VideoInfo)
				require.NoError(t, err)
				assert.JSONEq(t, tc.want, string(encoded))
				assert.NotContains(t, string(encoded), "private-canary")
			}
		})
	}
}

func TestHistoricalVideoTokensWithoutRequestSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, action, status, data string
		want                       int
	}{
		{"actual tokens", constant.TaskActionTextToVideo, "SUCCESS", `{"usage":{"total_tokens":40594,"completion_tokens":40594}}`, 40594},
		{"completion fallback", constant.TaskActionImageToVideo, "SUCCESS", `{"usage":{"completion_tokens":42}}`, 42},
		{"zero", constant.TaskActionTextToVideo, "SUCCESS", `{"usage":{"total_tokens":0}}`, 0},
		{"pending", constant.TaskActionTextToVideo, "IN_PROGRESS", `{"usage":{"total_tokens":42}}`, -1},
		{"units", constant.TaskActionTextToVideo, "SUCCESS", `{"usage":{"credits":42}}`, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{Action: tc.action, Status: model.TaskStatus(tc.status), Data: []byte(tc.data)}
			for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
				view := tasksToDto([]*model.Task{task}, false, role)[0]
				require.NotNil(t, view.VideoInfo)
				if tc.want < 0 {
					assert.Nil(t, view.VideoInfo.ConsumedTokens)
				} else {
					require.NotNil(t, view.VideoInfo.ConsumedTokens)
					assert.Equal(t, tc.want, *view.VideoInfo.ConsumedTokens)
				}
				assert.Empty(t, view.VideoInfo.Resolution)
				assert.Nil(t, view.VideoInfo.DurationSeconds)
				assert.Nil(t, task.PrivateData.VideoInfo)
			}
		})
	}
	assert.Nil(t, taskVideoLogInfo(&model.Task{Action: "text_to_image", Status: model.TaskStatusSuccess, Data: []byte(`{"usage":{"total_tokens":42}}`)}))
}

func TestHistoricalVideoTokensLoadOmittedListData(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := model.Task{TaskID: "historical-video-tokens", UserId: 1, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, Data: []byte(`{"usage":{"total_tokens":40594},"content":{"video_url":"private-canary"}}`)}
	require.NoError(t, db.Create(&task).Error)
	for _, tasks := range [][]*model.Task{model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{}), model.TaskGetAllTasks(0, 10, model.SyncTaskQueryParams{})} {
		require.Len(t, tasks, 1)
		for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
			view := tasksToDto(tasks, false, role)[0]
			require.NotNil(t, view.VideoInfo)
			require.NotNil(t, view.VideoInfo.ConsumedTokens)
			assert.Equal(t, 40594, *view.VideoInfo.ConsumedTokens)
			encoded, err := common.Marshal(view)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-canary")
			assert.Empty(t, tasks[0].Data)
			assert.Nil(t, tasks[0].PrivateData.VideoInfo)
		}
	}
}

func TestTaskLogVideoSnapshotDatabase(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{})
	var private model.TaskPrivateData
	require.NoError(t, private.Scan(`{"video_info":{"duration_seconds":0,"has_reference_video":false}}`))
	task := model.Task{TaskID: "video-snapshot", UserId: 1, Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess, PrivateData: private, Data: []byte(`{"usage":{"total_tokens":12},"url":"private-canary"}`)}
	require.NoError(t, db.Create(&task).Error)
	var loaded model.Task
	require.NoError(t, db.First(&loaded, task.ID).Error)
	encoded, err := common.Marshal(tasksToDto([]*model.Task{&loaded}, false, common.RoleCommonUser)[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"duration_seconds":0`)
	assert.Contains(t, string(encoded), `"has_reference_video":false`)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	for _, tasks := range [][]*model.Task{model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{}), model.TaskGetAllTasks(0, 10, model.SyncTaskQueryParams{})} {
		require.Len(t, tasks, 1)
		encoded, err := common.Marshal(tasksToDto(tasks, false, common.RoleCommonUser)[0])
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"consumed_tokens":12`)
		assert.NotContains(t, string(encoded), "private-canary")
	}
}

func TestTaskLogVideoInfoUsesOnlyActualSuccessfulUsage(t *testing.T) {
	for _, tc := range []struct {
		name, status, data, want string
	}{
		{"total first", "SUCCESS", `{"usage":{"total_tokens":123,"completion_tokens":100}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false,"consumed_tokens":123}`},
		{"zero actual", "SUCCESS", `{"usage":{"total_tokens":0,"completion_tokens":100}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false,"consumed_tokens":0}`},
		{"completion fallback", "SUCCESS", `{"usage":{"completion_tokens":42}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false,"consumed_tokens":42}`},
		{"not completed", "IN_PROGRESS", `{"usage":{"total_tokens":123}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false}`},
		{"units not tokens", "SUCCESS", `{"usage":{"credits":20,"units":50},"tokens":900,"upstreamUnits":999}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false}`},
		{"invalid actual", "SUCCESS", `{"usage":{"total_tokens":-1,"completion_tokens":1.5}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false}`},
		{"oversized", "SUCCESS", `{"usage":{"total_tokens":1e100}}`, `{"resolution":"720p","duration_seconds":0,"has_reference_video":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{Action: constant.TaskActionTextToVideo, Status: model.TaskStatus(tc.status), Data: []byte(tc.data)}
			require.NoError(t, task.PrivateData.Scan(`{"key":"secret-canary","video_info":{"resolution":"720p","duration_seconds":0,"has_reference_video":false,"consumed_tokens":9999}}`))
			for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
				encoded, err := common.Marshal(tasksToDto([]*model.Task{task}, false, role)[0])
				require.NoError(t, err)
				var view map[string]any
				require.NoError(t, common.Unmarshal(encoded, &view))
				videoJSON, err := common.Marshal(view["video_info"])
				require.NoError(t, err)
				assert.JSONEq(t, tc.want, string(videoJSON))
				assert.NotContains(t, string(encoded), "secret-canary")
				viewDTO := tasksToDto([]*model.Task{task}, false, role)[0]
				*viewDTO.VideoInfo.DurationSeconds = 99
				*viewDTO.VideoInfo.HasReferenceVideo = true
				assert.Equal(t, float64(0), *task.PrivateData.VideoInfo.DurationSeconds)
				assert.False(t, *task.PrivateData.VideoInfo.HasReferenceVideo)
				assert.Equal(t, 9999, *task.PrivateData.VideoInfo.ConsumedTokens)
			}
		})
	}
}

func TestTaskLogDTODoesNotInventHistoricalPluginProvenance(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_without_snapshot",
		Platform: "document-parser",
	}

	adminView := tasksToDto([]*model.Task{task}, false, common.RoleAdminUser)[0]

	assert.Nil(t, adminView.AdminInfo)
	assert.Nil(t, adminView.RootInfo)
}

func TestTaskLogDTOReplacesLegacyVideoURLWithAvailabilityFlag(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_legacy_video",
		Platform:   "jimeng",
		Action:     constant.TaskActionTextToVideo,
		Status:     model.TaskStatusSuccess,
		FailReason: "https://private-upstream.invalid/video.mp4?signature=secret",
	}

	view := tasksToDto([]*model.Task{task}, false, common.RoleCommonUser)[0]
	assert.True(t, view.LegacyVideoAvailable)
	assert.Empty(t, view.ResultURL)
	assert.Empty(t, view.FailReason)
	encoded, err := common.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-upstream.invalid")
	assert.NotContains(t, string(encoded), "result_url")
	assert.Contains(t, string(encoded), "legacy_video_available")
}

func TestTaskLogDTOKeepsFailureReasonAndDoesNotMarkPluginTaskLegacy(t *testing.T) {
	failed := &model.Task{
		TaskID:     "task_failed",
		Platform:   "jimeng",
		Action:     constant.TaskActionTextToVideo,
		Status:     model.TaskStatusFailure,
		FailReason: "provider rejected the request",
	}
	failedView := tasksToDto([]*model.Task{failed}, false, common.RoleCommonUser)[0]
	assert.Equal(t, "provider rejected the request", failedView.FailReason)
	assert.False(t, failedView.LegacyVideoAvailable)

	pluginTask := &model.Task{
		TaskID:     "task_plugin_video",
		Platform:   "community-video",
		Action:     constant.TaskActionTextToVideo,
		Status:     model.TaskStatusSuccess,
		FailReason: "https://stale-upstream.invalid/plugin-video.mp4",
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://private-upstream.invalid/plugin-video.mp4",
			Execution: &model.TaskExecutionSnapshot{
				TaskPlugin: &model.TaskPluginSnapshot{Key: "community-video"},
			},
		},
	}
	pluginView := tasksToDto([]*model.Task{pluginTask}, false, common.RoleCommonUser)[0]
	assert.False(t, pluginView.LegacyVideoAvailable)
	assert.Empty(t, pluginView.ResultURL)
	assert.Empty(t, pluginView.FailReason)
}
