package model

import (
	"context"
	"errors"
	"os"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MobileCloudMaterialConfigOption = "MobileCloudMaterialConfigSecret"
	TopenRouterMaterialConfigOption = "TopenRouterMaterialConfigSecret"
)

var ErrMobileCloudMaterialTestOnly = errors.New("mobile cloud material configuration is available only in the enabled new-api-test environment")

func ValidateMobileCloudMaterialTestDatabase(tx *gorm.DB) error {
	if os.Getenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED") != "true" || tx == nil || tx.Dialector.Name() != "postgres" {
		return ErrMobileCloudMaterialTestOnly
	}
	var database string
	if err := tx.Raw("SELECT current_database()").Scan(&database).Error; err != nil {
		return err
	}
	if database != "new-api-test" {
		return ErrMobileCloudMaterialTestOnly
	}
	return nil
}

func ReadMobileCloudMaterialConfig() (string, error) {
	return ReadMaterialIntegrationConfig(MobileCloudMaterialConfigOption)
}

func ReadMaterialIntegrationConfig(key string) (string, error) {
	if key != MobileCloudMaterialConfigOption && key != TopenRouterMaterialConfigOption {
		return "", ErrMobileCloudMaterialTestOnly
	}
	if DB == nil || os.Getenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED") != "true" {
		return "", ErrMobileCloudMaterialTestOnly
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var value string
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ValidateMobileCloudMaterialTestDatabase(tx); err != nil {
			return err
		}
		var option Option
		err := tx.Where("key = ?", key).First(&option).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		value = option.Value
		return err
	})
	return value, err
}

func UpdateMobileCloudMaterialConfig(update func(*gorm.DB, string) (string, error)) error {
	return UpdateMaterialIntegrationConfig(MobileCloudMaterialConfigOption, update)
}

func UpdateMaterialIntegrationConfig(key string, update func(*gorm.DB, string) (string, error)) error {
	if key != MobileCloudMaterialConfigOption && key != TopenRouterMaterialConfigOption {
		return ErrMobileCloudMaterialTestOnly
	}
	if DB == nil || os.Getenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED") != "true" {
		return ErrMobileCloudMaterialTestOnly
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ValidateMobileCloudMaterialTestDatabase(tx); err != nil {
			return err
		}
		var current string
		// Both suppliers share one binding decision, so acquire their rows in a fixed order.
		for _, configKey := range []string{MobileCloudMaterialConfigOption, TopenRouterMaterialConfigOption} {
			option := Option{Key: configKey}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&option).Error; err != nil {
				return err
			}
			if err := lockForUpdate(tx).Where("key = ?", configKey).First(&option).Error; err != nil {
				return err
			}
			if configKey == key {
				current = option.Value
			}
		}
		value, err := update(tx, current)
		if err != nil {
			return err
		}
		return tx.Model(&Option{}).Where("key = ?", key).Update("value", value).Error
	})
}
