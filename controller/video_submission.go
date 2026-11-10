package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type videoSubmissionHandler struct{}

func (videoSubmissionHandler) Type() string            { return "video_submission" }
func (videoSubmissionHandler) Interval() time.Duration { return 15 * time.Second }
func (videoSubmissionHandler) NewPayload() any         { return nil }
func (videoSubmissionHandler) Enabled() bool {
	if !constant.UpdateTask {
		return false
	}
	var count int64
	return model.DB.Model(&model.Task{}).Where("submission_stage IN ?", []string{"pending", "preparing", "submitting", "settling", "failed"}).Count(&count).Error == nil && count > 0
}

func (videoSubmissionHandler) Run(ctx context.Context, systemTask *model.SystemTask, runnerID string) {
	count, err := runVideoSubmissionPass(ctx, 0)
	if err != nil {
		finishSystemTaskHandler(systemTask, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(systemTask, runnerID, model.SystemTaskStatusSucceeded, map[string]any{"tasks": count}, nil)
}

func runVideoSubmissionPass(ctx context.Context, afterID int64) (int, error) {
	var tasks []*model.Task
	err := model.DB.WithContext(ctx).Where("id > ? AND submission_stage IN ? AND submission_lease_until < ?", afterID, []string{"pending", "preparing", "submitting", "settling", "failed"}, common.GetTimestamp()).Order("id").Limit(4).Find(&tasks).Error
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			wg.Wait()
			return len(tasks), ctx.Err()
		case slots <- struct{}{}:
		}
		wg.Go(func() {
			defer func() {
				<-slots
				if recovered := recover(); recovered != nil {
					logger.LogError(ctx, fmt.Sprintf("video submission %s interrupted", task.TaskID))
				}
			}()
			plugin, _, _ := resolveTaskPluginForProtocolRetrieve(task.Platform)
			if err := runVideoSubmission(ctx, task, plugin); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("video submission %s: %v", task.TaskID, err))
			}
		})
	}
	wg.Wait()
	return len(tasks), ctx.Err()
}

type localVideoSubmissionRunner struct {
	afterID int64
}

func StartLocalVideoSubmissionRunner() error {
	configured := os.Getenv("LOCAL_VIDEO_SUBMISSION_AFTER_ID")
	if configured == "" {
		return nil
	}
	if common.IsMasterNode || constant.UpdateTask {
		return errors.New("local video submission runner requires NODE_TYPE=slave and UPDATE_TASK=false")
	}
	afterID, err := strconv.ParseInt(configured, 10, 64)
	if err != nil || afterID < 0 {
		return errors.New("LOCAL_VIDEO_SUBMISSION_AFTER_ID must be a non-negative task ID")
	}
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) || model.LOG_DB != model.DB {
		return errors.New("local video submission runner requires the isolated test database for tasks and logs")
	}
	var database string
	if err := model.DB.Raw("SELECT current_database()").Scan(&database).Error; err != nil {
		return errors.New("cannot verify local video submission database")
	}
	if database != "new-api-test" {
		return errors.New("local video submission runner refuses databases other than new-api-test")
	}
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("local video submission runner requires local Redis")
	}
	host, _, err := net.SplitHostPort(common.RDB.Options().Addr)
	if err != nil || host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return errors.New("local video submission runner refuses non-local Redis")
	}
	runner := localVideoSubmissionRunner{afterID: afterID}
	go func() {
		logger.LogInfo(context.Background(), fmt.Sprintf("local video submission runner started: after_task_id=%d; historical and general system tasks disabled", afterID))
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := runVideoSubmissionPass(context.Background(), runner.afterID); err != nil {
				logger.LogWarn(context.Background(), "local video submission pass failed: "+err.Error())
			}
			if err := runner.poll(context.Background()); err != nil {
				logger.LogWarn(context.Background(), "local video polling failed: "+err.Error())
			}
		}
	}()
	return nil
}

