package service

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

type videoUpstreamError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
}

var videoContentIndex = regexp.MustCompile(`\bcontent\[([0-9]{1,6})\]`)

func parseVideoUpstreamError(body json.RawMessage) videoUpstreamError {
	return parseVideoErrorEnvelope(body, 0)
}

func parseVideoErrorEnvelope(body json.RawMessage, depth int) videoUpstreamError {
	if depth > 6 || len(body) > model.TaskEventPayloadLimit {
		return videoUpstreamError{}
	}
	var object map[string]json.RawMessage
	if common.Unmarshal(body, &object) != nil {
		return videoUpstreamError{}
	}
	for _, key := range []string{"error", "Error", "body", "ResponseMetadata", "output", "data"} {
		if nested := object[key]; len(nested) > 0 {
			if parsed := parseVideoErrorEnvelope(nested, depth+1); parsed.Code != "" || parsed.Message != "" {
				return parsed
			}
		}
	}
	var result videoUpstreamError
	_ = common.Unmarshal(body, &result)
	return result
}

func videoErrorSubject(failure videoUpstreamError, request json.RawMessage) string {
	code := strings.ToLower(failure.Code)
	reason := strings.ToLower(topenRouterMaterialReasonURL.ReplaceAllString(failure.Message, ""))
	media := ""
	switch {
	case strings.Contains(code, "inputtext"), strings.Contains(code, "inputprompt"):
		return "提示词"
	case strings.Contains(code, "inputimage"):
		media = "图片"
	case strings.Contains(code, "inputaudio"):
		media = "音频"
	case strings.Contains(code, "inputvideo"):
		media = "视频"
	}
	if media == "" {
		switch {
		case failure.Param == "prompt", failure.Param == "negative_prompt":
			return "提示词"
		case strings.Contains(reason, "input image"), strings.Contains(reason, "image format"), strings.Contains(reason, "download image"), strings.Contains(failure.Param, "image"):
			media = "图片"
		case strings.Contains(reason, "input audio"), strings.Contains(reason, "audio format"), strings.Contains(reason, "download audio"), strings.Contains(failure.Param, "audio"):
			media = "音频"
		case strings.Contains(reason, "input video"), strings.Contains(reason, "video format"), strings.Contains(reason, "download video"), strings.Contains(failure.Param, "video_url"):
			media = "视频"
		}
	}
	field := failure.Param
	if field == "" {
		field = topenRouterMaterialReasonURL.ReplaceAllString(failure.Message, "")
	}
	positions := videoContentIndex.FindAllStringSubmatch(field, -1)
	var body struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if len(positions) > 0 && common.Unmarshal(request, &body) == nil {
		var labels []string
		for _, position := range positions {
			index, _ := strconv.Atoi(position[1])
			if index >= len(body.Content) {
				continue
			}
			kind := videoContentMediaType(body.Content[index].Type)
			if kind == "" || media != "" && media != kind {
				continue
			}
			number := 0
			for _, item := range body.Content[:index+1] {
				if videoContentMediaType(item.Type) == kind {
					number++
				}
			}
			label := "第 " + strconv.Itoa(number) + " 个" + kind + "素材"
			if !slices.Contains(labels, label) {
				labels = append(labels, label)
			}
		}
		if len(labels) > 0 {
			return strings.Join(labels, "、")
		}
	}
	if media != "" {
		return media + "素材"
	}
	return "输入内容"
}

func videoContentMediaType(typ string) string {
	switch typ {
	case "image_url", "reference_image", "first_frame", "last_frame":
		return "图片"
	case "audio_url", "reference_audio":
		return "音频"
	case "video_url", "reference_video":
		return "视频"
	default:
		return ""
	}
}

