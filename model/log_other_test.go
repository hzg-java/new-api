package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogOtherScopesAndMerges(t *testing.T) {
	var other LogOther

	assert.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	other.MergePublic(map[string]any{
		"zero": 0,
	})
	assert.True(t, other.SetAdmin("use_channel", []string{"channel-a"}))
	other.MergeAdmin(map[string]any{
		"rejected": false,
	})
	assert.True(t, other.SetRoot("upstream_request_id", "upstream-private"))
	other.MergeRoot(map[string]any{
		"generation": 0,
	})
	assert.True(t, other.SetAudit("method", "POST"))
	other.MergeAudit(map[string]any{
		"success": false,
	})

	require.JSONEq(t, `{
		"request_path": "/v1/chat/completions",
		"zero": 0,
		"admin_info": {
			"use_channel": ["channel-a"],
			"rejected": false
		},
		"root_info": {
			"upstream_request_id": "upstream-private",
			"generation": 0
		},
		"audit_info": {
			"method": "POST",
			"success": false
		}
	}`, other.JSONString())
}

func TestLogOtherRejectsSensitivePublicFields(t *testing.T) {
	other := NewLogOther()

	for _, key := range []string{
		"admin_info",
		"root_info",
		"audit_info",
		"channel_id",
		"channel_name",
		"channel_type",
		"reject_reason",
	} {
		assert.False(t, other.SetPublic(key, "must-not-leak"), key)
	}
	other.MergePublic(map[string]any{
		"request_path": "/v1/responses",
		"channel_name": "still-must-not-leak",
		"admin_info":   map[string]any{"secret": true},
	})

	require.JSONEq(t, `{"request_path":"/v1/responses"}`, other.JSONString())
	require.JSONEq(t, `{}`, NewLogOther().JSONString())
}

func TestLogOtherJSONStringDoesNotMutateReceiver(t *testing.T) {
	other := NewLogOther()
	require.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	require.True(t, other.SetAdmin("rejected", false))

	before := other.Snapshot()
	first := other.JSONString()
	after := other.Snapshot()
	second := other.JSONString()

	require.Equal(t, before, after)
	require.Equal(t, first, second)
}

func TestUserLogBillingPrivacy(t *testing.T) {
	const stored = `{"model_ratio":1.23,"completion_ratio":2,"cache_ratio":0.1,"cache_creation_ratio":1.2,"cache_creation_ratio_5m":1.3,"cache_creation_ratio_1h":2,"group_ratio":0.8,"user_group_ratio":0.7,"audio_ratio":4,"audio_completion_ratio":5,"image_ratio":6,"audio_input_price":7,"model_price":0.004,"fixed_price":0.1,"expr_b64":"secret-expression","request_rules":{"multiplier":3},"custom_multiplier":8,"custom_string_multiplier":"9.2","custom_nested":{"price":10},"request_path":"/v1/videos","is_task":true,"task_id":"public-task","image_count":2,"cache_tokens":9007199254740993,"usage_facts":{"tokens":12,"seconds":5},"billing_tokens":{"p":12,"c":0},"pre_consumed_quota":100,"actual_quota":80,"billing_mode":"tiered_expr","tool_surcharges":[{"name":"search","count":2,"price":10}],"admin_info":{"diagnostic":true}}`
	for _, visibility := range []logOtherVisibility{logOtherVisibilityUser, logOtherVisibilityAdmin, logOtherVisibilityRoot} {
		t.Run([]string{"user", "admin", "root"}[visibility], func(t *testing.T) {
			formatted := formatLogOtherJSON(stored, visibility, LogTypeConsume)
			if visibility != logOtherVisibilityUser {
				assert.Equal(t, stored, formatted)
				return
			}
			assert.JSONEq(t, `{"request_path":"/v1/videos","is_task":true,"task_id":"public-task","image_count":2,"cache_tokens":9007199254740993,"usage_facts":{"tokens":12,"seconds":5},"billing_tokens":{"p":12,"c":0},"pre_consumed_quota":100,"actual_quota":80,"billing_mode":"tiered_expr","tool_surcharges":[{"name":"search","count":2}]}`, formatted)
			assert.Contains(t, formatted, `9007199254740993`)
		})
	}
}

