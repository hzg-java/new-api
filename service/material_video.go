package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type MaterialImageInput struct {
	Index  int
	Source string
	Field  string
	Usage  string
}

func VideoMaterialFailure(issues []MaterialPreparationIssue) (string, []MaterialPreparationIssue) {
	var public []MaterialPreparationIssue
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		reason := strings.TrimSpace(strings.ToLower(topenRouterMaterialReasonURL.ReplaceAllString(issue.Reason, "")))
		message := "参考图片处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。"
		imageSpecific := true
		switch {
		case strings.Contains(reason, "not configured"), strings.Contains(reason, "未正确配置"):
			message, imageSpecific = "图片上传服务未正确配置，暂时无法使用，请联系管理员处理。", false
		case strings.Contains(reason, "unauthorized"), strings.Contains(reason, "forbidden"), strings.Contains(reason, "accessdenied"), strings.Contains(reason, "authenticationfailed"), strings.Contains(reason, "http status 401"), strings.Contains(reason, "http status 403"), strings.Contains(reason, "视频服务异常"):
			message, imageSpecific = "视频服务异常，请联系管理员", false
		case strings.Contains(reason, "gateway internal error"), strings.Contains(reason, "图片上传服务发生内部错误"):
			message, imageSpecific = "图片上传服务发生内部错误，请联系管理员处理，无需修改图片。", false
		case strings.Contains(reason, "eof"), strings.Contains(reason, "connection reset"), strings.Contains(reason, "连接意外中断"):
			message = "处理参考图片时连接意外中断，结果无法确认，请勿重复提交，联系管理员核查。"
		case strings.Contains(reason, "material upload result is unknown"), strings.Contains(reason, "material upload result could not be confirmed"), strings.Contains(reason, "上传结果无法确认"):
			message = "参考图片上传结果无法确认，请勿重复上传，联系管理员核查。"
		case strings.Contains(reason, "timed out"), strings.Contains(reason, "timeout"), strings.Contains(reason, "wait ended"), strings.Contains(reason, "处理超时"):
			message = "参考图片处理超时，结果尚未确认，请勿重复上传，联系管理员核查。"
		case strings.Contains(reason, "in use"), strings.Contains(reason, "操作占用"):
			message = "参考图片正在被其他操作占用，请等待当前操作结束后再试。"
		case strings.Contains(reason, "response does not match"), strings.Contains(reason, "处理结果与原图片不一致"):
			message = "参考图片的处理结果与原图片不一致，请勿重复上传，联系管理员核查。"
		case strings.Contains(reason, "readiness could not be confirmed"), strings.Contains(reason, "unrecognized material status"), strings.Contains(reason, "material status query"), strings.Contains(reason, "处理状态无法确认"):
			message = "参考图片的处理状态无法确认，请勿重复上传，联系管理员核查。"
		case strings.Contains(reason, "material no longer exists"), strings.Contains(reason, "已不存在或无权访问"):
			message = "参考图片已不存在或无权访问，请联系管理员核查。"
		case issue.Status == "pending" || issue.Status == "creating" || issue.Status == "processing":
			message = "参考图片仍在处理中，暂未完成。"
		case strings.Contains(reason, "copyright restrictions"), strings.Contains(reason, "copyright") && (strings.Contains(reason, "rejected") || strings.Contains(reason, "policyviolation")), strings.Contains(reason, "版权限制"):
			message = "参考图片因可能涉及版权限制未通过审核，请更换图片。"
		case strings.Contains(reason, "sensitivecontentdetected"), strings.Contains(reason, "sensitive") && (strings.Contains(reason, "rejected") || strings.Contains(reason, "policyviolation")), strings.Contains(reason, "内容安全审核"):
			message = "参考图片未通过内容安全审核，请更换图片。"
		case (strings.Contains(reason, "content review") || strings.Contains(reason, "moderation")) && (strings.Contains(reason, "rejected") || strings.Contains(reason, "policyviolation")), strings.Contains(reason, "未通过审核"), strings.Contains(reason, "审核不通过"):
			message = "参考图片未通过审核，请更换图片"
		case strings.Contains(reason, "unsupported image format"), strings.Contains(reason, "image format unsupported"), strings.Contains(reason, "格式不支持"):
			message = "参考图片格式不支持，请更换图片"
		case strings.Contains(reason, "download image"), strings.Contains(reason, "image download"), strings.Contains(reason, "image url"), strings.Contains(reason, "public https"), strings.Contains(reason, "无法读取"):
			message = "参考图片无法读取，请检查图片地址"
		case strings.Contains(reason, "material reference"), strings.Contains(reason, "material deletion"), strings.Contains(reason, "material is unavailable or its source changed"), strings.Contains(reason, "不可用，请更换图片"):
			message = "参考图片不可用，请更换图片"
		}
		if imageSpecific {
			label := fmt.Sprintf("参考图片（第 %d 项）", issue.Index+1)
			if issue.imageNumber > 0 {
				label = fmt.Sprintf("第 %d 张参考图片", issue.imageNumber)
			}
			message = strings.Replace(message, "参考图片", label, 1)
		}
		issue.MaterialID, issue.Reason = "", message
		public = append(public, issue)
		if !slices.Contains(messages, message) {
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		return "参考图片处理失败，暂未获取到具体原因，请联系管理员并提供任务 ID。", public
	}
	if len(messages) > 1 {
		for i := range messages {
			messages[i] = strings.TrimSuffix(messages[i], "。")
		}
	}
	return strings.Join(messages, "；"), public
}

