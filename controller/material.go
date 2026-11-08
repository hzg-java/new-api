package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterMaterialLibraryRoutes(apiRouter *gin.RouterGroup) {
	materialRoute := apiRouter.Group("/material")
	materialRoute.Use(middleware.DisableCache(), middleware.AdminAuth())
	{
		materialRoute.GET("/list", ListMaterials)
		materialRoute.GET("/groups", ListMaterialGroups)
		materialRoute.POST("/groups", CreateMaterialGroup)
		materialRoute.PUT("/groups/:id", RenameMaterialGroup)
		materialRoute.PUT("/:id/group", UpdateMaterialGroup)
		materialRoute.GET("/:id", GetMaterial)
		materialRoute.POST("/", AddMaterial)
		materialRoute.POST("/:id/retry", RetryMaterial)
		materialRoute.DELETE("/:id", DeleteMaterial)
	}
	materialAdminRoute := apiRouter.Group("/material")
	materialAdminRoute.Use(middleware.DisableCache(), middleware.AdminAuth())
	materialAdminRoute.GET("/mobile-cloud/config", GetMobileCloudMaterialConfig)
	materialAdminRoute.PUT("/mobile-cloud/config", UpdateMobileCloudMaterialConfig)
	materialAdminRoute.POST("/mobile-cloud/test", TestMobileCloudMaterialIntegration)
	materialAdminRoute.GET("/topenrouter/config", GetTopenRouterMaterialConfig)
	materialAdminRoute.PUT("/topenrouter/config", UpdateTopenRouterMaterialConfig)
	materialAdminRoute.POST("/topenrouter/test", TestTopenRouterMaterialIntegration)
	materialAdminRoute.GET("/all", ListAllMaterials)
	ownerRoute := materialAdminRoute.Group("/admin/users/:user_id")
	{
		ownerRoute.POST("/materials", AdminAddMaterial)
		ownerRoute.GET("/materials/:id", AdminGetMaterial)
		ownerRoute.DELETE("/materials/:id", AdminDeleteMaterial)
		ownerRoute.POST("/materials/:id/retry", AdminRetryMaterial)
		ownerRoute.PUT("/materials/:id/group", AdminUpdateMaterialGroup)
		ownerRoute.GET("/groups", AdminListMaterialGroups)
		ownerRoute.POST("/groups", AdminCreateMaterialGroup)
		ownerRoute.PUT("/groups/:id", AdminRenameMaterialGroup)
		ownerRoute.DELETE("/groups/:id", AdminDeleteMaterialGroup)
		ownerRoute.POST("/groups/:id/retry", AdminRetryMaterialGroup)
	}
}

type addMaterialRequest struct {
	Name        string `json:"name"`
	LibraryType string `json:"library_type"`
	FileType    string `json:"file_type"`
	SourceURL   string `json:"source_url"`
	GroupId     *int   `json:"group_id"`
}

func AddMaterial(c *gin.Context) {
	addMaterial(c, c.GetInt("id"))
}

func AdminAddMaterial(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		addMaterial(c, ownerId)
	}
}

func addMaterial(c *gin.Context, ownerId int) {
	var request addMaterialRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if request.LibraryType == model.MaterialLibraryLivenessFace {
		common.ApiErrorMsg(c, "real-person material authentication is pending verification and not available yet")
		return
	}
	request.LibraryType = model.MaterialLibraryAIGC
	material := model.Material{
		UserId:      ownerId,
		Name:        request.Name,
		LibraryType: request.LibraryType,
		FileType:    request.FileType,
		SourceURL:   request.SourceURL,
		GroupId:     request.GroupId,
	}
	if err := material.ValidateForCreate(); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if material.FileType != model.MaterialFileImage {
		common.ApiErrorMsg(c, "only image materials are supported")
		return
	}
	if err := service.ValidateMaterialPreparationSource(material.SourceURL); err != nil {
		common.ApiErrorMsg(c, "material URL rejected by security policy")
		return
	}
	material.MaterialID = model.NewPlatformMaterialID()
	material.Status = model.MaterialStatusPending
	now := common.GetTimestamp()
	material.CreatedTime = now
	material.UpdatedTime = now
	if err := model.CreateMaterial(&material); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, material)
}

