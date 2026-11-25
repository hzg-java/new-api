package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateVideoMaterialBodyImageURL(t *testing.T) {
	fetch := system_setting.GetFetchSetting()
	previous := *fetch
	*fetch = system_setting.FetchSetting{EnableSSRFProtection: true, AllowedPorts: []string{"80", "443", "8080"}}
	t.Cleanup(func() { *fetch = previous })

	for _, tc := range []struct {
		name, source string
		allowed      bool
	}{
		{"public HTTP", "http://8.8.8.8/reference.png", true},
		{"public HTTPS", "https://8.8.8.8/reference.png", true},
		{"HTTP port allowed", "http://8.8.8.8:8080/reference.png", true},
		{"HTTP port not allowed", "http://8.8.8.8:8888/reference.png", false},
		{"private HTTP", "http://10.0.0.1/reference.png", false},
		{"embedded credentials", "http://user:pass@8.8.8.8/reference.png", false},
		{"fragment", "http://8.8.8.8/reference.png#frame", false},
		{"inline image", "data:image/png;base64,AAAA", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "prompt"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": tc.source}},
			}}
			err := ValidateVideoMaterialBody(body)
			if tc.allowed {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, "content[1].image_url.url must be a public HTTP or HTTPS image URL")
		})
	}
}

