package controller

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func userModelPricingTarget(c *gin.Context) (int, bool) {
	if c.GetInt("role") < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Root role required"})
		return 0, false
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid user ID"})
		return 0, false
	}
	return id, true
}

func GetUserModelPricing(c *gin.Context) {
	id, ok := userModelPricingTarget(c)
	if !ok {
		return
	}
	multipliers, err := model.GetUserModelPricing(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"success": false, "message": "Unable to load user model pricing"})
		return
	}
	common.ApiSuccess(c, gin.H{"multipliers": multipliers})
}

func PutUserModelPricing(c *gin.Context) {
	id, ok := userModelPricingTarget(c)
	if !ok {
		return
	}
	var request struct {
		Multipliers map[string]float64 `json:"multipliers"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Multipliers == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "A multipliers object with finite positive values is required"})
		return
	}
	for name, value := range request.Multipliers {
		if strings.TrimSpace(name) == "" || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Model names must be nonempty and multipliers finite and positive"})
			return
		}
	}
	// Validate the target on both operations.
	if _, err := model.GetUserModelPricing(id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"success": false, "message": "Unable to load user model pricing"})
		return
	}
	if err := model.ReplaceUserModelPricing(id, request.Multipliers); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"success": false, "message": "Unable to save user model pricing"})
		return
	}
	recordManageAudit(c, "user.model_pricing.update", map[string]any{"target_user_id": id, "model_count": len(request.Multipliers)})
	common.ApiSuccess(c, gin.H{"multipliers": request.Multipliers})
}
