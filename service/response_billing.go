package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// InjectUsageBilling only publishes the local charge; upstream billing is not authoritative.
func InjectUsageBilling(data []byte, amount *float64) []byte {
	if !gjson.ValidBytes(data) || !gjson.ParseBytes(data).IsObject() {
		return data
	}
	path := "usage.billing"
	if strings.HasPrefix(gjson.GetBytes(data, "type").String(), "response.") && gjson.GetBytes(data, "response").IsObject() {
		path = "response.usage.billing"
	}
	updated, err := sjson.DeleteBytes(data, "usage.billing")
	if err != nil {
		return data
	}
	updated, _ = sjson.DeleteBytes(updated, "response.usage.billing")
	updated, _ = sjson.DeleteBytes(updated, "message.usage.billing")
	errorValue := gjson.GetBytes(data, "error")
	if amount == nil || gjson.GetBytes(data, "type").String() == "error" || errorValue.Exists() && errorValue.Type != gjson.Null {
		return updated
	}
	updated, err = sjson.SetBytes(updated, path, *amount)
	if err != nil {
		return data
	}
	return updated
}

type BillingResponseWriter struct {
	gin.ResponseWriter
	ExpectJSON bool
	mu         sync.Mutex
	body       bytes.Buffer
	partial    []byte
	tail       [][]byte
	identity   map[string]json.RawMessage
	imageType  string
	finished   bool
	written    bool
	size       int
}

func NewBillingResponseWriter(writer gin.ResponseWriter) *BillingResponseWriter {
	return &BillingResponseWriter{ResponseWriter: writer}
}

func (w *BillingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *BillingResponseWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written || w.ResponseWriter.Written()
}

func (w *BillingResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.written {
		return w.size
	}
	return w.ResponseWriter.Size()
}