func TestVideoMaterialFailure(t *testing.T) {
	for _, tc := range []struct {
		name, reason, message string
	}{
		{"configuration rejection", "Material preparation rejected: topenrouter material HTTP status 403: not configured", "图片上传服务未正确配置，暂时无法使用，请联系管理员处理。"},
		{"current configuration rejection", "Material preparation rejected: topenrouter material upstream HTTP status 403: not configured", "图片上传服务未正确配置，暂时无法使用，请联系管理员处理。"},
		{"authorization rejection", "mobile cloud material HTTP status 401: Unauthorized", "视频服务异常，请联系管理员"},
		{"configuration takes priority over image text", "HTTP status 403: image url not accessible", "视频服务异常，请联系管理员"},
		{"sensitive content", "Upstream material processing failed: InputImageSensitiveContentDetected: The input image may contain sensitive information", "参考图片（第 5 项）未通过内容安全审核，请更换图片。"},
		{"copyright restriction", "Upstream material processing failed: PolicyViolation: The input image may be related to copyright restrictions", "参考图片（第 5 项）因可能涉及版权限制未通过审核，请更换图片。"},
		{"copyright takes priority over review", "Image rejected by content review because of copyright restrictions", "参考图片（第 5 项）因可能涉及版权限制未通过审核，请更换图片。"},
		{"legacy review rejection", "Image rejected by content review", "参考图片（第 5 项）未通过审核，请更换图片"},
		{"unreadable image", "Failed to download image", "参考图片（第 5 项）无法读取，请检查图片地址"},
		{"invalid image source", "Material source must be a public HTTPS image URL", "参考图片（第 5 项）无法读取，请检查图片地址"},
		{"unsupported format", "Unsupported image format", "参考图片（第 5 项）格式不支持，请更换图片"},
		{"missing reference", "Material reference is unavailable in the selected resource space", "参考图片（第 5 项）不可用，请更换图片"},
		{"preparation timeout", "Material preparation timed out; retry to check the existing material", "参考图片（第 5 项）处理超时，结果尚未确认，请勿重复上传，联系管理员核查。"},
		{"wait timeout", "Material preparation wait ended; retry to check the existing material", "参考图片（第 5 项）处理超时，结果尚未确认，请勿重复上传，联系管理员核查。"},
		{"download timeout", "Failed to download image: timed out", "参考图片（第 5 项）处理超时，结果尚未确认，请勿重复上传，联系管理员核查。"},
		{"copyright service timeout is not rejection", "copyright check service timed out", "参考图片（第 5 项）处理超时，结果尚未确认，请勿重复上传，联系管理员核查。"},
		{"sensitive service timeout is not rejection", "sensitive content review service timeout", "参考图片（第 5 项）处理超时，结果尚未确认，请勿重复上传，联系管理员核查。"},
		{"busy material", "Material is unavailable or in use; retry after the current operation finishes", "参考图片（第 5 项）正在被其他操作占用，请等待当前操作结束后再试。"},
		{"response mismatch", "Material response does not match the requested material: group ID", "参考图片（第 5 项）的处理结果与原图片不一致，请勿重复上传，联系管理员核查。"},
		{"unconfirmed readiness", "Material readiness could not be confirmed; synchronize the existing material before retrying", "参考图片（第 5 项）的处理状态无法确认，请勿重复上传，联系管理员核查。"},
		{"unrecognized failure", "Material preparation rejected: vendor-specific error", "参考图片（第 5 项）处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。"},
		{"review service error is not rejection", "moderation service unavailable", "参考图片（第 5 项）处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。"},
		{"sensitive keyword alone is not rejection", "sensitive content check unavailable", "参考图片（第 5 项）处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。"},
		{"unknown upload result", "Material upload result is unknown; retry to check without uploading again", "参考图片（第 5 项）上传结果无法确认，请勿重复上传，联系管理员核查。"},
		{"unconfirmed upload result", "Material upload result could not be confirmed; retry to check without uploading again", "参考图片（第 5 项）上传结果无法确认，请勿重复上传，联系管理员核查。"},
		{"gateway error", "Material preparation rejected: topenrouter material HTTP status 422: gateway internal error, please retry.", "图片上传服务发生内部错误，请联系管理员处理，无需修改图片。"},
		{"gateway error takes priority over image text", "HTTP status 422: gateway internal error: image url failure", "图片上传服务发生内部错误，请联系管理员处理，无需修改图片。"},
		{"URL keywords are not error evidence", "Material preparation rejected: https://private.example/copyright/sensitive?token=secret", "参考图片（第 5 项）处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。"},
		{"connection EOF", "Material preparation rejected: topenrouter material HTTP status 400: Post \"https://private.example/api?token=secret\": EOF", "处理参考图片（第 5 项）时连接意外中断，结果无法确认，请勿重复提交，联系管理员核查。"},
		{"private upstream details", "Image rejected by content review; https://private.example/image?token=secret Authorization: Bearer credential", "参考图片（第 5 项）未通过审核，请更换图片"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issue := MaterialPreparationIssue{Index: 4, MaterialID: "mat_internal", Field: "content[4].image_url.url", Usage: "reference_image", Status: "failed", Reason: tc.reason}
			message, public := VideoMaterialFailure([]MaterialPreparationIssue{issue})
			assert.Equal(t, tc.message, message)
			require.Len(t, public, 1)
			assert.Equal(t, tc.message, public[0].Reason)
			assert.Equal(t, issue.Index, public[0].Index)
			assert.Equal(t, issue.Field, public[0].Field)
			assert.Equal(t, issue.Usage, public[0].Usage)
			assert.Equal(t, issue.Status, public[0].Status)
			assert.Empty(t, public[0].MaterialID)
			assert.Equal(t, tc.reason, issue.Reason)
			assert.Equal(t, "mat_internal", issue.MaterialID)
			repeatedMessage, repeatedPublic := VideoMaterialFailure(public)
			assert.Equal(t, message, repeatedMessage)
			assert.Equal(t, public, repeatedPublic)
		})
	}
	t.Run("all affected positions remain identifiable", func(t *testing.T) {
		issues := []MaterialPreparationIssue{
			{Index: 1, Field: "content[1].image_url.url", Reason: "Image rejected by content review"},
			{Index: 4, Field: "content[4].image_url.url", Reason: "Failed to download image"},
		}
		message, public := VideoMaterialFailure(issues)
		assert.Equal(t, "参考图片（第 2 项）未通过审核，请更换图片；参考图片（第 5 项）无法读取，请检查图片地址", message)
		require.Len(t, public, 2)
		assert.Equal(t, issues[0].Field, public[0].Field)
		assert.Equal(t, issues[1].Field, public[1].Field)
	})
	t.Run("processing is not a failed image", func(t *testing.T) {
		message, public := VideoMaterialFailure([]MaterialPreparationIssue{
			{Index: 1, imageNumber: 1, Status: "processing", Reason: "Material preparation is in progress"},
			{Index: 4, imageNumber: 2, Status: "failed", Reason: "InputImageSensitiveContentDetected"},
		})
		assert.Equal(t, "第 1 张参考图片仍在处理中，暂未完成；第 2 张参考图片未通过内容安全审核，请更换图片", message)
		require.Len(t, public, 2)
		assert.Equal(t, "processing", public[0].Status)
		repeated, _ := VideoMaterialFailure(public)
		assert.Equal(t, message, repeated)
	})
	t.Run("repeated service failures stay concise", func(t *testing.T) {
		message, public := VideoMaterialFailure([]MaterialPreparationIssue{{Index: 1, Reason: "HTTP status 403: not configured"}, {Index: 4, Reason: "HTTP status 403: not configured"}})
		assert.Equal(t, "图片上传服务未正确配置，暂时无法使用，请联系管理员处理。", message)
		assert.Len(t, public, 2)
	})
	t.Run("empty issues", func(t *testing.T) {
		message, public := VideoMaterialFailure(nil)
		assert.Equal(t, "参考图片处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。", message)
		assert.Nil(t, public)
	})
}

