package model

import (
	"encoding/json"
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
)

type TaskEvent struct {
	ID        int64  `json:"-" gorm:"primaryKey"`
	TaskID    int64  `json:"-" gorm:"uniqueIndex:idx_task_event_kind"`
	Kind      string `json:"kind" gorm:"type:varchar(20);uniqueIndex:idx_task_event_kind"`
	Timestamp int64  `json:"timestamp"`
	Payload   string `json:"payload" gorm:"type:text"`
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
	return DB.Where(TaskEvent{TaskID: taskID, Kind: kind}).FirstOrCreate(&TaskEvent{
		TaskID: taskID, Kind: kind, Timestamp: timestamp, Payload: string(encoded),
	}).Error
}

func GetTaskEvents(taskID int64) ([]TaskEvent, error) {
	var events []TaskEvent
	err := DB.Where("task_id = ?", taskID).Order("timestamp asc, id asc").Find(&events).Error
	return events, err
}

func TaskResultEventPayload(task *Task) map[string]any {
	result := map[string]any{"task_id": task.TaskID, "status": task.Status}
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
