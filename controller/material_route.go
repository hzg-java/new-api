package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type materialRouteRequest struct {
	Model           string `json:"model"`
	LibraryScope    string `json:"library_scope"`
	AccessBoundary  string `json:"access_boundary"`
	ChannelIds      []int  `json:"channel_ids"`
	FailoverAllowed bool   `json:"failover_allowed"`
	AuthPolicy      string `json:"auth_policy"`
	Enabled         bool   `json:"enabled"`
}

func (request materialRouteRequest) toRoute() *model.MaterialRoute {
	route := &model.MaterialRoute{
		Model:           request.Model,
		LibraryScope:    request.LibraryScope,
		AccessBoundary:  request.AccessBoundary,
		FailoverAllowed: request.FailoverAllowed,
		AuthPolicy:      request.AuthPolicy,
		Enabled:         request.Enabled,
	}
	route.SetChannelIDs(request.ChannelIds)
	return route
}

// ListMaterialRoutes returns admin model authentication routes.
func ListMaterialRoutes(c *gin.Context) {
	filter := model.MaterialRouteFilter{
		Keyword:        c.Query("keyword"),
		LibraryScope:   c.Query("library_scope"),
		AccessBoundary: c.Query("access_boundary"),
		Enabled:        c.Query("enabled"),
	}
	if filter.LibraryScope != "" && !model.IsValidMaterialLibraryType(filter.LibraryScope) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid library scope"})
		return
	}
	if filter.AccessBoundary != "" && !model.IsValidAccessBoundary(filter.AccessBoundary) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid access boundary"})
		return
	}
	offset, limit, ok := parseMaterialPaging(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid paging parameters"})
		return
	}
	filter.Offset = offset
	filter.Limit = limit
	routes, total, err := model.SearchMaterialRoutes(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    routes,
		"total":   total,
	})
}

// CreateMaterialRoute adds a platform route after validating its enable requirements.
func CreateMaterialRoute(c *gin.Context) {
	var request materialRouteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	route := request.toRoute()
	if err := model.ValidateMaterialRoute(route); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.CheckMaterialRouteEnable(route); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	now := common.GetTimestamp()
	route.CreatedTime = now
	route.UpdatedTime = now
	if err := model.CreateMaterialRoute(route); err != nil {
		common.ApiError(c, err)
		return
	}
	route.LoadChannelIDs()
	common.ApiSuccess(c, route)
}

// UpdateMaterialRoute edits an existing route, re-checking the enable gate.
func UpdateMaterialRoute(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid route id"})
		return
	}
	existing, err := model.GetMaterialRouteById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if existing == nil {
		common.ApiErrorMsg(c, "route not found")
		return
	}
	var request materialRouteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	route := request.toRoute()
	route.Id = id
	route.CreatedTime = existing.CreatedTime
	if err := model.ValidateMaterialRoute(route); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.CheckMaterialRouteEnable(route); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.UpdateMaterialRoute(route); err != nil {
		common.ApiError(c, err)
		return
	}
	route.LoadChannelIDs()
	common.ApiSuccess(c, route)
}

// DeleteMaterialRoute removes a route.
func DeleteMaterialRoute(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid route id"})
		return
	}
	deleted, err := model.DeleteMaterialRoute(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !deleted {
		common.ApiErrorMsg(c, "route not found")
		return
	}
	common.ApiSuccess(c, true)
}
