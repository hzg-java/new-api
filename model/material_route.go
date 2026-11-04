package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Authentication policies for a model route (design spec §6.1): authenticate on
// the channel actually used at request time, or pre-authenticate on a designated
// channel.
const (
	MaterialAuthPolicyOnDemand = "on_demand"
	MaterialAuthPolicyPreAuth  = "pre_auth"
)

func IsValidMaterialAuthPolicy(policy string) bool {
	return policy == MaterialAuthPolicyOnDemand || policy == MaterialAuthPolicyPreAuth
}

// RequiredCapabilitiesForScope lists the capability keys that must be verified on
// a route's access boundary before the route may be enabled for a library scope
// (design spec §6.2/§12.9).
func RequiredCapabilitiesForScope(scope string) []string {
	switch scope {
	case MaterialLibraryAIGC:
		return []string{CapabilityVirtualMaterialOps, CapabilityVideoReferenceFormat}
	case MaterialLibraryLivenessFace:
		return []string{
			CapabilityRealPersonMaterialUpload,
			CapabilityRealPersonSessionCreate,
			CapabilityVideoReferenceFormat,
		}
	default:
		return nil
	}
}

// MaterialRoute is platform configuration, not runtime generation routing.
type MaterialRoute struct {
	Id              int    `json:"id"`
	Model           string `json:"model" gorm:"type:varchar(255);index"`
	LibraryScope    string `json:"library_scope" gorm:"type:varchar(32);index"`
	AccessBoundary  string `json:"access_boundary" gorm:"type:varchar(32);index"`
	ChannelPriority string `json:"-" gorm:"type:text"`
	// ChannelIds is the ordered preferred-channel list exposed to the API. It is
	// not persisted directly; ChannelPriority holds the comma-separated form.
	ChannelIds      []int  `json:"channel_ids" gorm:"-"`
	FailoverAllowed bool   `json:"failover_allowed" gorm:"type:boolean"`
	AuthPolicy      string `json:"auth_policy" gorm:"type:varchar(32)"`
	Enabled         bool   `json:"enabled" gorm:"type:boolean;index"`
	CreatedTime     int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime     int64  `json:"updated_time" gorm:"bigint"`
}

// SetChannelIDs stores an ordered channel list as a comma-separated string.
func (route *MaterialRoute) SetChannelIDs(ids []int) {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			parts = append(parts, strconv.Itoa(id))
		}
	}
	route.ChannelPriority = strings.Join(parts, ",")
	route.ChannelIds = ids
}

// LoadChannelIDs parses the stored comma-separated channel list back into ids.
func (route *MaterialRoute) LoadChannelIDs() {
	route.ChannelIds = parseChannelPriority(route.ChannelPriority)
}

func parseChannelPriority(raw string) []int {
	ids := make([]int, 0)
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if id, err := strconv.Atoi(trimmed); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// ValidateMaterialRoute normalizes and validates route fields. It does not check
// the enable capability gate; see CheckMaterialRouteEnable for that.
func ValidateMaterialRoute(route *MaterialRoute) error {
	route.Model = strings.TrimSpace(route.Model)
	if route.Model == "" {
		return errors.New("model name is required")
	}
	if !IsValidMaterialLibraryType(route.LibraryScope) {
		return errors.New("invalid library scope")
	}
	if !IsValidAccessBoundary(route.AccessBoundary) {
		return errors.New("invalid access boundary")
	}
	if !IsValidMaterialAuthPolicy(route.AuthPolicy) {
		return errors.New("invalid authentication policy")
	}
	route.SetChannelIDs(route.ChannelIds)
	if route.FailoverAllowed && len(route.ChannelIds) < 2 {
		return errors.New("failover requires at least two preferred channels")
	}
	return nil
}

func CheckMaterialRouteEnable(route *MaterialRoute) error {
	if !route.Enabled {
		return nil
	}
	if route.AccessBoundary == AccessBoundaryMobileCloudDir && route.LibraryScope == MaterialLibraryAIGC {
		return nil
	}
	required := RequiredCapabilitiesForScope(route.LibraryScope)
	if len(required) == 0 {
		return errors.New("route library scope has no defined capabilities")
	}
	missing := make([]string, 0, len(required))
	for _, capability := range required {
		verified, err := IsMaterialCapabilityVerified(route.AccessBoundary, capability)
		if err != nil {
			return err
		}
		if !verified {
			missing = append(missing, capability)
		}
	}
	if len(missing) > 0 {
		return errors.New("cannot enable route: capabilities not verified on this access boundary: " + strings.Join(missing, ", "))
	}
	return nil
}

func CreateMaterialRoute(route *MaterialRoute) error {
	return DB.Create(route).Error
}

func GetMaterialRouteById(id int) (*MaterialRoute, error) {
	var route MaterialRoute
	err := DB.Where("id = ?", id).First(&route).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	route.LoadChannelIDs()
	return &route, nil
}

// MaterialRouteFilter narrows a route list query.
type MaterialRouteFilter struct {
	Keyword        string
	LibraryScope   string
	AccessBoundary string
	Enabled        string
	Offset         int
	Limit          int
}

func (f MaterialRouteFilter) scope(db *gorm.DB) *gorm.DB {
	if keyword := strings.TrimSpace(f.Keyword); keyword != "" {
		db = db.Where("model LIKE ?", "%"+keyword+"%")
	}
	if f.LibraryScope != "" {
		db = db.Where("library_scope = ?", f.LibraryScope)
	}
	if f.AccessBoundary != "" {
		db = db.Where("access_boundary = ?", f.AccessBoundary)
	}
	switch f.Enabled {
	case "true":
		db = db.Where("enabled = ?", true)
	case "false":
		db = db.Where("enabled = ?", false)
	}
	return db
}

// SearchMaterialRoutes returns a page of routes plus the total matching count.
func SearchMaterialRoutes(filter MaterialRouteFilter) ([]*MaterialRoute, int64, error) {
	var total int64
	if err := filter.scope(DB.Model(&MaterialRoute{})).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	var routes []*MaterialRoute
	err := filter.scope(DB.Model(&MaterialRoute{})).
		Order("id DESC").
		Offset(filter.Offset).
		Limit(filter.Limit).
		Find(&routes).Error
	if err != nil {
		return nil, 0, err
	}
	for _, route := range routes {
		route.LoadChannelIDs()
	}
	return routes, total, nil
}

// UpdateMaterialRoute applies editable fields to an existing route.
func UpdateMaterialRoute(route *MaterialRoute) error {
	now := common.GetTimestamp()
	return DB.Model(route).
		Where("id = ?", route.Id).
		Updates(map[string]any{
			"model":            route.Model,
			"library_scope":    route.LibraryScope,
			"access_boundary":  route.AccessBoundary,
			"channel_priority": route.ChannelPriority,
			"failover_allowed": route.FailoverAllowed,
			"auth_policy":      route.AuthPolicy,
			"enabled":          route.Enabled,
			"updated_time":     now,
		}).Error
}

// DeleteMaterialRoute removes a route and reports whether a row was deleted.
func DeleteMaterialRoute(id int) (bool, error) {
	result := DB.Where("id = ?", id).Delete(&MaterialRoute{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