func VideoSubmissionErrorMessage(taskErr *dto.TaskError, request json.RawMessage) string {
	fallback := VideoSubmissionFailureMessage(taskErr.Code)
	if taskErr.Code == "video_generation_failed" {
		fallback = "视频生成失败，暂未获取到具体原因，请提供任务 ID 联系管理员核查。"
	}
	if taskErr.Code != "fail_to_fetch_task" && taskErr.Code != "video_generation_failed" {
		return fallback
	}
	body := taskErr.Message
	if taskErr.Error != nil {
		body = taskErr.Error.Error()
	}
	failure := parseVideoUpstreamError(json.RawMessage(body))
	if failure.Code == "" && len(taskErr.Diagnostic) > 0 {
		failure = parseVideoUpstreamError(taskErr.Diagnostic)
	}
	code := strings.ToLower(failure.Code)
	reason := strings.ToLower(topenRouterMaterialReasonURL.ReplaceAllString(failure.Message, ""))
	subject := videoErrorSubject(failure, request)
	if strings.Contains(code, "insufficientbalance") || strings.Contains(code, "insufficientquota") || strings.Contains(code, "quotaexceeded") || strings.Contains(code, "arrears") {
		return "视频服务额度不足，暂时无法生成，请联系管理员处理。"
	}
	if taskErr.StatusCode == http.StatusUnauthorized || taskErr.StatusCode == http.StatusForbidden || strings.Contains(code, "unauthorized") || strings.Contains(code, "accessdenied") || strings.Contains(code, "authentication") || strings.Contains(code, "forbidden") {
		return "视频服务权限异常，暂时无法生成，请联系管理员处理。"
	}
	if taskErr.StatusCode == http.StatusRequestTimeout || strings.Contains(code, "timeout") || strings.Contains(reason, "timeout") || strings.Contains(reason, "timed out") {
		return "视频处理超时，结果尚未确认，请勿重复提交，请提供任务 ID 联系管理员核查。"
	}
	if taskErr.StatusCode == http.StatusTooManyRequests || strings.Contains(code, "throttl") || strings.Contains(code, "ratelimit") || strings.Contains(code, "toomanyrequests") {
		return "当前视频请求过多，暂时无法处理，请稍后查询已有任务或联系管理员核查。"
	}
	if taskErr.StatusCode >= 500 {
		return "视频服务暂时不可用，提交结果尚未确认，请勿重复提交，请联系管理员核查。"
	}
	if strings.Contains(code, "internal") || strings.Contains(code, "serviceunavailable") {
		return "视频生成服务发生内部错误，本次生成失败，请联系管理员处理。"
	}
	safety := strings.Contains(code, "sensitivecontentdetected") || strings.Contains(code, "contentpolicyviolation") || strings.Contains(code, "contentsensitive") || strings.Contains(code, "contentfilter")
	copyright := strings.Contains(code, "policyviolation") && strings.Contains(reason, "copyright")
	if (safety || copyright) && strings.HasPrefix(code, "output") {
		if copyright {
			return "生成的视频因可能涉及版权限制未通过审核，无法返回，请调整提示词或参考素材后再试。"
		}
		return "生成的视频未通过内容安全审核，无法返回，请调整提示词或参考素材后再试。"
	}
	if copyright {
		return subject + "因可能涉及版权限制未通过审核，请修改或更换后再试。"
	}
	if safety {
		if subject == "提示词" {
			return "提示词未通过内容安全审核，请修改提示词后再试。"
		}
		if subject == "输入内容" {
			return "输入内容未通过内容安全审核，请修改提示词或参考素材后再试。"
		}
		return subject + "未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"
	}
	if strings.Contains(code, "download") || strings.Contains(code, "invalidurl") || strings.Contains(reason, "failed to download") || strings.Contains(reason, "failed to fetch") || strings.Contains(reason, "url is not accessible") {
		return subject + "无法读取，请确认素材地址有效且允许访问。"
	}
	if strings.Contains(code, "unsupportedformat") || strings.Contains(reason, "unsupported") && strings.Contains(reason, "format") || strings.Contains(reason, "format is not supported") {
		return subject + "格式不支持，请转换为支持的格式后再试。"
	}
	if subject != "输入内容" && strings.Contains(code, "notfound") {
		return subject + "不存在或无权使用，请检查素材引用或更换素材。"
	}
	if strings.HasPrefix(code, "invalidparameter") || strings.HasPrefix(code, "missingparameter") {
		field := strings.ToLower(failure.Param)
		if field == "" {
			_, field, _ = strings.Cut(code, ".")
		}
		label := ""
		switch field {
		case "duration", "seconds", "duration_seconds":
			label = "视频时长"
		case "resolution", "size", "width", "height":
			label = "视频尺寸"
		case "aspect_ratio":
			label = "视频宽高比"
		case "fps":
			label = "视频帧率"
		case "prompt", "negative_prompt":
			label = "提示词"
		}
		if label != "" {
			if strings.HasPrefix(code, "missingparameter") {
				return "缺少必要参数：" + label + "，请补充后再试。"
			}
			return label + "参数不符合要求，请调整后再试。"
		}
		if subject != "输入内容" && strings.Contains(reason, "duration") {
			return subject + "时长不符合要求，请调整后再试。"
		}
		if subject != "输入内容" && (strings.Contains(reason, "size") || strings.Contains(reason, "resolution")) {
			return subject + "尺寸或大小不符合要求，请调整后再试。"
		}
		return "视频请求参数不符合要求，请检查参数后再试。"
	}
	return fallback
}

