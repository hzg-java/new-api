package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		var contents []string
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
		}
		if snap := info.TieredBillingSnapshot; snap != nil {
			for key, value := range snap.UsageFacts {
				contents = append(contents, fmt.Sprintf("%s: %v", key, value))
			}
		}
		if len(contents) > 0 {
			logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
		}
	}
	other := model.NewLogOther()
	other.SetPublic("is_task", true)
	other.SetPublic("request_path", c.Request.URL.Path)
	if taskDeliveredInline(c, task) {
		other.SetPublic("task_sync", true)
	}
	other.SetPublic("model_price", info.PriceData.ModelPrice)
	if info.PriceData.ModelRatio > 0 {
		other.SetPublic("model_ratio", info.PriceData.ModelRatio)
	}
	other.SetPublic("group_ratio", info.PriceData.GroupRatioInfo.GroupRatio)
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other.SetPublic("user_group_ratio", info.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	if info.IsModelMapped {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", info.UpstreamModelName)
	}
	if snap := info.TieredBillingSnapshot; snap != nil {
		other.SetPublic("billing_mode", "tiered_expr")
		other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
		other.SetPublic("matched_tier", snap.EstimatedTier)
		if len(snap.UsageFacts) > 0 {
			other.SetPublic("usage_facts", snap.UsageFacts)
		}
		setTaskImageCount(other, snap.UsageFacts["image_count"])
	} else {
		setTaskImageCount(other, info.PriceData.OtherRatios()["image_count"])
	}
	appendTaskLogInfo(task, other)
	attachQuotaSaturation(c, info, other)
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
	if task.PrivateData.VideoSubmission == nil {
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
		model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
	}
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	if delta > 0 {
		return model.DecreaseUserQuota(task.UserId, delta, false)
	}
	return model.IncreaseUserQuota(task.UserId, -delta, false)
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) bool {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return true
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return false
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
	return err == nil
}

func TaskBillingAmountCNY(task *model.Task) *float64 {
	if task == nil || task.PrivateData.BillingAdjustmentFailed || task.PrivateData.SettledQuota == nil ||
		(task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure) {
		return nil
	}
	return BillingAmountCNY(*task.PrivateData.SettledQuota)
}

// Billing belongs to the host envelope, never the plugin's task snapshot.
func TaskBillingResponse(task *model.Task, response any) any {
	encoded, err := common.Marshal(response)
	if err != nil {
		return response
	}
	cleaned, err := stripTaskUsageBilling(encoded)
	if err != nil {
		return response
	}
	var object map[string]json.RawMessage
	if common.Unmarshal(cleaned, &object) != nil || object == nil {
		return json.RawMessage(cleaned)
	}
	// This is a task envelope, not an ordinary HTTP error: a terminal failed
	// task may have a confirmed refund. Never repeat its total on deliverables.
	if amount := TaskBillingAmountCNY(task); amount != nil {
		var usage map[string]json.RawMessage
		_ = common.Unmarshal(object["usage"], &usage)
		if usage == nil {
			usage = make(map[string]json.RawMessage)
		}
		usage["billing"], err = common.Marshal(*amount)
		if err != nil {
			return json.RawMessage(cleaned)
		}
		object["usage"], err = common.Marshal(usage)
		if err != nil {
			return json.RawMessage(cleaned)
		}
		if patched, err := common.Marshal(object); err == nil {
			return json.RawMessage(patched)
		}
	}
	return json.RawMessage(cleaned)
}

// stripTaskUsageBilling preserves scalar JSON verbatim (including large numbers)
// while removing untrusted charges at every object/array nesting level.
func stripTaskUsageBilling(data json.RawMessage) (json.RawMessage, error) {
	switch common.GetJsonType(data) {
	case "object":
		var object map[string]json.RawMessage
		if err := common.Unmarshal(data, &object); err != nil {
			return nil, err
		}
		for key, value := range object {
			if key == "usage" && common.GetJsonType(value) == "object" {
				var usage map[string]json.RawMessage
				if err := common.Unmarshal(value, &usage); err != nil {
					return nil, err
				}
				delete(usage, "billing")
				var err error
				value, err = common.Marshal(usage)
				if err != nil {
					return nil, err
				}
			}
			cleaned, err := stripTaskUsageBilling(value)
			if err != nil {
				return nil, err
			}
			object[key] = cleaned
		}
		return common.Marshal(object)
	case "array":
		var items []json.RawMessage
		if err := common.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		for i := range items {
			cleaned, err := stripTaskUsageBilling(items[i])
			if err != nil {
				return nil, err
			}
			items[i] = cleaned
		}
		return common.Marshal(items)
	default:
		return data, nil
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) *model.LogOther {
	other := model.NewLogOther()
	if bc := task.PrivateData.BillingContext; bc != nil {
		other.SetPublic("model_price", bc.ModelPrice)
		if bc.ModelRatio > 0 {
			other.SetPublic("model_ratio", bc.ModelRatio)
		}
		other.SetPublic("group_ratio", bc.GroupRatio)
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				if !other.SetPublic(k, v) {
					common.SysError("task billing other ratio key rejected: " + k)
				}
			}
		}
		if snap := bc.TieredSnapshot; snap != nil {
			other.SetPublic("billing_mode", "tiered_expr")
			other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
			other.SetPublic("matched_tier", snap.EstimatedTier)
			if len(snap.UsageFacts) > 0 {
				other.SetPublic("usage_facts", snap.UsageFacts)
			}
			setTaskImageCount(other, snap.UsageFacts["image_count"])
		} else if priceData := taskBillingContextPriceData(bc); priceData != nil {
			setTaskImageCount(other, priceData.OtherRatios()["image_count"])
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", props.UpstreamModelName)
	}
	appendTaskLogInfo(task, other)
	return other
}

