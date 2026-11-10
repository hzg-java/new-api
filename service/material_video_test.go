package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoMaterialFailure(t *testing.T) {
	for _, tc := range []struct {
		name, reason, message, publicReason string
	}{
		{"configuration rejection", "Material preparation rejected: topenrouter material HTTP status 403: not configured", "视频服务异常，请联系管理员", "视频服务异常，请联系管理员"},
		{"current configuration rejection", "Material preparation rejected: topenrouter material upstream HTTP status 403: not configured", "视频服务异常，请联系管理员", "视频服务异常，请联系管理员"},
		{"authorization rejection", "mobile cloud material HTTP status 401: Unauthorized", "视频服务异常，请联系管理员", "视频服务异常，请联系管理员"},
		{"configuration takes priority over image text", "HTTP status 403: image url not accessible", "视频服务异常，请联系管理员", "视频服务异常，请联系管理员"},
		{"review rejection", "Upstream material processing failed: InputImageSensitiveContentDetected: The input image may contain sensitive information", "参考图片（第 5 项）未通过审核，请更换图片", "参考图片未通过审核，请更换图片"},
		{"legacy review rejection", "Image rejected by content review", "参考图片（第 5 项）未通过审核，请更换图片", "参考图片未通过审核，请更换图片"},
		{"unreadable image", "Failed to download image", "参考图片（第 5 项）无法读取，请检查图片地址", "参考图片无法读取，请检查图片地址"},
		{"invalid image source", "Material source must be a public HTTPS image URL", "参考图片（第 5 项）无法读取，请检查图片地址", "参考图片无法读取，请检查图片地址"},
		{"unsupported format", "Unsupported image format", "参考图片（第 5 项）格式不支持，请更换图片", "参考图片格式不支持，请更换图片"},
		{"missing reference", "Material reference is unavailable in the selected resource space", "参考图片（第 5 项）不可用，请更换图片", "参考图片不可用，请更换图片"},
		{"preparation timeout", "Material preparation timed out; retry to check the existing material", "参考图片处理超时，请稍后重试", "参考图片处理超时，请稍后重试"},
		{"wait timeout", "Material preparation wait ended; retry to check the existing material", "参考图片处理超时，请稍后重试", "参考图片处理超时，请稍后重试"},
		{"download timeout", "Failed to download image: timed out", "参考图片处理超时，请稍后重试", "参考图片处理超时，请稍后重试"},
		{"unrecognized failure", "Material preparation rejected: vendor-specific error", "视频请求处理失败，请联系管理员", "视频请求处理失败，请联系管理员"},
		{"unknown upload result", "Material upload result is unknown; retry to check without uploading again", "视频请求处理失败，请联系管理员", "视频请求处理失败，请联系管理员"},
		{"private upstream details", "Image rejected by content review; https://private.example/image?token=secret Authorization: Bearer credential", "参考图片（第 5 项）未通过审核，请更换图片", "参考图片未通过审核，请更换图片"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issue := MaterialPreparationIssue{Index: 4, MaterialID: "mat_internal", Field: "content[4].image_url.url", Usage: "reference_image", Status: "failed", Reason: tc.reason}
			message, public := VideoMaterialFailure([]MaterialPreparationIssue{issue})
			assert.Equal(t, tc.message, message)
			require.Len(t, public, 1)
			assert.Equal(t, MaterialPreparationIssue{Index: 4, Field: issue.Field, Usage: issue.Usage, Status: "failed", Reason: tc.publicReason}, public[0])
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
	t.Run("repeated service failures stay concise", func(t *testing.T) {
		message, public := VideoMaterialFailure([]MaterialPreparationIssue{{Index: 1, Reason: "HTTP status 403: not configured"}, {Index: 4, Reason: "HTTP status 403: not configured"}})
		assert.Equal(t, "视频服务异常，请联系管理员", message)
		assert.Len(t, public, 2)
	})
	t.Run("empty issues", func(t *testing.T) {
		message, public := VideoMaterialFailure(nil)
		assert.Equal(t, "视频请求处理失败，请联系管理员", message)
		assert.Nil(t, public)
	})
}

func TestVideoSubmissionFailureMessage(t *testing.T) {
	for _, tc := range []struct{ code, message string }{
		{"material_configuration_changed", "视频服务异常，请联系管理员"},
		{"task_channel_changed", "视频服务异常，请联系管理员"},
		{"material_preparation_timeout", "参考图片处理超时，请稍后重试"},
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
