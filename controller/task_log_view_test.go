package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFailedTaskSubmissionErrorSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name                                                              string
		local, retry, success, oversized, disabled, fallback, suppressed  bool
		want                                                              int
		disconnected, discarded, missingResult, cancelBefore, cancelAfter bool
	}{
		{name: "upstream without task ID", want: 1},
		{name: "local without task ID", local: true, want: 1},
		{name: "retry final failure", retry: true, want: 2},
		{name: "retry then success", retry: true, success: true, want: 1},
		{name: "bounded details", oversized: true, want: 1},
		{name: "logging disabled", local: true, disabled: true},
		{name: "error policy suppressed", suppressed: true},
		{name: "ordinary fallback", fallback: true, want: 1},
		{name: "discarded durable response", discarded: true, want: 1},
		{name: "disconnected missing result", disconnected: true, missingResult: true, want: 1},
		{name: "disconnected before attempt", disconnected: true, cancelBefore: true, want: 1},
		{name: "disconnected after submit", disconnected: true, cancelAfter: true, want: 1},
		{name: "disconnected local attempt", disconnected: true, local: true, want: 1},
		{name: "disconnected retry then missing result", disconnected: true, retry: true, missingResult: true, want: 2},
		{name: "disconnected suppressed", disconnected: true, suppressed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_TASK_DB_DIALECT", "sqlite")
			db, _ := openTaskDialectDatabase(t, &model.User{}, &model.Log{}, &model.Task{}, &model.TaskEvent{}, &model.Channel{})
			oldDB, oldLog := model.DB, model.LOG_DB
			oldEnabled, oldRetries := constant.ErrorLogEnabled, common.RetryTimes
			oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
			oldConsume := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			model.DB, model.LOG_DB = db, db
			constant.ErrorLogEnabled, common.RetryTimes = !tc.disabled, 1
			common.RedisEnabled, common.MemoryCacheEnabled = false, false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLog
				constant.ErrorLogEnabled, common.RetryTimes = oldEnabled, oldRetries
				common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
				common.LogConsumeEnabled = oldConsume
			})
			require.NoError(t, db.Create(&model.User{Id: 7, Username: "snapshot-owner", AffCode: "snapshot"}).Error)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := `{"prompt":"original","api_key":"request-canary","count":9007199254740993}`
			if tc.oversized {
				body = `{"prompt":"` + strings.Repeat("x", 61*1024) + `"}`
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			requestContext, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(requestContext)
			if tc.cancelBefore {
				cancel()
			}
			c.Set("id", 7)
			c.Set("token_id", 12)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			if tc.suppressed {
				capture := installTaskEventResponseCapture(c)
				capture.arm(c, taskEventOriginalRequest(c))
				capture.finalError = types.NewErrorWithStatusCode(fmt.Errorf("policy rejection"), types.ErrorCodeBadResponseStatusCode, 400, types.ErrOptionWithNoRecordErrorLog())
				service.RecordRelayErrorLog(c, 1, capture.finalError, nil)
				if !tc.disconnected {
					c.JSON(400, gin.H{"error": "policy rejection"})
				}
				capture.record()
			} else if tc.fallback {
				RelayTaskPluginEndpoint(c, func(c *gin.Context) {
					service.ProcessChannelError(c, types.ChannelError{ChannelId: 1}, taskSubmissionAPIError(&dto.TaskError{Message: "ordinary", StatusCode: 400}), nil)
					c.JSON(400, gin.H{"error": "ordinary"})
				})
			} else {
				capture := installTaskEventResponseCapture(c)
				info := taskSubmissionRelayInfo(nil)
				info.UserId = 7
				// A context-selected channel can be reused without channel-cache fixtures.
				info.LockedChannel, info.ChannelMeta = nil, nil
				c.Set("channel_id", 1)
				attempts := 0
				_, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
					attempts++
					if tc.cancelAfter {
						cancel()
					}
					if tc.missingResult && (!tc.retry || attempts == 2) {
						return nil, nil
					}
					if attempts == 2 && tc.success {
						info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 1}
						return &relay.TaskSubmitResult{Platform: "video"}, nil
					}
					status := 400
					if tc.retry && attempts == 1 {
						status = 503
					}
					return nil, &dto.TaskError{Code: fmt.Sprintf("attempt_%d", attempts), Message: fmt.Sprintf("attempt %d api_key=error-canary", attempts), StatusCode: status, LocalError: tc.local}
				})
				if tc.success {
					// A later successful presenter must never replace the earlier error snapshot.
					c.JSON(200, gin.H{"result": "success-canary"})
				} else {
					require.NotNil(t, taskErr)
					if tc.disconnected {
						assert.False(t, c.Writer.Written())
					} else {
						respondTaskSubmissionError(c, taskErr)
					}
					if tc.discarded {
						capture.task = &model.Task{ID: 1, SubmitTime: 1700000000, PrivateData: model.TaskPrivateData{ResultDiscarded: true}}
					}
					// Native presenters may own the final envelope; capture must use it.
					if tc.oversized {
						_, err := c.Writer.WriteString(strings.Repeat("x", 61*1024))
						require.NoError(t, err)
					}
				}
				capture.record()
				capture.record()
			}
			var rows []*model.Log
			require.NoError(t, db.Order("id").Find(&rows).Error)
			require.Len(t, rows, tc.want)
			for i, row := range rows {
				if tc.fallback {
					assert.NotContains(t, row.Other, "task_submission")
					continue
				}
				var stored struct {
					Admin struct {
						Submission struct{ Request, Response json.RawMessage } `json:"task_submission"`
					} `json:"admin_info"`
				}
				require.NoError(t, common.UnmarshalJsonStr(row.Other, &stored))
				require.NotEmpty(t, stored.Admin.Submission.Request)
				require.NotEmpty(t, stored.Admin.Submission.Response)
				assert.LessOrEqual(t, len(stored.Admin.Submission.Request), model.TaskEventPayloadLimit)
				assert.LessOrEqual(t, len(stored.Admin.Submission.Response), model.TaskEventPayloadLimit)
				assert.NotContains(t, row.Other, "canary")
				if tc.oversized {
					assert.Contains(t, string(stored.Admin.Submission.Request), "size_limit_exceeded")
					assert.Contains(t, string(stored.Admin.Submission.Response), "size_limit_exceeded")
				} else {
					assert.Contains(t, string(stored.Admin.Submission.Request), "9007199254740993")
					assert.Contains(t, string(stored.Admin.Submission.Request), "original")
				}
				if tc.discarded {
					assert.JSONEq(t, `{"unavailable":"result_discarded"}`, string(stored.Admin.Submission.Response))
				} else if tc.disconnected && i == len(rows)-1 {
					assert.JSONEq(t, `{"unavailable":"response_not_written"}`, string(stored.Admin.Submission.Response))
				}
				if !tc.success && !tc.oversized && !tc.discarded && !tc.disconnected && i == len(rows)-1 {
					redacted, err := model.RedactTaskEventDetail(json.RawMessage(recorder.Body.Bytes()))
					require.NoError(t, err)
					assert.JSONEq(t, string(redacted), string(stored.Admin.Submission.Response))
				}
				if tc.retry && i == 0 {
					assert.Contains(t, string(stored.Admin.Submission.Response), "attempt_1")
					assert.NotContains(t, string(stored.Admin.Submission.Response), "attempt_2")
				}
				admin := *row
				model.FormatAdminLogs([]*model.Log{&admin})
				assert.Contains(t, admin.Other, "task_submission")
			}
			userRows, err := model.GetLogByTokenId(12)
			require.NoError(t, err)
			for _, row := range userRows {
				assert.NotContains(t, row.Other, "task_submission")
				assert.NotContains(t, row.Other, "original")
			}
		})
	}
}

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

