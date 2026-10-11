package model

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm/clause"
)

type TaskEvent struct {
	ID                int64  `json:"-" gorm:"primaryKey"`
	TaskID            int64  `json:"-" gorm:"uniqueIndex:idx_task_event_kind"`
	Kind              string `json:"kind" gorm:"type:varchar(20);uniqueIndex:idx_task_event_kind"`
	Timestamp         int64  `json:"timestamp"`
	Payload           string `json:"payload" gorm:"type:text"`
	DetailComplete    *bool  `json:"detail_complete,omitempty" gorm:"-"`
	DetailUnavailable string `json:"detail_unavailable,omitempty" gorm:"-"`
}

const TaskEventPayloadLimit = 60 * 1024

var (
	taskEventURLPattern           = regexp.MustCompile(`https?://[^\s<>"']+`)
	taskEventAuthorizationPattern = regexp.MustCompile(`(?i)\b(Bearer|Basic)[ \t]+[A-Za-z0-9._~+/=-]+`)
	taskEventAPIKeyPattern        = regexp.MustCompile(`(?i)\b((?:x[-_])?api[-_ ]?key["']?\s*[:=]\s*["']?)[^\s"',;&<>]+`)
	taskEventKeyTokenPattern      = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+`)
)

func taskEventCredentialKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
	switch key {
	case "key", "apikey", "authorization", "proxyauthorization", "cookie", "setcookie", "password", "passwd", "secret", "clientsecret", "accesskey", "secretkey", "privatekey", "credential", "credentials", "auth", "token", "accesstoken", "refreshtoken", "idtoken", "session", "sessionid", "signature", "sig", "xamzsignature", "xamzcredential", "xamzsecuritytoken", "xgoogsignature", "xgoogcredential":
		return true
	}
	return strings.HasSuffix(key, "token") || strings.HasSuffix(key, "secret") || strings.HasSuffix(key, "password") || strings.HasSuffix(key, "apikey")
}

// RedactTaskEventDetail walks encoded JSON, preserving numeric lexemes and RawMessage values.
func RedactTaskEventDetail(value any) (json.RawMessage, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return nil, err
	}
	return redactTaskEventJSON(encoded)
}

func redactTaskEventJSON(encoded json.RawMessage) (json.RawMessage, error) {
	switch common.GetJsonType(encoded) {
	case "object":
		var object map[string]json.RawMessage
		if err := common.Unmarshal(encoded, &object); err != nil {
			return nil, err
		}
		// Header/form envelopes use the name to describe the credential stored in value.
		var name string
		_ = common.Unmarshal(object["name"], &name)
		credentialEnvelope := taskEventCredentialKey(strings.TrimSpace(name))
		for key, value := range object {
			if taskEventCredentialKey(key) || (credentialEnvelope && (key == "value" || key == "values")) {
				object[key] = json.RawMessage(`"[REDACTED]"`)
				continue
			}
			redacted, err := redactTaskEventJSON(value)
			if err != nil {
				return nil, err
			}
			object[key] = redacted
		}
		return common.Marshal(object)
	case "array":
		var items []json.RawMessage
		if err := common.Unmarshal(encoded, &items); err != nil {
			return nil, err
		}
		for i := range items {
			redacted, err := redactTaskEventJSON(items[i])
			if err != nil {
				return nil, err
			}
			items[i] = redacted
		}
		return common.Marshal(items)
	case "string":
		var text string
		if err := common.Unmarshal(encoded, &text); err != nil {
			return nil, err
		}
		text = taskEventURLPattern.ReplaceAllStringFunc(text, func(raw string) string {
			u, err := url.Parse(raw)
			if err != nil {
				return "[REDACTED URL]"
			}
			u.User = nil
			query, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				u.RawQuery = "redacted"
			} else {
				for key := range query {
					if taskEventCredentialKey(key) {
						query[key] = []string{"[REDACTED]"}
					}
				}
				u.RawQuery = query.Encode()
			}
			if u.Fragment != "" {
				u.Fragment = "[REDACTED]"
			}
			return u.String()
		})
		text = taskEventAuthorizationPattern.ReplaceAllString(text, "$1 [REDACTED]")
		text = taskEventAPIKeyPattern.ReplaceAllString(text, "${1}[REDACTED]")
		text = taskEventKeyTokenPattern.ReplaceAllString(text, "[REDACTED]")
		return common.Marshal(text)
	default:
		return encoded, nil
	}
}

// RecordTaskEventDetail changes only admin_info; summary and original timestamp remain intact.
// The compare-and-swap prevents concurrent summary/detail updates from losing each other.
func RecordTaskEventDetail(taskID int64, kind string, timestamp int64, detail any) error {
	if err := RecordTaskEvent(taskID, kind, timestamp, nil); err != nil {
		return err
	}
	redacted, err := RedactTaskEventDetail(detail)
	info := map[string]any{"payload": redacted, "complete": true}
	if marker, ok := detail.(map[string]any); ok {
		if reason, ok := marker["unavailable"].(string); ok {
			info["complete"] = false
			info["unavailable"] = reason
		}
	}
	if err != nil {
		info = map[string]any{"complete": false, "unavailable": "invalid_payload"}
	}
	for range 8 {
		var event TaskEvent
		if err := DB.Where("task_id = ? AND kind = ?", taskID, kind).First(&event).Error; err != nil {
			return err
		}
		var payload map[string]json.RawMessage
		if err := common.UnmarshalJsonStr(event.Payload, &payload); err != nil {
			return err
		}
		admin, err := common.Marshal(info)
		if err != nil {
			return err
		}
		payload["admin_info"] = admin
		encoded, err := common.Marshal(payload)
		if err != nil {
			return err
		}
		if len(encoded) > TaskEventPayloadLimit {
			payload["admin_info"], _ = common.Marshal(map[string]any{"complete": false, "unavailable": "size_limit_exceeded", "size_bytes": len(encoded)})
			encoded, err = common.Marshal(payload)
			if err != nil {
				return err
			}
		}
		result := DB.Model(&TaskEvent{}).Where("id = ? AND payload = ?", event.ID, event.Payload).Update("payload", string(encoded))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 || event.Payload == string(encoded) {
			return nil
		}
	}
	return errors.New("concurrent task event detail update")
}

func (event *TaskEvent) ProjectDetail(admin bool) error {
	var payload map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(event.Payload, &payload); err != nil {
		return err
	}
	info := payload["admin_info"]
	delete(payload, "admin_info")
	var detail struct {
		Payload     json.RawMessage `json:"payload"`
		Complete    bool            `json:"complete"`
		Unavailable string          `json:"unavailable"`
	}
	if len(info) > 0 {
		if err := common.Unmarshal(info, &detail); err != nil {
			return err
		}
	} else {
		detail.Unavailable = "not_recorded"
	}
	if len(detail.Payload) == 0 && detail.Complete {
		detail.Complete = false
		detail.Unavailable = "not_recorded"
	}
	event.DetailComplete = &detail.Complete
	event.DetailUnavailable = detail.Unavailable
	encoded := detail.Payload
	if len(encoded) == 0 {
		var err error
		encoded, err = common.Marshal(payload)
		if err != nil {
			return err
		}
	}
	if !admin {
		var err error
		encoded, err = projectTaskEventBusinessJSON(encoded, event.Kind == "request", true, "")
		if err != nil {
			return err
		}
	}
	// Redact again at the read boundary, including historical snapshots.
	redacted, err := redactTaskEventJSON(encoded)
	event.Payload = string(redacted)
	return err
}

// Project only reviewed business fields, never arbitrary provider metadata.
// Model is safe only in the original client request (including form fields),
// not in provider responses or nested request metadata. RawMessage preserves
// explicit zero/false values and numeric lexemes without a float64 round trip.
func projectTaskEventBusinessJSON(encoded json.RawMessage, request, root bool, container string) (json.RawMessage, error) {
	switch common.GetJsonType(encoded) {
	case "array":
		var items []json.RawMessage
		if err := common.Unmarshal(encoded, &items); err != nil {
			return nil, err
		}
		for i := range items {
			value, err := projectTaskEventBusinessJSON(items[i], request, false, container)
			if err != nil {
				return nil, err
			}
			items[i] = value
		}
		return common.Marshal(items)
	case "object":
		var object map[string]json.RawMessage
		if err := common.Unmarshal(encoded, &object); err != nil {
			return nil, err
		}
		filtered := make(map[string]json.RawMessage)
		for key, value := range object {
			allowed := false
			switch key {
			case "model":
				allowed = request && (root || container == "fields")
			case "kind", "fields", "files":
				allowed = request && root
			case "field", "filename", "mime_type":
				allowed = request && container == "files"
			case "status_code", "content_type", "body", "events", "unavailable":
				allowed = root
			case "event":
				allowed = container == "events"
			case "prompt", "negative_prompt", "seed", "n", "seconds", "duration", "duration_seconds", "durationSeconds", "resolution", "size", "width", "height", "quality", "aspect_ratio", "fps", "generate_audio", "audio", "watermark", "camera_fixed", "return_last_frame", "image", "images", "image_url", "image_urls", "video_url", "video_urls", "reference_video", "reference_videos", "reference_video_url", "reference_video_urls", "first_frame", "last_frame", "last_frame_url", "audio_url", "url", "b64_json", "revised_prompt", "text", "type", "role":
				allowed = container != "usage"
			case "metadata", "parameters", "input":
				allowed = request
			case "id", "task_id", "status", "task_status", "progress", "created", "created_at", "updated_at", "completed_at", "finished_at", "expires_at", "error", "code", "message", "fail_reason", "result_url", "object", "output", "content", "data", "result", "results", "videos", "choices", "usage", "video_info", "has_reference_video":
				allowed = container != "usage"
			case "total_tokens", "completion_tokens", "prompt_tokens", "input_tokens", "output_tokens", "image_count", "output_image_count", "video_count":
				allowed = container == "usage"
			}
			if !allowed {
				continue
			}
			projected, err := projectTaskEventBusinessJSON(value, request, false, key)
			if err != nil {
				return nil, err
			}
			filtered[key] = projected
		}
		return common.Marshal(filtered)
	default:
		return encoded, nil
	}
}

func GetTaskByID(taskID int64, userID int) (*Task, bool, error) {
	var task Task
	query := DB.Where("id = ?", taskID)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	} else if userID != 0 {
		return nil, false, nil
	}
	err := query.First(&task).Error
	exists, err := RecordExist(err)
	if err != nil || !exists {
		return nil, exists, err
	}
	return &task, true, nil
}

func RecordTaskEvent(taskID int64, kind string, timestamp int64, payload map[string]any) error {
	if taskID <= 0 || timestamp <= 0 {
		return errors.New("invalid task event identity or time")
	}
	if _, exists := payload["video_info"]; exists {
		return errors.New("video request snapshots must be persisted with task creation")
	}
	filtered := make(map[string]any)
	for _, key := range []string{"model", "id", "task_id", "status"} {
		if value, ok := payload[key]; ok {
			var text string
			switch v := value.(type) {
			case string:
				text = v
			case TaskStatus:
				text = string(v)
			default:
				return errors.New("invalid task event field")
			}
			if len(text) > 191 {
				return errors.New("invalid task event field")
			}
			filtered[key] = text
		}
	}
	switch kind {
	case "request":
		delete(filtered, "id")
		delete(filtered, "task_id")
		delete(filtered, "status")
	case "accepted":
		delete(filtered, "task_id")
	case "result":
		delete(filtered, "id")
		delete(filtered, "model")
		if usage, ok := payload["usage"].(map[string]int64); ok {
			limited := make(map[string]int64)
			for _, key := range []string{"total_tokens", "completion_tokens", "prompt_tokens"} {
				if count, present := usage[key]; present && count >= 0 && count <= math.MaxInt32 {
					limited[key] = count
				}
			}
			if len(limited) > 0 {
				filtered["usage"] = limited
			}
		}
	default:
		return errors.New("invalid task event kind")
	}
	encoded, err := common.Marshal(filtered)
	if err != nil {
		return err
	}
	if len(encoded) > 4096 {
		return errors.New("task event exceeds storage limit")
	}
	created := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&TaskEvent{
		TaskID: taskID, Kind: kind, Timestamp: timestamp, Payload: string(encoded),
	})
	if created.Error != nil {
		return created.Error
	}
	if detail, exists := payload["admin_info"]; exists && kind == "result" && created.RowsAffected > 0 {
		return RecordTaskEventDetail(taskID, kind, timestamp, detail)
	}
	return nil
}

func GetTaskEvents(taskID int64) ([]TaskEvent, error) {
	var events []TaskEvent
	err := DB.Where("task_id = ?", taskID).Order("timestamp asc, id asc").Find(&events).Error
	return events, err
}

func TaskResultEventPayload(task *Task) map[string]any {
	result := map[string]any{"task_id": task.TaskID, "status": task.Status}
	detail := map[string]any{"task_id": task.TaskID, "status": task.Status, "fail_reason": task.FailReason}
	if task.PrivateData.ResultDiscarded {
		detail["unavailable"] = "result_discarded"
	} else {
		if len(task.Data) > 0 {
			detail["data"] = json.RawMessage(task.Data)
		}
		if task.PrivateData.ResultURL != "" {
			detail["result_url"] = task.PrivateData.ResultURL
		}
		if task.EventDetailUnavailable != "" {
			detail["unavailable"] = task.EventDetailUnavailable
		}
	}
	result["admin_info"] = detail
	var response struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if common.Unmarshal(task.Data, &response) != nil {
		return result
	}
	usage := make(map[string]int64)
	for _, key := range []string{"total_tokens", "completion_tokens", "prompt_tokens"} {
		var value float64
		if common.Unmarshal(response.Usage[key], &value) == nil && value >= 0 && value <= math.MaxInt32 && value == math.Trunc(value) {
			usage[key] = int64(value)
		}
	}
	if len(usage) > 0 {
		result["usage"] = usage
	}
	return result
}
