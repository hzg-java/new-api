package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MaterialProvider interface {
	CreateAssetGroup(context.Context, string, *string) (*MobileCloudAssetGroup, error)
	ListAssetGroups(context.Context, MobileCloudGroupQuery) (*MobileCloudMaterialPage[MobileCloudAssetGroup], error)
	CreateAsset(context.Context, MobileCloudCreateAssetInput) (string, error)
	ListAssets(context.Context, MobileCloudAssetQuery) (*MobileCloudMaterialPage[MobileCloudAsset], error)
	GetAsset(context.Context, string) (*MobileCloudAsset, error)
}

func MobileCloudMaterialScope(config *MobileCloudMaterialConfiguration, channelID int) string {
	endpoint := config.Endpoint
	if base, err := url.Parse(endpoint); err == nil {
		base.Host = strings.ToLower(base.Host)
		if base.Scheme == "https" && base.Port() == "443" {
			base.Host = base.Hostname()
			if strings.Contains(base.Host, ":") {
				base.Host = "[" + base.Host + "]"
			}
		}
		base.Path, base.RawPath = mobileCloudMaterialGateway, ""
		endpoint = base.String()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("mobile-cloud\x00%d\x00%s\x00%s\x00%s", channelID, endpoint, config.AccessKey, config.PoolID)))
	return hex.EncodeToString(sum[:])
}

type MaterialReference struct {
	ID    int
	Field string
	Usage string
}

