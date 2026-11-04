package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const TopenRouterMaterialDefaultEndpoint = "https://tp-api.chinadatapay.com:8000"

var (
	ErrTopenRouterMaterialConfig = errors.New("TopenRouter material configuration could not be read or saved")
	ErrTopenRouterMaterialInput  = errors.New("select an enabled single-key video channel and enter a valid HTTPS TopenRouter endpoint and token; a blank token only preserves an existing token")
)

type TopenRouterMaterialConfiguration struct {
	Enabled            *bool  `json:"enabled,omitempty"`
	ChannelID          int    `json:"channel_id"`
	Endpoint           string `json:"endpoint"`
	Token              string `json:"token"`
	ChannelFingerprint string `json:"channel_fingerprint,omitempty"`
}

type TopenRouterMaterialConfigView struct {
	Enabled         bool   `json:"enabled"`
	Available       bool   `json:"available"`
	Configured      bool   `json:"configured"`
	Endpoint        string `json:"endpoint"`
	TokenConfigured bool   `json:"token_configured"`
	ChannelID       int    `json:"channel_id"`
	ChannelName     string `json:"channel_name"`
}

func decodeTopenRouterMaterialConfig(raw string) (*TopenRouterMaterialConfiguration, error) {
	config := &TopenRouterMaterialConfiguration{Endpoint: TopenRouterMaterialDefaultEndpoint}
	if raw == "" {
		return config, nil
	}
	aead, err := mobileCloudMaterialCipher(false)
	if err != nil {
		return nil, err
	}
	channelID, encoded, err := topenRouterMaterialConfigBinding(raw)
	if err != nil {
		return nil, err
	}
	associatedData := model.TopenRouterMaterialConfigOption + ":v1"
	if channelID > 0 {
		associatedData = model.TopenRouterMaterialConfigOption + ":v2:" + strconv.Itoa(channelID)
	}
	sealed, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrTopenRouterMaterialConfig
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(associatedData))
	if err != nil || common.Unmarshal(plain, config) != nil || channelID > 0 && config.ChannelID != channelID {
		return nil, ErrTopenRouterMaterialConfig
	}
	return config, nil
}

func topenRouterMaterialConfigBinding(raw string) (int, string, error) {
	if encoded, ok := strings.CutPrefix(raw, "v1."); ok && len(encoded) <= 32768 {
		return 0, encoded, nil
	}
	value, ok := strings.CutPrefix(raw, "v2.")
	if !ok {
		return 0, "", ErrTopenRouterMaterialConfig
	}
	binding, encoded, ok := strings.Cut(value, ".")
	channelID, err := strconv.Atoi(binding)
	if !ok || err != nil || channelID <= 0 || strconv.Itoa(channelID) != binding || encoded == "" || len(encoded) > 32768 {
		return 0, "", ErrTopenRouterMaterialConfig
	}
	return channelID, encoded, nil
}

func GetTopenRouterMaterialConfigView() (*TopenRouterMaterialConfigView, error) {
	view := &TopenRouterMaterialConfigView{Endpoint: TopenRouterMaterialDefaultEndpoint}
	raw, err := model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	if err != nil {
		return nil, ErrTopenRouterMaterialConfig
	}
	config, err := decodeTopenRouterMaterialConfig(raw)
	if err != nil {
		return nil, ErrTopenRouterMaterialConfig
	}
	view.Available, view.Configured = true, raw != ""
	view.Enabled = config.Enabled == nil || *config.Enabled
	view.Endpoint, view.TokenConfigured = config.Endpoint, config.Token != ""
	view.ChannelID = config.ChannelID
	if config.ChannelID > 0 {
		var channel model.Channel
		if err := model.DB.Select("id", "name").Where("id = ?", config.ChannelID).Find(&channel).Error; err != nil {
			return nil, ErrTopenRouterMaterialConfig
		}
		view.ChannelName = channel.Name
	}
	return view, nil
}

