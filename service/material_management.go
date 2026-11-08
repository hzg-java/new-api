package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"gorm.io/gorm"
)

type MaterialManagementProvider interface {
	MaterialProvider
	GetAssetGroup(context.Context, string) (*MobileCloudAssetGroup, error)
	UpdateAssetGroup(context.Context, string, MobileCloudGroupUpdate) (*MobileCloudAssetGroup, error)
	DeleteAssetGroup(context.Context, string) (bool, error)
	DeleteAsset(context.Context, string) (bool, error)
}

type MaterialChannel struct {
	ChannelID   int
	ChannelName string
	Provider    string
	Scope       string
	Client      MaterialManagementProvider
}

type MaterialIntegrationTestResult struct {
	Provider    string `json:"provider"`
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Operation   string `json:"operation"`
	Success     bool   `json:"success"`
	ElapsedMS   int64  `json:"elapsed_ms"`
	Total       *int64 `json:"total,omitempty"`
	Message     string `json:"message,omitempty"`
	Response    any    `json:"response,omitempty"`
}

type MaterialIntegrationTestParameters struct {
	GroupID     string   `json:"groupId"`
	AssetID     string   `json:"assetId"`
	GroupName   *string  `json:"groupName"`
	AssetName   *string  `json:"assetName"`
	Description *string  `json:"description"`
	AssetURL    string   `json:"assetUrl"`
	AssetType   string   `json:"assetType"`
	GroupType   *string  `json:"groupType"`
	PageNo      *int32   `json:"pageNo"`
	PageSize    *int32   `json:"pageSize"`
	GroupIDs    []string `json:"groupIds"`
	Statuses    []string `json:"statuses"`
}

type MaterialIntegrationTestRequest struct {
	Operation  string                            `json:"operation"`
	Parameters MaterialIntegrationTestParameters `json:"parameters"`
	Confirmed  bool                              `json:"confirmed"`
}

func ParseMaterialIntegrationTestRequest(provider string, body []byte) (*MaterialIntegrationTestRequest, error) {
	invalid := errors.New("invalid material test operation or parameters")
	var fields map[string]common.RawMessage
	if common.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, invalid
	}
	for name := range fields {
		if name != "operation" && name != "parameters" && name != "confirmed" {
			return nil, invalid
		}
	}
	var input MaterialIntegrationTestRequest
	if common.Unmarshal(body, &input) != nil {
		return nil, invalid
	}
	var parameters map[string]common.RawMessage
	if common.Unmarshal(fields["parameters"], &parameters) != nil || parameters == nil {
		return nil, invalid
	}
	var allowed []string
	switch input.Operation {
	case "create_group":
		allowed = []string{"groupName", "description"}
	case "list_groups":
		allowed = []string{"pageNo", "pageSize"}
		if provider == "mobile-cloud" {
			allowed = append(allowed, "groupType", "groupName", "groupIds")
		}
	case "get_group", "delete_group":
		allowed = []string{"groupId"}
	case "update_group":
		allowed = []string{"groupId", "groupName", "description"}
	case "create_asset":
		allowed = []string{"groupId", "assetName", "assetUrl", "assetType"}
	case "list_assets":
		allowed = []string{"pageNo", "pageSize", "groupIds"}
		if provider == "mobile-cloud" {
			allowed = append(allowed, "groupType", "assetName", "statuses")
		}
	case "get_asset", "delete_asset":
		allowed = []string{"assetId"}
	case "update_asset":
		allowed = []string{"assetId", "assetName"}
	default:
		return nil, invalid
	}
	for name := range parameters {
		if !slices.Contains(allowed, name) {
			return nil, invalid
		}
	}
	if err := input.validate(provider); err != nil {
		return nil, err
	}
	return &input, nil
}

