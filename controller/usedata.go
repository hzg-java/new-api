package controller

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func getDashboardUsage(startTime, endTime int64, username string, userID int) ([]*model.QuotaData, error) {
	rows, err := getUsageRows(model.UsageLogFilter{Start: startTime, End: endTime, Username: username, UserID: userID}, true)
	if err != nil {
		return nil, err
	}
	result := make([]*model.QuotaData, 0, len(rows))
	for _, row := range rows {
		var metadata struct {
			RootInfo struct {
				NodeName string `json:"node_name"`
			} `json:"root_info"`
		}
		if row.Other != "" {
			_ = common.UnmarshalJsonStr(row.Other, &metadata)
		}
		result = append(result, &model.QuotaData{
			UserID: row.UserId, Username: row.Username, ModelName: row.ModelName, UseGroup: row.Group,
			CreatedAt: row.CreatedAt - row.CreatedAt%3600, TokenID: row.TokenId, ChannelID: row.ChannelId,
			Count: row.Count, Quota: row.Quota, TokenUsed: row.PromptTokens + row.CompletionTokens, NodeName: metadata.RootInfo.NodeName,
		})
	}
	return result, nil
}

func getDashboardQuotaDates(startTime, endTime int64, username string, userID int, byUser bool) ([]*model.QuotaData, error) {
	rows, err := getDashboardUsage(startTime, endTime, username, userID)
	if err != nil {
		return nil, err
	}
	type key struct {
		userID          int
		username, model string
		hour            int64
	}
	grouped := make(map[key]*model.QuotaData)
	for _, row := range rows {
		item := key{hour: row.CreatedAt}
		if byUser {
			item.username = row.Username
		} else {
			item.model = row.ModelName
			if username != "" || userID != 0 {
				item.userID, item.username = row.UserID, row.Username
			}
		}
		if grouped[item] == nil {
			grouped[item] = &model.QuotaData{UserID: item.userID, Username: item.username, ModelName: item.model, CreatedAt: item.hour}
		}
		total := grouped[item]
		total.Count += row.Count
		total.Quota += row.Quota
		total.TokenUsed += row.TokenUsed
	}
	result := make([]*model.QuotaData, 0, len(grouped))
	for _, row := range grouped {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt != result[j].CreatedAt {
			return result[i].CreatedAt < result[j].CreatedAt
		}
		if result[i].Username != result[j].Username {
			return result[i].Username < result[j].Username
		}
		return result[i].ModelName < result[j].ModelName
	})
	return result, nil
}

func getDashboardFlowDates(startTime, endTime int64, username string, userID, role int) ([]*model.FlowQuotaData, error) {
	rows, err := getDashboardUsage(startTime, endTime, username, userID)
	if err != nil {
		return nil, err
	}
	type key struct {
		userID                       int
		username, node, group, model string
		tokenID, channelID           int
	}
	grouped := make(map[key]*model.FlowQuotaData)
	for _, row := range rows {
		item := key{group: row.UseGroup, model: row.ModelName}
		switch {
		case role >= common.RoleRootUser:
			item.userID, item.username, item.node, item.tokenID, item.channelID = row.UserID, row.Username, row.NodeName, row.TokenID, row.ChannelID
		case role >= common.RoleAdminUser:
			item.userID, item.username, item.channelID = row.UserID, row.Username, row.ChannelID
		default:
			item.tokenID = row.TokenID
		}
		if grouped[item] == nil {
			grouped[item] = &model.FlowQuotaData{UserID: item.userID, Username: item.username, NodeName: item.node, UseGroup: item.group, ModelName: item.model, TokenID: item.tokenID, ChannelID: item.channelID}
		}
		total := grouped[item]
		total.Count += row.Count
		total.Quota += row.Quota
		total.TokenUsed += row.TokenUsed
	}
	result := make([]*model.FlowQuotaData, 0, len(grouped))
	for _, row := range grouped {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Quota != result[j].Quota {
			return result[i].Quota > result[j].Quota
		}
		if result[i].ModelName != result[j].ModelName {
			return result[i].ModelName < result[j].ModelName
		}
		return result[i].UserID < result[j].UserID
	})
	if err := model.FillFlowNames(result, role); err != nil {
		return nil, err
	}
	return result, nil
}

func parseDashboardTimeRange(c *gin.Context) (int64, int64, bool) {
	startTimestamp, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid start_timestamp")
		return 0, 0, false
	}
	endTimestamp, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid end_timestamp")
		return 0, 0, false
	}
	if endTimestamp < startTimestamp {
		common.ApiErrorMsg(c, "invalid time range")
		return 0, 0, false
	}
	return startTimestamp, endTimestamp, true
}

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseDashboardTimeRange(c)
	if !ok {
		return
	}
	username := c.Query("username")
	dates, err := getDashboardQuotaDates(startTimestamp, endTimestamp, username, 0, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseDashboardTimeRange(c)
	if !ok {
		return
	}
	dates, err := getDashboardQuotaDates(startTimestamp, endTimestamp, "", 0, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, endTimestamp, ok := parseDashboardTimeRange(c)
	if !ok {
		return
	}
	// 判断时间跨度是否超过 1 个月
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := getDashboardQuotaDates(startTimestamp, endTimestamp, "", userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetAllFlowQuotaDates(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseDashboardTimeRange(c)
	if !ok {
		return
	}
	username := c.Query("username")
	dates, err := getDashboardFlowDates(startTimestamp, endTimestamp, username, 0, c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetUserFlowQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, endTimestamp, ok := parseDashboardTimeRange(c)
	if !ok {
		return
	}
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := getDashboardFlowDates(startTimestamp, endTimestamp, "", userId, common.RoleCommonUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}
