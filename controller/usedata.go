package controller

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type dashboardBucket struct {
	userID                 int
	username, model, group string
	hour                   int64
	tokenID, channelID     int
}

func dashboardUsageKey(row *model.QuotaData) dashboardBucket {
	return dashboardBucket{row.UserID, row.Username, row.ModelName, row.UseGroup, row.CreatedAt, row.TokenID, row.ChannelID}
}

func getDashboardUsage(startTime, endTime int64, username string, userID int) ([]*model.QuotaData, error) {
	persisted, ordinary, logs, err := model.GetDashboardUsageSource(startTime, endTime, username, userID)
	if err != nil {
		return nil, err
	}
	history, err := model.GetDashboardTaskHistory(logs)
	if err != nil {
		return nil, err
	}
	type taskOwner struct {
		id     string
		userID int
	}
	type taskDetails struct {
		TaskID           string `json:"task_id"`
		ActualQuota      *int   `json:"actual_quota"`
		PreConsumedQuota *int   `json:"pre_consumed_quota"`
		Pending          bool   `json:"task_billing_pending"`
	}
	initial := make(map[taskOwner]*model.Log)
	for _, log := range history {
		var detail taskDetails
		if common.UnmarshalJsonStr(log.Other, &detail) != nil || log.Type != model.LogTypeConsume || detail.ActualQuota != nil || detail.PreConsumedQuota != nil {
			continue
		}
		key := taskOwner{detail.TaskID, log.UserId}
		if prior := initial[key]; prior == nil || log.CreatedAt < prior.CreatedAt || log.CreatedAt == prior.CreatedAt && log.Id < prior.Id {
			initial[key] = log
		}
	}
	projectedCopies := make([]*model.Log, 0, len(history))
	for _, log := range history {
		copy := *log
		projectedCopies = append(projectedCopies, &copy)
	}
	projected, err := projectVideoTaskLogs(projectedCopies, true)
	if err != nil {
		return nil, err
	}
	videoOwners := make(map[taskOwner]bool)
	for _, log := range projected {
		var detail taskDetails
		if common.UnmarshalJsonStr(log.Other, &detail) == nil && log.VideoTask {
			videoOwners[taskOwner{detail.TaskID, log.UserId}] = true
		}
	}
	base := make(map[dashboardBucket][]*model.QuotaData)
	raw := make(map[dashboardBucket]*model.QuotaData)
	ordinaryByBucket := make(map[dashboardBucket]*model.QuotaData)
	for _, row := range persisted {
		key := dashboardUsageKey(row)
		base[key] = append(base[key], row)
	}
	for _, row := range ordinary {
		key := dashboardUsageKey(row)
		ordinaryByBucket[key] = row
		copy := *row
		raw[key] = &copy
	}
	for _, log := range history {
		if log.Type != model.LogTypeConsume {
			continue
		}
		hour := log.CreatedAt - log.CreatedAt%3600
		if hour < startTime || hour > endTime {
			continue
		}
		row := &model.QuotaData{UserID: log.UserId, Username: log.Username, ModelName: log.ModelName, UseGroup: log.Group, CreatedAt: hour, TokenID: log.TokenId, ChannelID: log.ChannelId}
		key := dashboardUsageKey(row)
		if raw[key] == nil {
			raw[key] = row
		}
		raw[key].Count++
		raw[key].Quota += log.Quota
		raw[key].TokenUsed += log.PromptTokens + log.CompletionTokens
	}
	verified := make(map[dashboardBucket]bool)
	for key, rows := range base {
		var count, quota, tokens int
		for _, row := range rows {
			count += row.Count
			quota += row.Quota
			tokens += row.TokenUsed
		}
		actual := raw[key]
		verified[key] = len(rows) == 1 && actual != nil && actual.Count == count && actual.Quota == quota && actual.TokenUsed == tokens
	}
	blocked := make(map[taskOwner]bool)
	ownerBuckets := make(map[taskOwner][]dashboardBucket)
	for _, log := range history {
		var identity struct {
			TaskID string `json:"task_id"`
		}
		if common.UnmarshalJsonStr(log.Other, &identity) != nil {
			continue
		}
		owner := taskOwner{identity.TaskID, log.UserId}
		hour := log.CreatedAt - log.CreatedAt%3600
		key := dashboardBucket{log.UserId, log.Username, log.ModelName, log.Group, hour, log.TokenId, log.ChannelId}
		if hour >= startTime && hour <= endTime && log.Type == model.LogTypeConsume {
			ownerBuckets[owner] = append(ownerBuckets[owner], key)
			var detail taskDetails
			if !videoOwners[owner] || common.UnmarshalJsonStr(log.Other, &detail) != nil {
				verified[key] = false
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for owner, keys := range ownerBuckets {
			if blocked[owner] {
				continue
			}
			for _, key := range keys {
				if !verified[key] {
					blocked[owner] = true
					break
				}
			}
			if initial[owner] == nil {
				blocked[owner] = true
			}
			if blocked[owner] {
				for _, key := range keys {
					if verified[key] {
						verified[key] = false
						changed = true
					}
				}
			}
		}
	}
	out := make([]*model.QuotaData, 0, len(persisted))
	for key, rows := range base {
		if !verified[key] {
			for _, row := range rows {
				row.Unreconciled = true
				out = append(out, row)
			}
			continue
		}
		if ordinaryRow := ordinaryByBucket[key]; ordinaryRow != nil {
			copy := *ordinaryRow
			copy.NodeName = rows[0].NodeName
			out = append(out, &copy)
		}
	}
	for _, log := range projected {
		var detail taskDetails
		if common.UnmarshalJsonStr(log.Other, &detail) != nil {
			continue
		}
		owner := taskOwner{detail.TaskID, log.UserId}
		first := initial[owner]
		if blocked[owner] || first == nil {
			continue
		}
		hour := first.CreatedAt - first.CreatedAt%3600
		if hour < startTime || hour > endTime {
			continue
		}
		key := dashboardBucket{first.UserId, first.Username, first.ModelName, first.Group, hour, first.TokenId, first.ChannelId}
		if !verified[key] {
			continue
		}
		quota := log.Quota
		if detail.Pending {
			quota = 0
		}
		node := base[key][0].NodeName
		out = append(out, &model.QuotaData{UserID: first.UserId, Username: first.Username, ModelName: first.ModelName, CreatedAt: hour, UseGroup: first.Group, TokenID: first.TokenId, ChannelID: first.ChannelId, NodeName: node, Count: 1, Quota: quota, TokenUsed: log.PromptTokens + log.CompletionTokens})
	}
	return out, nil
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
		total.Unreconciled = total.Unreconciled || row.Unreconciled
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
		if row.UseGroup == "" {
			continue
		}
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
		total.Unreconciled = total.Unreconciled || row.Unreconciled
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

func parseFlowQuotaTimeRange(c *gin.Context) (int64, int64, bool) {
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
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
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
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
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
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
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
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
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
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
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