func TestLogTaskEventsAdminSubmissionView(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{}, &model.Log{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	tasks := []model.Task{
		{TaskID: "shared-public-id", UserId: 7},
		{TaskID: "shared-public-id", UserId: 8},
		{TaskID: "ambiguous", UserId: 7},
		{TaskID: "ambiguous", UserId: 7},
		{TaskID: "historical", UserId: 7},
	}
	require.NoError(t, db.Create(&tasks).Error)
	// Seed historical, unsanitized details directly to verify read-time redaction.
	events := []model.TaskEvent{
		{TaskID: tasks[0].ID, Kind: "request", Timestamp: 1, Payload: `{"admin_info":{"complete":true,"payload":{"prompt":"owner-seven","api_key":"key-canary","nested":{"authorization":"Bearer bearer-canary"},"url":"https://user:pass@cdn.example/input?token=url-canary&width=720","count":9007199254740993}}}`},
		{TaskID: tasks[0].ID, Kind: "accepted", Timestamp: 2, Payload: `{"admin_info":{"complete":true,"payload":{"body":{"id":"shared-public-id","status":"queued","token":"response-canary"}}}}`},
		{TaskID: tasks[0].ID, Kind: "result", Timestamp: 3, Payload: `{"status":"result-hidden"}`},
		{TaskID: tasks[0].ID, Kind: "future-kind", Timestamp: 4, Payload: `{"message":"future-hidden"}`},
		{TaskID: tasks[1].ID, Kind: "request", Timestamp: 1, Payload: `{"model":"owner-eight"}`},
	}
	require.NoError(t, db.Create(&events).Error)
	logs := []model.Log{{UserId: 7, Type: model.LogTypeConsume, Other: `{"task_id":"shared-public-id"}`}}
	require.NoError(t, db.Create(&logs).Error)
	for _, tc := range []struct {
		name, query                string
		role, token, status, count int
		want                       string
	}{
		{"admin", "task_id=shared-public-id&user_id=7", common.RoleAdminUser, 0, 200, 2, "owner-seven"},
		{"root", "task_id=shared-public-id&user_id=7", common.RoleRootUser, 0, 200, 2, "owner-seven"},
		{"other owner", "task_id=shared-public-id&user_id=8", common.RoleAdminUser, 0, 200, 1, "owner-eight"},
		{"ordinary owner", "task_id=shared-public-id&user_id=7", common.RoleCommonUser, 0, 403, 0, ""},
		{"guest", "task_id=shared-public-id&user_id=7", common.RoleGuestUser, 0, 403, 0, ""},
		{"admin token", "task_id=shared-public-id&user_id=7", common.RoleAdminUser, 1, 403, 0, ""},
		{"root token", "task_id=shared-public-id&user_id=7", common.RoleRootUser, 1, 403, 0, ""},
		{"missing", "task_id=missing&user_id=7", common.RoleAdminUser, 0, 404, 0, ""},
		{"wrong owner", "task_id=shared-public-id&user_id=9", common.RoleAdminUser, 0, 404, 0, ""},
		{"ambiguous", "task_id=ambiguous&user_id=7", common.RoleAdminUser, 0, 409, 0, ""},
		{"historical", "task_id=historical&user_id=7", common.RoleAdminUser, 0, 200, 0, ""},
		{"no task", "user_id=7", common.RoleAdminUser, 0, 400, 0, ""},
		{"no owner", "task_id=shared-public-id", common.RoleAdminUser, 0, 400, 0, ""},
		{"invalid owner", "task_id=shared-public-id&user_id=abc", common.RoleAdminUser, 0, 400, 0, ""},
		{"zero owner", "task_id=shared-public-id&user_id=0", common.RoleAdminUser, 0, 400, 0, ""},
		{"negative owner", "task_id=shared-public-id&user_id=-7", common.RoleAdminUser, 0, 400, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/log/task-events?"+tc.query, nil)
			c.Set("id", 7)
			c.Set("role", tc.role)
			c.Set("token_id", tc.token)
			GetLogTaskEvents(c)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
			var response struct {
				Success bool              `json:"success"`
				Data    []model.TaskEvent `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.status == 200, response.Success)
			assert.Len(t, response.Data, tc.count)
			body := recorder.Body.String()
			assert.NotContains(t, body, "canary")
			assert.NotContains(t, body, "user:pass")
			assert.NotContains(t, body, "result-hidden")
			assert.NotContains(t, body, "future-hidden")
			if tc.want != "" {
				assert.Contains(t, body, tc.want)
			}
			if tc.count == 2 {
				assert.Equal(t, "request", response.Data[0].Kind)
				assert.Equal(t, "accepted", response.Data[1].Kind)
				assert.Contains(t, response.Data[0].Payload, "9007199254740993")
				assert.Contains(t, response.Data[0].Payload, "width=720")
				require.NotNil(t, response.Data[0].DetailComplete)
				assert.True(t, *response.Data[0].DetailComplete)
				assert.NotContains(t, body, "owner-eight")
			}
			if tc.name == "historical" {
				assert.Contains(t, body, `"data":[]`)
			}
		})
	}
	var afterTasks []model.Task
	var afterEvents []model.TaskEvent
	var afterLogs []model.Log
	require.NoError(t, db.Order("id").Find(&afterTasks).Error)
	require.NoError(t, db.Order("id").Find(&afterEvents).Error)
	require.NoError(t, db.Order("id").Find(&afterLogs).Error)
	assert.Equal(t, tasks, afterTasks)
	assert.Equal(t, events, afterEvents)
	assert.Equal(t, logs, afterLogs)
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
	require.NoError(t, model.RecordTaskEventDetail(task.ID, "request", 1700000000, map[string]any{"model": "public-model", "prompt": "hello"}))
	require.NoError(t, model.RecordTaskEventDetail(task.ID, "accepted", 1700000001, map[string]any{"id": task.TaskID, "status": "queued"}))
	require.NoError(t, model.RecordTaskEvent(task.ID, "result", 1700000010, map[string]any{"status": "SUCCESS", "total_tokens": 12}))
	require.NoError(t, model.RecordTaskEvent(task.ID, "result", 1700000011, map[string]any{"status": "FAILURE"}))

	for _, tc := range []struct {
		name                 string
		userID, role, status int
		tokenID              int
		wantCount            int
	}{
		{"owner", 7, common.RoleCommonUser, 200, 0, 2},
		{"same public ID, different owner", 8, common.RoleCommonUser, 404, 0, 0},
		{"foreign user", 9, common.RoleCommonUser, 404, 0, 0},
		{"admin", 8, common.RoleAdminUser, 200, 0, 3},
		{"root", 9, common.RoleRootUser, 200, 0, 3},
		{"admin owner token", 7, common.RoleAdminUser, 200, 1, 2},
		{"root owner token", 7, common.RoleRootUser, 200, 1, 2},
		{"api token", 9, common.RoleRootUser, 404, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/task/%d/events", task.ID), nil)
			c.Params = gin.Params{{Key: "task_id", Value: strconv.FormatInt(task.ID, 10)}}
			c.Set("id", tc.userID)
			c.Set("role", tc.role)
			c.Set("token_id", tc.tokenID)
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
			assert.Equal(t, "request", response.Data[0].Kind)
			assert.Equal(t, "accepted", response.Data[1].Kind)
			assert.JSONEq(t, `{"model":"public-model","prompt":"hello"}`, response.Data[0].Payload)
			assert.JSONEq(t, `{"id":"public-id","status":"queued"}`, response.Data[1].Payload)
			if tc.wantCount == 3 {
				assert.Equal(t, "result", response.Data[2].Kind)
				assert.Contains(t, response.Data[2].Payload, "SUCCESS")
			} else {
				assert.NotContains(t, recorder.Body.String(), `"kind":"result"`)
				assert.NotContains(t, recorder.Body.String(), "SUCCESS")
			}
			assert.NotContains(t, recorder.Body.String(), "foreign-model")
			assert.NotContains(t, recorder.Body.String(), "FAILURE")
			if tc.name == "admin" {
				assert.Contains(t, recorder.Body.String(), `"detail_complete":false`)
				assert.Contains(t, recorder.Body.String(), `"detail_unavailable":"not_recorded"`)
			}
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
	delete(payload, "admin_info")
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
	require.NoError(t, events[0].ProjectDetail(false))
	// The original test request was {}, not the normalized plugin request.
	assert.JSONEq(t, `{}`, events[0].Payload)
	assert.JSONEq(t, `{"id":"task_public","status":"queued","model":"plugin-model"}`, events[1].Payload)
	for _, event := range events {
		assert.NotContains(t, event.Payload, "secret-canary")
		assert.NotContains(t, event.Payload, "private-id")
	}
}

func TestTaskEventAdminDetailProjection(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := &model.Task{TaskID: "detail-task", UserId: 7, Status: model.TaskStatusFailure, FailReason: "provider rejected", Data: []byte(`{"nested":[{"api_key":"credential-canary","number":9007199254740993,"url":"https://user:password@cdn.example/video?token=url-canary&width=720"}]}`)}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, model.RecordTaskEvent(task.ID, "result", 1700000000, model.TaskResultEventPayload(task)))
	for _, tc := range []struct {
		name        string
		role, token int
		full        bool
	}{
		{"admin browser", common.RoleAdminUser, 0, true},
		{"root browser", common.RoleRootUser, 0, true},
		{"ordinary browser", common.RoleCommonUser, 0, false},
		{"root token", common.RoleRootUser, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/task/events", nil)
			c.Params = gin.Params{{Key: "task_id", Value: strconv.FormatInt(task.ID, 10)}}
			c.Set("id", 7)
			c.Set("role", tc.role)
			c.Set("token_id", tc.token)
			GetDashboardTaskEvents(c)
			require.Equal(t, 200, recorder.Code)
			body := recorder.Body.String()
			assert.NotContains(t, body, "credential-canary")
			assert.NotContains(t, body, "url-canary")
			assert.NotContains(t, body, "user:password")
			if tc.full {
				assert.Contains(t, body, "9007199254740993")
				assert.Contains(t, body, "provider rejected")
				assert.Contains(t, body, `"detail_complete":true`)
			} else {
				var response struct {
					Data []model.TaskEvent `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Empty(t, response.Data)
				assert.Contains(t, body, `"data":[]`)
				assert.NotContains(t, body, "provider rejected")
				assert.NotContains(t, body, "nested")
				assert.NotContains(t, body, "admin_info")
			}
		})
	}
}

