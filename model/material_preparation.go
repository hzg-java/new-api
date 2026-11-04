package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MaterialUpstreamGroup struct {
	Id            int    `gorm:"primaryKey"`
	UserId        int    `gorm:"uniqueIndex:idx_material_group_scope_name"`
	Scope         string `gorm:"type:varchar(64);uniqueIndex:idx_material_group_scope_name"`
	Name          string `gorm:"type:varchar(64);uniqueIndex:idx_material_group_scope_name"`
	CreationToken string `gorm:"type:varchar(64)"`
	UpstreamID    string `gorm:"type:varchar(255)"`
	State         string `gorm:"type:varchar(32)"`
}

type MaterialBinding struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	MaterialId     int    `json:"-" gorm:"uniqueIndex:idx_material_binding_scope"`
	Scope          string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_material_binding_scope"`
	Version        string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_material_binding_scope"`
	ChannelID      int    `json:"channel_id"`
	ChannelName    string `json:"channel_name" gorm:"type:varchar(255)"`
	Provider       string `json:"provider" gorm:"type:varchar(64)"`
	Name           string `json:"-" gorm:"type:varchar(64)"`
	GroupName      string `json:"-" gorm:"type:varchar(64)"`
	GroupID        string `json:"group_id" gorm:"type:varchar(255)"`
	AssetID        string `json:"asset_id" gorm:"type:varchar(255)"`
	State          string `json:"state" gorm:"type:varchar(32);index"`
	UpstreamStatus string `json:"upstream_status" gorm:"type:varchar(64)"`
	FailReason     string `json:"fail_reason" gorm:"type:text"`
	CanRetry       bool   `json:"can_retry"`
	UpdatedTime    int64  `json:"updated_time"`
	StartedAt      int64  `json:"-"`
	LockedBy       string `json:"-" gorm:"type:varchar(64)"`
	LockedUntil    int64  `json:"-"`
}

type MaterialGroupBinding struct {
	Id            int    `json:"id" gorm:"primaryKey"`
	GroupId       int    `json:"-" gorm:"uniqueIndex:idx_material_explicit_group_scope"`
	UserId        int    `json:"-"`
	Scope         string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_material_explicit_group_scope"`
	ChannelID     int    `json:"channel_id"`
	ChannelName   string `json:"channel_name" gorm:"type:varchar(255)"`
	Provider      string `json:"provider" gorm:"type:varchar(64)"`
	Name          string `json:"name" gorm:"type:varchar(64)"`
	CreationToken string `json:"-" gorm:"type:varchar(64)"`
	UpstreamID    string `json:"upstream_id" gorm:"type:varchar(255)"`
	State         string `json:"state" gorm:"type:varchar(32);index"`
	FailReason    string `json:"fail_reason" gorm:"type:text"`
	UpdatedTime   int64  `json:"updated_time"`
	LockedBy      string `json:"-" gorm:"type:varchar(64)"`
	LockedUntil   int64  `json:"-"`
}

func MaterialSourceVersion(material *Material) string {
	sum := sha256.Sum256([]byte(material.FileType + "\x00" + material.SourceURL))
	return hex.EncodeToString(sum[:])
}