func (input MaterialIntegrationTestRequest) validate(provider string) error {
	invalid := errors.New("invalid material test operation or parameters")
	if provider != "mobile-cloud" && provider != "topenrouter" {
		return invalid
	}
	p := &input.Parameters
	switch input.Operation {
	case "create_group":
		if p.GroupName == nil {
			return invalid
		}
	case "get_group", "update_group", "delete_group":
		if validateTopenRouterMaterialID(p.GroupID) != nil {
			return invalid
		}
	case "get_asset", "update_asset", "delete_asset":
		if validateTopenRouterMaterialID(p.AssetID) != nil || input.Operation == "update_asset" && p.AssetName == nil {
			return invalid
		}
	case "create_asset", "list_groups", "list_assets":
	default:
		return invalid
	}
	for _, name := range []*string{p.GroupName, p.AssetName} {
		if name != nil && (utf8.RuneCountInString(*name) > 64 || (!strings.HasPrefix(input.Operation, "list_") && strings.TrimSpace(*name) == "")) {
			return invalid
		}
	}
	if p.Description != nil && utf8.RuneCountInString(*p.Description) > 300 {
		return invalid
	}
	if input.Operation == "update_group" && p.GroupName == nil && p.Description == nil {
		return invalid
	}
	if strings.HasPrefix(input.Operation, "delete_") && !input.Confirmed {
		return errors.New("confirm deletion before executing the material test")
	}
	if strings.HasPrefix(input.Operation, "list_") {
		if provider == "mobile-cloud" && (p.PageNo == nil || p.PageSize == nil) {
			return invalid
		}
		if p.PageNo != nil && *p.PageNo < 1 || p.PageSize != nil && *p.PageSize < 1 {
			return invalid
		}
		if p.PageSize != nil && (provider == "topenrouter" && *p.PageSize > 200 || provider == "mobile-cloud" && input.Operation == "list_groups" && *p.PageSize > 100) {
			return invalid
		}
		if p.GroupType != nil && *p.GroupType != "AIGC" && *p.GroupType != "LivenessFace" {
			return invalid
		}
		if provider == "mobile-cloud" && input.Operation == "list_assets" && p.GroupType == nil {
			return invalid
		}
		for _, id := range p.GroupIDs {
			if validateTopenRouterMaterialID(id) != nil {
				return invalid
			}
		}
		for _, status := range p.Statuses {
			if !slices.Contains([]string{"ACTIVE", "PROCESSING", "FAILED"}, status) {
				return invalid
			}
		}
	}
	if input.Operation == "create_asset" {
		if provider == "mobile-cloud" && (validateTopenRouterMaterialID(p.GroupID) != nil || p.AssetName == nil) || p.GroupID != "" && validateTopenRouterMaterialID(p.GroupID) != nil {
			return invalid
		}
		source, err := url.Parse(p.AssetURL)
		if err != nil || source.Scheme != "http" && source.Scheme != "https" || source.Hostname() == "" || source.User != nil || source.Opaque != "" || strings.Contains(p.AssetURL, "#") || !slices.Contains([]string{"Image", "Video", "Audio"}, p.AssetType) {
			return invalid
		}
	}
	return nil
}

