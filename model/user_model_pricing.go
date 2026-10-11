package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const UserModelPricingMultiplierOption = "UserModelPricingMultiplier"

var userModelPricingMutex sync.Mutex

func GetUserModelPricing(userID int) (map[string]float64, error) {
	var user User
	if err := DB.Select("id").First(&user, userID).Error; err != nil {
		return nil, err
	}
	var option Option
	err := DB.Where(&Option{Key: UserModelPricingMultiplierOption}).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return map[string]float64{}, nil
	}
	if err != nil {
		return nil, err
	}
	values, err := ratio_setting.ParseUserModelPricingMultipliers(option.Value)
	if err != nil {
		return nil, err
	}
	result := make(map[string]float64)
	prefix := strconv.Itoa(userID) + "::"
	for key, value := range values {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			result[name] = value
		}
	}
	return result, nil
}

// ReplaceUserModelPricing serializes read/modify/write and publishes only committed values.
// The option-row write acquires the database lock even on SQLite and on first creation.
func ReplaceUserModelPricing(userID int, multipliers map[string]float64) error {
	if userID <= 0 || multipliers == nil {
		return fmt.Errorf("a positive user ID and multipliers object are required")
	}
	prefix := strconv.Itoa(userID) + "::"
	draft := make(map[string]float64, len(multipliers))
	for name, value := range multipliers {
		draft[prefix+name] = value
	}
	encoded, err := common.Marshal(draft)
	if err != nil {
		return err
	}
	if _, err := ratio_setting.ParseUserModelPricingMultipliers(string(encoded)); err != nil {
		return err
	}
	userModelPricingMutex.Lock()
	defer userModelPricingMutex.Unlock()
	var committed string
	err = DB.Transaction(func(tx *gorm.DB) error {
		row := Option{Key: UserModelPricingMultiplierOption, Value: "{}"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		// Acquire a write lock before reading to prevent lost updates across instances.
		if err := tx.Model(&Option{}).Where(&Option{Key: row.Key}).Update("value", gorm.Expr("value")).Error; err != nil {
			return err
		}
		var user User
		if err := tx.Select("id").First(&user, userID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where(&Option{Key: row.Key}).First(&row).Error; err != nil {
			return err
		}
		values, err := ratio_setting.ParseUserModelPricingMultipliers(row.Value)
		if err != nil {
			return err
		}
		for key := range values {
			if strings.HasPrefix(key, prefix) {
				delete(values, key)
			}
		}
		for key, value := range draft {
			values[key] = value
		}
		encoded, err := common.Marshal(values)
		if err != nil {
			return err
		}
		committed = string(encoded)
		return tx.Model(&Option{}).Where(&Option{Key: row.Key}).Update("value", committed).Error
	})
	if err != nil {
		return err
	}
	return updateOptionMap(UserModelPricingMultiplierOption, committed)
}