func VideoSubmissionErrorMessage(taskErr *taskdto.TaskError) string {
	if taskErr.StatusCode == http.StatusUnauthorized || taskErr.StatusCode == http.StatusForbidden {
		return VideoSubmissionFailureMessage("material_configuration_failed")
	}
	fallback := VideoSubmissionFailureMessage(taskErr.Code)
	if taskErr.Code != "fail_to_fetch_task" || taskErr.StatusCode < 400 || taskErr.StatusCode >= 500 || taskErr.StatusCode == http.StatusRequestTimeout || taskErr.StatusCode == http.StatusTooManyRequests {
		return fallback
	}
	body := taskErr.Message
	if taskErr.Error != nil {
		body = taskErr.Error.Error()
	}
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if common.Unmarshal([]byte(body), &response) != nil {
		return fallback
	}
	if response.Error != nil {
		response.Code, response.Message = response.Error.Code, response.Error.Message
	}
	code := strings.ToLower(response.Code)
	reason := strings.ToLower(topenRouterMaterialReasonURL.ReplaceAllString(response.Message, ""))
	if strings.Contains(code, "internal") || strings.Contains(reason, "timeout") || strings.Contains(reason, "timed out") {
		return fallback
	}
	switch {
	case strings.Contains(code, "inputimagesensitivecontentdetected"):
		return "参考图片未通过内容安全审核，请更换图片。"
	case strings.Contains(code, "policyviolation") && strings.Contains(reason, "copyright"):
		return "参考内容因可能涉及版权限制未通过审核，请修改后再试。"
	case strings.Contains(code, "contentpolicyviolation"), strings.Contains(code, "contentsensitive"), strings.Contains(code, "contentfilter"):
		return "参考内容或提示词未通过内容安全审核，请修改后再试。"
	case strings.HasPrefix(code, "invalidparameter"), strings.HasPrefix(code, "missingparameter"):
		return "视频请求参数不符合要求，请检查参数后再试。"
	default:
		return fallback
	}
}

func VideoSubmissionFailureMessage(code string) string {
	switch code {
	case "material_configuration_failed", "material_configuration_changed", "task_channel_changed", "task_channel_unavailable", "task_plugin_changed", "task_plugin_unavailable":
		return "视频服务异常，请联系管理员"
	case "material_preparation_timeout":
		return "参考图片处理超时，视频尚未提交，请查询已有任务或联系管理员核查。"
	case "task_submission_unknown", "task_submission_interrupted", "task_submission_timeout":
		return "视频提交结果暂无法确认，请勿重复提交"
	case "task_timeout":
		return "视频任务超时，请联系管理员"
	default:
		return "视频请求处理失败，请联系管理员"
	}
}

