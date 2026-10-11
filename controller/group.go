package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]any)
	userGroup := ""
	isAdmin := false
	// UserAuth/TryUserAuth populate group and role from the validated user.
	// The anonymous route has neither; do not query a nonexistent user ID.
	if c.GetInt("id") > 0 {
		userGroup = c.GetString("group")
		isAdmin = c.GetInt("role") >= common.RoleAdminUser
	}
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]any{"desc": desc}
			if isAdmin {
				usableGroups[groupName]["ratio"] = service.GetUserGroupRatio(userGroup, groupName)
			}
		}
	}
	if _, ok := userUsableGroups["auto"]; ok {
		usableGroups["auto"] = map[string]any{
			"desc": setting.GetUsableGroupDescription("auto"),
		}
		if isAdmin {
			usableGroups["auto"]["ratio"] = "自动"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