func materialListResponse(c *gin.Context, userId int) {
	filter := model.MaterialFilter{
		UserId:         userId,
		Keyword:        c.Query("keyword"),
		SourceURL:      c.Query("source_url"),
		UpstreamStatus: c.Query("upstream_status"),
		LibraryType:    c.Query("library_type"),
		Status:         c.Query("status"),
	}
	if raw, present := c.GetQuery("channel_id"); present {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material channel"})
			return
		}
		filter.ChannelID = id
	}
	if filter.UpstreamStatus != "" && filter.UpstreamStatus != "ACTIVE" && filter.UpstreamStatus != "PROCESSING" && filter.UpstreamStatus != "FAILED" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid upstream material status"})
		return
	}
	if filter.LibraryType != "" && !model.IsValidMaterialLibraryType(filter.LibraryType) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid library type"})
		return
	}
	if raw, present := c.GetQuery("group_id"); present {
		groupId, err := strconv.Atoi(raw)
		if err != nil || groupId < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material group"})
			return
		}
		filter.GroupId = &groupId
	}
	offset, limit, ok := parseMaterialPaging(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid paging parameters"})
		return
	}
	filter.Offset = offset
	filter.Limit = limit
	materials, total, err := model.SearchMaterials(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialUsernames(materials); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialBindings(materials); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    materials,
		"total":   total,
	})
}

// ListMaterials returns only the calling user's materials.
func ListMaterials(c *gin.Context) {
	materialListResponse(c, c.GetInt("id"))
}

func ListAllMaterials(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid query parameters"})
		return
	}
	ownerId := 0
	if raw, present := query["user_id"]; present {
		if len(raw) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material owner"})
			return
		}
		var ok bool
		ownerId, ok = validateMaterialOwner(c, raw[0])
		if !ok {
			return
		}
	}
	materialListResponse(c, ownerId)
}

// validateMaterialOwner validates the resource owner without changing the
// authenticated actor used by authorization and operation auditing.
func validateMaterialOwner(c *gin.Context, raw string) (int, bool) {
	ownerId, err := strconv.Atoi(raw)
	if err != nil || ownerId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material owner"})
		return 0, false
	}
	if _, err := model.GetUserById(ownerId, false); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "material owner not found"})
		} else {
			common.ApiError(c, err)
		}
		return 0, false
	}
	return ownerId, true
}

// GetMaterial returns a single material owned by the calling user.
func GetMaterial(c *gin.Context) {
	getMaterial(c, c.GetInt("id"))
}

func AdminGetMaterial(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		getMaterial(c, ownerId)
	}
}

func getMaterial(c *gin.Context, ownerId int) {
	id, ok := parseMaterialID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material id"})
		return
	}
	material, err := model.GetUserMaterial(ownerId, id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if material == nil {
		common.ApiErrorMsg(c, "material not found")
		return
	}
	if err := model.PopulateMaterialUsernames([]*model.Material{material}); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialBindings([]*model.Material{material}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, material)
}

// DeleteMaterial removes a material owned by the calling user.
func DeleteMaterial(c *gin.Context) {
	deleteMaterial(c, c.GetInt("id"))
}

func AdminDeleteMaterial(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		deleteMaterial(c, ownerId)
	}
}

func deleteMaterial(c *gin.Context, ownerId int) {
	id, ok := parseMaterialID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material id"})
		return
	}
	material, err := model.QueueMaterialDeletion(ownerId, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorMsg(c, "material not found")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialGroupNames([]*model.Material{material}); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialUsernames([]*model.Material{material}); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialBindings([]*model.Material{material}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, material)
}

func RetryMaterial(c *gin.Context) {
	retryMaterial(c, c.GetInt("id"))
}

func AdminRetryMaterial(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		retryMaterial(c, ownerId)
	}
}

