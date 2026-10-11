package model

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
)

const (
	logOtherAdminInfoKey = "admin_info"
	logOtherRootInfoKey  = "root_info"
	logOtherAuditInfoKey = "audit_info"
)

// legacySensitiveLogOtherKeys are historical top-level fields that must never
// be written via SetPublic and must be stripped from user-visible projections.
var legacySensitiveLogOtherKeys = []string{
	"channel_id",
	"channel_name",
	"channel_type",
	"reject_reason",
}

type logOtherVisibility int

const (
	logOtherVisibilityUser logOtherVisibility = iota
	logOtherVisibilityAdmin
	logOtherVisibilityRoot
)

// LogOther separates usage-log metadata by the audience allowed to see it.
// Its maps stay private so callers cannot accidentally place privileged fields
// in the user-visible top level.
type LogOther struct {
	public    map[string]any
	adminInfo map[string]any
	rootInfo  map[string]any
	auditInfo map[string]any
}

func NewLogOther() *LogOther {
	return &LogOther{}
}

func isReservedLogOtherKey(key string) bool {
	switch key {
	case logOtherAdminInfoKey, logOtherRootInfoKey, logOtherAuditInfoKey:
		return true
	default:
		return slices.Contains(legacySensitiveLogOtherKeys, key)
	}
}

// SetPublic records metadata that log owners may receive from self/token log APIs.
// It rejects role-scoped and legacy-sensitive keys so new writers cannot recreate
// the historical channel/reject-reason leak.
func (o *LogOther) SetPublic(key string, value any) bool {
	if o == nil || key == "" || isReservedLogOtherKey(key) {
		return false
	}
	if o.public == nil {
		o.public = make(map[string]any)
	}
	o.public[key] = value
	return true
}

func (o *LogOther) MergePublic(values map[string]any) {
	for key, value := range values {
		o.SetPublic(key, value)
	}
}

func (o *LogOther) SetAdmin(key string, value any) bool {
	if o == nil || key == "" {
		return false
	}
	if o.adminInfo == nil {
		o.adminInfo = make(map[string]any)
	}
	o.adminInfo[key] = value
	return true
}

func (o *LogOther) MergeAdmin(values map[string]any) {
	for key, value := range values {
		o.SetAdmin(key, value)
	}
}

func (o *LogOther) SetRoot(key string, value any) bool {
	if o == nil || key == "" {
		return false
	}
	if o.rootInfo == nil {
		o.rootInfo = make(map[string]any)
	}
	o.rootInfo[key] = value
	return true
}

func (o *LogOther) MergeRoot(values map[string]any) {
	for key, value := range values {
		o.SetRoot(key, value)
	}
}

func (o *LogOther) SetAudit(key string, value any) bool {
	if o == nil || key == "" {
		return false
	}
	if o.auditInfo == nil {
		o.auditInfo = make(map[string]any)
	}
	o.auditInfo[key] = value
	return true
}

func (o *LogOther) MergeAudit(values map[string]any) {
	for key, value := range values {
		o.SetAudit(key, value)
	}
}

func copyLogOtherMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	copyValues := make(map[string]any, len(values))
	maps.Copy(copyValues, values)
	return copyValues
}

func (o *LogOther) toMap() map[string]any {
	result := make(map[string]any)
	if o == nil {
		return result
	}

	maps.Copy(result, o.public)
	if adminInfo := copyLogOtherMap(o.adminInfo); len(adminInfo) > 0 {
		result[logOtherAdminInfoKey] = adminInfo
	}
	if auditInfo := copyLogOtherMap(o.auditInfo); len(auditInfo) > 0 {
		result[logOtherAuditInfoKey] = auditInfo
	}
	if rootInfo := copyLogOtherMap(o.rootInfo); len(rootInfo) > 0 {
		result[logOtherRootInfoKey] = rootInfo
	}
	return result
}

func (o *LogOther) jsonString() string {
	if o == nil {
		return ""
	}
	data, err := common.Marshal(o.toMap())
	if err != nil {
		common.SysError("failed to marshal log other: " + err.Error())
		return ""
	}
	return string(data)
}

// JSONString returns the complete stored representation, including all
// privileged scopes. API projections must use the role-specific formatter.
func (o *LogOther) JSONString() string {
	return o.jsonString()
}