func VideoTaskErrorMessage(task *model.Task, taskErr *dto.TaskError) string {
	var request json.RawMessage
	if task.ID > 0 && model.DB != nil {
		var event model.TaskEvent
		if err := model.DB.Select("payload").Where("task_id = ? AND kind = ?", task.ID, "request").Take(&event).Error; err == nil {
			var detail struct {
				Admin struct {
					Upstream json.RawMessage `json:"upstream_request"`
				} `json:"admin_info"`
			}
			if common.UnmarshalJsonStr(event.Payload, &detail) == nil {
				request = detail.Admin.Upstream
			}
		}
	}
	return VideoSubmissionErrorMessage(taskErr, request)
}

func VideoTaskFailureMessage(task *model.Task, reason string) string {
	body := task.Data
	if failure := parseVideoUpstreamError(body); failure.Code == "" && failure.Message == "" {
		body = json.RawMessage(reason)
		if failure := parseVideoUpstreamError(body); failure.Code == "" && failure.Message == "" {
			body, _ = common.Marshal(videoUpstreamError{Message: reason})
		}
	}
	return VideoTaskErrorMessage(task, &dto.TaskError{Code: "video_generation_failed", Message: string(body)})
}

func VideoTaskLogFailureMessages(tasks []*model.Task) map[int64]string {
	ids := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == model.TaskStatusFailure && IsVideoTask(task) && (task.FailReason == "" || task.FailReason == VideoSubmissionFailureMessage("") || task.FailReason == VideoSubmissionFailureMessage("material_configuration_failed")) {
			ids = append(ids, task.ID)
		}
	}
	messages := make(map[int64]string)
	if len(ids) == 0 || model.DB == nil {
		return messages
	}
	var events []model.TaskEvent
	if model.DB.Where("task_id IN ? AND kind IN ?", ids, []string{"request", "result"}).Find(&events).Error != nil {
		return messages
	}
	requests := make(map[int64]json.RawMessage)
	errors := make(map[int64]*dto.TaskError)
	for _, event := range events {
		var detail struct {
			Admin struct {
				Upstream json.RawMessage `json:"upstream_request"`
				Payload  struct {
					Error json.RawMessage `json:"upstream_error"`
					Data  json.RawMessage `json:"data"`
				} `json:"payload"`
			} `json:"admin_info"`
		}
		if common.UnmarshalJsonStr(event.Payload, &detail) != nil {
			continue
		}
		if event.Kind == "request" {
			requests[event.TaskID] = detail.Admin.Upstream
			continue
		}
		var taskErr struct {
			Code       string `json:"code"`
			StatusCode int    `json:"status_code"`
		}
		if len(detail.Admin.Payload.Error) > 0 {
			_ = common.Unmarshal(detail.Admin.Payload.Error, &taskErr)
			if taskErr.Code == "fail_to_fetch_task" {
				errors[event.TaskID] = &dto.TaskError{Code: taskErr.Code, StatusCode: taskErr.StatusCode, Diagnostic: detail.Admin.Payload.Error}
			}
		} else if len(detail.Admin.Payload.Data) > 0 {
			errors[event.TaskID] = &dto.TaskError{Code: "video_generation_failed", Message: string(detail.Admin.Payload.Data)}
		}
	}
	for _, task := range tasks {
		if taskErr := errors[task.ID]; taskErr != nil {
			message := VideoSubmissionErrorMessage(taskErr, requests[task.ID])
			if message != VideoSubmissionFailureMessage("") && message != "视频生成失败，暂未获取到具体原因，请提供任务 ID 联系管理员核查。" {
				messages[task.ID] = message
			}
		}
	}
	return messages
}

func IsVideoTask(task *model.Task) bool {
	switch constant.NormalizeTaskAction(task.Action) {
	case constant.TaskActionImageToVideo, constant.TaskActionTextToVideo, constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo, constant.TaskActionRemix:
		return true
	default:
		return false
	}
}