func retryMaterial(c *gin.Context, ownerId int) {
	id, ok := parseMaterialID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material id"})
		return
	}
	_, err := model.RetryUserMaterial(ownerId, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorMsg(c, "material not found")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	getMaterial(c, ownerId)
}

func ListMaterialGroups(c *gin.Context) {
	listMaterialGroups(c, c.GetInt("id"))
}

func AdminListMaterialGroups(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		listMaterialGroups(c, ownerId)
	}
}

func listMaterialGroups(c *gin.Context, ownerId int) {
	groups, err := model.ListUserMaterialGroups(ownerId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, groups)
}

func CreateMaterialGroup(c *gin.Context) {
	createMaterialGroup(c, c.GetInt("id"))
}

func AdminCreateMaterialGroup(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		createMaterialGroup(c, ownerId)
	}
}

func createMaterialGroup(c *gin.Context, ownerId int) {
	var request struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	group := model.MaterialGroup{UserId: ownerId, Name: request.Name}
	if err := model.SaveMaterialGroup(&group); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialGroupBindings([]*model.MaterialGroup{&group}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, group)
}

func RenameMaterialGroup(c *gin.Context) {
	renameMaterialGroup(c, c.GetInt("id"))
}

func AdminRenameMaterialGroup(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		renameMaterialGroup(c, ownerId)
	}
}

func renameMaterialGroup(c *gin.Context, ownerId int) {
	id, ok := parseMaterialID(c)
	if !ok {
		common.ApiErrorMsg(c, "invalid material group")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	group := model.MaterialGroup{Id: id, UserId: ownerId, Name: request.Name}
	if err := model.SaveMaterialGroup(&group); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialGroupBindings([]*model.MaterialGroup{&group}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, group)
}

func AdminDeleteMaterialGroup(c *gin.Context) {
	ownerID, ok := validateMaterialOwner(c, c.Param("user_id"))
	if !ok {
		return
	}
	id, ok := parseMaterialID(c)
	if !ok {
		common.ApiErrorMsg(c, "invalid material group")
		return
	}
	group, err := model.QueueMaterialGroupDeletion(ownerID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorMsg(c, "material group not found")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialGroupBindings([]*model.MaterialGroup{group}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, group)
}

func AdminRetryMaterialGroup(c *gin.Context) {
	ownerID, ok := validateMaterialOwner(c, c.Param("user_id"))
	if !ok {
		return
	}
	id, ok := parseMaterialID(c)
	if !ok {
		common.ApiErrorMsg(c, "invalid material group")
		return
	}
	group, err := model.RetryMaterialGroup(ownerID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorMsg(c, "material group not found")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.PopulateMaterialGroupBindings([]*model.MaterialGroup{group}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, group)
}

func UpdateMaterialGroup(c *gin.Context) {
	updateMaterialGroup(c, c.GetInt("id"))
}

func AdminUpdateMaterialGroup(c *gin.Context) {
	if ownerId, ok := validateMaterialOwner(c, c.Param("user_id")); ok {
		updateMaterialGroup(c, ownerId)
	}
}

func updateMaterialGroup(c *gin.Context, ownerId int) {
	id, ok := parseMaterialID(c)
	if !ok {
		common.ApiErrorMsg(c, "invalid material id")
		return
	}
	var request struct {
		GroupId json.RawMessage `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || len(request.GroupId) == 0 {
		common.ApiErrorMsg(c, "group_id is required")
		return
	}
	var groupId *int
	if err := common.Unmarshal(request.GroupId, &groupId); err != nil {
		common.ApiErrorMsg(c, "invalid material group")
		return
	}
	material, err := model.SetUserMaterialGroup(ownerId, id, groupId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if material == nil {
		common.ApiErrorMsg(c, "material not found")
		return
	}
	common.ApiSuccess(c, material)
}

func parseMaterialID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func parseMaterialPaging(c *gin.Context) (offset int, limit int, ok bool) {
	offset = 0
	limit = 20
	if raw := c.Query("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, false
		}
		offset = parsed
	}
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 100 {
			return 0, 0, false
		}
		limit = parsed
	}
	return offset, limit, true
}
