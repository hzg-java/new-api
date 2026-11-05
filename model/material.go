package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Material library types. AIGC covers virtual materials; LivenessFace covers
// real-person authenticated materials whose upstream flow is not yet verified.
const (
	MaterialLibraryAIGC         = "AIGC"
	MaterialLibraryLivenessFace = "LivenessFace"
)

// Material file types accepted by the upstream material APIs.
const (
	MaterialFileImage = "Image"
	MaterialFileVideo = "Video"
	MaterialFileAudio = "Audio"
)

const (
	MaterialStatusPending    = "pending"
	MaterialStatusProcessing = "processing"
	MaterialStatusReady      = "ready"
	MaterialStatusFailed     = "failed"
)

// Upstream identifiers are stored separately from user-visible material records.
type Material struct {
	Id          int               `json:"id"`
	MaterialID  string            `json:"material_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId      int               `json:"user_id" gorm:"index"`
	Username    string            `json:"username,omitempty" gorm:"-"`
	Name        string            `json:"name" gorm:"type:varchar(255)"`
	GroupId     *int              `json:"group_id" gorm:"index"`
	GroupName   string            `json:"group_name" gorm:"-"`
	LibraryType string            `json:"library_type" gorm:"type:varchar(32);index"`
	FileType    string            `json:"file_type" gorm:"type:varchar(32)"`
	SourceURL   string            `json:"source_url" gorm:"type:text"`
	Status      string            `json:"status" gorm:"type:varchar(32);index"`
	FailReason  string            `json:"fail_reason" gorm:"type:text"`
	CanRetry    bool              `json:"can_retry"`
	Deleting    bool              `json:"deleting"`
	Bindings    []MaterialBinding `json:"bindings,omitempty" gorm:"-"`
	CreatedTime int64             `json:"created_time" gorm:"bigint"`
	UpdatedTime int64             `json:"updated_time" gorm:"bigint"`
}

// NewPlatformMaterialID returns an opaque platform material identifier that is
// distinct from any upstream material ID.
func NewPlatformMaterialID() string {
	return "mat_" + common.GetUUID()
}

func IsValidMaterialLibraryType(libraryType string) bool {
	return libraryType == MaterialLibraryAIGC || libraryType == MaterialLibraryLivenessFace
}

func IsValidMaterialFileType(fileType string) bool {
	return fileType == MaterialFileImage || fileType == MaterialFileVideo || fileType == MaterialFileAudio
}

func CreateMaterial(material *Material) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		if material.GroupId != nil {
			var group MaterialGroup
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", *material.GroupId, material.UserId).First(&group).Error; err != nil {
				return errors.New("material group not found")
			}
			if group.Deleting {
				return errors.New("material group deletion is in progress")
			}
		}
		return tx.Create(material).Error
	})
	if err != nil {
		return err
	}
	return PopulateMaterialGroupNames([]*Material{material})
}

func FindOrCreateVideoMaterial(db *gorm.DB, userID int, source, scope string) (*Material, error) {
	var material Material
	err := db.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, userID).Error; err != nil {
			return err
		}
		var existing []Material
		bindings := tx.Model(&MaterialBinding{}).Select("material_id").Where("scope = ? AND state NOT IN ?", scope, []string{"deleted", "retired"})
		if err := lockForUpdate(tx).Where("user_id = ? AND source_url = ? AND library_type = ? AND file_type = ? AND id IN (?)", userID, source, MaterialLibraryAIGC, MaterialFileImage, bindings).Order("id").Find(&existing).Error; err != nil {
			return err
		}
		existing = slices.DeleteFunc(existing, func(candidate Material) bool { return candidate.SourceURL != source })
		bestRank := -1
		for _, candidate := range existing {
			var binding MaterialBinding
			err := lockForUpdate(tx).Where("material_id = ? AND scope = ? AND version = ? AND state NOT IN ?", candidate.Id, scope, MaterialSourceVersion(&candidate), []string{"deleted", "retired"}).First(&binding).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			// Uploads require a durable GroupID, so empty IDs prove no upload was started.
			if candidate.GroupId != nil && binding.State == "failed" && binding.GroupID == "" && binding.AssetID == "" && binding.LockedUntil < common.GetTimestamp() {
				var group MaterialGroup
				if err := tx.Where("id = ? AND user_id = ?", *candidate.GroupId, userID).First(&group).Error; err != nil {
					return err
				}
				var groupBindings []MaterialGroupBinding
				if err := tx.Where("group_id = ? AND state <> ?", group.Id, "deleted").Find(&groupBindings).Error; err != nil {
					return err
				}
				currentGroup, otherGroup := false, false
				for _, mapping := range groupBindings {
					if mapping.Scope == scope {
						currentGroup = true
					} else if mapping.State == "ready" && mapping.UpstreamID != "" {
						otherGroup = true
					}
				}
				if !currentGroup && otherGroup {
					if err := tx.Model(&binding).Update("state", "deleted").Error; err != nil {
						return err
					}
					if err := AggregateMaterialPreparation(tx, &candidate); err != nil {
						return err
					}
					continue
				}
			}
			rank := 1
			switch {
			case candidate.Deleting:
				rank = 0
			case binding.State == "ready" && binding.UpstreamStatus == "ACTIVE":
				rank = 4
			case binding.AssetID != "" || binding.State == "creating" || binding.State == "processing" || binding.State == "uncertain":
				rank = 3
			case binding.State == "failed":
				rank = 2
			}
			if rank > bestRank {
				material, bestRank = candidate, rank
			}
		}
		if bestRank >= 0 {
			return nil
		}
		now := common.GetTimestamp()
		material = Material{MaterialID: NewPlatformMaterialID(), UserId: userID, Name: "AIGC video image", LibraryType: MaterialLibraryAIGC, FileType: MaterialFileImage, SourceURL: source, Status: MaterialStatusPending, CreatedTime: now, UpdatedTime: now}
		if err := tx.Create(&material).Error; err != nil {
			return err
		}
		binding := MaterialBinding{MaterialId: material.Id, Scope: scope, Version: MaterialSourceVersion(&material), Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: now, UpdatedTime: now}
		return tx.Create(&binding).Error
	})
	return &material, err
}

func GetMaterialById(id int) (*Material, error) {
	var material Material
	err := DB.Where("id = ?", id).First(&material).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := PopulateMaterialGroupNames([]*Material{&material}); err != nil {
		return nil, err
	}
	return &material, nil
}

// GetUserMaterial returns a material only when it belongs to the given user,
// enforcing per-user isolation at the data layer.
func GetUserMaterial(userId int, id int) (*Material, error) {
	var material Material
	err := DB.Where("id = ? AND user_id = ?", id, userId).First(&material).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := PopulateMaterialGroupNames([]*Material{&material}); err != nil {
		return nil, err
	}
	return &material, nil
}

// MaterialFilter narrows a material list query. A zero UserId means "all users"
// (admin scope); callers must set it for user-scoped queries.
type MaterialFilter struct {
	UserId         int
	Keyword        string
	SourceURL      string
	ChannelID      int
	UpstreamStatus string
	LibraryType    string
	Status         string
	GroupId        *int
	Offset         int
	Limit          int
}

func (f MaterialFilter) scope(db *gorm.DB) *gorm.DB {
	if f.UserId != 0 {
		db = db.Where("user_id = ?", f.UserId)
	}
	bindings := DB.Model(&MaterialBinding{}).Select("material_id")
	if f.ChannelID > 0 {
		bindings = bindings.Where("channel_id = ?", f.ChannelID)
	}
	if f.UpstreamStatus != "" {
		bindings = bindings.Where("upstream_status = ?", f.UpstreamStatus)
	}
	if keyword := strings.TrimSpace(f.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		matchingBindings := bindings.Session(&gorm.Session{}).Where("asset_id LIKE ? OR group_id LIKE ?", like, like)
		db = db.Where("name LIKE ? OR material_id LIKE ? OR id IN (?)", like, like, matchingBindings)
	}
	if f.ChannelID > 0 || f.UpstreamStatus != "" {
		db = db.Where("id IN (?)", bindings)
	}
	if source := strings.TrimSpace(f.SourceURL); source != "" {
		db = db.Where("source_url LIKE ?", "%"+source+"%")
	}
	if f.LibraryType != "" {
		db = db.Where("library_type = ?", f.LibraryType)
	}
	if f.Status != "" {
		db = db.Where("status = ?", f.Status)
	}
	if f.GroupId != nil {
		if *f.GroupId == 0 {
			db = db.Where("group_id IS NULL")
		} else {
			db = db.Where("group_id = ?", *f.GroupId)
		}
	}
	return db
}

// SearchMaterials returns a page of materials plus the total matching count.
func SearchMaterials(filter MaterialFilter) ([]*Material, int64, error) {
	var total int64
	if err := filter.scope(DB.Model(&Material{})).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	var materials []*Material
	err := filter.scope(DB.Model(&Material{})).
		Order("id DESC").
		Offset(filter.Offset).
		Limit(filter.Limit).
		Find(&materials).Error
	if err != nil {
		return nil, 0, err
	}
	if err := PopulateMaterialGroupNames(materials); err != nil {
		return nil, 0, err
	}
	return materials, total, nil
}

func PopulateMaterialBindings(materials []*Material) error {
	ids := make([]int, 0, len(materials))
	byID := make(map[int]*Material, len(materials))
	for _, material := range materials {
		material.Bindings = nil
		if material.GroupId == nil {
			material.GroupName = ""
		}
		ids = append(ids, material.Id)
		byID[material.Id] = material
	}
	if len(ids) == 0 {
		return nil
	}
	var bindings []MaterialBinding
	if err := DB.Where("material_id IN ?", ids).Order("id ASC").Find(&bindings).Error; err != nil {
		return err
	}
	var userIDs []int
	var scopes []string
	for _, binding := range bindings {
		material := byID[binding.MaterialId]
		material.Bindings = append(material.Bindings, binding)
		if material.GroupId == nil && binding.GroupID != "" && binding.Version == MaterialSourceVersion(material) {
			userIDs = append(userIDs, material.UserId)
			scopes = append(scopes, binding.Scope)
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	var groups []MaterialUpstreamGroup
	if err := DB.Where("user_id IN ? AND scope IN ?", userIDs, scopes).Find(&groups).Error; err != nil {
		return err
	}
	type groupKey struct {
		userID     int
		scope      string
		upstreamID string
	}
	groupNames := make(map[groupKey]string, len(groups))
	for _, group := range groups {
		groupNames[groupKey{group.UserId, group.Scope, group.UpstreamID}] = group.Name
	}
	for _, material := range materials {
		if material.GroupId != nil {
			continue
		}
		var names []string
		version := MaterialSourceVersion(material)
		for _, binding := range material.Bindings {
			if binding.GroupID == "" || binding.Version != version {
				continue
			}
			name := groupNames[groupKey{material.UserId, binding.Scope, binding.GroupID}]
			if name != "" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		material.GroupName = strings.Join(names, ", ")
	}
	return nil
}

// Soft-deleted owners retain their names on historical material records.
func PopulateMaterialUsernames(materials []*Material) error {
	ids := make([]int, 0, len(materials))
	seen := make(map[int]struct{}, len(materials))
	for _, material := range materials {
		if material == nil {
			continue
		}
		material.Username = ""
		if _, exists := seen[material.UserId]; exists {
			continue
		}
		seen[material.UserId] = struct{}{}
		ids = append(ids, material.UserId)
	}
	if len(ids) == 0 {
		return nil
	}
	var users []User
	if err := DB.Unscoped().Select("id", "username").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return err
	}
	usernames := make(map[int]string, len(users))
	for _, user := range users {
		usernames[user.Id] = user.Username
	}
	for _, material := range materials {
		if material != nil {
			material.Username = usernames[material.UserId]
		}
	}
	return nil
}

// DeleteUserMaterial deletes a material only when it belongs to the given user.
// It reports whether a row was actually removed so the caller can distinguish a
// foreign or missing record from a successful delete.
func DeleteUserMaterial(userId int, id int) (bool, error) {
	result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Material{})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	return true, nil
}

func QueueMaterialDeletion(userID, id int) (*Material, error) {
	var material Material
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userID).First(&material).Error; err != nil {
			return err
		}
		if material.Deleting && material.Status != MaterialStatusFailed {
			return errors.New("material deletion is already in progress")
		}
		var active int64
		if err := tx.Model(&MaterialBinding{}).Where("material_id = ? AND locked_until >= ?", id, common.GetTimestamp()).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return errors.New("material synchronization is in progress; try again after it finishes")
		}
		if err := tx.Model(&MaterialUseLease{}).Where("material_id = ? AND expires_at >= ?", id, common.GetTimestamp()).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return errors.New("material is in use; try again after video submission finishes")
		}
		return tx.Model(&material).Updates(map[string]any{"deleting": true, "status": MaterialStatusProcessing, "fail_reason": "", "can_retry": false, "updated_time": common.GetTimestamp()}).Error
	})
	return &material, err
}

func QueueMaterialGroupDeletion(userID, id int) (*MaterialGroup, error) {
	var group MaterialGroup
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userID).First(&group).Error; err != nil {
			return err
		}
		if group.Deleting && group.Status != MaterialStatusFailed {
			return errors.New("material group deletion is already in progress")
		}
		var count int64
		if err := tx.Model(&Material{}).Where("group_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("delete the materials in this group before deleting the group")
		}
		if err := tx.Model(&MaterialGroupBinding{}).Where("group_id = ? AND locked_until >= ?", id, common.GetTimestamp()).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("material group synchronization is in progress; try again after it finishes")
		}
		return tx.Model(&group).Updates(map[string]any{"deleting": true, "status": MaterialStatusProcessing, "fail_reason": "", "updated_time": common.GetTimestamp()}).Error
	})
	return &group, err
}

func RetryMaterialGroup(userID, id int) (*MaterialGroup, error) {
	var group MaterialGroup
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userID).First(&group).Error; err != nil {
			return err
		}
		if group.Status != MaterialStatusFailed || group.Deleting {
			return errors.New("material group cannot be retried in its current state")
		}
		return tx.Model(&group).Updates(map[string]any{"status": MaterialStatusPending, "fail_reason": "", "updated_time": common.GetTimestamp()}).Error
	})
	return &group, err
}

func (material *Material) ValidateForCreate() error {
	material.Name = strings.TrimSpace(material.Name)
	if material.Name == "" {
		return errors.New("material name is required")
	}
	if !IsValidMaterialLibraryType(material.LibraryType) {
		return fmt.Errorf("invalid library type: %s", material.LibraryType)
	}
	if !IsValidMaterialFileType(material.FileType) {
		return fmt.Errorf("invalid file type: %s", material.FileType)
	}
	material.SourceURL = strings.TrimSpace(material.SourceURL)
	if material.SourceURL == "" {
		return errors.New("material source URL is required")
	}
	return nil
}