type MaterialPreparationIssue struct {
	Index      int    `json:"index"`
	MaterialID string `json:"material_id"`
	Field      string `json:"field"`
	Usage      string `json:"usage"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
}

func ResolvePreparedMaterials(ctx context.Context, userID int, scope string, references []MaterialReference) ([]string, []MaterialPreparationIssue, error) {
	resolved := make([]string, len(references))
	issues := make([]MaterialPreparationIssue, 0)
	for i, reference := range references {
		var material model.Material
		err := model.DB.WithContext(ctx).Where("id = ? AND user_id = ? AND library_type = ? AND file_type = ?", reference.ID, userID, model.MaterialLibraryAIGC, model.MaterialFileImage).First(&material).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		issue := MaterialPreparationIssue{Index: i, Field: reference.Field, Usage: reference.Usage, MaterialID: material.MaterialID, Status: "pending", Reason: "Material is not prepared in the selected resource space"}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			issue.Status, issue.Reason = "unavailable", "Material not found"
			issues = append(issues, issue)
			continue
		}
		if material.Deleting {
			issue.Status, issue.Reason = "unavailable", "Material deletion is in progress"
			issues = append(issues, issue)
			continue
		}
		var binding model.MaterialBinding
		err = model.DB.WithContext(ctx).Where("material_id = ? AND scope = ? AND version = ?", material.Id, scope, model.MaterialSourceVersion(&material)).First(&binding).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		if err == nil && binding.State == "ready" && validateMobileCloudMaterialID(binding.AssetID) == nil {
			resolved[i] = "asset://" + binding.AssetID
			continue
		}
		if err == nil {
			issue.Status = binding.State
			if binding.FailReason != "" {
				issue.Reason = binding.FailReason
			}
		}
		issues = append(issues, issue)
	}
	if len(issues) != 0 {
		return nil, issues, nil
	}
	return resolved, issues, nil
}

func PrepareMaterial(ctx context.Context, material *model.Material, scope string, provider MaterialProvider) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Second)
	defer persistCancel()
	db := model.DB.WithContext(persistCtx)
	owner := common.GetUUID()
	binding, claimed, err := model.ClaimMaterialBinding(db, material, scope, owner)
	if err != nil || !claimed {
		return err
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		model.DB.WithContext(releaseCtx).Model(&model.MaterialBinding{}).Where("id = ? AND locked_by = ?", binding.Id, owner).Updates(map[string]any{"locked_by": "", "locked_until": 0})
	}()
	var current model.Material
	if err := db.First(&current, material.Id).Error; err != nil {
		return err
	}
	if current.Deleting {
		return nil
	}
	if material.LibraryType != model.MaterialLibraryAIGC || material.FileType != model.MaterialFileImage || ValidateMaterialPreparationSource(material.SourceURL) != nil {
		return finishMaterialPreparation(db, material, binding, owner, "failed", "Only public HTTPS AIGC images can be prepared", false)
	}
	if material.Status == model.MaterialStatusPending && binding.State != "pending" && binding.State != "ready" {
		binding.StartedAt = common.GetTimestamp()
	}
	if binding.State == "ready" {
		return aggregateMaterialPreparation(db, material)
	}
	if binding.State == "failed" {
		if material.Status != model.MaterialStatusPending {
			return finishMaterialPreparation(db, material, binding, owner, binding.State, binding.FailReason, binding.CanRetry)
		}
		if err := model.ResetFailedMaterialBinding(db, binding, owner); err != nil {
			return err
		}
	}
	if material.GroupId != nil && binding.AssetID == "" && (binding.GroupID == "" || binding.State == "pending") {
		var group model.MaterialGroup
		if err := db.Where("id = ? AND user_id = ?", *material.GroupId, material.UserId).First(&group).Error; err != nil {
			return finishMaterialPreparation(db, material, binding, owner, "failed", "Material group is unavailable", true)
		}
		var mapping model.MaterialGroupBinding
		err := db.Where("group_id = ? AND scope = ?", group.Id, scope).First(&mapping).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if group.Deleting || err != nil || mapping.State != "ready" || mapping.Name != group.Name || mapping.FailReason != "" || validateMobileCloudMaterialID(mapping.UpstreamID) != nil {
			return finishMaterialPreparation(db, material, binding, owner, "failed", "Material group is not ready in this channel; synchronize the group and retry", true)
		}
		if binding.GroupID != "" && binding.GroupID != mapping.UpstreamID && binding.State != "pending" {
			return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material group changed while an upload result was unknown; restore the previous group to recover", false)
		}
		binding.GroupID = mapping.UpstreamID
		if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
			return err
		}
	}
	if binding.GroupID == "" {
		if binding.GroupName == "" {
			var legacy []model.MaterialUpstreamGroup
			if err := db.Where("user_id = ? AND scope = ? AND state = ? AND name LIKE ?", material.UserId, scope, "creating", "aigc-%").Find(&legacy).Error; err != nil {
				return err
			}
			if len(legacy) > 1 {
				return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Multiple pending material groups require administrator review", false)
			}
			if len(legacy) == 1 {
				binding.GroupName = legacy[0].Name
			}
			if binding.GroupName == "" {
				started := binding.StartedAt
				if started == 0 {
					started = common.GetTimestamp()
				}
				binding.GroupName = "group-" + time.Unix(started, 0).In(mobileCloudMaterialTimeZone).Format("20060102")
			}
			if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
				return err
			}
		}
		group := model.MaterialUpstreamGroup{UserId: material.UserId, Scope: scope, Name: binding.GroupName, CreationToken: "new-api-material-group:" + common.GetUUID(), State: "pending"}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&group).Error; err != nil {
			return err
		}
		group = model.MaterialUpstreamGroup{}
		if err := db.Where("user_id = ? AND scope = ? AND name = ?", material.UserId, scope, binding.GroupName).First(&group).Error; err != nil {
			return err
		}
		if group.State == "pending" {
			if group.CreationToken == "" {
				group.CreationToken = "new-api-material-group:" + common.GetUUID()
			}
			result := db.Model(&model.MaterialUpstreamGroup{}).Where("id = ? AND state = ?", group.Id, "pending").Updates(map[string]any{"state": "creating", "creation_token": group.CreationToken})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				created, createErr := provider.CreateAssetGroup(ctx, group.Name, &group.CreationToken)
				if createErr != nil {
					if _, rejected := materialRejectionMessage(createErr); rejected {
						if err := db.Model(&group).Update("state", "pending").Error; err != nil {
							return err
						}
						return finishMaterialPreparation(db, material, binding, owner, "failed", materialOperationFailure("Material preparation rejected", createErr), true)
					}
					return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material group creation result is unknown; retry to check the existing result", true)
				}
				group.UpstreamID, group.State = created.ID, "ready"
				if err := db.Model(&group).Updates(map[string]any{"upstream_id": group.UpstreamID, "state": group.State}).Error; err != nil {
					return err
				}
			}
		}
		if group.State == "creating" {
			items, queryErr := listMaterialGroups(ctx, provider, MobileCloudGroupQuery{GroupType: common.GetPointer("AIGC"), Name: &group.Name})
			if queryErr != nil {
				return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material group result could not be confirmed; retry to check without creating again", true)
			}
			for _, candidate := range items {
				if candidate.Name == group.Name && candidate.Type == "AIGC" && (group.CreationToken != "" && candidate.Description == group.CreationToken || group.CreationToken == "" && strings.HasPrefix(group.Name, "aigc-")) {
					if group.UpstreamID != "" {
						return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Multiple matching material groups require administrator review", false)
					}
					group.UpstreamID = candidate.ID
				}
			}
			if group.UpstreamID == "" {
				return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material group creation result is unknown; retry to check the existing result", true)
			}
			if err := db.Model(&group).Updates(map[string]any{"upstream_id": group.UpstreamID, "state": "ready"}).Error; err != nil {
				return err
			}
		}
		if group.UpstreamID == "" {
			return nil
		}
		binding.GroupID = group.UpstreamID
		if binding.State == "uncertain" {
			binding.State = "pending"
		}
		if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
			return err
		}
	}
	if binding.AssetID == "" && binding.State == "pending" {
		binding.State = "creating"
		if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
			return err
		}
		id, createErr := provider.CreateAsset(ctx, MobileCloudCreateAssetInput{GroupID: binding.GroupID, Name: binding.Name, URL: material.SourceURL, Type: "Image"})
		if createErr != nil {
			if _, rejected := materialRejectionMessage(createErr); rejected {
				return finishMaterialPreparation(db, material, binding, owner, "failed", materialOperationFailure("Material preparation rejected", createErr), true)
			}
			return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material upload result is unknown; retry to check without uploading again", true)
		}
		binding.AssetID, binding.State = id, "processing"
		if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
			return err
		}
	}
	if binding.AssetID == "" {
		items, queryErr := listMaterialAssets(ctx, provider, MobileCloudAssetQuery{GroupType: common.GetPointer("AIGC"), GroupIDs: []string{binding.GroupID}, Name: &binding.Name})
		if queryErr != nil {
			return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material upload result could not be confirmed; retry to check without uploading again", true)
		}
		for _, candidate := range items {
			if candidate.Name == binding.Name && candidate.GroupID == binding.GroupID && candidate.Type == "Image" {
				if binding.AssetID != "" {
					binding.AssetID = ""
					return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Multiple matching materials require administrator review", false)
				}
				binding.AssetID = candidate.ID
			}
		}
		if binding.AssetID == "" {
			return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material upload result is unknown; retry to check without uploading again", true)
		}
		binding.State = "processing"
		if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
			return err
		}
	}
	asset, err := provider.GetAsset(ctx, binding.AssetID)
	if err != nil {
		if common.GetTimestamp()-binding.StartedAt > 600 {
			return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material preparation timed out; retry to check the existing material", true)
		}
		return finishMaterialPreparation(db, material, binding, owner, "processing", "", false)
	}
	if asset.ID != binding.AssetID || asset.GroupID != binding.GroupID || asset.Type != "Image" {
		return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material response does not match the requested material", false)
	}
	binding.UpstreamStatus = asset.Status
	switch asset.Status {
	case "ACTIVE":
		return finishMaterialPreparation(db, material, binding, owner, "ready", "", false)
	case "FAILED":
		reason := "Upstream material processing failed"
		if asset.ErrorMessage != "" {
			reason += ": " + types.NewError(errors.New(reason), types.ErrorCodeBadResponse,
				types.ErrOptionWithUpstreamResponse(types.OpenAIError{Message: asset.ErrorMessage})).ToOpenAIRelayError().Message
		}
		return finishMaterialPreparation(db, material, binding, owner, "failed", reason, true)
	case "PROCESSING":
		if common.GetTimestamp()-binding.StartedAt > 600 {
			return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Material preparation timed out; retry to check the existing material", true)
		}
		return finishMaterialPreparation(db, material, binding, owner, "processing", "", false)
	default:
		return finishMaterialPreparation(db, material, binding, owner, "uncertain", "Unrecognized material status; retry to check the existing material", true)
	}
}

func finishMaterialPreparation(db *gorm.DB, material *model.Material, binding *model.MaterialBinding, owner, state, reason string, retry bool) error {
	binding.State, binding.FailReason, binding.CanRetry = state, reason, retry
	if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
		return err
	}
	return aggregateMaterialPreparation(db, material)
}

func aggregateMaterialPreparation(db *gorm.DB, material *model.Material) error {
	return model.AggregateMaterialPreparation(db, material)
}

func MaterialPreparationQueue(db *gorm.DB, scopes []string) *gorm.DB {
	bound := db.Model(&model.MaterialBinding{}).Select("material_id").Where("state NOT IN ?", []string{"deleted", "retired"})
	inFlight := db.Model(&model.MaterialBinding{}).Select("material_id").Where("scope IN ? AND state IN ?", scopes, []string{"pending", "creating", "processing"})
	return db.Model(&model.Material{}).Where("deleting = ? OR id IN (?)", true, bound).
		Where("status IN ? OR (status = ? AND deleting = ? AND id IN (?))", []string{model.MaterialStatusPending, model.MaterialStatusProcessing}, model.MaterialStatusFailed, false, inFlight)
}

func materialGroupPreparationQueue(db *gorm.DB, scopes []string) *gorm.DB {
	boundGroups := db.Model(&model.MaterialGroupBinding{}).Select("group_id").Where("state <> ?", "deleted")
	boundMaterials := db.Model(&model.MaterialBinding{}).Select("material_id").Where("state NOT IN ?", []string{"deleted", "retired"})
	materialGroups := db.Model(&model.Material{}).Select("group_id").Where("group_id IS NOT NULL AND id IN (?)", boundMaterials)
	inFlight := db.Model(&model.MaterialGroupBinding{}).Select("group_id").Where("scope IN ? AND state IN ?", scopes, []string{"pending", "creating", "updating"})
	return db.Model(&model.MaterialGroup{}).Where("deleting = ? OR id IN (?) OR id IN (?)", true, boundGroups, materialGroups).
		Where("status IN ? OR (status = ? AND deleting = ? AND id IN (?))", []string{model.MaterialStatusPending, model.MaterialStatusProcessing}, model.MaterialStatusFailed, false, inFlight)
}

func RunMaterialPreparationOnce(ctx context.Context) error {
	db := model.DB.WithContext(ctx)
	if err := model.ValidateMobileCloudMaterialTestDatabase(db); err != nil {
		return err
	}
	channels, loadErr := LoadMaterialChannels()
	var failures []error
	if loadErr != nil {
		failures = append(failures, loadErr)
	}
	availableScopes := make([]string, 0, len(channels))
	for _, channel := range channels {
		if channel.Client != nil {
			availableScopes = append(availableScopes, channel.Scope)
		}
	}
	var groups []model.MaterialGroup
	if err := materialGroupPreparationQueue(db, availableScopes).Order("updated_time, id").Limit(100).Find(&groups).Error; err != nil {
		return err
	}
	for i := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		var scopes []string
		if err := db.Model(&model.MaterialGroupBinding{}).Where("group_id = ? AND state <> ?", groups[i].Id, "deleted").Pluck("scope", &scopes).Error; err != nil {
			return err
		}
		materials := db.Model(&model.Material{}).Select("id").Where("group_id = ?", groups[i].Id)
		var materialScopes []string
		if err := db.Model(&model.MaterialBinding{}).Where("material_id IN (?) AND state NOT IN ?", materials, []string{"deleted", "retired"}).Pluck("scope", &materialScopes).Error; err != nil {
			return err
		}
		scopes = append(scopes, materialScopes...)
		targets := make([]MaterialChannel, 0, len(channels))
		for _, channel := range channels {
			if slices.Contains(scopes, channel.Scope) {
				targets = append(targets, channel)
			}
		}
		if len(scopes) == 0 && !groups[i].Deleting {
			continue
		}
		if err := SyncMaterialGroup(ctx, &groups[i], targets); err != nil {
			failures = append(failures, err)
		}
	}
	var materials []model.Material
	if err := MaterialPreparationQueue(db, availableScopes).Where("library_type = ?", "AIGC").Order("updated_time, id").Limit(100).Find(&materials).Error; err != nil {
		return err
	}
	for i := range materials {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := SyncMaterial(ctx, &materials[i], channels); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

type MaterialPreparationHandler struct{}

func (MaterialPreparationHandler) Type() string            { return "material_preparation" }
func (MaterialPreparationHandler) Interval() time.Duration { return 15 * time.Second }
func (MaterialPreparationHandler) NewPayload() any         { return nil }
func (MaterialPreparationHandler) Enabled() bool {
	if model.ValidateMobileCloudMaterialTestDatabase(model.DB) != nil {
		return false
	}
	var count int64
	if materialGroupPreparationQueue(model.DB, nil).Limit(1).Count(&count).Error == nil && count > 0 {
		return true
	}
	if MaterialPreparationQueue(model.DB, nil).Limit(1).Count(&count).Error == nil && count > 0 {
		return true
	}
	channels, _ := LoadMaterialChannels()
	availableScopes := make([]string, 0, len(channels))
	for _, channel := range channels {
		if channel.Client != nil {
			availableScopes = append(availableScopes, channel.Scope)
		}
	}
	if len(availableScopes) == 0 {
		return false
	}
	inFlightGroups := model.DB.Model(&model.MaterialGroupBinding{}).Select("group_id").Where("scope IN ? AND state IN ?", availableScopes, []string{"pending", "creating", "updating"})
	if model.DB.Model(&model.MaterialGroup{}).Where("status = ? AND deleting = ? AND id IN (?)", model.MaterialStatusFailed, false, inFlightGroups).Limit(1).Count(&count).Error == nil && count > 0 {
		return true
	}
	return MaterialPreparationQueue(model.DB, availableScopes).Limit(1).Count(&count).Error == nil && count > 0
}
func (MaterialPreparationHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	err := RunMaterialPreparationOnce(ctx)
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, message = model.SystemTaskStatusFailed, "Material preparation could not run"
	}
	_ = model.FinishSystemTask(task.TaskID, runnerID, status, nil, message)
}

func ValidateMaterialPreparationSource(source string) error {
	target, err := url.Parse(source)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || strings.Contains(source, "#") || target.Opaque != "" {
		return errors.New("material source must be a public HTTPS image URL")
	}
	protection := common.SSRFProtection{ApplyIPFilterForDomain: true}
	if err := protection.ValidateURL(source); err != nil {
		return errors.New("material source must be a public HTTPS image URL")
	}
	return ValidateSSRFProtectedFetchURL(source)
}