func TestTaskEventOriginalRequestAndActualResponse(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{}, &model.User{}, &model.Channel{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	oldConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = oldConsume })
	c := taskSubmissionTestContext()
	c.Request.Body = io.NopCloser(strings.NewReader(`{"model":"original-model","prompt":"original prompt","count":9007199254740993}`))
	c.Request.Header.Set("Content-Type", "application/json")
	capture := installTaskEventResponseCapture(c)
	var billing []string
	outcome, taskErr := executeTaskSubmissionWith(c, taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &billing}), func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		c.Set("task_request", map[string]any{"prompt": "mutated prompt"})
		return &relay.TaskSubmitResult{Platform: "video", UpstreamTaskID: "vendor-id"}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	c.JSON(502, gin.H{"error": gin.H{"message": "actual presenter failure", "api_key": "response-canary"}})
	capture.record()
	events, err := model.GetTaskEvents(outcome.Task.ID)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.NoError(t, events[0].ProjectDetail(true))
	assert.Contains(t, events[0].Payload, "original prompt")
	assert.NotContains(t, events[0].Payload, "mutated prompt")
	assert.Contains(t, events[0].Payload, "9007199254740993")
	require.NoError(t, events[1].ProjectDetail(true))
	assert.Contains(t, events[1].Payload, "actual presenter failure")
	assert.NotContains(t, events[1].Payload, "response-canary")
}

