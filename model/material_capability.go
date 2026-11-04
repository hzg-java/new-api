package model

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// Access boundaries distinguish upstream integration surfaces that must NOT be
// assumed to share resource spaces or material IDs (design spec §8/§9). The
// Millu gateway is not the same as Volcano direct access, and Mobile Cloud
// direct access is a separate boundary again.
const (
	AccessBoundaryMilluGateway   = "millu_gateway"
	AccessBoundaryVolcanoDirect  = "volcano_direct"
	AccessBoundaryMobileCloudDir = "mobile_cloud_direct"
)

// Capability keys enumerate the per-boundary abilities described in design spec
// §7. Each is admin-configured matrix data, never an auto-detected live result.
const (
	CapabilityVirtualGroupOps          = "virtual_group_ops"
	CapabilityVirtualMaterialOps       = "virtual_material_ops"
	CapabilityRealPersonSessionCreate  = "real_person_session_create"
	CapabilityRealPersonStatus         = "real_person_status"
	CapabilityRealPersonMaterialUpload = "real_person_material_register"
	CapabilityVideoReferenceFormat     = "video_reference_format"
)

// Capability verification status (design spec §7). Only "verified" capabilities
// may back an enabled model route; "pending" and "unsupported" may not.
const (
	CapabilityStatusVerified    = "verified"
	CapabilityStatusUnsupported = "unsupported"
	CapabilityStatusPending     = "pending"
)

// accessBoundaries is the fixed matrix row order.
var accessBoundaries = []string{
	AccessBoundaryMilluGateway,
	AccessBoundaryVolcanoDirect,
	AccessBoundaryMobileCloudDir,
}

// capabilities is the fixed matrix column order.
var capabilities = []string{
	CapabilityVirtualGroupOps,
	CapabilityVirtualMaterialOps,
	CapabilityRealPersonSessionCreate,
	CapabilityRealPersonStatus,
	CapabilityRealPersonMaterialUpload,
	CapabilityVideoReferenceFormat,
}

// AllAccessBoundaries returns the ordered access-boundary keys for the matrix.
func AllAccessBoundaries() []string {
	out := make([]string, len(accessBoundaries))
	copy(out, accessBoundaries)
	return out
}

// AllMaterialCapabilities returns the ordered capability keys for the matrix.
func AllMaterialCapabilities() []string {
	out := make([]string, len(capabilities))
	copy(out, capabilities)
	return out
}

func IsValidAccessBoundary(boundary string) bool {
	return containsString(accessBoundaries, boundary)
}

func IsValidMaterialCapability(capability string) bool {
	return containsString(capabilities, capability)
}

func IsValidCapabilityStatus(status string) bool {
	switch status {
	case CapabilityStatusVerified, CapabilityStatusUnsupported, CapabilityStatusPending:
		return true
	default:
		return false
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// MaterialCapability is one admin-configured cell of the channel capability
// matrix: the verification status of a capability on an access boundary, plus
// the human-recorded evidence. It is configuration data, not a probe result.
type MaterialCapability struct {
	Id               int    `json:"id"`
	AccessBoundary   string `json:"access_boundary" gorm:"type:varchar(32);uniqueIndex:idx_material_capability,priority:1"`
	Capability       string `json:"capability" gorm:"type:varchar(48);uniqueIndex:idx_material_capability,priority:2"`
	Status           string `json:"status" gorm:"type:varchar(16)"`
	VerificationNote string `json:"verification_note" gorm:"type:text"`
	DocBasis         string `json:"doc_basis" gorm:"type:text"`
	VerifiedTime     int64  `json:"verified_time" gorm:"bigint"`
	CreatedTime      int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime      int64  `json:"updated_time" gorm:"bigint"`
}

// GetMaterialCapability returns the stored cell for a boundary/capability pair,
// or nil when the admin has never edited it (meaning it defaults to pending).
func GetMaterialCapability(boundary string, capability string) (*MaterialCapability, error) {
	var record MaterialCapability
	err := DB.Where("access_boundary = ? AND capability = ?", boundary, capability).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// ListMaterialCapabilities returns every stored matrix cell.
func ListMaterialCapabilities() ([]*MaterialCapability, error) {
	var records []*MaterialCapability
	err := DB.Order("id ASC").Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}

// UpsertMaterialCapability creates or updates the cell for a boundary/capability
// pair. The caller supplies normalized, validated values.
func UpsertMaterialCapability(record *MaterialCapability) error {
	existing, err := GetMaterialCapability(record.AccessBoundary, record.Capability)
	if err != nil {
		return err
	}
	if existing == nil {
		return DB.Create(record).Error
	}
	record.Id = existing.Id
	record.CreatedTime = existing.CreatedTime
	return DB.Model(existing).
		Where("id = ?", existing.Id).
		Updates(map[string]any{
			"status":            record.Status,
			"verification_note": record.VerificationNote,
			"doc_basis":         record.DocBasis,
			"verified_time":     record.VerifiedTime,
			"updated_time":      record.UpdatedTime,
		}).Error
}

// IsMaterialCapabilityVerified reports whether a capability is verified on a
// boundary. A missing cell is treated as pending (not verified).
func IsMaterialCapabilityVerified(boundary string, capability string) (bool, error) {
	record, err := GetMaterialCapability(boundary, capability)
	if err != nil {
		return false, err
	}
	if record == nil {
		return false, nil
	}
	return record.Status == CapabilityStatusVerified, nil
}

// ValidateMaterialCapabilityCell normalizes and validates a matrix cell. Marking
// a capability verified requires recorded evidence (doc basis and verified time)
// so the matrix cannot claim verification without support.
func ValidateMaterialCapabilityCell(record *MaterialCapability) error {
	record.AccessBoundary = strings.TrimSpace(record.AccessBoundary)
	record.Capability = strings.TrimSpace(record.Capability)
	if !IsValidAccessBoundary(record.AccessBoundary) {
		return errors.New("invalid access boundary")
	}
	if !IsValidMaterialCapability(record.Capability) {
		return errors.New("invalid capability")
	}
	record.Status = strings.TrimSpace(record.Status)
	if !IsValidCapabilityStatus(record.Status) {
		return errors.New("invalid capability status")
	}
	record.VerificationNote = strings.TrimSpace(record.VerificationNote)
	record.DocBasis = strings.TrimSpace(record.DocBasis)
	if record.Status == CapabilityStatusVerified {
		if record.DocBasis == "" {
			return errors.New("verified capability requires a documentation basis")
		}
		if record.VerifiedTime <= 0 {
			return errors.New("verified capability requires a verification time")
		}
	} else {
		record.VerifiedTime = 0
	}
	return nil
}