func (r localVideoSubmissionRunner) poll(ctx context.Context) error {
	platformTasks := make(map[constant.TaskPlatform][]*model.Task)
	limit, selected, afterID := max(constant.TaskQueryLimit, 1), 0, r.afterID
	for selected < limit {
		var tasks []*model.Task
		err := model.DB.WithContext(ctx).Scopes(model.TaskCanPollUpstream).Where("id > ? AND status NOT IN ?", afterID, []model.TaskStatus{model.TaskStatusFailure, model.TaskStatusSuccess}).Order("id").Limit(limit).Find(&tasks).Error
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			break
		}
		afterID = tasks[len(tasks)-1].ID
		for _, task := range tasks {
			if task.PrivateData.VideoSubmission == nil || task.GetUpstreamTaskID() == "" {
				continue
			}
			selected++
			if constant.TaskTimeoutMinutes > 0 && task.SubmitTime < common.GetTimestamp()-int64(constant.TaskTimeoutMinutes)*60 {
				owner := common.GetUUID()
				claimed := model.DB.WithContext(ctx).Model(task).Scopes(model.TaskCanPollUpstream).Where("status = ? AND submission_lease_until < ?", task.Status, common.GetTimestamp()).Updates(map[string]any{"submission_owner": owner, "submission_lease_until": common.GetTimestamp() + 60})
				if claimed.Error != nil {
					return claimed.Error
				}
				if claimed.RowsAffected == 1 {
					reason := fmt.Sprintf("任务超时（%d分钟）", constant.TaskTimeoutMinutes)
					if err := failVideoSubmission(ctx, task, owner, &dto.TaskError{Code: "task_timeout", Message: reason}); err != nil {
						return err
					}
				}
				continue
			}
			platformTasks[task.Platform] = append(platformTasks[task.Platform], task)
		}
	}
	for platform, pending := range platformTasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		channels := make(map[int][]string)
		byUpstreamID := make(map[string]*model.Task)
		for _, task := range pending {
			upstreamID := task.GetUpstreamTaskID()
			channels[task.ChannelId] = append(channels[task.ChannelId], upstreamID)
			byUpstreamID[upstreamID] = task
		}
		service.DispatchPlatformUpdate(ctx, platform, channels, byUpstreamID)
	}
	return ctx.Err()
}