// Snapshot returns a detached top-level view for tests and read-only
// inspection. Mutating it cannot add or replace fields in LogOther.
func (o *LogOther) Snapshot() map[string]any {
	if o == nil {
		return nil
	}
	return o.toMap()
}

func (o *LogOther) MarshalJSON() ([]byte, error) {
	return common.Marshal(o.toMap())
}

func normalizeLegacyRejectReason(values map[string]json.RawMessage) bool {
	rejectReason, ok := values["reject_reason"]
	if !ok {
		return false
	}

	adminInfo := make(map[string]json.RawMessage)
	if rawAdminInfo, exists := values[logOtherAdminInfoKey]; exists {
		_ = common.Unmarshal(rawAdminInfo, &adminInfo)
	}
	if adminInfo == nil {
		adminInfo = make(map[string]json.RawMessage)
	}
	if _, exists := adminInfo["reject_reason"]; !exists {
		adminInfo["reject_reason"] = rejectReason
	}
	encodedAdminInfo, err := common.Marshal(adminInfo)
	if err != nil {
		return false
	}
	values[logOtherAdminInfoKey] = encodedAdminInfo
	delete(values, "reject_reason")
	return true
}

// userLogOtherKey is deliberately an allowlist: task OtherRatios historically
// wrote arbitrary keys (including strings and nested diagnostics) at the top level.
// New metadata must be reviewed before exposing it to log owners.
func userLogOtherKey(key string) bool {
	switch key {
	case "request_path", "frt", "reasoning_effort", "is_system_prompt_overwritten",
		"stream_status", "request_conversion", "error_type", "error_code", "status_code",
		"billing_source", "billing_preference", "billing_mode", "billing_unit", "matched_tier",
		"subscription_id", "subscription_plan_id", "subscription_plan_title",
		"subscription_pre_consumed", "subscription_post_delta", "subscription_total",
		"subscription_used", "subscription_remain", "subscription_consumed", "wallet_quota_deducted",
		"is_task", "task_sync", "task_id", "task_status", "task_billing_pending", "result_discarded",
		"pre_consumed_quota", "actual_quota", "image_count", "usage_facts", "billing_tokens",
		"claude", "ws", "audio", "image", "usage_semantic", "audio_input_seperate_price",
		"audio_input", "audio_output", "text_input", "text_output", "image_output",
		"audio_input_token_count", "input_tokens_total", "cache_tokens", "image_cache_tokens",
		"cache_creation_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h", "cache_write_tokens",
		"web_search", "web_search_call_count", "file_search", "file_search_call_count",
		"image_generation_call", "image_generation_call_count", "tool_surcharges",
		"violation_fee", "violation_fee_code", "violation_fee_marker", "fee_quota":
		return true
	default:
		return false
	}
}

// formatLogOtherJSON applies the role projection while keeping untouched JSON
// values as RawMessage. This preserves integers larger than JavaScript's safe
// range instead of round-tripping them through float64.
func formatLogOtherJSON(value string, visibility logOtherVisibility) string {
	if value == "" {
		return ""
	}

	var values map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(value, &values); err != nil {
		if visibility == logOtherVisibilityRoot {
			return value
		}
		return "{}"
	}

	changed := false
	if visibility == logOtherVisibilityUser {
		for key := range values {
			if !userLogOtherKey(key) {
				delete(values, key)
				changed = true
			}
		}
		if raw, exists := values["tool_surcharges"]; exists {
			// Retain tool usage, never the internal unit price or arbitrary fields.
			var items []map[string]json.RawMessage
			delete(values, "tool_surcharges")
			if common.Unmarshal(raw, &items) == nil {
				for _, item := range items {
					for key := range item {
						if key != "name" && key != "count" {
							delete(item, key)
						}
					}
				}
				if encoded, err := common.Marshal(items); err == nil {
					values["tool_surcharges"] = encoded
				}
			}
			changed = true
		}
	} else {
		changed = normalizeLegacyRejectReason(values)
		if visibility == logOtherVisibilityAdmin {
			if _, exists := values[logOtherRootInfoKey]; exists {
				delete(values, logOtherRootInfoKey)
				changed = true
			}
		}
	}

	if !changed {
		return value
	}
	formatted, err := common.Marshal(values)
	if err != nil {
		if visibility == logOtherVisibilityRoot {
			return value
		}
		return "{}"
	}
	return string(formatted)
}