func TestMaterialIntegration(ctx context.Context, provider string, input MaterialIntegrationTestRequest) (*MaterialIntegrationTestResult, error) {
	if err := input.validate(provider); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	optionKey := model.MobileCloudMaterialConfigOption
	if provider == "topenrouter" {
		optionKey = model.TopenRouterMaterialConfigOption
	} else if provider != "mobile-cloud" {
		return nil, errors.New("unsupported material provider")
	}
	if model.DB == nil {
		return nil, model.ErrMaterialIntegrationUnavailable
	}
	var option model.Option
	if err := model.DB.WithContext(ctx).Select("value").Where("key = ?", optionKey).First(&option).Error; err != nil || option.Value == "" {
		return nil, errors.New("material integration configuration is unavailable")
	}
	var target MaterialChannel
	var secrets []string
	switch provider {
	case "mobile-cloud":
		aead, err := mobileCloudMaterialCipher(false)
		if err != nil {
			return nil, err
		}
		config, err := decodeMobileCloudMaterialConfig(option.Value, aead)
		if err != nil {
			return nil, err
		}
		channels, err := materialChannelsForConfig(model.DB.WithContext(ctx), config)
		if err != nil {
			return nil, err
		}
		target = channels[0]
		secrets = []string{config.AccessKey, config.SecretKey, config.PoolID}
	case "topenrouter":
		config, err := decodeTopenRouterMaterialConfig(option.Value)
		if err != nil {
			return nil, ErrTopenRouterMaterialConfig
		}
		target, err = topenRouterMaterialChannel(model.DB.WithContext(ctx), config)
		if err != nil {
			return nil, err
		}
		secrets = []string{config.Token}
	}
	result := &MaterialIntegrationTestResult{Provider: target.Provider, ChannelID: target.ChannelID, ChannelName: target.ChannelName, Operation: input.Operation}
	started := time.Now()
	p := input.Parameters
	var response any
	var err error
	switch input.Operation {
	case "create_group":
		response, err = target.Client.CreateAssetGroup(ctx, *p.GroupName, p.Description)
	case "list_groups":
		var page *MobileCloudMaterialPage[MobileCloudAssetGroup]
		page, err = target.Client.ListAssetGroups(ctx, MobileCloudGroupQuery{GroupType: p.GroupType, Name: p.GroupName, PageNo: p.PageNo, PageSize: p.PageSize, GroupIDs: p.GroupIDs})
		if err == nil {
			result.Total = &page.Total
			response = page
		}
	case "get_group":
		response, err = target.Client.GetAssetGroup(ctx, p.GroupID)
	case "update_group":
		response, err = target.Client.UpdateAssetGroup(ctx, p.GroupID, MobileCloudGroupUpdate{Name: p.GroupName, Description: p.Description})
	case "delete_group":
		var deleted bool
		deleted, err = target.Client.DeleteAssetGroup(ctx, p.GroupID)
		response = deleted
		if err == nil && !deleted {
			err = errors.New("upstream did not confirm material group deletion")
		}
	case "create_asset":
		name := ""
		if p.AssetName != nil {
			name = *p.AssetName
		}
		var id string
		id, err = target.Client.CreateAsset(ctx, MobileCloudCreateAssetInput{GroupID: p.GroupID, Name: name, URL: p.AssetURL, Type: p.AssetType})
		response = map[string]string{"assetId": id}
	case "list_assets":
		var page *MobileCloudMaterialPage[MobileCloudAsset]
		page, err = target.Client.ListAssets(ctx, MobileCloudAssetQuery{GroupType: p.GroupType, Name: p.AssetName, PageNo: p.PageNo, PageSize: p.PageSize, GroupIDs: p.GroupIDs, Statuses: p.Statuses})
		if err == nil {
			result.Total = &page.Total
			response = page
		}
	case "get_asset":
		response, err = target.Client.GetAsset(ctx, p.AssetID)
	case "update_asset":
		if provider == "mobile-cloud" {
			response, err = target.Client.(*MobileCloudMaterialClient).UpdateAsset(ctx, p.AssetID, *p.AssetName)
		} else {
			response, err = target.Client.(*TopenRouterMaterialClient).UpdateAsset(ctx, p.AssetID, *p.AssetName)
		}
	case "delete_asset":
		var deleted bool
		deleted, err = target.Client.DeleteAsset(ctx, p.AssetID)
		response = deleted
		if err == nil && !deleted {
			err = errors.New("upstream did not confirm material deletion")
		}
	default:
		return nil, errors.New("unsupported material test operation")
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	result.Success = err == nil
	if err != nil {
		result.Message = redactMaterialTestResponse(err.Error(), secrets).(string)
		if !strings.HasPrefix(input.Operation, "list_") && !strings.HasPrefix(input.Operation, "get_") {
			if _, rejected := materialRejectionMessage(err); !rejected {
				result.Message += "; result may be unknown, check the resource before executing again"
			}
		}
		return result, nil
	}
	encoded, err := common.Marshal(response)
	if err != nil || common.Unmarshal(encoded, &result.Response) != nil {
		result.Success = false
		result.Message = "material test response could not be displayed; check the resource before executing again"
		return result, nil
	}
	result.Response = redactMaterialTestResponse(result.Response, secrets)
	return result, nil
}

func redactMaterialTestResponse(value any, secrets []string) any {
	switch value := value.(type) {
	case map[string]any:
		for name, field := range value {
			if strings.EqualFold(name, "assetUrl") || strings.EqualFold(name, "url") {
				delete(value, name)
				continue
			}
			value[name] = redactMaterialTestResponse(field, secrets)
		}
	case []any:
		for i, field := range value {
			value[i] = redactMaterialTestResponse(field, secrets)
		}
	case string:
		for _, secret := range secrets {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "***")
			}
		}
		return topenRouterMaterialReasonURL.ReplaceAllString(common.MaskSensitiveInfo(value), "***")
	}
	return value
}