func runVideoSubmission(parent context.Context, task *model.Task, plugin *pluginruntime.LoadedPlugin) error {
	owner := common.GetUUID()
	if task.SubmissionStage == "settling" || task.SubmissionStage == "failed" {
		result := model.DB.WithContext(parent).Model(task).Where("submission_stage = ? AND submission_lease_until < ?", task.SubmissionStage, common.GetTimestamp()).Updates(map[string]any{"submission_owner": owner, "submission_lease_until": common.GetTimestamp() + 60})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		if err := model.DB.WithContext(parent).First(task, task.ID).Error; err != nil {
			return err
		}
		return settleVideoSubmission(parent, task, owner)
	}
	if task.SubmissionStage == "submitting" {
		result := model.DB.WithContext(parent).Model(task).Where("submission_stage = ? AND submission_lease_until < ? AND status NOT IN ?", task.SubmissionStage, common.GetTimestamp(), []model.TaskStatus{model.TaskStatusFailure, model.TaskStatusSuccess}).Updates(map[string]any{"submission_owner": owner, "submission_lease_until": common.GetTimestamp() + 60})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		task.SubmissionOwner = owner
		reason := "Video submission result is unknown; the provider may have accepted the request. This task will not be submitted again automatically"
		return failVideoSubmission(parent, task, owner, &dto.TaskError{Code: "task_submission_interrupted", Message: reason})
	}
	claimed, err := model.ClaimVideoSubmission(parent, task.ID, owner)
	if err != nil || !claimed {
		return err
	}
	if err := model.DB.WithContext(parent).First(task, task.ID).Error; err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(parent, time.Unix(task.SubmissionStartedAt, 0).Add(12*time.Minute))
	defer cancel()
	if ctx.Err() != nil {
		return failVideoSubmission(parent, task, owner, &dto.TaskError{Code: "material_preparation_timeout", Message: "Video material preparation timed out before submission"})
	}
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	defer func() { close(done); <-heartbeatDone }()
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				result := model.DB.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND submission_owner = ? AND submission_lease_until >= ? AND submission_stage IN ?", task.ID, owner, common.GetTimestamp(), []string{"preparing", "submitting", "settling", "failed"}).Update("submission_lease_until", common.GetTimestamp()+60)
				if result.Error != nil || result.RowsAffected != 1 {
					logger.LogWarn(ctx, fmt.Sprintf("video submission %s lease renewal failed: rows=%d error=%v", task.TaskID, result.RowsAffected, result.Error))
					cancel()
					return
				}
			}
		}
	}()
	failure := func(code, message string) error {
		return failVideoSubmission(ctx, task, owner, &dto.TaskError{Code: code, Message: message})
	}
	snapshot := task.PrivateData.VideoSubmission
	if snapshot == nil || snapshot.Request == nil || task.PrivateData.BillingContext == nil {
		return failure("task_submission_invalid", "Video task submission data is unavailable")
	}
	if plugin == nil || snapshot.PluginVersion != plugin.Meta.Version || snapshot.PluginHash != plugin.SourceHash {
		return failure("task_plugin_changed", "Video task plugin changed before submission; submit a new task")
	}
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil || channel.Status != common.ChannelStatusEnabled || snapshot.ChannelFingerprint != service.VideoSubmissionChannelFingerprint(channel) {
		return failure("task_channel_changed", "Video channel changed before submission; submit a new task")
	}
	fingerprint, err := service.VideoMaterialIntegrationFingerprint(task.ChannelId)
	if err != nil || fingerprint != snapshot.IntegrationFingerprint {
		return failure("material_configuration_changed", "Video material configuration changed before submission; submit a new task")
	}
	path := "/v1/videos"
	if task.PrivateData.Execution != nil && task.PrivateData.Execution.RequestPath != "" {
		path = task.PrivateData.Execution.RequestPath
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader("{}"))
	if err != nil {
		return failure("task_request_invalid", "Video task request is unavailable")
	}
	c := &gin.Context{Request: request}
	c.Set("id", task.UserId)
	c.Set("task_plugin_key", plugin.Meta.Key)
	c.Set("expected_task_plugin_key", plugin.Meta.Key)
	c.Set("platform", plugin.Meta.Key)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin, Generation: pluginruntime.DefaultRegistry.Generation()})
	if setupErr := middleware.SetupContextForSelectedChannel(c, channel, task.Properties.OriginModelName); setupErr != nil {
		return failure("task_channel_unavailable", "Video channel is unavailable")
	}
	var body any
	if err := common.Unmarshal(snapshot.Request, &body); err != nil {
		return failure("task_request_invalid", "Video task request is unavailable")
	}
	c.Set("task_request", body)
	c.Set("task_action", task.Action)
	billing := task.PrivateData.BillingContext
	info := &relaycommon.RelayInfo{
		UserId: task.UserId, TokenId: task.PrivateData.TokenId, UsingGroup: task.Group,
		OriginModelName: task.Properties.OriginModelName, StartTime: time.Now(),
		RelayFormat:           kittypes.RelayFormatTask,
		TaskRelayInfo:         &relaycommon.TaskRelayInfo{Action: task.Action, PublicTaskID: task.TaskID, LockedChannel: channel},
		PriceData:             types.PriceData{Quota: snapshot.TargetQuota, ModelPrice: billing.ModelPrice, ModelRatio: billing.ModelRatio, UsePrice: snapshot.UsePrice, FreeModel: snapshot.FreeModel, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: billing.GroupRatio}},
		TieredBillingSnapshot: billing.TieredSnapshot,
	}
	info.InitChannelMeta(c)
	info.UpstreamModelName = task.Properties.UpstreamModelName
	info.PriceData.ReplaceOtherRatios(billing.OtherRatios)
	for _, id := range snapshot.OriginTaskIDs {
		origin, exists, err := model.GetByTaskId(task.UserId, id)
		if err != nil || !exists || origin.ChannelId != task.ChannelId || origin.GetUpstreamTaskID() == "" {
			return failure("origin_task_unavailable", "A referenced video task is unavailable")
		}
		info.OriginTasks = append(info.OriginTasks, relaycommon.OriginTaskRef{TaskID: id, UpstreamTaskID: origin.GetUpstreamTaskID(), Action: origin.Action, Status: string(origin.Status), Data: origin.Data})
	}
	result, taskErr := relay.RelayPreparedTaskSubmit(c, info, task.Platform, func() error {
		current, err := service.VideoMaterialIntegrationFingerprint(task.ChannelId)
		latest, channelErr := model.GetChannelById(task.ChannelId, true)
		if err != nil || current != snapshot.IntegrationFingerprint || channelErr != nil || latest.Status != common.ChannelStatusEnabled || service.VideoSubmissionChannelFingerprint(latest) != snapshot.ChannelFingerprint {
			return errors.New("video channel or material configuration changed during preparation")
		}
		updated := model.DB.WithContext(ctx).Model(task).Where("submission_owner = ? AND submission_stage = ? AND submission_lease_until >= ?", owner, "preparing", common.GetTimestamp()).Update("submission_stage", "submitting")
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("video submission lease lost")
		}
		task.SubmissionStage = "submitting"
		return nil
	})
	if ctx.Err() != nil {
		if parent.Err() != nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ctx.Err()
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
		defer cancel()
		if taskErr != nil && taskErr.Code == "material_preparation_failed" {
			return failVideoSubmission(cleanup, task, owner, taskErr)
		}
		return failVideoSubmission(cleanup, task, owner, &dto.TaskError{Code: "task_submission_timeout", Message: "Video task submission timed out; the provider request will not be replayed automatically"})
	}
	if taskErr != nil {
		if task.SubmissionStage == "submitting" && (taskErr.Code == "do_request_failed" || taskErr.Code == "read_response_body_failed" || taskErr.Code == "plugin_submit_response_failed" || taskErr.Code == "plugin_submit_response_invalid") {
			taskErr = &dto.TaskError{Code: "task_submission_unknown", Message: "Video submission result is unknown; the provider may have accepted the request. This task will not be submitted again automatically"}
		}
		return failVideoSubmission(ctx, task, owner, taskErr)
	}
	if result.UpstreamTaskID == "" && result.Immediate == nil {
		return failure("task_submission_unknown", "Video submission returned no task ID; the request will not be submitted again automatically")
	}
	task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
	task.PrivateData.PluginState = result.PluginState
	task.PrivateData.VideoSubmission.Request = nil
	task.PrivateData.VideoSubmission.TargetQuota = result.Quota
	task.PrivateData.BillingContext.OtherRatios = info.PriceData.OtherRatios()
	task.PrivateData.BillingContext.TieredSnapshot = info.TieredBillingSnapshot
	task.Data = result.TaskData
	task.Status = model.TaskStatusSubmitted
	if result.Immediate != nil {
		task.Status = model.TaskStatus(result.Immediate.Status)
		task.Progress = result.Immediate.Progress
		task.FailReason = result.Immediate.Reason
		task.PrivateData.ResultURL = result.Immediate.Url
		if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
			task.FinishTime = common.GetTimestamp()
		}
	}
	updated := model.DB.WithContext(ctx).Model(task).Where("submission_owner = ? AND submission_stage = ? AND submission_lease_until >= ?", owner, "submitting", common.GetTimestamp()).Updates(map[string]any{"status": task.Status, "progress": task.Progress, "fail_reason": task.FailReason, "finish_time": task.FinishTime, "data": task.Data, "private_data": task.PrivateData, "submission_stage": "settling"})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return errors.New("video submission lease lost before persistence")
	}
	task.SubmissionStage = "settling"
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		if err := model.RecordTaskEvent(task.ID, "result", task.FinishTime, model.TaskResultEventPayload(task)); err != nil {
			logger.LogWarn(ctx, "record video task result event: "+err.Error())
		}
	}
	return settleVideoSubmission(ctx, task, owner)
}

