package controller

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetTopenRouterMaterialConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, err := service.GetTopenRouterMaterialConfigView()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": service.ErrTopenRouterMaterialConfig.Error()})
		return
	}
	common.ApiSuccess(c, view)
}

func UpdateTopenRouterMaterialConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"success": false, "message": "application/json is required"})
		return
	}
	const maxBody = 16 << 10
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
	var input service.TopenRouterMaterialConfiguration
	if err != nil || len(body) > maxBody || common.Unmarshal(body, &input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid TopenRouter configuration request"})
		return
	}
	if err := service.SaveTopenRouterMaterialConfiguration(input); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, service.ErrTopenRouterMaterialInput) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	GetTopenRouterMaterialConfig(c)
}