func LoadMaterialChannels() ([]MaterialChannel, error) {
	var channels []MaterialChannel
	var failures []error
	var unresolved []error
	raw, err := model.ReadMobileCloudMaterialConfig()
	if err != nil {
		return nil, err
	}
	if raw != "" {
		config, err := LoadMobileCloudMaterialConfiguration()
		if err != nil {
			unresolved = append(unresolved, ErrMobileCloudMaterialConfig)
		} else {
			loaded, err := materialChannelsForConfig(model.DB, config)
			if err != nil {
				failures = append(failures, err)
				channels = append(channels, MaterialChannel{ChannelID: config.ChannelID, Provider: "mobile_cloud", Scope: MobileCloudMaterialScope(config, config.ChannelID)})
			} else {
				channels = append(channels, loaded...)
			}
		}
	}
	raw, err = model.ReadMaterialIntegrationConfig(model.TopenRouterMaterialConfigOption)
	if err != nil {
		return channels, err
	}
	if raw != "" {
		config, err := LoadTopenRouterMaterialConfiguration()
		if err != nil {
			unresolved = append(unresolved, ErrTopenRouterMaterialConfig)
		} else {
			target, err := topenRouterMaterialChannel(model.DB, config)
			if err != nil {
				failures = append(failures, err)
			}
			channels = append(channels, target)
		}
	}
	available := false
	for _, channel := range channels {
		available = available || channel.Client != nil
	}
	if !available {
		return nil, errors.Join(append(append(failures, unresolved...), errors.New("material channel configuration is unavailable"))...)
	}
	return channels, errors.Join(unresolved...)
}

func materialChannelsForConfig(db *gorm.DB, config *MobileCloudMaterialConfiguration) ([]MaterialChannel, error) {
	if config.ChannelID <= 0 {
		return nil, errors.New("material configuration requires an enabled channel")
	}
	var channel model.Channel
	if err := db.Where("id = ? AND status = ?", config.ChannelID, 1).First(&channel).Error; err != nil {
		return nil, errors.New("material configuration channel is missing or disabled")
	}
	if channel.ChannelInfo.IsMultiKey || config.ChannelFingerprint != "" && config.ChannelFingerprint != MobileCloudVideoChannelFingerprint(&channel) {
		return nil, ErrMobileCloudMaterialConfig
	}
	client, err := NewMobileCloudMaterialClient(config.Endpoint, config.AccessKey, config.SecretKey, config.PoolID)
	if err != nil {
		return nil, ErrMobileCloudMaterialConfig
	}
	return []MaterialChannel{{ChannelID: channel.Id, ChannelName: channel.Name, Provider: "mobile_cloud", Scope: MobileCloudMaterialScope(config, channel.Id), Client: client}}, nil
}

func materialRejectionMessage(err error) (string, bool) {
	var mobileCloud *MobileCloudMaterialRejection
	if errors.As(err, &mobileCloud) {
		return mobileCloud.Message, true
	}
	var topenRouter *TopenRouterMaterialRejection
	if errors.As(err, &topenRouter) {
		return topenRouter.Message, true
	}
	return "", false
}

func materialOperationFailure(operation string, err error) string {
	message, rejected := materialRejectionMessage(err)
	if !rejected {
		return operation + "; result could not be confirmed, retry to check the existing result"
	}
	reason := types.NewError(errors.New(operation), types.ErrorCodeBadResponse,
		types.ErrOptionWithUpstreamResponse(types.OpenAIError{Message: message})).ToOpenAIRelayError().Message
	reason = strings.Join(strings.Fields(reason), " ")
	return operation + ": " + string([]rune(reason)[:min(512, utf8.RuneCountInString(reason))])
}

func materialChannelReason(id int, name, reason string) string {
	return fmt.Sprintf("Channel %d (%s): %s", id, name, reason)
}

// Recovery requires a complete, consistent listing. Incomplete pages, duplicate
// IDs and changing totals must never be interpreted as proof of absence.
func listMaterialGroups(ctx context.Context, provider MaterialProvider, query MobileCloudGroupQuery) ([]MobileCloudAssetGroup, error) {
	var result []MobileCloudAssetGroup
	seen := make(map[string]bool)
	var total int64 = -1
	for pageNo := int32(1); pageNo <= 1000; pageNo++ {
		query.PageNo, query.PageSize = &pageNo, common.GetPointer(int32(100))
		page, err := provider.ListAssetGroups(ctx, query)
		if err != nil || page == nil {
			return nil, errors.New("material group listing could not be confirmed")
		}
		if page.Total < 0 || total >= 0 && page.Total != total {
			return nil, errors.New("material group listing changed during recovery")
		}
		total = page.Total
		for _, item := range page.Data {
			if validateMobileCloudMaterialID(item.ID) != nil || seen[item.ID] {
				return nil, errors.New("material group listing is ambiguous")
			}
			seen[item.ID] = true
			result = append(result, item)
		}
		if int64(len(result)) == total {
			return result, nil
		}
		if len(page.Data) == 0 || int64(len(result)) > total {
			return nil, errors.New("material group listing is incomplete")
		}
	}
	return nil, errors.New("material group listing exceeds recovery limit")
}