func PrepareVideoMaterials(ctx context.Context, userID int, channel MaterialChannel, inputs []MaterialImageInput) (resolved []string, issues []MaterialPreparationIssue, err error) {
	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	defer func() {
		for i := range issues {
			for j, input := range inputs {
				if issues[i].Index == input.Index {
					issues[i].imageNumber = j + 1
					break
				}
			}
		}
	}()
	materials := make([]*model.Material, len(inputs))
	issues = make([]MaterialPreparationIssue, 0)
	for i, input := range inputs {
		source := strings.TrimSpace(input.Source)
		var material *model.Material
		var err error
		if assetID, isAsset := strings.CutPrefix(source, "asset://"); isAsset {
			if validateMobileCloudMaterialID(assetID) != nil {
				issues = append(issues, MaterialPreparationIssue{Index: input.Index, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Invalid material reference"})
				continue
			}
			var candidates []model.Material
			err = model.DB.WithContext(ctx).Where("user_id = ? AND library_type = ? AND file_type = ? AND id IN (?)", userID, model.MaterialLibraryAIGC, model.MaterialFileImage,
				model.DB.Model(&model.MaterialBinding{}).Select("material_id").Where("scope = ? AND asset_id = ? AND state <> ?", channel.Scope, assetID, "retired")).Order("id").Find(&candidates).Error
			for j := range candidates {
				var count int64
				if err == nil {
					err = model.DB.WithContext(ctx).Model(&model.MaterialBinding{}).Where("material_id = ? AND scope = ? AND version = ? AND asset_id = ?", candidates[j].Id, channel.Scope, model.MaterialSourceVersion(&candidates[j]), assetID).Count(&count).Error
				}
				if count > 0 {
					material = &candidates[j]
					break
				}
			}
			if err == nil && material == nil {
				issues = append(issues, MaterialPreparationIssue{Index: input.Index, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Material reference is unavailable in the selected resource space"})
				continue
			}
		} else if ValidateMaterialPreparationSource(source) != nil {
			issues = append(issues, MaterialPreparationIssue{Index: input.Index, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Material source must be a public HTTPS image URL"})
		}
		if err != nil {
			issues = append(issues, MaterialPreparationIssue{Index: input.Index, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Video material lookup failed; retry to check the existing material"})
		}
		materials[i] = material
	}
	if len(issues) > 0 {
		return nil, issues, nil
	}
	for i, input := range inputs {
		if materials[i] == nil {
			material, err := model.FindOrCreateVideoMaterial(model.DB.WithContext(ctx), userID, strings.TrimSpace(input.Source), channel.Scope)
			if err != nil {
				return nil, []MaterialPreparationIssue{{Index: input.Index, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Video material lookup failed; retry to check the existing material"}}, nil
			}
			materials[i] = material
		}
		if materials[i].Deleting {
			issues = append(issues, MaterialPreparationIssue{Index: input.Index, MaterialID: materials[i].MaterialID, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Material deletion is in progress"})
		}
	}
	if len(issues) > 0 {
		return nil, issues, nil
	}
	resolved = make([]string, len(inputs))
	for {
		issues = issues[:0]
		pending := false
		seen := make(map[int]bool, len(materials))
		for i, material := range materials {
			input := inputs[i]
			if !seen[material.Id] && ctx.Err() == nil {
				seen[material.Id] = true
				binding := model.MaterialBinding{MaterialId: material.Id, Scope: channel.Scope, Version: model.MaterialSourceVersion(material), ChannelID: channel.ChannelID, ChannelName: channel.ChannelName, Provider: channel.Provider, Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: common.GetTimestamp(), UpdatedTime: common.GetTimestamp()}
				if err := model.EnsureMaterialChannelBindings(model.DB.WithContext(ctx), material, []model.MaterialBinding{binding}); err != nil {
					return nil, []MaterialPreparationIssue{{Index: input.Index, MaterialID: material.MaterialID, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Video material mapping failed; retry to check the existing material"}}, nil
				}
				if err := PrepareMaterial(ctx, material, channel.Scope, channel.Client); err != nil && ctx.Err() == nil {
					return nil, []MaterialPreparationIssue{{Index: input.Index, MaterialID: material.MaterialID, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Video material preparation failed; retry to check the existing material"}}, nil
				}
			}
			checkCtx, checkCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			var current model.Material
			err := model.DB.WithContext(checkCtx).First(&current, material.Id).Error
			var binding model.MaterialBinding
			if err == nil {
				err = model.DB.WithContext(checkCtx).Where("material_id = ? AND scope = ? AND version = ?", material.Id, channel.Scope, model.MaterialSourceVersion(material)).First(&binding).Error
			}
			checkCancel()
			issue := MaterialPreparationIssue{Index: input.Index, MaterialID: material.MaterialID, Field: input.Field, Usage: input.Usage, Status: binding.State, Reason: binding.FailReason}
			switch {
			case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
				issue.Status, issue.Reason = "unavailable", "Video material readiness check failed; retry to check the existing material"
			case errors.Is(err, gorm.ErrRecordNotFound) || current.Deleting || model.MaterialSourceVersion(&current) != model.MaterialSourceVersion(material):
				issue.Status, issue.Reason = "unavailable", "Material is unavailable or its source changed"
			case binding.State == "ready" && binding.UpstreamStatus == "ACTIVE" && validateMobileCloudMaterialID(binding.AssetID) == nil:
				resolved[i] = "asset://" + binding.AssetID
				continue
			case binding.State == "ready":
				issue.Status, issue.Reason = "unavailable", "Material readiness could not be confirmed; synchronize the existing material before retrying"
			case binding.State == "failed" || binding.State == "uncertain" || binding.State == "deleted" || binding.State == "retired":
				if issue.Reason == "" {
					issue.Reason = "Material is not ready; check the existing material before retrying"
				}
			case ctx.Err() != nil:
				if issue.Reason == "" {
					issue.Reason = "Material preparation wait ended; retry to check the existing material"
				}
			default:
				pending = true
				if issue.Reason == "" {
					issue.Reason = "Material preparation is in progress"
				}
			}
			issues = append(issues, issue)
		}
		if len(issues) == 0 {
			for i, material := range materials {
				var current model.Material
				err := model.DB.WithContext(ctx).First(&current, material.Id).Error
				var binding model.MaterialBinding
				if err == nil {
					err = model.DB.WithContext(ctx).Where("material_id = ? AND scope = ? AND version = ?", material.Id, channel.Scope, model.MaterialSourceVersion(material)).First(&binding).Error
				}
				if err != nil || current.Deleting || model.MaterialSourceVersion(&current) != model.MaterialSourceVersion(material) || binding.State != "ready" || binding.UpstreamStatus != "ACTIVE" || "asset://"+binding.AssetID != resolved[i] {
					input := inputs[i]
					issues = append(issues, MaterialPreparationIssue{Index: input.Index, MaterialID: material.MaterialID, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Material is unavailable or changed during preparation"})
				}
			}
			if len(issues) == 0 {
				return resolved, nil, nil
			}
			return nil, issues, nil
		}
		if !pending || ctx.Err() != nil {
			return nil, issues, nil
		}
		for _, issue := range issues {
			if issue.Status == "failed" || issue.Status == "uncertain" || issue.Status == "unavailable" || issue.Status == "deleted" || issue.Status == "retired" {
				return nil, issues, nil
			}
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func MobileCloudVideoChannelFingerprint(channel *model.Channel) string {
	headers := channel.GetHeaderOverride()
	if len(headers) == 0 {
		headers = nil
	}
	encoded, _ := common.Marshal([]any{channel.Type, strings.TrimRight(channel.GetBaseURL(), "/"), channel.Key, headers})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func VideoSubmissionChannelFingerprint(channel *model.Channel) string {
	encoded, _ := common.Marshal([]any{MobileCloudVideoChannelFingerprint(channel), channel.GetSetting(), channel.GetOtherSettings(), channel.GetParamOverride(), channel.GetModelMapping()})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func VideoMaterialIntegrationFingerprint(channelID int) (string, error) {
	for _, key := range []string{model.TopenRouterMaterialConfigOption, model.MobileCloudMaterialConfigOption} {
		raw, err := model.ReadMaterialIntegrationConfig(key)
		if err != nil {
			return "", err
		}
		if raw == "" {
			continue
		}
		var boundID int
		var enabled *bool
		if key == model.TopenRouterMaterialConfigOption {
			boundID, _, err = topenRouterMaterialConfigBinding(raw)
			if err == nil && (boundID == 0 || boundID == channelID) {
				var config *TopenRouterMaterialConfiguration
				config, err = decodeTopenRouterMaterialConfig(raw)
				if err == nil {
					boundID, enabled = config.ChannelID, config.Enabled
				}
			}
		} else {
			boundID, _, err = mobileCloudMaterialConfigBinding(raw)
			if err == nil && (boundID == 0 || boundID == channelID) {
				var config *MobileCloudMaterialConfiguration
				aead, cipherErr := mobileCloudMaterialCipher(false)
				err = cipherErr
				if err == nil {
					config, err = decodeMobileCloudMaterialConfig(raw, aead)
				}
				if err == nil {
					boundID, enabled = config.ChannelID, config.Enabled
				}
			}
		}
		if err != nil {
			return "", err
		}
		if boundID != channelID {
			continue
		}
		if enabled != nil && !*enabled {
			return "", nil
		}
		sum := sha256.Sum256([]byte(key + "\x00" + raw))
		return hex.EncodeToString(sum[:]), nil
	}
	return "", nil
}

type MaterialReservation struct {
	Deadline time.Time
	Release  func()
}

func PrepareMobileCloudVideoBody(ctx context.Context, userID int, target *model.Channel, body any) (any, []MaterialPreparationIssue, *MaterialReservation, error) {
	raw, err := model.ReadMobileCloudMaterialConfig()
	if err == nil && raw == "" {
		return body, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, ErrMobileCloudMaterialConfig
	}
	channelID, _, err := mobileCloudMaterialConfigBinding(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	// The channel binding is trusted database metadata written only by the dedicated configuration path.
	if channelID > 0 && channelID != target.Id {
		return body, nil, nil, nil
	}
	aead, err := mobileCloudMaterialCipher(false)
	if err != nil {
		return nil, nil, nil, err
	}
	config, err := decodeMobileCloudMaterialConfig(raw, aead)
	if err != nil {
		return nil, nil, nil, err
	}
	if config.ChannelID != target.Id || config.Enabled != nil && !*config.Enabled {
		return body, nil, nil, nil
	}
	if config.ChannelID != target.Id || target.ChannelInfo.IsMultiKey || config.ChannelFingerprint == "" || config.ChannelFingerprint != MobileCloudVideoChannelFingerprint(target) {
		return nil, nil, nil, ErrMobileCloudMaterialConfig
	}
	var channel model.Channel
	if err := model.DB.WithContext(ctx).Where("id = ? AND status = ?", target.Id, common.ChannelStatusEnabled).First(&channel).Error; err != nil || channel.ChannelInfo.IsMultiKey || config.ChannelFingerprint != MobileCloudVideoChannelFingerprint(&channel) {
		return nil, nil, nil, ErrMobileCloudMaterialConfig
	}
	channels, err := materialChannelsForConfig(model.DB.WithContext(ctx), config)
	if err != nil {
		return nil, nil, nil, err
	}
	prepared, issues, reservation, err := prepareVideoMaterialBody(ctx, userID, channels[0], body)
	if err != nil || len(issues) > 0 || reservation == nil {
		return prepared, issues, reservation, err
	}
	current, err := model.ReadMobileCloudMaterialConfig()
	_, channelErr := materialChannelsForConfig(model.DB.WithContext(ctx), config)
	if err != nil || current != raw || channelErr != nil {
		reservation.Release()
		return nil, nil, nil, ErrMobileCloudMaterialConfig
	}
	return prepared, issues, reservation, nil
}

func PrepareVideoMaterialRequest(ctx context.Context, userID int, target *model.Channel, body any) (any, []MaterialPreparationIssue, *MaterialReservation, error) {
	raw, err := model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	if err != nil {
		return nil, nil, nil, ErrTopenRouterMaterialConfig
	}
	if raw != "" {
		channelID, _, err := topenRouterMaterialConfigBinding(raw)
		if err != nil {
			return nil, nil, nil, err
		}
		if channelID == 0 {
			config, err := decodeTopenRouterMaterialConfig(raw)
			if err != nil {
				return nil, nil, nil, err
			}
			channelID = config.ChannelID
		}
		if channelID == target.Id {
			return prepareTopenRouterVideoBody(ctx, userID, target, body, raw)
		}
	}
	return PrepareMobileCloudVideoBody(ctx, userID, target, body)
}

func PrepareTopenRouterVideoBody(ctx context.Context, userID int, target *model.Channel, body any) (any, []MaterialPreparationIssue, *MaterialReservation, error) {
	raw, err := model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	if err == nil && raw == "" {
		return body, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, ErrTopenRouterMaterialConfig
	}
	return prepareTopenRouterVideoBody(ctx, userID, target, body, raw)
}

func prepareTopenRouterVideoBody(ctx context.Context, userID int, target *model.Channel, body any, raw string) (any, []MaterialPreparationIssue, *MaterialReservation, error) {
	channelID, _, err := topenRouterMaterialConfigBinding(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	if channelID > 0 && channelID != target.Id {
		return body, nil, nil, nil
	}
	config, err := decodeTopenRouterMaterialConfig(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	if config.ChannelID != target.Id || config.Enabled != nil && !*config.Enabled {
		return body, nil, nil, nil
	}
	if target.ChannelInfo.IsMultiKey || config.ChannelFingerprint == "" || config.ChannelFingerprint != MobileCloudVideoChannelFingerprint(target) {
		return nil, nil, nil, ErrTopenRouterMaterialConfig
	}
	channel, err := topenRouterMaterialChannel(model.DB.WithContext(ctx), config)
	if err != nil {
		return nil, nil, nil, err
	}
	prepared, issues, reservation, err := prepareVideoMaterialBody(ctx, userID, channel, body)
	if err != nil || len(issues) > 0 || reservation == nil {
		return prepared, issues, reservation, err
	}
	current, err := model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	_, channelErr := topenRouterMaterialChannel(model.DB.WithContext(ctx), config)
	if err != nil || current != raw || channelErr != nil {
		reservation.Release()
		return nil, nil, nil, ErrTopenRouterMaterialConfig
	}
	return prepared, nil, reservation, nil
}

func videoMaterialInputs(body any) (map[string]any, []MaterialImageInput, error) {
	data, err := common.Marshal(body)
	if err != nil {
		return nil, nil, errors.New("video request must be a JSON object")
	}
	var object map[string]any
	if common.Unmarshal(data, &object) != nil || object == nil {
		return nil, nil, errors.New("video request must be a JSON object")
	}
	content, ok := object["content"].([]any)
	if !ok {
		return nil, nil, errors.New("configured material channel requires a video content array")
	}
	inputs := make([]MaterialImageInput, 0)
	for i, value := range content {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, nil, errors.New("invalid video content item")
		}
		if item["type"] != "image_url" {
			continue
		}
		image, ok := item["image_url"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("content[%d].image_url must contain a URL", i)
		}
		source, ok := image["url"].(string)
		if !ok || strings.TrimSpace(source) == "" {
			return nil, nil, fmt.Errorf("content[%d].image_url.url is required", i)
		}
		usage, _ := item["role"].(string)
		inputs = append(inputs, MaterialImageInput{Index: i, Source: source, Field: fmt.Sprintf("content[%d].image_url.url", i), Usage: usage})
	}
	return object, inputs, nil
}

func ValidateVideoMaterialBody(body any) error {
	_, inputs, err := videoMaterialInputs(body)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		source := strings.TrimSpace(input.Source)
		if id, asset := strings.CutPrefix(source, "asset://"); asset {
			if validateMobileCloudMaterialID(id) != nil {
				return fmt.Errorf("%s contains an invalid material reference", input.Field)
			}
			continue
		}
		if ValidateMaterialPreparationSource(source) != nil {
			return fmt.Errorf("%s must be a public HTTPS image URL", input.Field)
		}
	}
	return nil
}

func prepareVideoMaterialBody(ctx context.Context, userID int, channel MaterialChannel, body any) (any, []MaterialPreparationIssue, *MaterialReservation, error) {
	object, inputs, err := videoMaterialInputs(body)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(inputs) == 0 {
		return body, nil, nil, nil
	}
	content := object["content"].([]any)
	resolved, issues, err := PrepareVideoMaterials(ctx, userID, channel, inputs)
	if err != nil || len(issues) > 0 {
		return nil, issues, nil, err
	}
	assetIDs := make([]string, len(resolved))
	for i, reference := range resolved {
		assetIDs[i] = strings.TrimPrefix(reference, "asset://")
	}
	owner := common.GetUUID()
	db := model.DB
	deadline, err := model.ReserveVideoMaterials(db.WithContext(ctx), userID, channel.Scope, owner, assetIDs)
	if err != nil {
		for i, input := range inputs {
			issue := MaterialPreparationIssue{Index: input.Index, imageNumber: i + 1, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Material is unavailable or in use; retry after the current operation finishes"}
			var material model.Material
			bindings := db.Model(&model.MaterialBinding{}).Select("material_id").Where("scope = ? AND asset_id = ?", channel.Scope, assetIDs[i])
			if lookupErr := db.WithContext(ctx).Where("user_id = ? AND id IN (?)", userID, bindings).Order("id").First(&material).Error; lookupErr == nil {
				issue.MaterialID = material.MaterialID
			}
			issues = append(issues, issue)
		}
		return nil, issues, nil, nil
	}
	for i, input := range inputs {
		content[input.Index].(map[string]any)["image_url"].(map[string]any)["url"] = resolved[i]
	}
	release := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = model.ReleaseVideoMaterials(db.WithContext(cleanupCtx), owner)
	}
	return object, nil, &MaterialReservation{Deadline: deadline, Release: release}, nil
}