func TestTaskEventBusinessDetailProjection(t *testing.T) {
	for _, tc := range []struct{ kind, detail, want string }{
		{"request", `{"model":"client-model","prompt":"hello","duration":0,"generate_audio":false,"metadata":{"seed":9007199254740993,"channel":"ops-canary","model":"mapped-canary"},"price_data":{"ratio":"ops-canary"}}`, `{"model":"client-model","prompt":"hello","duration":0,"generate_audio":false,"metadata":{"seed":9007199254740993}}`},
		{"request", `{"kind":"multipart","fields":{"prompt":["first","second"],"model":["client-model"],"channel_id":["ops-canary"]},"files":[{"field":"image","filename":"input.png","mime_type":"image/png","size":42}],"unavailable":"uploaded_file_bytes_omitted"}`, `{"kind":"multipart","fields":{"prompt":["first","second"],"model":["client-model"]},"files":[{"field":"image","filename":"input.png","mime_type":"image/png","size":42}],"unavailable":"uploaded_file_bytes_omitted"}`},
		{"accepted", `{"status_code":200,"content_type":"application/json","body":{"id":"public-id","status":"queued","model":"mapped-canary","admin_info":{"channel":"ops-canary"},"unknown":{"prompt":"ops-canary"}}}`, `{"status_code":200,"content_type":"application/json","body":{"id":"public-id","status":"queued"}}`},
		{"accepted", `{"events":[{"event":"response.completed","data":{"status":"completed","model":"mapped-canary","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}]}`, `{"events":[{"event":"response.completed","data":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}]}`},
		{"result", `{"task_id":"public-id","status":"SUCCESS","data":{"id":"vendor-id","model":"mapped-canary","content":{"video_url":"https://cdn.example/video?token=credential-canary&width=720","last_frame_url":"https://cdn.example/frame"},"usage":{"total_tokens":9007199254740993,"price":"ops-canary"},"resolution":"720p","duration":5,"node_name":"ops-canary","unknown":"ops-canary"}}`, `{"task_id":"public-id","status":"SUCCESS","data":{"id":"vendor-id","content":{"video_url":"https://cdn.example/video?token=%5BREDACTED%5D&width=720","last_frame_url":"https://cdn.example/frame"},"usage":{"total_tokens":9007199254740993},"resolution":"720p","duration":5}}`},
	} {
		t.Run(tc.kind+tc.detail[:12], func(t *testing.T) {
			stored := `{"admin_info":{"complete":true,"payload":` + tc.detail + `}}`
			event := model.TaskEvent{Kind: tc.kind, Payload: stored}
			require.NoError(t, event.ProjectDetail(false))
			assert.JSONEq(t, tc.want, event.Payload)
			require.NotNil(t, event.DetailComplete)
			assert.True(t, *event.DetailComplete)
			assert.NotContains(t, event.Payload, "canary")
			admin := model.TaskEvent{Kind: tc.kind, Payload: stored}
			require.NoError(t, admin.ProjectDetail(true))
			assert.NotContains(t, admin.Payload, "credential-canary")
		})
	}
	for _, info := range []string{"", `,"admin_info":{"complete":false,"unavailable":"size_limit_exceeded"}`} {
		event := model.TaskEvent{Kind: "request", Payload: `{"model":"client-model"` + info + `}`}
		require.NoError(t, event.ProjectDetail(false))
		require.NotNil(t, event.DetailComplete)
		assert.False(t, *event.DetailComplete)
		assert.NotEmpty(t, event.DetailUnavailable)
		assert.JSONEq(t, `{"model":"client-model"}`, event.Payload)
	}
}