func failVideoSubmission(ctx context.Context, task *model.Task, owner string, taskErr *dto.TaskError) error {
	reason := kittypes.NewError(errors.New("Video task submission failed"), kittypes.ErrorCodeBadResponse, kittypes.ErrOptionWithUpstreamResponse(kittypes.OpenAIError{Message: taskErr.Message})).ToOpenAIRelayError().Message
	logger.LogWarn(ctx, "video_submission task_id=%q channel_id=%d code=%q reason=%q", task.TaskID, task.ChannelId, taskErr.Code, reason)
	message := service.VideoSubmissionFailureMessage(taskErr.Code)
	if taskErr.StatusCode == http.StatusUnauthorized || taskErr.StatusCode == http.StatusForbidden {
		message = service.VideoSubmissionFailureMessage("material_configuration_failed")
	}
	var materials []service.MaterialPreparationIssue
	if taskErr.Code == "material_preparation_failed" {
		if issues, ok := taskErr.Data.([]service.MaterialPreparationIssue); ok && len(issues) > 0 {
			message, materials = service.VideoMaterialFailure(issues)
		} else {
			message = service.VideoSubmissionFailureMessage("material_configuration_failed")
		}
	}
	task.Status, task.Progress, task.FailReason = model.TaskStatusFailure, "100%", message
	task.FinishTime = common.GetTimestamp()
	if task.PrivateData.VideoSubmission != nil {
		task.PrivateData.VideoSubmission.Request = nil
	}
	task.SetData(map[string]any{"id": task.TaskID, "status": "failed", "error": map[string]any{"code": taskErr.Code, "message": message, "materials": materials}})
	result := model.DB.WithContext(ctx).Model(task).Where("submission_owner = ? AND submission_lease_until >= ? AND status NOT IN ?", owner, common.GetTimestamp(), []model.TaskStatus{model.TaskStatusFailure, model.TaskStatusSuccess}).Updates(map[string]any{"status": task.Status, "progress": task.Progress, "fail_reason": task.FailReason, "finish_time": task.FinishTime, "private_data": task.PrivateData, "data": task.Data, "submission_stage": "failed"})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("video submission lease lost before failure persistence")
	}
	task.SubmissionStage = "failed"
	if err := model.RecordTaskEvent(task.ID, "result", task.FinishTime, model.TaskResultEventPayload(task)); err != nil {
		logger.LogWarn(ctx, "record failed video task event: "+err.Error())
	}
	return settleVideoSubmission(ctx, task, owner)
}

