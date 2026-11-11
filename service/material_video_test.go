package service

import (
	"context"
	"errors"
	"testing"

	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		{"image safety", `{"error":{"code":"InputImageSensitiveContentDetected","message":"input image rejected"}}`, "参考图片未通过内容安全审核，请更换图片。", 400},
		{"copyright", `{"error":{"code":"PolicyViolation","message":"copyright restrictions"}}`, "参考内容因可能涉及版权限制未通过审核，请修改后再试。", 400},
		{"invalid parameter", `{"error":{"code":"InvalidParameter.duration","message":"duration is invalid; token=secret"}}`, "视频请求参数不符合要求，请检查参数后再试。", 400},
		{"authentication", `{"error":{"code":"Unauthorized","message":"token=secret"}}`, "视频服务异常，请联系管理员", 403},
		{"timeout is not review rejection", `{"error":{"code":"InternalError","message":"copyright review service timeout"}}`, "视频请求处理失败，请联系管理员", 400},
		{"arbitrary text is not forwarded", `{"error":{"message":"token=secret; https://private.example/copyright"}}`, "视频请求处理失败，请联系管理员", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskErr := &taskdto.TaskError{Code: "fail_to_fetch_task", Message: tc.body, Error: errors.New(tc.body), StatusCode: tc.status}
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr))
			taskErr.Message = tc.want
			assert.Equal(t, tc.want, VideoSubmissionErrorMessage(taskErr), "public projection must not hide the original refusal")
		})
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