func TestUserTopupLogsRetainBalanceSnapshots(t *testing.T) {
	for _, action := range []string{"user.quota_add", "user.quota_subtract", "user.quota_override", "user.delete"} {
		for _, logType := range []int{LogTypeTopup, LogTypeConsume} {
			stored, err := common.Marshal(map[string]any{
				"op": map[string]any{"action": action, "secret": "private", "params": map[string]any{
					"from": 500000, "to": 1000000, "quota": 500000, "internal_price": 123,
				}},
				"admin_info": map[string]any{"admin_username": "private"},
				"root_info":  map[string]any{"secret": "private"},
			})
			require.NoError(t, err)
			log := &Log{Type: logType, Other: string(stored)}
			formatUserLogs([]*Log{log}, 0)
			if logType != LogTypeTopup || action == "user.delete" {
				assert.JSONEq(t, `{}`, log.Other)
				continue
			}
			assert.JSONEq(t, `{"op":{"action":"`+action+`","params":{"from":500000,"to":1000000,"quota":500000}}}`, log.Other)
		}
	}
}

func TestUserLogsRetainLegacyToolUsageAndChargedQuota(t *testing.T) {
	const stored = `{"web_search":true,"web_search_call_count":2,"web_search_price":5,"file_search":true,"file_search_call_count":1,"file_search_price":3,"image_generation_call":true,"image_generation_call_count":4,"image_generation_call_price":10,"violation_fee":true,"fee_quota":50,"violation_fee_code":"policy","violation_fee_marker":"blocked"}`
	assert.JSONEq(t, `{"web_search":true,"web_search_call_count":2,"file_search":true,"file_search_call_count":1,"image_generation_call":true,"image_generation_call_count":4,"violation_fee":true,"fee_quota":50,"violation_fee_code":"policy","violation_fee_marker":"blocked"}`, formatLogOtherJSON(stored, logOtherVisibilityUser, LogTypeConsume))
}

func TestUserTaskLogContentPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name, content, other, want string
		logType                    int
	}{
		{"task consume", "操作 text_to_video, 计算参数：duration: 5, discount: 0.75", `{"is_task":true}`, "Task consumption", LogTypeConsume},
		{"historical consume", "操作 imagine, 计算参数：size: 2.00", `{}`, "Task consumption", LogTypeConsume},
		{"token adjustment", "token重算：tokens=12, modelRatio=1.23, groupRatio=0.8, otherMultiplier=0.75", `{"task_id":"task"}`, "Task consumption", LogTypeConsume},
		{"refund", "refund modelRatio=1.23", `{"task_id":"task","reason":"modelRatio=1.23"}`, "Task refund", LogTypeRefund},
		{"safe task context", "adaptor调整", `{"task_id":"task"}`, "adaptor调整", LogTypeConsume},
		{"non task", "上游没有返回计费信息，无法扣费（可能是上游超时）", `{}`, "上游没有返回计费信息，无法扣费（可能是上游超时）", LogTypeConsume},
		{"topup", "Purchased 10 credits", `{}`, "Purchased 10 credits", LogTypeTopup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := Log{Type: tc.logType, Content: tc.content, Other: tc.other, Quota: 80, ModelName: "requested-model", CompletionTokens: 12}
			user := original
			formatUserLogs([]*Log{&user}, 0)
			assert.Equal(t, tc.want, user.Content)
			assert.Equal(t, 80, user.Quota)
			assert.Equal(t, "requested-model", user.ModelName)
			assert.Equal(t, 12, user.CompletionTokens)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(user.Other, &other))
			assert.NotContains(t, other, "reason")
			admin := original
			FormatAdminLogs([]*Log{&admin})
			assert.Equal(t, tc.content, admin.Content)
			assert.Equal(t, tc.other, admin.Other)
		})
	}
}
