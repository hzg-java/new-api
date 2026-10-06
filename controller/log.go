package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func enrichVideoTaskLogs(logs []*model.Log) error {
	_, err := projectVideoTaskLogs(logs, false)
	return err
}

// projectVideoTaskLogs changes display rows only. Dashboard grouping must run
// before pagination; token-key views retain their existing ungrouped shape.
func projectVideoTaskLogs(logs []*model.Log, consolidate bool) ([]*model.Log, error) {
	type taskOwner struct {
		id     string
		userID int
	}
	owners := make(map[*model.Log]taskOwner)
	var taskIDs []string
	seenIDs := make(map[string]bool)
	for _, log := range logs {
		if log.Type != model.LogTypeConsume && log.Type != model.LogTypeRefund {
			continue
		}
		var other struct {
			TaskID string `json:"task_id"`
		}
		if common.UnmarshalJsonStr(log.Other, &other) != nil || other.TaskID == "" {
			continue
		}
		owners[log] = taskOwner{other.TaskID, log.UserId}
		if !seenIDs[other.TaskID] {
			seenIDs[other.TaskID] = true
			taskIDs = append(taskIDs, other.TaskID)
		}
	}
	if len(taskIDs) == 0 {
		return logs, nil
	}
	var tasks []model.Task
	// Bound lookup parameters even when the filtered log history is large.
	for start := 0; start < len(taskIDs); start += 500 {
		var batch []model.Task
		if err := model.DB.Select("id", "task_id", "user_id", "action", "status", "quota", "submit_time", "finish_time", "data", "private_data").Where("task_id IN ?", taskIDs[start:min(start+500, len(taskIDs))]).Find(&batch).Error; err != nil {
			return nil, err
		}
		tasks = append(tasks, batch...)
	}
	taskByOwner := make(map[taskOwner]*model.Task, len(tasks))
	for i := range tasks {
		owner := taskOwner{tasks[i].TaskID, tasks[i].UserId}
		if _, exists := taskByOwner[owner]; exists {
			taskByOwner[owner] = nil
			continue
		}
		taskByOwner[owner] = &tasks[i]
	}
	if consolidate {
		rows := make([]*model.Log, 0, len(logs))
		firstByOwner := make(map[taskOwner]*model.Log)
		for _, log := range logs {
			owner, ok := owners[log]
			task := taskByOwner[owner]
			if !ok || task == nil || taskVideoLogInfo(task) == nil {
				rows = append(rows, log)
				continue
			}
			first := firstByOwner[owner]
			if first == nil {
				firstByOwner[owner] = log
				rows = append(rows, log)
				continue
			}
			var latest, older map[string]any
			if common.UnmarshalJsonStr(first.Other, &latest) != nil || common.UnmarshalJsonStr(log.Other, &older) != nil {
				rows = append(rows, log)
				continue
			}
			_, adjusted := older["actual_quota"]
			_, settled := older["pre_consumed_quota"]
			initial := log.Type == model.LogTypeConsume && !adjusted && !settled
			for key, value := range older {
				_, exists := latest[key]
				requestMetadata := initial && key != "usage_facts" && key != "matched_tier" && key != "actual_quota" && key != "pre_consumed_quota"
				if !exists || requestMetadata {
					latest[key] = value
				}
			}
			if initial {
				first.RequestId, first.UpstreamRequestId = log.RequestId, log.UpstreamRequestId
				first.TokenId, first.TokenName, first.Group = log.TokenId, log.TokenName, log.Group
				first.Username, first.ModelName, first.Ip = log.Username, log.ModelName, log.Ip
				first.ChannelId, first.ChannelName = log.ChannelId, log.ChannelName
				first.Content, first.IsStream = log.Content, log.IsStream
			}
			encoded, err := common.Marshal(latest)
			if err != nil {
				return nil, err
			}
			first.Other = string(encoded)
		}
		logs = rows
	}
	for _, log := range logs {
		owner, ok := owners[log]
		if !ok {
			continue
		}
		task := taskByOwner[owner]
		if task == nil {
			continue
		}
		info := taskVideoLogInfo(task)
		if info == nil {
			continue
		}
		var other map[string]any
		if common.UnmarshalJsonStr(log.Other, &other) != nil || other == nil {
			continue
		}
		pending := task.Status != model.TaskStatusSuccess && (task.Status != model.TaskStatusFailure || task.Quota != 0)
		log.Quota = task.Quota
		if task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure {
			log.Quota = 0
		}
		other["task_status"] = task.Status
		other["task_billing_pending"] = pending
		if !pending {
			other["actual_quota"] = task.Quota
			if other["billing_source"] == "subscription" {
				other["subscription_consumed"] = task.Quota
			}
		}
		encoded, err := common.Marshal(other)
		if err != nil {
			return nil, err
		}
		log.Other = string(encoded)
		if consolidate {
			log.Type = model.LogTypeConsume
		}
		if log.PromptTokens == 0 && log.CompletionTokens == 0 && info.ConsumedTokens != nil {
			log.CompletionTokens = *info.ConsumedTokens
			var response struct {
				Usage struct {
					CompletionTokens *int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if common.Unmarshal(task.Data, &response) == nil && response.Usage.CompletionTokens != nil {
				completion := *response.Usage.CompletionTokens
				if completion >= 0 && completion <= *info.ConsumedTokens {
					log.PromptTokens = *info.ConsumedTokens - completion
					log.CompletionTokens = completion
				}
			}
		}
		if log.UseTime == 0 && task.SubmitTime > 0 && task.FinishTime >= task.SubmitTime {
			log.UseTime = int(min(task.FinishTime-task.SubmitTime, int64(common.MaxQuota)))
		}
	}
	return logs, nil
}

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, _, err := model.GetAllLogs(logType, startTimestamp, endTimestamp, modelName, username, tokenName, 0, -1, channel, group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		model.FormatAdminLogs(logs)
	} else {
		model.FormatRootLogs(logs)
	}
	logs, err = projectVideoTaskLogs(logs, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(len(logs))
	start := min(pageInfo.GetStartIdx(), len(logs))
	end := start + min(pageInfo.GetPageSize(), len(logs)-start)
	pageInfo.SetItems(logs[start:end])
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, _, err := model.GetUserLogs(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, 0, -1, group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	logs, err = projectVideoTaskLogs(logs, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(len(logs))
	start := min(pageInfo.GetStartIdx(), len(logs))
	end := start + min(pageInfo.GetPageSize(), len(logs)-start)
	pageInfo.SetItems(logs[start:end])
	common.ApiSuccess(c, pageInfo)
	return
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "无效的令牌",
		})
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err == nil {
		err = enrichVideoTaskLogs(logs)
	}
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	stat, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	quotaNum, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
			//"token": tokenNum,
		},
	})
	return
}
