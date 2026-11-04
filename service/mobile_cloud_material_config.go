package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const MobileCloudMaterialDefaultEndpoint = "https://ecloud.10086.cn/api/openapi-maas"

var (
	ErrMobileCloudMaterialEncryption = errors.New("credential encryption is unavailable; check the backend key file or master key environment variable")
	mobileCloudMaterialKeyMu         sync.Mutex
	ErrMobileCloudMaterialConfig     = errors.New("mobile cloud material configuration could not be read or saved")
	ErrMobileCloudMaterialInput      = errors.New("select an enabled channel and enter a valid HTTPS MaaS endpoint, AccessKey, SecretKey and Pool-Id; blank keys only preserve existing credentials")
)

type MobileCloudMaterialConfiguration struct {
	Enabled            *bool  `json:"enabled,omitempty"`
	ChannelID          int    `json:"channel_id"`
	Endpoint           string `json:"endpoint"`
	AccessKey          string `json:"access_key"`
	SecretKey          string `json:"secret_key"`
	PoolID             string `json:"pool_id"`
	ChannelFingerprint string `json:"channel_fingerprint,omitempty"`
}

type MobileCloudMaterialConfigView struct {
	Enabled             bool   `json:"enabled"`
	Available           bool   `json:"available"`
	Configured          bool   `json:"configured"`
	EncryptionReady     bool   `json:"encryption_ready"`
	Endpoint            string `json:"endpoint"`
	AccessKeyMasked     string `json:"access_key_masked"`
	SecretKeyConfigured bool   `json:"secret_key_configured"`
	PoolID              string `json:"pool_id"`
	ChannelID           int    `json:"channel_id"`
	ChannelName         string `json:"channel_name"`
}

func mobileCloudMaterialCipher(initialize bool) (cipher.AEAD, error) {
	raw := os.Getenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY")
	if raw == "" {
		mobileCloudMaterialKeyMu.Lock()
		defer mobileCloudMaterialKeyMu.Unlock()

		keyPath := os.Getenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE")
		if keyPath == "" {
			keyPath = filepath.Join("data", "mobile-cloud-material.key")
		}
		stored, err := os.ReadFile(keyPath)
		if errors.Is(err, os.ErrNotExist) && initialize {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, ErrMobileCloudMaterialEncryption
			}
			if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
				return nil, ErrMobileCloudMaterialEncryption
			}
			file, createErr := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if errors.Is(createErr, os.ErrExist) {
				stored, err = os.ReadFile(keyPath)
			} else if createErr != nil {
				return nil, ErrMobileCloudMaterialEncryption
			} else {
				stored = []byte(base64.StdEncoding.EncodeToString(key))
				_, err = file.Write(stored)
				if err == nil {
					err = file.Sync()
				}
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					_ = os.Remove(keyPath)
					return nil, ErrMobileCloudMaterialEncryption
				}
			}
		}
		if err != nil {
			return nil, ErrMobileCloudMaterialEncryption
		}
		raw = string(stored)
	}
	if strings.ContainsAny(raw, "\r\n") {
		return nil, ErrMobileCloudMaterialEncryption
	}
	key, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, ErrMobileCloudMaterialEncryption
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrMobileCloudMaterialEncryption
	}
	return cipher.NewGCM(block)
}

func decodeMobileCloudMaterialConfig(raw string, aead cipher.AEAD) (*MobileCloudMaterialConfiguration, error) {
	config := &MobileCloudMaterialConfiguration{Endpoint: MobileCloudMaterialDefaultEndpoint}
	if raw == "" {
		return config, nil
	}
	channelID, encoded, err := mobileCloudMaterialConfigBinding(raw)
	if err != nil {
		return nil, err
	}
	associatedData := model.MobileCloudMaterialConfigOption + ":v1"
	if channelID > 0 {
		associatedData = model.MobileCloudMaterialConfigOption + ":v2:" + strconv.Itoa(channelID)
	}
	sealed, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrMobileCloudMaterialConfig
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(associatedData))
	if err != nil || common.Unmarshal(plain, config) != nil || channelID > 0 && config.ChannelID != channelID {
		return nil, ErrMobileCloudMaterialConfig
	}
	return config, nil
}

func mobileCloudMaterialConfigBinding(raw string) (int, string, error) {
	if encoded, ok := strings.CutPrefix(raw, "v1."); ok && len(encoded) <= 32768 {
		return 0, encoded, nil
	}
	value, ok := strings.CutPrefix(raw, "v2.")
	if !ok {
		return 0, "", ErrMobileCloudMaterialConfig
	}
	binding, encoded, ok := strings.Cut(value, ".")
	channelID, err := strconv.Atoi(binding)
	if !ok || err != nil || channelID <= 0 || strconv.Itoa(channelID) != binding || encoded == "" || len(encoded) > 32768 {
		return 0, "", ErrMobileCloudMaterialConfig
	}
	return channelID, encoded, nil
}