func SaveTopenRouterMaterialConfiguration(input TopenRouterMaterialConfiguration) error {
	err := model.UpdateMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption, func(tx *gorm.DB, raw string) (string, error) {
		current, err := decodeTopenRouterMaterialConfig(raw)
		if err != nil {
			return "", err
		}
		if input.Enabled == nil {
			input.Enabled = common.GetPointer(current.Enabled == nil || *current.Enabled)
		}
		for _, field := range []string{input.Endpoint, input.Token} {
			if len(field) > 4096 || strings.IndexFunc(field, unicode.IsControl) >= 0 || field != "" && strings.TrimSpace(field) == "" {
				return "", ErrTopenRouterMaterialInput
			}
		}
		input.Endpoint = strings.TrimRight(strings.TrimSpace(input.Endpoint), "/")
		if input.ChannelID == 0 {
			input.ChannelID = current.ChannelID
		}
		var channel model.Channel
		if input.ChannelID <= 0 || tx.Where("id = ? AND status = ?", input.ChannelID, common.ChannelStatusEnabled).First(&channel).Error != nil || channel.ChannelInfo.IsMultiKey || IsMaterialOnlyChannel(&channel) {
			return "", ErrTopenRouterMaterialInput
		}
		input.ChannelFingerprint = MobileCloudVideoChannelFingerprint(&channel)
		if input.Token == "" {
			input.Token = current.Token
		}
		client, err := NewTopenRouterMaterialClient(input.Endpoint, input.Token)
		if err != nil {
			return "", ErrTopenRouterMaterialInput
		}
		endpoint := client.endpoint
		endpoint.Path, endpoint.RawPath = "", ""
		endpoint.Host = strings.ToLower(endpoint.Host)
		input.Endpoint = endpoint.String()
		var mobileConfig model.Option
		if err := tx.Select("value").Where("key = ?", model.MobileCloudMaterialConfigOption).Find(&mobileConfig).Error; err != nil {
			return "", err
		}
		aead, err := mobileCloudMaterialCipher(raw == "" && mobileConfig.Value == "")
		if err != nil {
			return "", err
		}
		if mobileConfig.Value != "" {
			mobile, err := decodeMobileCloudMaterialConfig(mobileConfig.Value, aead)
			if err != nil {
				return "", err
			}
			if mobile.ChannelID == input.ChannelID {
				return "", ErrTopenRouterMaterialInput
			}
		}
		plain, err := common.Marshal(input)
		if err != nil {
			return "", err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return "", err
		}
		binding := strconv.Itoa(input.ChannelID)
		sealed := aead.Seal(nonce, nonce, plain, []byte(model.TopenRouterMaterialConfigOption+":v2:"+binding))
		return "v2." + binding + "." + base64.StdEncoding.EncodeToString(sealed), nil
	})
	if err != nil && !errors.Is(err, ErrTopenRouterMaterialInput) && !errors.Is(err, ErrMobileCloudMaterialEncryption) {
		return ErrTopenRouterMaterialConfig
	}
	return err
}

func LoadTopenRouterMaterialConfiguration() (*TopenRouterMaterialConfiguration, error) {
	raw, err := model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, ErrTopenRouterMaterialConfig
	}
	return decodeTopenRouterMaterialConfig(raw)
}

func topenRouterMaterialChannel(config *TopenRouterMaterialConfiguration) (MaterialChannel, error) {
	target := MaterialChannel{ChannelID: config.ChannelID, Provider: "topenrouter", Scope: TopenRouterMaterialScope(config)}
	var channel model.Channel
	if err := model.DB.Where("id = ? AND status = ?", config.ChannelID, common.ChannelStatusEnabled).First(&channel).Error; err != nil {
		return target, ErrTopenRouterMaterialConfig
	}
	target.ChannelName = channel.Name
	if config.ChannelFingerprint != "" && (channel.ChannelInfo.IsMultiKey || config.ChannelFingerprint != MobileCloudVideoChannelFingerprint(&channel)) {
		return target, ErrTopenRouterMaterialConfig
	}
	client, err := NewTopenRouterMaterialClient(config.Endpoint, config.Token)
	if err != nil {
		return target, ErrTopenRouterMaterialConfig
	}
	target.Client = client
	return target, nil
}

func IsMaterialOnlyChannel(channel *model.Channel) bool {
	return channel.Type == constant.ChannelTypeCustom && strings.HasPrefix(channel.Key, "material-managed:")
}

func TopenRouterMaterialScope(config *TopenRouterMaterialConfiguration) string {
	scope := fmt.Sprintf("topenrouter\x00%d\x00%s\x00%s", config.ChannelID, config.Endpoint, config.Token)
	if config.ChannelFingerprint != "" {
		scope += "\x00" + config.ChannelFingerprint
	}
	sum := sha256.Sum256([]byte(scope))
	return hex.EncodeToString(sum[:])
}