func ClaimMaterialBinding(ctxDB *gorm.DB, material *Material, scope, owner string) (*MaterialBinding, bool, error) {
	binding := &MaterialBinding{MaterialId: material.Id, Scope: scope, Version: MaterialSourceVersion(material), Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: common.GetTimestamp()}
	claimed := false
	err := ctxDB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, material.UserId).Error; err != nil {
			return err
		}
		var current Material
		if err := lockForUpdate(tx).First(&current, material.Id).Error; err != nil {
			return err
		}
		if current.Deleting || MaterialSourceVersion(&current) != binding.Version {
			return nil
		}
		material.GroupId = current.GroupId
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(binding).Error; err != nil {
			return err
		}
		if err := tx.Where("material_id = ? AND scope = ? AND version = ?", material.Id, scope, binding.Version).First(binding).Error; err != nil {
			return err
		}
		if binding.State == "deleted" || binding.State == "retired" {
			return nil
		}
		if binding.AssetID == "" && binding.State == "pending" {
			duplicates := tx.Model(&Material{}).Select("id").Where("user_id = ? AND source_url = ? AND library_type = ? AND file_type = ? AND id <> ?", current.UserId, current.SourceURL, current.LibraryType, current.FileType, current.Id)
			var existing int64
			if err := tx.Model(&MaterialBinding{}).Where("material_id IN (?) AND scope = ? AND version = ? AND state NOT IN ? AND (asset_id <> '' OR state IN ? OR locked_until >= ?)", duplicates, scope, binding.Version, []string{"deleted", "retired"}, []string{"creating", "processing", "uncertain"}, common.GetTimestamp()).Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				return errors.New("another library record owns this material upload; retry using the existing material")
			}
		}
		now := common.GetTimestamp()
		result := tx.Model(&MaterialBinding{}).Where("id = ? AND (locked_until IS NULL OR locked_until < ?)", binding.Id, now).
			Updates(map[string]any{"locked_by": owner, "locked_until": now + 120})
		claimed = result.RowsAffected == 1
		return result.Error
	})
	return binding, claimed, err
}

func ReserveVideoMaterials(db *gorm.DB, userID int, scope, owner string, assetIDs []string) (time.Time, error) {
	assetIDs = slices.Clone(assetIDs)
	slices.Sort(assetIDs)
	assetIDs = slices.Compact(assetIDs)
	var deadline time.Time
	err := db.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, userID).Error; err != nil {
			return err
		}
		bindings := make([]MaterialBinding, 0, len(assetIDs))
		for _, assetID := range assetIDs {
			var material Material
			matching := tx.Model(&MaterialBinding{}).Select("material_id").Where("scope = ? AND asset_id = ? AND state = ?", scope, assetID, "ready")
			if err := lockForUpdate(tx).Where("user_id = ? AND deleting = ? AND id IN (?)", userID, false, matching).Order("id").First(&material).Error; err != nil {
				return err
			}
			var binding MaterialBinding
			if err := lockForUpdate(tx).Where("material_id = ? AND scope = ? AND version = ? AND asset_id = ? AND state = ? AND upstream_status = ?", material.Id, scope, MaterialSourceVersion(&material), assetID, "ready", "ACTIVE").First(&binding).Error; err != nil {
				return err
			}
			bindings = append(bindings, binding)
		}
		now := common.GetTimestamp()
		deadline = time.Unix(now+90, 0)
		for _, binding := range bindings {
			result := tx.Model(&MaterialBinding{}).Where("id = ? AND (locked_until IS NULL OR locked_until < ? OR locked_by = ?)", binding.Id, now, owner).
				Updates(map[string]any{"locked_by": owner, "locked_until": now + 120})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("material is unavailable or in use; retry after the current operation finishes")
			}
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return deadline, nil
}

func ClaimMaterialDeletionBinding(db *gorm.DB, binding *MaterialBinding, owner string) (bool, error) {
	claimed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var material Material
		if err := lockForUpdate(tx).First(&material, binding.MaterialId).Error; err != nil {
			return err
		}
		if !material.Deleting {
			return errors.New("material is not marked for deletion")
		}
		now := common.GetTimestamp()
		result := tx.Model(&MaterialBinding{}).Where("id = ? AND (locked_until IS NULL OR locked_until < ?)", binding.Id, now).
			Updates(map[string]any{"locked_by": owner, "locked_until": now + 120})
		claimed = result.RowsAffected == 1
		if result.Error != nil || !claimed {
			return result.Error
		}
		return tx.First(binding, binding.Id).Error
	})
	return claimed, err
}

func ClaimMaterialGroupBinding(db *gorm.DB, binding *MaterialGroupBinding, owner string) (bool, error) {
	claimed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var group MaterialGroup
		if err := lockForUpdate(tx).First(&group, binding.GroupId).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		result := tx.Model(&MaterialGroupBinding{}).Where("id = ? AND (locked_until IS NULL OR locked_until < ?)", binding.Id, now).
			Updates(map[string]any{"locked_by": owner, "locked_until": now + 120})
		claimed = result.RowsAffected == 1
		if result.Error != nil || !claimed {
			return result.Error
		}
		return tx.First(binding, binding.Id).Error
	})
	return claimed, err
}

