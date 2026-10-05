package controller

import (
	"bytes"
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

type chatCompletionResponseWriter struct {
	gin.ResponseWriter
}

func (w *chatCompletionResponseWriter) WriteHeader(code int) {
	// 普通响应会在写正文前提交上游长度，必须在提交响应头时提前移除。
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

func (w *chatCompletionResponseWriter) WriteHeaderNow() {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeaderNow()
}

func (w *chatCompletionResponseWriter) Write(data []byte) (int, error) {
	responseData := stripChatCompletionResponseModel(data)
	// 响应体缩短后原长度不再有效，必须交给服务器重新计算传输长度。
	w.Header().Del("Content-Length")
	if _, err := w.ResponseWriter.Write(responseData); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *chatCompletionResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func stripChatCompletionResponseModel(data []byte) []byte {
	if stripped := stripTopLevelModel(data); !bytes.Equal(stripped, data) {
		return stripped
	}

	lines := bytes.Split(data, []byte("\n"))
	changed := false
	for index, line := range lines {
		lineBody := bytes.TrimSuffix(line, []byte("\r"))
		if !bytes.HasPrefix(lineBody, []byte("data:")) {
			continue
		}

		rawPayload := lineBody[len("data:"):]
		payload := bytes.TrimSpace(rawPayload)
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		stripped := stripTopLevelModel(payload)
		if bytes.Equal(stripped, payload) {
			continue
		}

		// 保留 data: 后原有空白及 CRLF 风格，只替换 JSON 载荷本身。
		leadingLength := len(rawPayload) - len(bytes.TrimLeft(rawPayload, " \t"))
		trailingLength := len(rawPayload) - len(bytes.TrimRight(rawPayload, " \t"))
		updated := make([]byte, 0, len(line)-len(payload)+len(stripped))
		updated = append(updated, lineBody[:len("data:")+leadingLength]...)
		updated = append(updated, stripped...)
		if trailingLength > 0 {
			updated = append(updated, rawPayload[len(rawPayload)-trailingLength:]...)
		}
		if len(line) != len(lineBody) {
			updated = append(updated, '\r')
		}
		lines[index] = updated
		changed = true
	}
	if !changed {
		return data
	}
	return bytes.Join(lines, []byte("\n"))
}

func stripTopLevelModel(data []byte) []byte {
	var response map[string]json.RawMessage
	if err := common.Unmarshal(data, &response); err != nil {
		return data
	}
	if _, exists := response["model"]; !exists {
		return data
	}
	delete(response, "model")
	stripped, err := common.Marshal(response)
	if err != nil {
		return data
	}
	return stripped
}