func TestTaskEventCredentialTextAndEnvelopes(t *testing.T) {
	for _, value := range []any{
		map[string]any{"message": "Authorization: Bearer bearer-canary; Basic basic-canary api_key=key-canary x-api-key: header-canary sk-free-canary"},
		map[string]any{"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer envelope-canary"}, map[string]any{"name": "x-api-key", "value": "envelope-key-canary"}}},
		map[string]any{"fields": []any{map[string]any{"name": "api_key", "value": []string{"form-canary", "second-canary"}}}},
	} {
		encoded, err := model.RedactTaskEventDetail(value)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "canary")
		assert.Contains(t, string(encoded), "[REDACTED]")
	}
}

func TestTaskEventURLEncodedRequest(t *testing.T) {
	for _, tc := range []struct{ body, reason string }{
		{"prompt=first&prompt=second&api_key=form-canary&Authorization=Bearer+auth-canary", ""},
		{"prompt=%zz", "invalid_form"},
	} {
		c := taskSubmissionTestContext()
		c.Request = httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		t.Cleanup(func() { common.CleanupBodyStorage(c) })
		snapshot := taskEventOriginalRequest(c)
		encoded, err := model.RedactTaskEventDetail(snapshot)
		require.NoError(t, err)
		if tc.reason != "" {
			assert.Contains(t, string(encoded), tc.reason)
		} else {
			assert.Contains(t, string(encoded), `"prompt":["first","second"]`)
			assert.Contains(t, string(encoded), `"kind":"form"`)
			assert.NotContains(t, string(encoded), "canary")
		}
		replayed, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, tc.body, string(replayed))
	}
}