func settleVideoSubmission(ctx context.Context, task *model.Task, owner string) error {
	target := 0
	if task.SubmissionStage == "settling" && task.Status != model.TaskStatusFailure && task.PrivateData.VideoSubmission != nil {
		target = task.PrivateData.VideoSubmission.TargetQuota
	}
	previous, applied, err := model.AdjustVideoSubmissionQuota(ctx, task.ID, owner, target)
	if err != nil || !applied {
		return err
	}
	if delta := target - previous; delta != 0 {
		logType, quota := model.LogTypeConsume, delta
		if delta < 0 {
			logType, quota = model.LogTypeRefund, -delta
		}
		other := model.NewLogOther()
		other.SetPublic("task_id", task.TaskID)
		if task.FailReason != "" {
			other.SetPublic("reason", task.FailReason)
		}
		model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: task.UserId, LogType: logType, ChannelId: task.ChannelId, ModelName: task.Properties.OriginModelName, Quota: quota, TokenId: task.PrivateData.TokenId, Group: task.Group, Other: other, NodeName: task.PrivateData.NodeName})
	}
	// The transaction may have merged newer private state.
	if err := model.DB.WithContext(ctx).First(task, task.ID).Error; err != nil {
		return err
	}
	stage := ""
	if task.Status == model.TaskStatusFailure {
		stage = "failed_final"
	}
	result := model.DB.WithContext(ctx).Model(task).Where("submission_owner = ? AND submission_stage IN ? AND submission_lease_until >= ?", owner, []string{"settling", "failed"}, common.GetTimestamp()).Updates(map[string]any{"submission_stage": stage, "submission_owner": "", "submission_lease_until": 0})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("video submission lease lost during settlement")
	}
	return nil
}