func SaveMaterialGroupBinding(db *gorm.DB, binding *MaterialGroupBinding, owner string) error {
	now := common.GetTimestamp()
	result := db.Model(&MaterialGroupBinding{}).Where("id = ? AND locked_by = ? AND locked_until >= ?", binding.Id, owner, now).
		Updates(map[string]any{"name": binding.Name, "creation_token": binding.CreationToken, "upstream_id": binding.UpstreamID, "state": binding.State, "fail_reason": binding.FailReason, "updated_time": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("material group synchronization lease lost")
	}
	return nil
}

func SaveMaterialBinding(db *gorm.DB, binding *MaterialBinding, owner string) error {
	now := common.GetTimestamp()
	result := db.Model(&MaterialBinding{}).Where("id = ? AND locked_by = ? AND locked_until >= ?", binding.Id, owner, now).
		Updates(map[string]any{"name": binding.Name, "group_name": binding.GroupName, "group_id": binding.GroupID, "asset_id": binding.AssetID, "state": binding.State, "fail_reason": binding.FailReason, "can_retry": binding.CanRetry, "started_at": binding.StartedAt, "upstream_status": binding.UpstreamStatus, "updated_time": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := db.Model(&MaterialBinding{}).Where("id = ? AND locked_by = ? AND locked_until >= ?", binding.Id, owner, now).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("material preparation lease lost")
		}
	}
	return nil
}

// ResetFailedMaterialBinding keeps remotely created failed assets reachable for
// eventual deletion without including them in the current source readiness gate.
func ResetFailedMaterialBinding(db *gorm.DB, binding *MaterialBinding, owner string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if binding.AssetID != "" {
			archived := *binding
			sum := sha256.Sum256([]byte(binding.Version + "\x00retired\x00" + binding.GroupID + "\x00" + binding.AssetID))
			archived.Id, archived.Version, archived.State = 0, hex.EncodeToString(sum[:]), "retired"
			archived.LockedBy, archived.LockedUntil = "", 0
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&archived).Error; err != nil {
				return err
			}
		}
		binding.State, binding.AssetID, binding.FailReason, binding.CanRetry = "pending", "", "", false
		binding.UpstreamStatus, binding.Name = "", "aigc-"+common.GetUUID()
		return SaveMaterialBinding(tx, binding, owner)
	})
}

func RetryUserMaterial(userID, id int) (*Material, error) {
	var material Material
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userID).First(&material).Error; err != nil {
			return err
		}
		if material.Deleting || material.Status != MaterialStatusFailed || !material.CanRetry {
			return errors.New("material cannot be retried in its current state")
		}
		return tx.Model(&material).Updates(map[string]any{"status": MaterialStatusPending, "fail_reason": "", "can_retry": false, "updated_time": common.GetTimestamp()}).Error
	})
	return &material, err
}

func EnsureMaterialGroupChannelBinding(db *gorm.DB, binding *MaterialGroupBinding) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var group MaterialGroup
		if err := lockForUpdate(tx).First(&group, binding.GroupId).Error; err != nil {
			return err
		}
		if group.Deleting {
			return nil
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(binding).Error; err != nil {
			return err
		}
		return tx.Model(&MaterialGroupBinding{}).Where("group_id = ? AND scope = ?", binding.GroupId, binding.Scope).
			Updates(map[string]any{"channel_id": binding.ChannelID, "channel_name": binding.ChannelName, "provider": binding.Provider}).Error
	})
}

