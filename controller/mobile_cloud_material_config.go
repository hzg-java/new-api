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

func TestMobileCloudMaterialIntegration(c *gin.Context) {
	testMaterialIntegration(c, "mobile-cloud")
}

func TestTopenRouterMaterialIntegration(c *gin.Context) {
	testMaterialIntegration(c, "topenrouter")
}

func testMaterialIntegration(c *gin.Context, provider string) {
	c.Header("Cache-Control", "no-store")
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"success": false, "message": "application/json is required"})
		return
	}
	const maxBody = 16 << 10
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid material test request"})
		return
	}
	input, err := service.ParseMaterialIntegrationTestRequest(provider, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	result, err := service.TestMaterialIntegration(c.Request.Context(), provider, *input)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, result)
}

func GetMobileCloudMaterialConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, err := service.GetMobileCloudMaterialConfigView()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": service.ErrMobileCloudMaterialConfig.Error()})
		return
	}
	common.ApiSuccess(c, view)
}

func UpdateMobileCloudMaterialConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"success": false, "message": "application/json is required"})
		return
	}
	const maxBody = 16 << 10
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
	var input service.MobileCloudMaterialConfiguration
	if err != nil || len(body) > maxBody || common.Unmarshal(body, &input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid mobile cloud configuration request"})
		return
	}
	if err := service.SaveMobileCloudMaterialConfiguration(input); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, service.ErrMobileCloudMaterialInput) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	GetMobileCloudMaterialConfig(c)
}