func TestVideoMaterialFailureImageOrder(t *testing.T) {
	body := map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "prompt"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "http://localhost/first.png"}},
		map[string]any{"type": "text", "text": "another prompt"},
		map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": "https://example.com/audio.mp3"}},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "http://localhost/second.png"}},
	}}
	_, inputs, err := videoMaterialInputs(body)
	require.NoError(t, err)
	_, issues, err := PrepareVideoMaterials(context.Background(), 1, MaterialChannel{}, inputs)
	require.NoError(t, err)
	require.Len(t, issues, 2)
	message, public := VideoMaterialFailure(issues)
	assert.Equal(t, "第 1 张参考图片无法读取，请检查图片地址；第 2 张参考图片无法读取，请检查图片地址", message)
	assert.Equal(t, "content[1].image_url.url", public[0].Field)
	assert.Equal(t, "content[4].image_url.url", public[1].Field)
	assert.Equal(t, 1, public[0].Index)
	assert.Equal(t, 4, public[1].Index)
	assert.Equal(t, "第 1 张参考图片无法读取，请检查图片地址", public[0].Reason)
	assert.Equal(t, "第 2 张参考图片无法读取，请检查图片地址", public[1].Reason)
	repeatedMessage, repeated := VideoMaterialFailure(public)
	assert.Equal(t, message, repeatedMessage)
	assert.Equal(t, public, repeated)
}

func TestVideoSubmissionErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"image safety", `{"error":{"code":"InputImageSensitiveContentDetected","message":"input image rejected"}}`, "图片素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。", 400},
		{"copyright", `{"error":{"code":"PolicyViolation","message":"copyright restrictions"}}`, "输入内容因可能涉及版权限制未通过审核，请修改或更换后再试。", 400},
		{"invalid parameter", `{"error":{"code":"InvalidParameter.duration","message":"duration is invalid; token=secret"}}`, "视频时长参数不符合要求，请调整后再试。", 400},
		{"authentication", `{"error":{"code":"Unauthorized","message":"token=secret"}}`, "视频服务权限异常，暂时无法生成，请联系管理员处理。", 403},
		{"timeout is not review rejection", `{"error":{"code":"InternalError","message":"copyright review service timeout"}}`, "视频处理超时，结果尚未确认，请勿重复提交，请提供任务 ID 联系管理员核查。", 400},
		{"arbitrary text is not forwarded", `{"error":{"message":"token=secret; https://private.example/copyright"}}`, "视频请求处理失败，请联系管理员", 400},
		{"rate limit is not content rejection", `{"error":{"code":"InputImageSensitiveContentDetected","message":"stale rejection"}}`, "当前视频请求过多，暂时无法处理，请稍后查询已有任务或联系管理员核查。", 429},
		{"gateway failure is not content rejection", `{"error":{"code":"InputAudioSensitiveContentDetected","message":"stale rejection"}}`, "视频服务暂时不可用，提交结果尚未确认，请勿重复提交，请联系管理员核查。", 502},
		{"download forbidden is not service credentials", `{"error":{"code":"InvalidParameter","message":"Failed to download input audio: HTTP 403"}}`, "音频素材无法读取，请确认素材地址有效且允许访问。", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskErr := &taskdto.TaskError{Code: "fail_to_fetch_task", Message: tc.body, Error: errors.New(tc.body), StatusCode: tc.status}
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr, nil))
			taskErr.Message = tc.want
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr, nil), "public projection must not hide the original refusal")
		})
	}
}

func TestVideoSubmissionSpecificErrors(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"audio safety", `{"error":{"code":"InputAudioSensitiveContentDetected","message":"The input audio may contain sensitive information","param":"content[9]"}}`, "音频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"video safety", `{"error":{"code":"InputVideoSensitiveContentDetected","message":"input video rejected"}}`, "视频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"prompt safety", `{"error":{"code":"InputTextSensitiveContentDetected","message":"input text rejected"}}`, "提示词未通过内容安全审核，请修改提示词后再试。"},
		{"prompt code overrides image words", `{"error":{"code":"InputTextSensitiveContentDetected","message":"prompt describing input image rejected"}}`, "提示词未通过内容安全审核，请修改提示词后再试。"},
		{"output safety", `{"error":{"code":"OutputVideoSensitiveContentDetected","message":"output video rejected","param":"content[9]"}}`, "生成的视频未通过内容安全审核，无法返回，请调整提示词或参考素材后再试。"},
		{"service credit", `{"error":{"code":"InsufficientBalance","message":"account balance insufficient"}}`, "视频服务额度不足，暂时无法生成，请联系管理员处理。"},
		{"internal error", `{"error":{"code":"InternalError","message":"internal processing failed"}}`, "视频生成服务发生内部错误，本次生成失败，请联系管理员处理。"},
		{"missing duration", `{"error":{"code":"MissingParameter.duration","message":"missing duration"}}`, "缺少必要参数：视频时长，请补充后再试。"},
		{"format", `{"error":{"code":"InvalidParameter","message":"Unsupported audio format","param":"content[9].audio_url.url"}}`, "音频素材格式不支持，请转换为支持的格式后再试。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskErr := &taskdto.TaskError{Code: "fail_to_fetch_task", Message: tc.body, StatusCode: 400}
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr, nil))
		})
	}
}