func TestTaskEventResultURLRedaction(t *testing.T) {
	for _, discarded := range []bool{false, true} {
		task := &model.Task{TaskID: "result", Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{ResultURL: "https://user:pass@cdn.example/video?token=result-canary&width=720", ResultDiscarded: discarded}}
		encoded, err := model.RedactTaskEventDetail(model.TaskResultEventPayload(task)["admin_info"])
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "result-canary")
		assert.NotContains(t, string(encoded), "user:pass")
		if discarded {
			assert.NotContains(t, string(encoded), "result_url")
		} else {
			assert.Contains(t, string(encoded), "https://cdn.example/video?")
			assert.Contains(t, string(encoded), "width=720")
		}
	}
}

func TestTaskEventRedactionAndMultipart(t *testing.T) {
	redacted, err := model.RedactTaskEventDetail(map[string]any{"raw": json.RawMessage(`{"values":[0,false,9007199254740993,1.2300],"Authorization":"Bearer canary","url":"https://u:p@example.com/?X-Amz-Signature=canary&width=0"}`)})
	require.NoError(t, err)
	assert.Contains(t, string(redacted), "9007199254740993")
	assert.Contains(t, string(redacted), "1.2300")
	assert.NotContains(t, string(redacted), "canary")
	assert.NotContains(t, string(redacted), "u:p")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("prompt", "original multipart"))
	file, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("file-bytes-canary"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c := taskSubmissionTestContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	defer common.CleanupBodyStorage(c)
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	snapshot := taskEventOriginalRequest(c)
	encoded, err := common.Marshal(snapshot)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "original multipart")
	assert.Contains(t, string(encoded), "input.png")
	assert.NotContains(t, string(encoded), "file-bytes-canary")
	assert.Contains(t, string(encoded), `"unavailable":"uploaded_file_bytes_omitted"`)
	replayed, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	assert.Equal(t, body.Bytes(), replayed)
}