// setTaskImageCount publishes the billed image quantity of an image task as
// other.image_count, the same field the HTTP image relay writes, so the log
// detail shows one "billable image count" regardless of the serving path. The
// value is a host-validated count fact (at most dto.MaxImageN), never a quota.
func setTaskImageCount(other *model.LogOther, value any) {
	count, ok := value.(float64)
	if !ok || count < 0 || count > float64(dto.MaxImageN) {
		return
	}
	other.SetPublic("image_count", common.QuotaRound(count))
}

// taskDeliveredInline reports whether the submitting HTTP request itself
// returns the task deliverable: the upstream completed immediately, or the
// request came through the synchronous OpenAI Images protocol, where the host
// waits for an asynchronous upstream task before answering. The usage log
// presents such requests as synchronous instead of as asynchronous jobs.
func taskDeliveredInline(c *gin.Context, task *model.Task) bool {
	if task != nil && (task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure) {
		return true
	}
	pinnedValue, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	if !exists {
		return false
	}
	pinned, ok := pinnedValue.(jsplugin.PinnedEndpoint)
	return ok && pinned.Protocol == jsplugin.ProtocolOpenAIImage
}

func appendTaskLogInfo(task *model.Task, other *model.LogOther) {
	if task == nil || other == nil {
		return
	}
	if task.TaskID != "" {
		other.SetPublic("task_id", task.TaskID)
	}
	if task.PrivateData.ResultDiscarded {
		// The result was delivered inline and the upstream snapshot was not
		// persisted, so no artifact can be retrieved for this task.
		other.SetPublic("result_discarded", true)
	}
	if task.PrivateData.Execution != nil {
		AppendTaskPluginAuditInfo(other, task.PrivateData.Execution.TaskPlugin)
	}
	if task.PrivateData.UpstreamTaskID == "" && task.PrivateData.NodeName == "" {
		return
	}
	if task.PrivateData.UpstreamTaskID != "" {
		other.SetRoot("upstream_task_id", task.PrivateData.UpstreamTaskID)
	}
	if task.PrivateData.NodeName != "" {
		other.SetRoot("node_name", task.PrivateData.NodeName)
	}
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，退还资金与令牌额度，并回减用户和渠道用量。
// 仅在资金、令牌和回写均成功时确认零费用；部分成功必须人工对账。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) bool {
	if task.PrivateData.BillingAdjustmentFailed {
		return false
	}
	quota := task.Quota
	if quota == 0 {
		if err := task.ConfirmSettledQuota(0); err != nil {
			logger.LogError(ctx, fmt.Sprintf("确认任务退款失败 task %s: %s", task.TaskID, err.Error()))
			return false
		}
		return true
	}

	// Persist the reconciliation guard before moving funds, so a failed final
	// writeback cannot make a reloaded task refund the same reservation twice.
	task.PrivateData.SettledQuota = nil
	task.PrivateData.BillingAdjustmentFailed = true
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("准备任务退款失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}
	// 1. 退还资金来源（钱包或订阅）
	if err := taskAdjustFunding(task, -quota); err != nil {
		task.PrivateData.BillingAdjustmentFailed = false
		if persistErr := task.UpdateQuota(); persistErr != nil {
			logger.LogError(ctx, fmt.Sprintf("回写任务退款失败状态 task %s: %s", task.TaskID, persistErr.Error()))
		}
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}

	// 2. 退还令牌额度。资金已移动后不再自动重试或补做另一轮退款。
	tokenAdjusted := taskAdjustTokenQuota(ctx, task, -quota)
	task.PrivateData.BillingAdjustmentFailed = true

	// 3. 回减预扣时累计的用户和渠道用量，请求次数保持不变
	model.UpdateUserUsedQuota(task.UserId, -quota)
	model.UpdateChannelUsedQuota(task.ChannelId, -quota)

	// 4. 记录日志
	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("reason", reason)
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})

	// 5. 资金退款完成后再清除持久化标记。
	// 回写失败必须显式告警，避免漏掉潜在的重复退款风险。
	task.Quota = 0
	if !tokenAdjusted || common.BatchUpdateEnabled {
		if err := task.UpdateQuota(); err != nil {
			logger.LogError(ctx, fmt.Sprintf("退款回写 quota 失败 task %s: %s", task.TaskID, err.Error()))
		}
		return false
	}
	if err := task.ConfirmSettledQuota(0); err != nil {
		logger.LogError(ctx, fmt.Sprintf("退款成功但确认 task quota 失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}
	task.PrivateData.BillingAdjustmentFailed = false
	return true
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
// clamps 可选：若计算 actualQuota 时发生额度饱和，将其记入日志 admin_info（仅管理员可见）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) bool {
	if actualQuota < 0 || task.PrivateData.BillingAdjustmentFailed {
		return false
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		// A paid reservation may still be queued. Only a genuinely free zero
		// needs no ledger movement; never change batch behavior for a response.
		if common.BatchUpdateEnabled && actualQuota != 0 {
			task.PrivateData.SettledQuota = nil
			if err := task.UpdateQuota(); err != nil {
				logger.LogError(ctx, fmt.Sprintf("回写任务用量快照失败 task %s: %s", task.TaskID, err.Error()))
			}
			return false
		}
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		if err := task.ConfirmSettledQuota(actualQuota); err != nil {
			logger.LogError(ctx, fmt.Sprintf("确认 task quota 失败 task %s: %s", task.TaskID, err.Error()))
			return false
		}
		return true
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// A durable guard keeps partial failures unconfirmed after reload too.
	task.PrivateData.SettledQuota = nil
	task.PrivateData.BillingAdjustmentFailed = true
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("准备任务差额结算失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}
	// 调整资金来源
	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		task.PrivateData.BillingAdjustmentFailed = false
		if persistErr := task.UpdateQuota(); persistErr != nil {
			logger.LogError(ctx, fmt.Sprintf("回写差额结算失败状态 task %s: %s", task.TaskID, persistErr.Error()))
		}
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}

	// Keep the applied funding total even if token adjustment or writeback fails.
	// Neither the caller's failure fallback nor a retry may move these funds twice.
	tokenAdjusted := taskAdjustTokenQuota(ctx, task, quotaDelta)
	task.PrivateData.BillingAdjustmentFailed = true
	task.Quota = actualQuota
	var persistErr error
	confirmed := tokenAdjusted && !common.BatchUpdateEnabled
	if confirmed {
		persistErr = task.ConfirmSettledQuota(actualQuota)
	} else {
		persistErr = task.UpdateQuota()
	}
	if persistErr != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算回写 quota 失败 task %s: %s", task.TaskID, persistErr.Error()))
	}
	task.PrivateData.BillingAdjustmentFailed = !confirmed || persistErr != nil

	// 提交阶段已经累计过一次请求；结算阶段只调整最终用量。
	model.UpdateUserUsedQuota(task.UserId, quotaDelta)
	model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("pre_consumed_quota", preConsumedQuota)
	other.SetPublic("actual_quota", actualQuota)
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	})
	return !task.PrivateData.BillingAdjustmentFailed
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) bool {
	_, settled := recalculateTaskQuotaByTokens(ctx, task, totalTokens)
	return settled
}

