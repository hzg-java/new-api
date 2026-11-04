package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// capabilityCell is one grid entry returned to the admin matrix. Cells the admin
// has never edited are synthesized as pending so the matrix always shows the full
// boundary × capability grid (design spec §7/§9).
type capabilityCell struct {
	Id               int    `json:"id"`
	AccessBoundary   string `json:"access_boundary"`
	Capability       string `json:"capability"`
	Status           string `json:"status"`
	VerificationNote string `json:"verification_note"`
	DocBasis         string `json:"doc_basis"`
	VerifiedTime     int64  `json:"verified_time"`
	UpdatedTime      int64  `json:"updated_time"`
}

// ListMaterialCapabilities returns the full capability matrix. This is admin
// configuration data; it is never an auto-detected provider probe result.
func ListMaterialCapabilities(c *gin.Context) {
	stored, err := model.ListMaterialCapabilities()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	byKey := make(map[string]*model.MaterialCapability, len(stored))
	for _, record := range stored {
		byKey[record.AccessBoundary+"\x00"+record.Capability] = record
	}
	boundaries := model.AllAccessBoundaries()
	caps := model.AllMaterialCapabilities()
	grid := make([]capabilityCell, 0, len(boundaries)*len(caps))
	for _, boundary := range boundaries {
		for _, capability := range caps {
			cell := capabilityCell{
				AccessBoundary: boundary,
				Capability:     capability,
				Status:         model.CapabilityStatusPending,
			}
			if record := byKey[boundary+"\x00"+capability]; record != nil {
				cell.Id = record.Id
				cell.Status = record.Status
				cell.VerificationNote = record.VerificationNote
				cell.DocBasis = record.DocBasis
				cell.VerifiedTime = record.VerifiedTime
				cell.UpdatedTime = record.UpdatedTime
			}
			grid = append(grid, cell)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"message":      "",
		"data":         grid,
		"boundaries":   boundaries,
		"capabilities": caps,
	})
}

type upsertCapabilityRequest struct {
	AccessBoundary   string `json:"access_boundary"`
	Capability       string `json:"capability"`
	Status           string `json:"status"`
	VerificationNote string `json:"verification_note"`
	DocBasis         string `json:"doc_basis"`
	VerifiedTime     int64  `json:"verified_time"`
}

// UpsertMaterialCapability records an admin's verification status for one matrix
// cell. Marking a cell verified requires a documentation basis and verification
// time, enforced in the model layer.
func UpsertMaterialCapability(c *gin.Context) {
	var request upsertCapabilityRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	now := common.GetTimestamp()
	record := model.MaterialCapability{
		AccessBoundary:   request.AccessBoundary,
		Capability:       request.Capability,
		Status:           request.Status,
		VerificationNote: request.VerificationNote,
		DocBasis:         request.DocBasis,
		VerifiedTime:     request.VerifiedTime,
		CreatedTime:      now,
		UpdatedTime:      now,
	}
	if err := model.ValidateMaterialCapabilityCell(&record); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.UpsertMaterialCapability(&record); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, record)
}