func TestTaskEventSSECapturePreservesFlushAndBounds(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	for _, tc := range []struct{ name, body, reason string }{
		{"stream", "event: result\ndata: {\"text\":\"actual stream\",\"token\":\"stream-canary\",\"number\":9007199254740993}\n\n", ""},
		{"oversize", strings.Repeat("x", 61*1024), "size_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			capture := installTaskEventResponseCapture(c)
			capture.task = &model.Task{ID: 1}
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.WriteString(tc.body)
			require.NoError(t, err)
			c.Writer.Flush()
			assert.True(t, recorder.Flushed)
			assert.Equal(t, tc.body, recorder.Body.String())
			capture.record()
			events, err := model.GetTaskEvents(1)
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.NoError(t, events[0].ProjectDetail(true))
			assert.NotContains(t, events[0].Payload, "stream-canary")
			assert.Equal(t, tc.reason, events[0].DetailUnavailable)
			if tc.reason == "" {
				assert.Contains(t, events[0].Payload, "actual stream")
				assert.Contains(t, events[0].Payload, "9007199254740993")
			}
		})
	}
}

func TestTaskEventDetailSizeAndDiscardPolicy(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	require.NoError(t, model.RecordTaskEvent(1, "request", 1700000000, map[string]any{"model": "summary"}))
	require.NoError(t, model.RecordTaskEventDetail(1, "request", 1700000001, map[string]any{"prompt": strings.Repeat("界", 30000)}))
	events, err := model.GetTaskEvents(1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.LessOrEqual(t, len(events[0].Payload), 60*1024)
	assert.Contains(t, events[0].Payload, "summary")
	assert.Equal(t, int64(1700000000), events[0].Timestamp)
	require.NoError(t, events[0].ProjectDetail(true))
	require.NotNil(t, events[0].DetailComplete)
	assert.False(t, *events[0].DetailComplete)
	assert.Equal(t, "size_limit_exceeded", events[0].DetailUnavailable)
	task := &model.Task{TaskID: "discarded", Status: model.TaskStatusSuccess, Data: []byte(`{"image":"discard-canary"}`), PrivateData: model.TaskPrivateData{ResultDiscarded: true}}
	require.NoError(t, model.RecordTaskEvent(2, "result", 1700000000, model.TaskResultEventPayload(task)))
	events, err = model.GetTaskEvents(2)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotContains(t, events[0].Payload, "discard-canary")
	require.NoError(t, events[0].ProjectDetail(true))
	assert.False(t, *events[0].DetailComplete)
	assert.Equal(t, "result_discarded", events[0].DetailUnavailable)
}

func TestBulkTaskFailureRecordsResultEvent(t *testing.T) {
	db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.TaskEvent{})
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	task := &model.Task{TaskID: "public-id", UserId: 7, Status: model.TaskStatusSubmitted}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, model.TaskBulkUpdateByID([]int64{task.ID}, map[string]any{"status": "FAILURE", "progress": "100%", "fail_reason": "final bulk reason"}))
	events, err := model.GetTaskEvents(task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "result", events[0].Kind)
	assert.Contains(t, events[0].Payload, `"status":"FAILURE"`)
	assert.Contains(t, events[0].Payload, "final bulk reason")
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