// The first result distinguishes unavailable token pricing from an attempted
// settlement failure; only the former may retain the reservation as final.
func recalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) (bool, bool) {
	if totalTokens <= 0 {
		return false, false
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	if bc := task.PrivateData.BillingContext; bc != nil {
		if bc.PerCallBilling || bc.TieredSnapshot != nil {
			return false, false
		}
		modelRatio, hasRatioSetting = bc.ModelRatio, true
	}
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return false, false
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return false, false
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}

	if bc := task.PrivateData.BillingContext; bc != nil {
		finalGroupRatio = bc.GroupRatio
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier（饱和转换，防止溢出成负数）
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	return true, RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}

// EvaluateTaskCompletionUsage evaluates actual facts against the frozen task
// expression. It neither mutates the snapshot nor moves funds: synchronous
// submission and polling have different persistence and settlement barriers.
func EvaluateTaskCompletionUsage(snap *billingexpr.BillingSnapshot, facts map[string]any) (billingexpr.TieredResult, map[string]any, error) {
	if snap == nil {
		return billingexpr.TieredResult{}, nil, fmt.Errorf("task billing snapshot is missing")
	}
	usage := make(map[string]any, len(snap.UsageFacts)+len(facts))
	maps.Copy(usage, snap.UsageFacts)
	maps.Copy(usage, facts)
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
	if err == nil && (result.ActualQuotaBeforeGroup < 0 || math.IsNaN(result.ActualQuotaBeforeGroup)) {
		err = fmt.Errorf("task completion expression produced an invalid cost")
	}
	return result, usage, err
}