func EnsureMaterialChannelBindings(db *gorm.DB, material *Material, bindings []MaterialBinding) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var current Material
		if err := lockForUpdate(tx).First(&current, material.Id).Error; err != nil {
			return err
		}
		if current.Deleting {
			return errors.New("material is being deleted")
		}
		for i := range bindings {
			binding := &bindings[i]
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(binding).Error; err != nil {
				return err
			}
			if err := tx.Model(&MaterialBinding{}).Where("material_id = ? AND scope = ? AND version = ?", binding.MaterialId, binding.Scope, binding.Version).
				Updates(map[string]any{"channel_id": binding.ChannelID, "channel_name": binding.ChannelName, "provider": binding.Provider}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Serialize the readiness snapshot with precreation, deletion and competing
// channel completions, so an older partial snapshot cannot overwrite ready.
func AggregateMaterialPreparation(db *gorm.DB, material *Material) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var current Material
		if err := lockForUpdate(tx).First(&current, material.Id).Error; err != nil {
			return err
		}
		if current.Deleting || MaterialSourceVersion(&current) != MaterialSourceVersion(material) {
			return nil
		}
		var bindings []MaterialBinding
		if err := tx.Where("material_id = ? AND version = ? AND state <> ?", material.Id, MaterialSourceVersion(&current), "deleted").Order("id").Find(&bindings).Error; err != nil {
			return err
		}
		status, reason, retry := MaterialStatusReady, "", false
		if len(bindings) == 0 {
			status = MaterialStatusPending
		}
		for _, binding := range bindings {
			if binding.State == "failed" || binding.State == "uncertain" {
				status = MaterialStatusFailed
				retry = retry || binding.CanRetry
				if reason == "" {
					failure := binding.FailReason
					if binding.ChannelID != 0 {
						failure = fmt.Sprintf("Channel %d (%s): %s", binding.ChannelID, binding.ChannelName, failure)
					}
					reason = fmt.Sprintf("Material %s: %s", current.MaterialID, failure)
				}
				continue
			}
			if status != MaterialStatusFailed && binding.State != "ready" {
				status = MaterialStatusProcessing
			}
		}
		return tx.Model(&current).Updates(map[string]any{"status": status, "fail_reason": reason, "can_retry": retry, "updated_time": common.GetTimestamp()}).Error
	})
}

func FinalizeMaterialDeletion(db *gorm.DB, material *Material) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var current Material
		if err := lockForUpdate(tx).First(&current, material.Id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&MaterialBinding{}).Where("material_id = ? AND (state <> ? OR locked_until >= ?)", material.Id, "deleted", common.GetTimestamp()).Count(&count).Error; err != nil {
			return err
		}
		if !current.Deleting || count != 0 {
			return nil
		}
		if err := tx.Where("material_id = ?", material.Id).Delete(&MaterialBinding{}).Error; err != nil {
			return err
		}
		return tx.Delete(&current).Error
	})
}

func FinalizeMaterialGroupDeletion(db *gorm.DB, group *MaterialGroup) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var current MaterialGroup
		if err := lockForUpdate(tx).First(&current, group.Id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&Material{}).Where("group_id = ?", group.Id).Count(&count).Error; err != nil {
			return err
		}
		if !current.Deleting || count != 0 {
			return nil
		}
		if err := tx.Model(&MaterialGroupBinding{}).Where("group_id = ? AND (state <> ? OR locked_until >= ?)", group.Id, "deleted", common.GetTimestamp()).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		if err := tx.Where("group_id = ?", group.Id).Delete(&MaterialGroupBinding{}).Error; err != nil {
			return err
		}
		return tx.Delete(&current).Error
	})
}

func MigrateMaterialPreparation(db *gorm.DB) error {
	if err := db.AutoMigrate(&Material{}, &MaterialGroup{}, &MaterialUpstreamGroup{}, &MaterialBinding{}, &MaterialGroupBinding{}); err != nil {
		return err
	}
	if db.Migrator().HasIndex(&MaterialUpstreamGroup{}, "idx_material_group_scope") {
		if err := db.Migrator().DropIndex(&MaterialUpstreamGroup{}, "idx_material_group_scope"); err != nil {
			return err
		}
	}
	if err := db.Model(&Material{}).Where("deleting IS NULL").Update("deleting", false).Error; err != nil {
		return err
	}
	if err := db.Model(&MaterialGroup{}).Where("deleting IS NULL").Update("deleting", false).Error; err != nil {
		return err
	}
	if err := db.Model(&MaterialGroup{}).Where("status IS NULL OR status = ?", "").Update("status", MaterialStatusPending).Error; err != nil {
		return err
	}
	return db.Model(&Material{}).Where("status = ?", "pending_auth").Update("status", MaterialStatusPending).Error
}