func TestTaskLogModelAndStatusFilters(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Task{}, &model.User{})
	oldDB := model.DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	model.DB = db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
	})
	for _, user := range []model.User{{Id: 7, Username: "filter-owner", AffCode: "filter7"}, {Id: 8, Username: "filter-other", AffCode: "filter8"}} {
		require.NoError(t, db.Create(&user).Error)
	}
	for _, task := range []model.Task{
		{TaskID: "match", UserId: 7, Status: model.TaskStatusSuccess, SubmitTime: 100, Properties: model.Properties{OriginModelName: "client-model", UpstreamModelName: "private-model"}},
		{TaskID: "pending", UserId: 7, Status: model.TaskStatusQueued, SubmitTime: 100, Properties: model.Properties{OriginModelName: "client-model"}},
		{TaskID: "prefix", UserId: 7, Status: model.TaskStatusSuccess, SubmitTime: 100, Properties: model.Properties{OriginModelName: "client-model-extra"}},
		{TaskID: "mapped", UserId: 7, Status: model.TaskStatusSuccess, SubmitTime: 100, Properties: model.Properties{OriginModelName: "other", UpstreamModelName: "client-model"}},
		{TaskID: "foreign", UserId: 8, Status: model.TaskStatusSuccess, SubmitTime: 100, Properties: model.Properties{OriginModelName: "client-model"}},
		{TaskID: "legacy", UserId: 7, Status: model.TaskStatusSuccess, SubmitTime: 100},
	} {
		require.NoError(t, db.Create(&task).Error)
	}
	for _, tc := range []struct {
		name, query string
		handler     gin.HandlerFunc
		want        int
	}{
		{"self combined", "model_name=client-model&status=SUCCESS", GetUserTask, 1},
		{"admin combined", "model_name=client-model&status=SUCCESS", GetAllTask, 2},
		{"self model", "model_name=client-model", GetUserTask, 2},
		{"admin model", "model_name=client-model", GetAllTask, 3},
		{"self upstream is not searchable", "model_name=private-model", GetUserTask, 0},
		{"admin exact model", "model_name=client", GetAllTask, 0},
		{"self task and model", "model_name=client-model&task_id=prefix", GetUserTask, 0},
		{"self time and model", "model_name=client-model&start_timestamp=101", GetUserTask, 0},
		{"self reset", "", GetUserTask, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/task/?page_size=1&"+tc.query, nil)
			c.Set("id", 7)
			c.Set("role", common.RoleAdminUser)
			tc.handler(c)
			var response struct {
				Success bool
				Data    struct {
					Total int
					Items []dto.TaskDto
				}
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success)
			assert.Equal(t, tc.want, response.Data.Total)
			assert.Len(t, response.Data.Items, min(tc.want, 1))
			if tc.name == "self combined" {
				require.Len(t, response.Data.Items, 1)
				assert.Equal(t, "match", response.Data.Items[0].TaskID)
				assert.NotContains(t, recorder.Body.String(), "private-model")
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
