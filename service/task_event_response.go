package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type TaskResponseCapture struct {
	gin.ResponseWriter
	body                  bytes.Buffer
	overflow, writeFailed bool
}

func (w *TaskResponseCapture) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		w.writeFailed = true
	}
	if !w.overflow {
		if w.body.Len()+n > model.TaskEventPayloadLimit {
			w.overflow = true
			w.body.Reset()
		} else {
			_, _ = w.body.Write(data[:n])
		}
	}
	return n, err
}

func (w *TaskResponseCapture) WriteString(text string) (int, error) {
	return w.Write([]byte(text))
}

func (w *TaskResponseCapture) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *TaskResponseCapture) Detail(discarded bool) map[string]any {
	detail := map[string]any{"status_code": w.Status(), "content_type": w.Header().Get("Content-Type")}
	switch {
	case discarded:
		detail["unavailable"] = "result_discarded"
	case w.overflow:
		detail["unavailable"] = "size_limit_exceeded"
	case w.writeFailed:
		detail["unavailable"] = "write_failed"
	case w.body.Len() == 0:
		detail["unavailable"] = "response_not_written"
	case strings.Contains(w.Header().Get("Content-Type"), "text/event-stream"):
		var events []any
		for block := range strings.SplitSeq(strings.ReplaceAll(w.body.String(), "\r\n", "\n"), "\n\n") {
			var data []string
			var eventName string
			for line := range strings.SplitSeq(block, "\n") {
				if value, ok := strings.CutPrefix(line, "data:"); ok {
					data = append(data, strings.TrimPrefix(value, " "))
				}
				if value, ok := strings.CutPrefix(line, "event:"); ok {
					eventName = strings.TrimSpace(value)
				}
			}
			if len(data) == 0 {
				continue
			}
			text := strings.Join(data, "\n")
			var raw json.RawMessage
			var value any = text
			if common.Unmarshal([]byte(text), &raw) == nil {
				value = raw
			}
			events = append(events, map[string]any{"event": eventName, "data": value})
		}
		detail["events"] = events
	default:
		var raw json.RawMessage
		if common.Unmarshal(w.body.Bytes(), &raw) == nil {
			detail["body"] = raw
		} else {
			detail["unavailable"] = "non_json_response"
		}
	}
	return detail
}

func (w *TaskResponseCapture) RecordFinal(task *model.Task) {
	if task == nil || task.ID <= 0 || !task.ResultRetrievable() || task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure {
		return
	}
	timestamp := task.FinishTime
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}
	if err := model.RecordTaskEvent(task.ID, "result", timestamp, model.TaskResultEventPayload(task)); err != nil {
		common.SysError("record task result event: " + err.Error())
		return
	}
	if err := model.RecordTaskEventDetail(task.ID, "result", timestamp, model.TaskEventClientResponse{Response: w.Detail(false)}); err != nil {
		common.SysError("record task final response detail: " + err.Error())
	}
}