func listMaterialAssets(ctx context.Context, provider MaterialProvider, query MobileCloudAssetQuery) ([]MobileCloudAsset, error) {
	var result []MobileCloudAsset
	seen := make(map[string]bool)
	var total int64 = -1
	for pageNo := int32(1); pageNo <= 1000; pageNo++ {
		query.PageNo, query.PageSize = &pageNo, common.GetPointer(int32(100))
		page, err := provider.ListAssets(ctx, query)
		if err != nil || page == nil {
			return nil, errors.New("material listing could not be confirmed")
		}
		if page.Total < 0 || total >= 0 && page.Total != total {
			return nil, errors.New("material listing changed during recovery")
		}
		total = page.Total
		for _, item := range page.Data {
			if validateMobileCloudMaterialID(item.ID) != nil || seen[item.ID] {
				return nil, errors.New("material listing is ambiguous")
			}
			seen[item.ID] = true
			result = append(result, item)
		}
		if int64(len(result)) == total {
			return result, nil
		}
		if len(page.Data) == 0 || int64(len(result)) > total {
			return nil, errors.New("material listing is incomplete")
		}
	}
	return nil, errors.New("material listing exceeds recovery limit")
}

func SyncMaterialGroup(ctx context.Context, group *model.MaterialGroup, channels []MaterialChannel) error {
	db := model.DB.WithContext(ctx)
	if err := db.First(group, group.Id).Error; err != nil {
		return err
	}
	if group.Deleting {
		var count int64
		if err := db.Model(&model.Material{}).Where("group_id = ?", group.Id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return db.Model(group).Updates(map[string]any{"status": "failed", "fail_reason": "Material group is not empty; delete its materials first", "updated_time": common.GetTimestamp()}).Error
		}
	}
	var existing []model.MaterialGroupBinding
	if err := db.Where("group_id = ?", group.Id).Order("id").Find(&existing).Error; err != nil {
		return err
	}
	targets := make(map[string]MaterialChannel, len(channels))
	for _, channel := range channels {
		targets[channel.Scope] = channel
		if group.Deleting {
			continue
		}
		stale := false
		for _, binding := range existing {
			if binding.State != "deleted" && binding.Scope != channel.Scope && (binding.ChannelID == channel.ChannelID || binding.ChannelID == 0) {
				stale = true
			}
		}
		if stale {
			continue
		}
		binding := model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: channel.Scope, ChannelID: channel.ChannelID, ChannelName: channel.ChannelName, Provider: channel.Provider, Name: group.Name, State: "pending"}
		if err := model.EnsureMaterialGroupChannelBinding(db, &binding); err != nil {
			return err
		}
	}
	var bindings []model.MaterialGroupBinding
	if err := db.Where("group_id = ?", group.Id).Order("id").Find(&bindings).Error; err != nil {
		return err
	}
	var failures []error
	for i := range bindings {
		binding := &bindings[i]
		if binding.State == "deleted" {
			continue
		}
		channel, ok := targets[binding.Scope]
		if !ok || channel.Client == nil {
			continue
		}
		if err := syncMaterialGroupBinding(ctx, group, binding, channel); err != nil {
			failures = append(failures, err)
		}
	}
	if err := db.Where("group_id = ?", group.Id).Order("id").Find(&bindings).Error; err != nil {
		return err
	}
	status, reason, allDeleted := "ready", "", group.Deleting
	for _, binding := range bindings {
		if binding.State == "deleted" {
			continue
		}
		allDeleted = false
		channel, ok := targets[binding.Scope]
		if !ok || channel.Client == nil {
			status = "failed"
			if reason == "" {
				reason = materialChannelReason(binding.ChannelID, binding.ChannelName, "Original material configuration is unavailable; restore the original configuration to synchronize or delete")
			}
			continue
		}
		if binding.FailReason != "" {
			status = "failed"
			if reason == "" {
				reason = materialChannelReason(binding.ChannelID, binding.ChannelName, binding.FailReason)
			}
		} else if status != "failed" && (binding.State != "ready" || binding.Name != group.Name || group.Deleting) {
			status = "processing"
		}
	}
	if !group.Deleting && len(bindings) == 0 {
		status, reason = "failed", "Material channel configuration is unavailable"
	}
	if allDeleted && len(failures) == 0 {
		return model.FinalizeMaterialGroupDeletion(db, group)
	}
	if err := model.UpdateMaterialGroupSyncStatus(db, group, status, reason); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func syncMaterialGroupBinding(ctx context.Context, group *model.MaterialGroup, binding *model.MaterialGroupBinding, channel MaterialChannel) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Second)
	defer persistCancel()
	db, owner := model.DB.WithContext(persistCtx), common.GetUUID()
	claimed, err := model.ClaimMaterialGroupBinding(db, binding, owner)
	if err != nil || !claimed {
		return err
	}
	defer db.Model(&model.MaterialGroupBinding{}).Where("id = ? AND locked_by = ?", binding.Id, owner).Updates(map[string]any{"locked_by": "", "locked_until": 0})
	// The deletion flag/name may have changed before the lease was acquired.
	if err := db.First(group, group.Id).Error; err != nil {
		return err
	}
	binding.FailReason = ""
	if binding.UpstreamID == "" && (binding.State == "creating" || binding.State == "uncertain") {
		if binding.CreationToken == "" {
			binding.FailReason = "Material group creation has no ownership marker; administrator review is required before recovery or deletion"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		items, err := listMaterialGroups(ctx, channel.Client, MobileCloudGroupQuery{GroupType: common.GetPointer("AIGC"), Name: &binding.Name})
		if err != nil {
			binding.FailReason = "Material group creation result could not be confirmed; retry to check without creating again"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		for _, item := range items {
			if item.Name != binding.Name || item.Type != "AIGC" || item.Description != binding.CreationToken {
				continue
			}
			if binding.UpstreamID != "" {
				binding.UpstreamID = ""
				binding.FailReason = "Multiple matching material groups require administrator review"
				return model.SaveMaterialGroupBinding(db, binding, owner)
			}
			binding.UpstreamID = item.ID
		}
		if binding.UpstreamID == "" && !group.Deleting {
			binding.FailReason = "Material group creation result is unknown; retry to check without creating again"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		binding.State = "ready"
		if err := model.SaveMaterialGroupBinding(db, binding, owner); err != nil {
			return err
		}
	}
	if group.Deleting {
		return deleteMaterialGroupBinding(ctx, db, binding, owner, channel.Client)
	}
	if binding.UpstreamID == "" {
		if binding.State == "rejected" && group.Status != "pending" {
			binding.FailReason = "Material group creation was rejected; retry the group to create again"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		binding.Name, binding.State = group.Name, "creating"
		if binding.CreationToken == "" {
			binding.CreationToken = "new-api-material-group:" + common.GetUUID()
		}
		if err := model.SaveMaterialGroupBinding(db, binding, owner); err != nil {
			return err
		}
		created, err := channel.Client.CreateAssetGroup(ctx, binding.Name, nil)
		if err != nil || created == nil || validateMobileCloudMaterialID(created.ID) != nil || created.Type != "AIGC" {
			if _, rejected := materialRejectionMessage(err); rejected {
				binding.State = "rejected"
			}
			binding.FailReason = materialOperationFailure("Material group creation failed", err)
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		binding.UpstreamID, binding.State = created.ID, "ready"
		if err := model.SaveMaterialGroupBinding(db, binding, owner); err != nil {
			return err
		}
	}
	if binding.Name == group.Name && binding.State == "ready" {
		return model.SaveMaterialGroupBinding(db, binding, owner)
	}
	if binding.State == "updating" || binding.State == "failed" {
		remote, err := channel.Client.GetAssetGroup(ctx, binding.UpstreamID)
		if err != nil || remote == nil || remote.ID != binding.UpstreamID || remote.Type != "AIGC" {
			binding.FailReason = materialOperationFailure("Material group rename could not be confirmed", err)
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		if remote.Name == group.Name {
			binding.Name, binding.State = remote.Name, "ready"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
	}
	binding.State = "updating"
	if err := model.SaveMaterialGroupBinding(db, binding, owner); err != nil {
		return err
	}
	remote, err := channel.Client.UpdateAssetGroup(ctx, binding.UpstreamID, MobileCloudGroupUpdate{Name: &group.Name})
	if err != nil || remote == nil || remote.ID != binding.UpstreamID || remote.Name != group.Name || remote.Type != "AIGC" {
		binding.FailReason = materialOperationFailure("Material group rename failed", err)
		return model.SaveMaterialGroupBinding(db, binding, owner)
	}
	binding.Name, binding.State = group.Name, "ready"
	return model.SaveMaterialGroupBinding(db, binding, owner)
}

func deleteMaterialGroupBinding(ctx context.Context, db *gorm.DB, binding *model.MaterialGroupBinding, owner string, provider MaterialManagementProvider) error {
	if binding.UpstreamID == "" {
		binding.State = "deleted"
		return model.SaveMaterialGroupBinding(db, binding, owner)
	}
	if binding.State == "deleting" {
		items, err := listMaterialGroups(ctx, provider, MobileCloudGroupQuery{GroupType: common.GetPointer("AIGC"), GroupIDs: []string{binding.UpstreamID}})
		if err != nil {
			binding.FailReason = "Material group deletion result could not be confirmed; retry to check before deleting again"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
		found := false
		for _, item := range items {
			if item.ID == binding.UpstreamID {
				found = true
			}
		}
		if !found {
			binding.State = "deleted"
			return model.SaveMaterialGroupBinding(db, binding, owner)
		}
	}
	items, err := listMaterialAssets(ctx, provider, MobileCloudAssetQuery{GroupType: common.GetPointer("AIGC"), GroupIDs: []string{binding.UpstreamID}})
	if err != nil {
		binding.FailReason = "Material group contents could not be confirmed; deletion was not attempted"
		return model.SaveMaterialGroupBinding(db, binding, owner)
	}
	if len(items) > 0 {
		binding.FailReason = "Upstream material group is not empty; delete its assets first"
		return model.SaveMaterialGroupBinding(db, binding, owner)
	}
	binding.State = "deleting"
	if err := model.SaveMaterialGroupBinding(db, binding, owner); err != nil {
		return err
	}
	deleted, err := provider.DeleteAssetGroup(ctx, binding.UpstreamID)
	if err != nil || !deleted {
		binding.FailReason = materialOperationFailure("Material group deletion failed", err)
		return model.SaveMaterialGroupBinding(db, binding, owner)
	}
	binding.State = "deleted"
	return model.SaveMaterialGroupBinding(db, binding, owner)
}

func SyncMaterial(ctx context.Context, material *model.Material, channels []MaterialChannel) error {
	db := model.DB.WithContext(ctx)
	if err := db.First(material, material.Id).Error; err != nil {
		return err
	}
	if material.Deleting {
		return deleteMaterial(ctx, material, channels)
	}
	var existing []model.MaterialBinding
	if err := db.Where("material_id = ? AND version = ? AND state NOT IN ?", material.Id, model.MaterialSourceVersion(material), []string{"deleted", "retired"}).Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	targets := make(map[string]MaterialChannel, len(channels))
	for _, channel := range channels {
		targets[channel.Scope] = channel
	}
	recheckOnly := material.Status == model.MaterialStatusPending && slices.ContainsFunc(existing, model.MaterialBinding.CanRecheckResponse)
	channels = make([]MaterialChannel, 0, len(existing))
	for _, binding := range existing {
		if recheckOnly && !binding.CanRecheckResponse() {
			continue
		}
		channel, ok := targets[binding.Scope]
		if !ok {
			channel = MaterialChannel{ChannelID: binding.ChannelID, ChannelName: binding.ChannelName, Provider: binding.Provider, Scope: binding.Scope}
		}
		channels = append(channels, channel)
	}
	bindings := make([]model.MaterialBinding, 0, len(channels))
	for _, channel := range channels {
		bindings = append(bindings, model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(material), Scope: channel.Scope, ChannelID: channel.ChannelID, ChannelName: channel.ChannelName, Provider: channel.Provider, Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: common.GetTimestamp(), UpdatedTime: common.GetTimestamp()})
	}
	if err := model.EnsureMaterialChannelBindings(db, material, bindings); err != nil {
		return err
	}
	var failures []error
	if material.GroupId != nil && !recheckOnly {
		var group model.MaterialGroup
		if err := db.Where("id = ? AND user_id = ?", *material.GroupId, material.UserId).First(&group).Error; err != nil {
			return err
		}
		if err := SyncMaterialGroup(ctx, &group, channels); err != nil {
			failures = append(failures, err)
		}
	}
	// Keep the retry request snapshot for every channel. Aggregating an earlier
	// target must not consume another target's explicit pending/retry request.
	retryStatus := material.Status
	unavailable := ""
	for _, channel := range channels {
		material.Status = retryStatus
		if channel.Client == nil {
			unavailable = materialChannelReason(channel.ChannelID, channel.ChannelName, "Material channel configuration is unavailable; restore it and retry")
			failures = append(failures, errors.New(unavailable))
			continue
		}
		if err := PrepareMaterial(ctx, material, channel.Scope, channel.Client); err != nil {
			failures = append(failures, err)
		}
	}
	if err := aggregateMaterialPreparation(db, material); err != nil {
		failures = append(failures, err)
	}
	if unavailable != "" {
		if err := db.Model(material).Updates(map[string]any{"status": model.MaterialStatusFailed, "fail_reason": unavailable, "can_retry": true, "updated_time": common.GetTimestamp()}).Error; err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func deleteMaterial(ctx context.Context, material *model.Material, channels []MaterialChannel) error {
	db := model.DB.WithContext(ctx)
	targets := make(map[string]MaterialChannel, len(channels))
	for _, channel := range channels {
		targets[channel.Scope] = channel
	}
	var bindings []model.MaterialBinding
	if err := db.Where("material_id = ?", material.Id).Order("id").Find(&bindings).Error; err != nil {
		return err
	}
	var failures []error
	for i := range bindings {
		binding := &bindings[i]
		channel, ok := targets[binding.Scope]
		if binding.State == "deleted" || !ok || channel.Client == nil {
			continue
		}
		if err := deleteMaterialBinding(ctx, binding, channel.Client); err != nil {
			failures = append(failures, err)
		}
	}
	if err := db.Where("material_id = ?", material.Id).Order("id").Find(&bindings).Error; err != nil {
		return err
	}
	status, reason, allDeleted := "processing", "", true
	for _, binding := range bindings {
		if binding.State == "deleted" {
			continue
		}
		allDeleted = false
		channel, ok := targets[binding.Scope]
		failure := binding.FailReason
		if !ok || channel.Client == nil {
			failure = "Original material configuration is unavailable; restore the original configuration before deleting"
		}
		if failure != "" {
			status = "failed"
			if reason == "" {
				reason = materialChannelReason(binding.ChannelID, binding.ChannelName, failure)
			}
		}
	}
	if allDeleted && len(failures) == 0 {
		return model.FinalizeMaterialDeletion(db, material)
	}
	if err := db.Model(material).Updates(map[string]any{"status": status, "fail_reason": reason, "can_retry": status == "failed", "updated_time": common.GetTimestamp()}).Error; err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func deleteMaterialBinding(ctx context.Context, binding *model.MaterialBinding, provider MaterialManagementProvider) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Second)
	defer persistCancel()
	db, owner := model.DB.WithContext(persistCtx), common.GetUUID()
	claimed, err := model.ClaimMaterialDeletionBinding(db, binding, owner)
	if err != nil || !claimed {
		return err
	}
	defer db.Model(&model.MaterialBinding{}).Where("id = ? AND locked_by = ?", binding.Id, owner).Updates(map[string]any{"locked_by": "", "locked_until": 0})
	binding.FailReason, binding.CanRetry = "", false
	if binding.State == "deleted" {
		return nil
	}
	if binding.AssetID == "" && binding.State == "pending" {
		binding.State = "deleted"
		return model.SaveMaterialBinding(db, binding, owner)
	}
	// A rejected request or a never-uploaded group failure has no asset, but
	// querying by its stable name is still the conservative deletion recovery.
	if binding.AssetID == "" || binding.State == "deleting" {
		if binding.GroupID == "" {
			binding.State = "deleted" // uploads are only issued after GroupID is durable
			return model.SaveMaterialBinding(db, binding, owner)
		}
		query := MobileCloudAssetQuery{GroupType: common.GetPointer("AIGC"), GroupIDs: []string{binding.GroupID}}
		if binding.AssetID == "" {
			query.Name = &binding.Name
		}
		items, err := listMaterialAssets(ctx, provider, query)
		if err != nil {
			binding.FailReason, binding.CanRetry = "Material deletion result could not be confirmed; retry to check without uploading or deleting again", true
			return model.SaveMaterialBinding(db, binding, owner)
		}
		found := ""
		for _, item := range items {
			matches := item.ID == binding.AssetID && item.GroupID == binding.GroupID
			if binding.AssetID == "" {
				matches = item.Name == binding.Name && item.GroupID == binding.GroupID && item.Type == "Image"
			}
			if !matches {
				continue
			}
			if found != "" {
				binding.FailReason = "Multiple matching materials require administrator review before deletion"
				return model.SaveMaterialBinding(db, binding, owner)
			}
			found = item.ID
		}
		if found == "" {
			binding.State = "deleted"
			return model.SaveMaterialBinding(db, binding, owner)
		}
		binding.AssetID = found
	}
	binding.State = "deleting"
	if err := model.SaveMaterialBinding(db, binding, owner); err != nil {
		return err
	}
	deleted, err := provider.DeleteAsset(ctx, binding.AssetID)
	if err != nil || !deleted {
		binding.FailReason, binding.CanRetry = materialOperationFailure("Material deletion failed", err), true
		return model.SaveMaterialBinding(db, binding, owner)
	}
	binding.State = "deleted"
	return model.SaveMaterialBinding(db, binding, owner)
}