func GetMobileCloudMaterialConfigView() (*MobileCloudMaterialConfigView, error) {
	view := &MobileCloudMaterialConfigView{Endpoint: MobileCloudMaterialDefaultEndpoint}
	raw, err := model.ReadMobileCloudMaterialConfig()
	if err != nil {
		return nil, ErrMobileCloudMaterialConfig
	}
	view.Available = true
	view.Configured = raw != ""
	aead, cipherErr := mobileCloudMaterialCipher(false)
	view.EncryptionReady = cipherErr == nil
	if cipherErr != nil {
		return view, nil
	}
	config, err := decodeMobileCloudMaterialConfig(raw, aead)
	if err != nil {
		return nil, err
	}
	view.Enabled = config.Enabled == nil || *config.Enabled
	view.Endpoint, view.PoolID = config.Endpoint, config.PoolID
	view.ChannelID = config.ChannelID
	if config.ChannelID > 0 {
		var channel model.Channel
		if err := model.DB.Select("id", "name").Where("id = ?", config.ChannelID).Find(&channel).Error; err != nil {
			return nil, ErrMobileCloudMaterialConfig
		}
		view.ChannelName = channel.Name
	}
	view.Configured = raw != ""
	view.SecretKeyConfigured = config.SecretKey != ""
	if config.AccessKey != "" {
		view.AccessKeyMasked = "********"
		if len(config.AccessKey) > 8 {
			view.AccessKeyMasked += config.AccessKey[len(config.AccessKey)-4:]
		}
	}
	return view, nil
}

func SaveMobileCloudMaterialConfiguration(input MobileCloudMaterialConfiguration) error {
	err := model.UpdateMobileCloudMaterialConfig(func(tx *gorm.DB, raw string) (string, error) {
		var topenRouterConfig model.Option
		if err := tx.Select("value").Where("key = ?", model.TopenRouterMaterialConfigOption).Find(&topenRouterConfig).Error; err != nil {
			return "", err
		}
		aead, err := mobileCloudMaterialCipher(raw == "" && topenRouterConfig.Value == "")
		if err != nil {
			return "", err
		}
		current, err := decodeMobileCloudMaterialConfig(raw, aead)
		if err != nil {
			return "", err
		}
		if input.Enabled == nil {
			input.Enabled = common.GetPointer(current.Enabled == nil || *current.Enabled)
		}
		for _, field := range []string{input.Endpoint, input.AccessKey, input.SecretKey, input.PoolID} {
			if len(field) > 1024 || strings.IndexFunc(field, unicode.IsControl) >= 0 || (field != "" && strings.TrimSpace(field) == "") {
				return "", ErrMobileCloudMaterialInput
			}
		}
		if input.AccessKey == "" {
			input.AccessKey = current.AccessKey
		}
		if input.SecretKey == "" {
			input.SecretKey = current.SecretKey
		}
		if input.ChannelID == 0 {
			input.ChannelID = current.ChannelID
		}
		if input.ChannelID <= 0 {
			return "", ErrMobileCloudMaterialInput
		}
		if topenRouterConfig.Value != "" {
			topenRouter, err := decodeTopenRouterMaterialConfig(topenRouterConfig.Value)
			if err != nil {
				return "", err
			}
			if topenRouter.ChannelID == input.ChannelID {
				return "", ErrMobileCloudMaterialInput
			}
		}
		var channel model.Channel
		if err := tx.Where("id = ? AND status = ?", input.ChannelID, 1).First(&channel).Error; err != nil {
			return "", ErrMobileCloudMaterialInput
		}
		if channel.ChannelInfo.IsMultiKey {
			return "", ErrMobileCloudMaterialInput
		}
		input.ChannelFingerprint = MobileCloudVideoChannelFingerprint(&channel)
		input.Endpoint = strings.TrimSpace(input.Endpoint)
		input.PoolID = strings.TrimSpace(input.PoolID)
		if _, err := NewMobileCloudMaterialClient(input.Endpoint, input.AccessKey, input.SecretKey, input.PoolID); err != nil {
			return "", ErrMobileCloudMaterialInput
		}
		plain, err := common.Marshal(input)
		if err != nil {
			return "", ErrMobileCloudMaterialConfig
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return "", ErrMobileCloudMaterialEncryption
		}
		binding := strconv.Itoa(input.ChannelID)
		sealed := aead.Seal(nonce, nonce, plain, []byte(model.MobileCloudMaterialConfigOption+":v2:"+binding))
		return "v2." + binding + "." + base64.StdEncoding.EncodeToString(sealed), nil
	})
	if err != nil && !errors.Is(err, ErrMobileCloudMaterialInput) && !errors.Is(err, ErrMobileCloudMaterialEncryption) {
		return ErrMobileCloudMaterialConfig
	}
	return err
}

func LoadMobileCloudMaterialConfiguration() (*MobileCloudMaterialConfiguration, error) {
	raw, err := model.ReadMobileCloudMaterialConfig()
	if err != nil {
		return nil, ErrMobileCloudMaterialConfig
	}
	if raw == "" {
		return nil, ErrMobileCloudMaterialConfig
	}
	aead, err := mobileCloudMaterialCipher(false)
	if err != nil {
		return nil, err
	}
	return decodeMobileCloudMaterialConfig(raw, aead)
}

func LoadMobileCloudMaterialClient() (*MobileCloudMaterialClient, error) {
	config, err := LoadMobileCloudMaterialConfiguration()
	if err != nil {
		return nil, err
	}
	client, err := NewMobileCloudMaterialClient(config.Endpoint, config.AccessKey, config.SecretKey, config.PoolID)
	if err != nil {
		return nil, ErrMobileCloudMaterialConfig
	}
	return client, nil
}