func (w *BillingResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

func (w *BillingResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written = true
	if w.finished || w.isStream() || !w.isJSON() {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *BillingResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written = true
	w.size += len(data)
	if w.finished || !w.isStream() && !w.isJSON() {
		return w.ResponseWriter.Write(data)
	}
	w.Header().Del("Content-Length")
	if !w.isStream() {
		return w.body.Write(data)
	}
	w.partial = append(w.partial, data...)
	for {
		end := bytes.Index(w.partial, []byte("\n\n"))
		separator := 2
		if crlf := bytes.Index(w.partial, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, separator = crlf, 4
		}
		if end < 0 {
			break
		}
		event := bytes.Clone(w.partial[:end+separator])
		w.partial = w.partial[end+separator:]
		if err := w.writeEvent(event); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (w *BillingResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *BillingResponseWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.body.Reset()
	w.partial = nil
	w.tail = nil
	w.identity = nil
	w.imageType = ""
	w.written = false
	w.size = 0
}

func (w *BillingResponseWriter) Abort() {
	w.Reset()
	w.mu.Lock()
	w.finished = true
	w.mu.Unlock()
}

func (w *BillingResponseWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written = true
	if w.finished || w.isStream() || !w.isJSON() {
		w.ResponseWriter.Flush()
	}
}

func (w *BillingResponseWriter) isStream() bool {
	return strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
}

func (w *BillingResponseWriter) isJSON() bool {
	contentType := w.Header().Get("Content-Type")
	return w.ExpectJSON || strings.Contains(contentType, "json")
}

func billingEventPayload(event []byte) []byte {
	for line := range bytes.SplitSeq(event, []byte("\n")) {
		if payload, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			return bytes.TrimSpace(payload)
		}
	}
	return nil
}

func (w *BillingResponseWriter) writeEvent(event []byte) error {
	payload := billingEventPayload(event)
	if len(payload) == 0 {
		_, err := w.ResponseWriter.Write(event)
		return err
	}
	terminal := bytes.Equal(payload, []byte("[DONE]"))
	if !terminal && gjson.ValidBytes(payload) {
		typeName := gjson.GetBytes(payload, "type").String()
		if typeName == "ping" {
			_, err := w.ResponseWriter.Write(patchBillingEvent(event, nil))
			return err
		}
		if typeName == "image_generation.completed" || typeName == "image_edit.completed" {
			w.imageType = strings.TrimSuffix(typeName, ".completed")
			_, err := w.ResponseWriter.Write(patchBillingEvent(event, nil))
			return err
		}
		usageOnly := gjson.GetBytes(payload, "usage").IsObject()
		for _, choice := range gjson.GetBytes(payload, "choices").Array() {
			if choice.Get("text").String() != "" || choice.Get("delta.content").String() != "" ||
				choice.Get("delta.reasoning_content").String() != "" || len(choice.Get("delta.tool_calls").Array()) > 0 || choice.Get("delta.function_call").Exists() {
				usageOnly = false
			}
		}
		terminal = usageOnly || typeName == "message_stop" ||
			typeName == "response.completed" || typeName == "response.done" || typeName == "response.incomplete" || typeName == "response.failed" ||
			typeName == "response.cancelled" || typeName == "response.canceled"
		var object map[string]json.RawMessage
		if common.Unmarshal(payload, &object) == nil {
			if w.identity == nil {
				w.identity = make(map[string]json.RawMessage)
			}
			for _, key := range []string{"id", "object", "created", "model", "system_fingerprint"} {
				if value, exists := object[key]; exists {
					w.identity[key] = value
				}
			}
		}
	}
	if terminal {
		if !bytes.Equal(payload, []byte("[DONE]")) && gjson.GetBytes(payload, "type").String() != "message_stop" {
			for _, pending := range w.tail {
				if _, err := w.ResponseWriter.Write(patchBillingEvent(pending, nil)); err != nil {
					return err
				}
			}
			w.tail = nil
		}
		w.tail = append(w.tail, event)
		return nil
	}
	for _, pending := range w.tail {
		if _, err := w.ResponseWriter.Write(patchBillingEvent(pending, nil)); err != nil {
			return err
		}
	}
	w.tail = nil
	_, err := w.ResponseWriter.Write(patchBillingEvent(event, nil))
	return err
}

func patchBillingEvent(event []byte, amount *float64) []byte {
	payload := billingEventPayload(event)
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return event
	}
	return bytes.Replace(event, payload, InjectUsageBilling(payload, amount), 1)
}

func (w *BillingResponseWriter) Finish(amount *float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return nil
	}
	w.finished = true
	w.Header().Del("Content-Length")
	if w.Status() < 200 || w.Status() >= 300 {
		amount = nil
	}
	if !w.isStream() {
		data := w.body.Bytes()
		if w.isJSON() {
			data = InjectUsageBilling(data, amount)
		}
		if len(data) > 0 {
			_, err := w.ResponseWriter.Write(data)
			return err
		}
		return nil
	}
	lastUsage := -1
	for i, event := range w.tail {
		payload := billingEventPayload(event)
		if !bytes.Equal(payload, []byte("[DONE]")) && gjson.ValidBytes(payload) && gjson.GetBytes(payload, "type").String() != "message_stop" {
			lastUsage = i
		}
	}
	if lastUsage < 0 && amount != nil && w.imageType != "" {
		data, err := common.Marshal(map[string]any{"type": w.imageType + ".usage", "usage": map[string]any{"billing": *amount}})
		if err != nil {
			return err
		}
		if _, err := w.ResponseWriter.Write(append(append([]byte("event: "+w.imageType+".usage\ndata: "), data...), '\n', '\n')); err != nil {
			return err
		}
	}
	for i, event := range w.tail {
		if i == lastUsage {
			event = patchBillingEvent(event, amount)
		} else {
			if lastUsage < 0 && amount != nil && w.imageType == "" && bytes.Equal(billingEventPayload(event), []byte("[DONE]")) {
				response := make(map[string]any, len(w.identity)+2)
				for key, value := range w.identity {
					response[key] = value
				}
				response["choices"] = []any{}
				response["usage"] = map[string]any{"billing": *amount}
				data, err := common.Marshal(response)
				if err != nil {
					return err
				}
				if _, err := w.ResponseWriter.Write(append(append([]byte("data: "), data...), '\n', '\n')); err != nil {
					return err
				}
			}
			event = patchBillingEvent(event, nil)
		}
		if _, err := w.ResponseWriter.Write(event); err != nil {
			return err
		}
	}
	w.tail = nil
	if len(w.partial) > 0 {
		if _, err := w.ResponseWriter.Write(w.partial); err != nil {
			return err
		}
		w.partial = nil
	}
	w.ResponseWriter.Flush()
	return nil
}

var _ http.Flusher = (*BillingResponseWriter)(nil)
