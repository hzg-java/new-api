package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type MaterialGroup struct {
	Id          int                    `json:"id"`
	UserId      int                    `json:"user_id" gorm:"uniqueIndex:idx_material_group_owner_name"`
	Name        string                 `json:"name" gorm:"type:varchar(64)"`
	NameKey     string                 `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_material_group_owner_name"`
	CreatedTime int64                  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64                  `json:"updated_time" gorm:"bigint"`
	Status      string                 `json:"status" gorm:"type:varchar(32);index"`
	FailReason  string                 `json:"fail_reason" gorm:"type:text"`
	Deleting    bool                   `json:"deleting"`
	Bindings    []MaterialGroupBinding `json:"bindings,omitempty" gorm:"-"`
}

func (group *MaterialGroup) Validate() error {
	group.Name = strings.TrimSpace(group.Name)
	if group.Name == "" || utf8.RuneCountInString(group.Name) > 64 {
		return errors.New("material group name must contain 1 to 64 characters")
	}
	// A hashed, lowercased key keeps uniqueness independent of database collation.
	group.NameKey = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(group.Name))))
	return nil
}

func ListUserMaterialGroups(userId int) ([]*MaterialGroup, error) {
	groups := make([]*MaterialGroup, 0)
	if err := DB.Where("user_id = ?", userId).Order("id ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	return groups, PopulateMaterialGroupBindings(groups)
}

func PopulateMaterialGroupBindings(groups []*MaterialGroup) error {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]int, len(groups))
	byID := make(map[int]*MaterialGroup, len(groups))
	for i, group := range groups {
		ids[i], byID[group.Id] = group.Id, group
		group.Bindings = nil
	}
	var bindings []MaterialGroupBinding
	if err := DB.Select("id", "group_id", "channel_id", "channel_name", "provider", "name", "upstream_id", "state", "fail_reason", "updated_time").Where("group_id IN ?", ids).Order("id").Find(&bindings).Error; err != nil {
		return err
	}
	for _, binding := range bindings {
		if group := byID[binding.GroupId]; group != nil {
			group.Bindings = append(group.Bindings, binding)
		}
	}
	return nil
}

func SaveMaterialGroup(group *MaterialGroup) error {
	if err := group.Validate(); err != nil {
		return err
	}
	var count int64
	if err := DB.Model(&MaterialGroup{}).Where("user_id = ? AND name_key = ? AND id <> ?", group.UserId, group.NameKey, group.Id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("a material group with this name already exists")
	}
	group.UpdatedTime = common.GetTimestamp()
	group.Status, group.FailReason = MaterialStatusPending, ""
	if group.Id == 0 {
		group.CreatedTime = group.UpdatedTime
		return DB.Create(group).Error
	}
	result := DB.Model(&MaterialGroup{}).Where("id = ? AND user_id = ? AND deleting = ?", group.Id, group.UserId, false).Updates(map[string]any{
		"name": group.Name, "name_key": group.NameKey, "updated_time": group.UpdatedTime, "status": group.Status, "fail_reason": "",
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var existing MaterialGroup
		if err := DB.Where("id = ? AND user_id = ?", group.Id, group.UserId).First(&existing).Error; err != nil {
			return errors.New("material group not found")
		}
		if existing.Deleting {
			return errors.New("material group deletion is in progress")
		}
	}
	return DB.Where("id = ? AND user_id = ?", group.Id, group.UserId).First(group).Error
}

func UpdateMaterialGroupSyncStatus(db *gorm.DB, snapshot *MaterialGroup, status, failReason string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var current MaterialGroup
		if err := lockForUpdate(tx).Select("id", "name", "deleting").First(&current, snapshot.Id).Error; err != nil {
			return err
		}
		if current.Name != snapshot.Name || current.Deleting != snapshot.Deleting {
			return nil
		}
		return tx.Model(&current).Updates(map[string]any{
			"status": status, "fail_reason": failReason, "updated_time": common.GetTimestamp(),
		}).Error
	})
}

func ValidateMaterialGroup(userId int, groupId *int) error {
	if groupId == nil {
		return nil
	}
	if *groupId <= 0 {
		return errors.New("invalid material group")
	}
	var count int64
	if err := DB.Model(&MaterialGroup{}).Where("id = ? AND user_id = ?", *groupId, userId).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return errors.New("material group not found")
	}
	return nil
}

func PopulateMaterialGroupNames(materials []*Material) error {
	ids := make([]int, 0, len(materials))
	for _, material := range materials {
		material.GroupName = ""
		if material.GroupId != nil {
			ids = append(ids, *material.GroupId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var groups []MaterialGroup
	if err := DB.Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return err
	}
	byId := make(map[int]MaterialGroup, len(groups))
	for _, group := range groups {
		byId[group.Id] = group
	}
	for _, material := range materials {
		if material.GroupId == nil {
			continue
		}
		group := byId[*material.GroupId]
		if group.UserId == material.UserId {
			material.GroupName = group.Name
		}
	}
	return nil
}

func SetUserMaterialGroup(userId, id int, groupId *int) (*Material, error) {
	err := DB.Transaction(func(tx *gorm.DB) error {
		if groupId != nil {
			var group MaterialGroup
			if *groupId <= 0 || lockForUpdate(tx).Where("id = ? AND user_id = ? AND deleting = ?", *groupId, userId, false).First(&group).Error != nil {
				return errors.New("material group not found")
			}
		}
		var material Material
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", id, userId).First(&material).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("material not found")
			}
			return err
		}
		if material.Deleting || material.Status != MaterialStatusPending {
			return errors.New("only an unsynchronized pending material can change groups")
		}
		var bindings int64
		if err := tx.Model(&MaterialBinding{}).Where("material_id = ?", id).Count(&bindings).Error; err != nil {
			return err
		}
		if bindings != 0 {
			return errors.New("the upstream does not support moving uploaded materials between groups")
		}
		return tx.Model(&material).Updates(map[string]any{"group_id": groupId, "updated_time": common.GetTimestamp()}).Error
	})
	if err != nil {
		return nil, err
	}
	return GetUserMaterial(userId, id)
}
