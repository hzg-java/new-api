package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type MaterialImageInput struct {
	Index  int
	Source string
	Field  string
	Usage  string
}

func PrepareVideoMaterials(ctx context.Context, userID int, channel MaterialChannel, inputs []MaterialImageInput) ([]string, []MaterialPreparationIssue, error) {
	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	materials := make([]*model.Material, len(inputs))
	issues := make([]MaterialPreparationIssue, 0)
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
	resolved := make([]string, len(inputs))
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
			case ctx.Err() != nil:
				issue.Reason = "Material preparation wait ended; retry to check the existing material"
			case binding.State == "ready" && binding.UpstreamStatus == "ACTIVE" && validateMobileCloudMaterialID(binding.AssetID) == nil:
				resolved[i] = "asset://" + binding.AssetID
				continue
			case binding.State == "ready":
				issue.Status, issue.Reason = "unavailable", "Material readiness could not be confirmed; synchronize the existing material before retrying"
			case binding.State == "failed" || binding.State == "uncertain" || binding.State == "deleted" || binding.State == "retired":
				if issue.Reason == "" {
					issue.Reason = "Material is not ready; check the existing material before retrying"
				}
			default:
				pending = true
				issue.Reason = "Material preparation is in progress"
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
	channels, err := materialChannelsForConfig(config)
	if err != nil {
		return nil, nil, nil, err
	}
	prepared, issues, reservation, err := prepareVideoMaterialBody(ctx, userID, channels[0], body)
	if err != nil || len(issues) > 0 || reservation == nil {
		return prepared, issues, reservation, err
	}
	current, err := model.ReadMobileCloudMaterialConfig()
	_, channelErr := materialChannelsForConfig(config)
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
	channel, err := topenRouterMaterialChannel(config)
	if err != nil {
		return nil, nil, nil, err
	}
	prepared, issues, reservation, err := prepareVideoMaterialBody(ctx, userID, channel, body)
	if err != nil || len(issues) > 0 || reservation == nil {
		return prepared, issues, reservation, err
	}
	current, err := model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	_, channelErr := topenRouterMaterialChannel(config)
	if err != nil || current != raw || channelErr != nil {
		reservation.Release()
		return nil, nil, nil, ErrTopenRouterMaterialConfig
	}
	return prepared, nil, reservation, nil
}

func prepareVideoMaterialBody(ctx context.Context, userID int, channel MaterialChannel, body any) (any, []MaterialPreparationIssue, *MaterialReservation, error) {
	data, err := common.Marshal(body)
	if err != nil {
		return nil, nil, nil, errors.New("video request must be a JSON object")
	}
	var object map[string]any
	if common.Unmarshal(data, &object) != nil || object == nil {
		return nil, nil, nil, errors.New("video request must be a JSON object")
	}
	content, ok := object["content"].([]any)
	if !ok {
		return nil, nil, nil, errors.New("configured material channel requires a video content array")
	}
	inputs := make([]MaterialImageInput, 0)
	for i, value := range content {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, nil, nil, errors.New("invalid video content item")
		}
		if item["type"] != "image_url" {
			continue
		}
		image, ok := item["image_url"].(map[string]any)
		if !ok {
			return nil, nil, nil, fmt.Errorf("content[%d].image_url must contain a URL", i)
		}
		source, ok := image["url"].(string)
		if !ok || strings.TrimSpace(source) == "" {
			return nil, nil, nil, fmt.Errorf("content[%d].image_url.url is required", i)
		}
		usage, _ := item["role"].(string)
		inputs = append(inputs, MaterialImageInput{Index: i, Source: source, Field: fmt.Sprintf("content[%d].image_url.url", i), Usage: usage})
	}
	if len(inputs) == 0 {
		return body, nil, nil, nil
	}
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
			issue := MaterialPreparationIssue{Index: input.Index, Field: input.Field, Usage: input.Usage, Status: "unavailable", Reason: "Material is unavailable or in use; retry after the current operation finishes"}
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