func TestVideoSubmissionMaterialPositions(t *testing.T) {
	request := json.RawMessage(`{"content":[{"type":"text"},{"type":"image_url"},{"type":"video_url"},{"type":"audio_url"},{"type":"image_url"},{"type":"audio_url"},{"type":"text"},{"type":"audio_url"}]}`)
	for _, tc := range []struct{ name, body, want string }{
		{"third audio among mixed content", `{"error":{"code":"InputAudioSensitiveContentDetected","message":"input audio rejected","param":"content[7]"}}`, "第 3 个音频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"second image among mixed content", `{"error":{"code":"InputImageSensitiveContentDetected","param":"content[4].image_url.url"}}`, "第 2 个图片素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"video identified by error message", `{"error":{"code":"InputVideoSensitiveContentDetected","message":"The input video 'content[2]' may contain sensitive information"}}`, "第 1 个视频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"multiple image positions", `{"error":{"code":"InputImageSensitiveContentDetected","message":"content[1], content[4] rejected"}}`, "第 1 个图片素材、第 2 个图片素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"missing index does not invent a number", `{"error":{"code":"InputAudioSensitiveContentDetected"}}`, "音频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"out of range index does not invent a number", `{"error":{"code":"InputAudioSensitiveContentDetected","param":"content[99]"}}`, "音频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"mismatched media does not invent a number", `{"error":{"code":"InputAudioSensitiveContentDetected","param":"content[1]"}}`, "音频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"URL content is not index evidence", `{"error":{"code":"InputAudioSensitiveContentDetected","message":"https://private.example/content[7]?token=secret"}}`, "音频素材未通过内容安全审核，可能包含敏感信息，请更换该素材或修改其内容后再试。"},
		{"output failure never blames input", `{"error":{"code":"OutputVideoSensitiveContentDetected","param":"content[7]"}}`, "生成的视频未通过内容安全审核，无法返回，请调整提示词或参考素材后再试。"},
		{"audio read failure", `{"error":{"code":"InvalidParameter","message":"Failed to download input audio","param":"content[7]"}}`, "第 3 个音频素材无法读取，请确认素材地址有效且允许访问。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskErr := &taskdto.TaskError{Code: "fail_to_fetch_task", Message: tc.body, StatusCode: 400}
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr, request))
			diagnostic := TaskErrorDiagnostic(taskErr)
			taskErr.Diagnostic, taskErr.Message = diagnostic, tc.want
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr, request), "normalizing twice preserves the same material position")
		})
	}
}

func TestVideoTaskFailureWithoutErrorCode(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"timeout"}}`, `{}`} {
		task := &model.Task{Data: json.RawMessage(body)}
		assert.Equal(t, "视频处理超时，结果尚未确认，请勿重复提交，请提供任务 ID 联系管理员核查。", VideoTaskFailureMessage(task, "timeout"))
	}
}

func TestVideoSubmissionFailureMessage(t *testing.T) {
	for _, tc := range []struct{ code, message string }{
		{"material_configuration_changed", "视频服务异常，请联系管理员"},
		{"task_channel_changed", "视频服务异常，请联系管理员"},
		{"material_preparation_timeout", "参考图片处理超时，视频尚未提交，请查询已有任务或联系管理员核查。"},
		{"task_submission_unknown", "视频提交结果暂无法确认，请勿重复提交"},
		{"task_submission_interrupted", "视频提交结果暂无法确认，请勿重复提交"},
		{"task_submission_timeout", "视频提交结果暂无法确认，请勿重复提交"},
		{"task_timeout", "视频任务超时，请联系管理员"},
		{"unrecognized_failure", "视频请求处理失败，请联系管理员"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			assert.Equal(t, tc.message, VideoSubmissionFailureMessage(tc.code))
		})
	}
}
