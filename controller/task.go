package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type taskArtifactResponse struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	MimeType   string `json:"mime_type,omitempty"`
	ContentURL string `json:"content_url"`
}

var (
	taskArtifactKeyPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
	errTaskArtifactPluginUnavailable = errors.New("task artifact plugin unavailable")
	errTaskArtifactPlugin            = errors.New("task artifact plugin error")
)

func GetTask(c *gin.Context) {
	task, exists, err := model.GetByTaskId(c.GetInt("id"), c.Param("key"))
	if err != nil {
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}
	createdAt := task.CreatedAt
	if createdAt == 0 {
		createdAt = task.SubmitTime
	}
	failReason := task.FailReason
	if task.Status == model.TaskStatusSuccess && taskFailReasonIsLegacyResultURL(task.FailReason) {
		failReason = ""
	}
	c.JSON(http.StatusOK, gin.H{
		"task_id":     task.TaskID,
		"platform":    task.Platform,
		"status":      task.Status,
		"progress":    task.Progress,
		"fail_reason": failReason,
		"created_at":  createdAt,
		"finished_at": task.FinishTime,
	})
}

func GetTaskArtifacts(c *gin.Context) {
	task, exists, err := model.GetByTaskId(c.GetInt("id"), c.Param("key"))
	if err != nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_internal_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	writeTaskArtifacts(c, task, false)
}

func GetDashboardTaskEvents(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	taskID, parseErr := strconv.ParseInt(c.Param("task_id"), 10, 64)
	if parseErr != nil || taskID <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Task not found"})
		return
	}
	var task *model.Task
	var exists bool
	var err error
	if c.GetInt("token_id") == 0 && c.GetInt("role") >= common.RoleAdminUser {
		task, exists, err = model.GetTaskByID(taskID, 0)
	} else if c.GetInt("id") > 0 {
		task, exists, err = model.GetTaskByID(taskID, c.GetInt("id"))
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists || task == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Task not found"})
		return
	}
	events, err := model.GetTaskEvents(task.ID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, events)
}

func GetDashboardTaskArtifacts(c *gin.Context) {
	task, exists, err := getTaskForArtifactRequest(c, c.Param("task_id"))
	if err != nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_internal_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	writeTaskArtifacts(c, task, true)
}

func writeTaskArtifacts(c *gin.Context, task *model.Task, dashboard bool) {
	c.Header("Cache-Control", "private, no-store")
	artifacts, err := projectTaskArtifacts(task)
	if err != nil {
		writeTaskArtifactProjectionError(c, err)
		return
	}
	items := make([]taskArtifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		contentURL, buildErr := service.BuildTaskArtifactContentURL(task.TaskID, artifact.Key)
		if buildErr != nil {
			writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_url_error", "Failed to build artifact content URL")
			return
		}
		items = append(items, taskArtifactResponse{
			Key:        artifact.Key,
			Type:       artifact.Type,
			MimeType:   artifact.MimeType,
			ContentURL: contentURL,
		})
	}
	response := gin.H{"task_id": task.TaskID, "artifacts": items}
	if dashboard && task.Status == model.TaskStatusSuccess && task.Platform == constant.TaskPlatformSuno {
		response["legacy_audio_clips"] = legacySunoAudioClips(task.Data)
	}
	if legacyVideoAvailable(task) {
		legacyContentURL, buildErr := service.BuildTaskArtifactContentURL(task.TaskID, "video")
		if buildErr != nil {
			writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_url_error", "Failed to build artifact content URL")
			return
		}
		response["legacy_content_url"] = legacyContentURL
	}
	if dashboard {
		common.ApiSuccess(c, response)
		return
	}
	c.JSON(http.StatusOK, response)
}

// legacySunoAudioClips projects a pre-plugin Suno task's persisted snapshot
// into the clips the dashboard audio preview renders. Task lists no longer
// carry the snapshot, so the dashboard reads it here on demand. The snapshot
// is either a clip array or a JSON string holding one; only clips with an
// audio URL are kept and only preview fields are exposed.
func legacySunoAudioClips(data json.RawMessage) []map[string]any {
	clips := make([]map[string]any, 0)
	if len(data) == 0 {
		return clips
	}
	var items []map[string]any
	if err := common.Unmarshal(data, &items); err != nil {
		var encoded string
		if common.Unmarshal(data, &encoded) != nil || common.UnmarshalJsonStr(encoded, &items) != nil {
			return clips
		}
	}
	for _, item := range items {
		audioURL, _ := item["audio_url"].(string)
		if strings.TrimSpace(audioURL) == "" {
			continue
		}
		clip := map[string]any{"audio_url": audioURL}
		for _, key := range []string{"clip_id", "id", "title", "tags", "duration", "image_url", "image_large_url", "metadata"} {
			if value, ok := item[key]; ok {
				clip[key] = value
			}
		}
		clips = append(clips, clip)
	}
	return clips
}

func projectTaskArtifacts(task *model.Task) ([]relaychannel.TaskArtifact, error) {
	if task == nil || task.Status != model.TaskStatusSuccess || !taskHasPluginExecution(task) || !task.ResultRetrievable() {
		return []relaychannel.TaskArtifact{}, nil
	}
	adaptor := relay.GetTaskAdaptor(task.Platform)
	if adaptor == nil {
		return nil, errTaskArtifactPluginUnavailable
	}
	provider, ok := adaptor.(relaychannel.TaskArtifactProvider)
	if !ok {
		return []relaychannel.TaskArtifact{}, nil
	}
	artifacts, err := provider.ListArtifacts(task)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTaskArtifactPlugin, err)
	}
	return validateProjectedTaskArtifacts(artifacts)
}

func validateProjectedTaskArtifacts(artifacts []relaychannel.TaskArtifact) ([]relaychannel.TaskArtifact, error) {
	if len(artifacts) > 64 {
		return nil, fmt.Errorf("%w: too many artifacts", errTaskArtifactPlugin)
	}
	seen := make(map[string]struct{}, len(artifacts))
	for i := range artifacts {
		if artifacts[i].Key != strings.TrimSpace(artifacts[i].Key) ||
			artifacts[i].Type != strings.TrimSpace(artifacts[i].Type) {
			return nil, fmt.Errorf("%w: invalid artifact identity", errTaskArtifactPlugin)
		}
		if !taskArtifactKeyPattern.MatchString(artifacts[i].Key) {
			return nil, fmt.Errorf("%w: invalid artifact key", errTaskArtifactPlugin)
		}
		if _, exists := seen[artifacts[i].Key]; exists {
			return nil, fmt.Errorf("%w: duplicate artifact key", errTaskArtifactPlugin)
		}
		seen[artifacts[i].Key] = struct{}{}
		switch artifacts[i].Type {
		case "video", "audio", "image", "file":
		default:
			return nil, fmt.Errorf("%w: invalid artifact type", errTaskArtifactPlugin)
		}
		if len(artifacts[i].MimeType) > 255 || strings.ContainsAny(artifacts[i].MimeType, "\r\n") {
			return nil, fmt.Errorf("%w: invalid artifact mime type", errTaskArtifactPlugin)
		}
	}
	return artifacts, nil
}

func initTaskArtifactAdaptor(task *model.Task) (relaychannel.TaskAdaptor, error) {
	if task == nil || !taskHasPluginExecution(task) {
		return nil, errTaskArtifactPluginUnavailable
	}
	channelModel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		return nil, fmt.Errorf("%w: channel unavailable", errTaskArtifactPluginUnavailable)
	}
	adaptor := relay.GetTaskAdaptor(task.Platform)
	if adaptor == nil {
		return nil, errTaskArtifactPluginUnavailable
	}
	pluginKey := task.PrivateData.Key
	if pluginKey == "" {
		pluginKey = channelModel.Key
	}
	baseURL := channelModel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(channelModel.Type)
	}
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    channelModel.Type,
			ChannelBaseUrl: baseURL,
			ApiKey:         pluginKey,
			ChannelSetting: channelModel.GetSetting(),
		},
	})
	return adaptor, nil
}

func taskHasPluginExecution(task *model.Task) bool {
	return task != nil &&
		task.PrivateData.Execution != nil &&
		task.PrivateData.Execution.TaskPlugin != nil &&
		strings.TrimSpace(task.PrivateData.Execution.TaskPlugin.Key) != ""
}

func legacyVideoAvailable(task *model.Task) bool {
	if task == nil || task.Status != model.TaskStatusSuccess ||
		taskHasPluginExecution(task) || task.Platform == constant.TaskPlatformSuno ||
		strings.TrimSpace(task.GetResultURL()) == "" {
		return false
	}
	switch constant.NormalizeTaskAction(task.Action) {
	case constant.TaskActionImageToVideo,
		constant.TaskActionTextToVideo,
		constant.TaskActionFirstTailToVideo,
		constant.TaskActionReferenceToVideo,
		constant.TaskActionRemix:
		return true
	default:
		return false
	}
}

func getTaskForArtifactRequest(c *gin.Context, taskID string) (*model.Task, bool, error) {
	if middleware.IsTaskArtifactAccess(c) {
		task, exists, err := model.GetUniqueByOnlyTaskId(taskID)
		if err != nil || !exists || task == nil {
			return task, exists, err
		}
		owner, err := model.GetUserCache(task.UserId)
		if err != nil || owner == nil || owner.Status != common.UserStatusEnabled {
			return nil, false, err
		}
		return task, true, nil
	}
	if c.GetInt("token_id") == 0 && c.GetInt("role") >= common.RoleAdminUser {
		return model.GetByOnlyTaskId(taskID)
	}
	return model.GetByTaskId(c.GetInt("id"), taskID)
}

func writeTaskArtifactProjectionError(c *gin.Context, err error) {
	if errors.Is(err, errTaskArtifactPluginUnavailable) {
		writeTaskArtifactError(c, http.StatusServiceUnavailable, "artifact_plugin_unavailable", "Artifact preview plugin is unavailable")
		return
	}
	writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_plugin_error", "Artifact preview plugin failed")
}

func writeTaskArtifactError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "private, no-store")
	if middleware.IsTaskArtifactAccess(c) {
		status = http.StatusNotFound
		code = "artifact_not_found"
		message = "Task or artifact not found"
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		c.JSON(status, gin.H{"success": false, "code": code, "message": message})
		return
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    code,
			"code":    code,
		},
	})
}

func TaskArtifactContent(c *gin.Context) {
	task, exists, err := getTaskForArtifactRequest(c, c.Param("key"))
	if err != nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_internal_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	artifactKey := strings.TrimSpace(c.Param("artifact_key"))
	if !taskArtifactKeyPattern.MatchString(artifactKey) || !task.ResultRetrievable() {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	if task.Status != model.TaskStatusSuccess {
		writeTaskArtifactError(c, http.StatusConflict, "artifact_not_ready", "Task artifacts are not ready")
		return
	}
	if !taskHasPluginExecution(task) {
		if artifactKey != "video" || !legacyVideoAvailable(task) {
			writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
			return
		}
		descriptor := &relaychannel.TaskContentRequest{
			URL:            task.GetResultURL(),
			Method:         c.Request.Method,
			Credentialless: true,
		}
		if err := proxyTaskMedia(c, task, descriptor); err != nil {
			writeTaskMediaProxyError(c, err)
		}
		return
	}
	artifacts, err := projectTaskArtifacts(task)
	if err != nil {
		writeTaskArtifactProjectionError(c, err)
		return
	}
	found := false
	for _, artifact := range artifacts {
		if artifact.Key == artifactKey {
			found = true
			break
		}
	}
	if !found {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	artifactStore := service.GetTaskArtifactStore()
	if ref, resolveErr := artifactStore.Resolve(task, artifactKey); resolveErr == nil && ref != nil {
		_ = artifactStore.Serve(c, task, ref)
		return
	}

	adaptor, err := initTaskArtifactAdaptor(task)
	if err != nil {
		writeTaskArtifactProjectionError(c, err)
		return
	}
	provider, ok := adaptor.(relaychannel.TaskContentRequestProvider)
	if !ok {
		writeTaskArtifactError(c, http.StatusServiceUnavailable, "artifact_plugin_unavailable", "Artifact content plugin is unavailable")
		return
	}
	clientRequest := relaychannel.TaskArtifactClientRequest{
		Method:  c.Request.Method,
		Headers: taskArtifactClientHeaders(c.Request.Header),
	}
	descriptor, err := provider.BuildContentRequest(task, artifactKey, clientRequest)
	if err != nil || descriptor == nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_plugin_error", "Artifact content plugin failed")
		return
	}
	if err := proxyTaskMedia(c, task, descriptor); err != nil {
		writeTaskMediaProxyError(c, err)
	}
}

func taskArtifactClientHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, 4)
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			result[name] = value
		}
	}
	return result
}

/*
	The task list handlers below deliberately do not call projectTaskArtifacts.
	Artifact projection is confined to the explicit endpoints above.
*/

func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	queryParams := model.SyncTaskQueryParams{Platform: constant.TaskPlatform(c.Query("platform")), TaskID: c.Query("task_id"), Status: c.Query("status"), Action: c.Query("action"), StartTimestamp: startTimestamp, EndTimestamp: endTimestamp, ChannelID: c.Query("channel_id")}
	items := model.TaskGetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	pageInfo.SetTotal(int(model.TaskCountAllTasks(queryParams)))
	pageInfo.SetItems(tasksToDto(items, true, c.GetInt("role")))
	common.ApiSuccess(c, pageInfo)
}

func GetUserTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userID := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	queryParams := model.SyncTaskQueryParams{Platform: constant.TaskPlatform(c.Query("platform")), TaskID: c.Query("task_id"), Status: c.Query("status"), Action: c.Query("action"), StartTimestamp: startTimestamp, EndTimestamp: endTimestamp}
	items := model.TaskGetAllUserTask(userID, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	pageInfo.SetTotal(int(model.TaskCountAllUserTask(userID, queryParams)))
	pageInfo.SetItems(tasksToDto(items, false, common.RoleCommonUser))
	common.ApiSuccess(c, pageInfo)
}

func tasksToDto(tasks []*model.Task, fillUser bool, viewerRole int) []*dto.TaskDto {
	var userIDMap map[int]*model.UserBase
	if fillUser {
		userIDMap = make(map[int]*model.UserBase)
		userIDs := types.NewSet[int]()
		for _, task := range tasks {
			userIDs.Add(task.UserId)
		}
		for _, userID := range userIDs.Items() {
			if cacheUser, err := model.GetUserCache(userID); err == nil {
				userIDMap[userID] = cacheUser
			}
		}
	}
	// List queries intentionally omit data. Load completion snapshots only for
	// video rows needing actual token display, in one bounded page query. Never
	// attach these payloads to the public DTO or mutate the listed tasks.
	requestSnapshots := make(map[int64]*dto.TaskVideoInfo)
	var taskIDs []int64
	for _, task := range tasks {
		if task.ID != 0 && taskVideoRequestSnapshot(nil, task.Action) != nil {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	if len(taskIDs) > 0 && model.DB != nil {
		var events []model.TaskEvent
		if model.DB.Where("task_id IN ? AND kind = ?", taskIDs, "request").Find(&events).Error == nil {
			for _, event := range events {
				var payload struct {
					VideoInfo *dto.TaskVideoInfo `json:"video_info"`
				}
				if common.UnmarshalJsonStr(event.Payload, &payload) == nil && payload.VideoInfo != nil {
					requestSnapshots[event.TaskID] = payload.VideoInfo
				}
			}
		}
	}
	videoData := make(map[int64]json.RawMessage)
	var videoIDs []int64
	for _, task := range tasks {
		if task.ID != 0 && (task.PrivateData.VideoInfo != nil || taskVideoRequestSnapshot(nil, task.Action) != nil) && task.Status == model.TaskStatusSuccess && len(task.Data) == 0 {
			videoIDs = append(videoIDs, task.ID)
		}
	}
	if len(videoIDs) > 0 && model.DB != nil {
		var snapshots []struct {
			ID   int64
			Data json.RawMessage
		}
		if model.DB.Model(&model.Task{}).Select("id", "data").Where("id IN ?", videoIDs).Find(&snapshots).Error == nil {
			for _, snapshot := range snapshots {
				videoData[snapshot.ID] = snapshot.Data
			}
		}
	}
	result := make([]*dto.TaskDto, len(tasks))
	for i, task := range tasks {
		if fillUser {
			if user, ok := userIDMap[task.UserId]; ok {
				task.Username = user.Username
			}
		}
		item := relay.TaskModel2Dto(task)
		if billing := task.PrivateData.BillingContext; billing != nil {
			switch {
			case billing.TieredSnapshot != nil:
				item.BillingMode = "tiered_expr"
			case billing.PerCallBilling:
				item.BillingMode = "per_call"
			default:
				item.BillingMode = "per_token"
			}
		}
		videoTask := *task
		if snapshot := requestSnapshots[task.ID]; snapshot != nil {
			videoTask.PrivateData.VideoInfo = snapshot
		}
		if len(videoTask.Data) == 0 {
			videoTask.Data = videoData[task.ID]
		}
		item.VideoInfo = taskVideoLogInfo(&videoTask)
		item.LegacyVideoAvailable = legacyVideoAvailable(task)
		item.ResultDiscarded = task.PrivateData.ResultDiscarded
		item.LegacyAudioAvailable = task.Platform == "suno"
		if viewerRole < common.RoleAdminUser {
			properties := task.Properties
			properties.UpstreamModelName = ""
			item.Properties = properties
			item.Platform = ""
			item.Data = nil
		}
		if task.Status == model.TaskStatusSuccess {
			item.ResultURL = ""
			if taskFailReasonIsLegacyResultURL(task.FailReason) {
				item.FailReason = ""
			}
		}
		if viewerRole >= common.RoleAdminUser {
			adminInfo := &dto.TaskAdminInfo{}
			if execution := task.PrivateData.Execution; execution != nil {
				adminInfo.RequestID = execution.RequestID
				adminInfo.RequestPath = execution.RequestPath
				if snapshot := execution.TaskPlugin; snapshot != nil {
					adminInfo.TaskPlugin = &dto.TaskPluginInfo{
						Key:     snapshot.Key,
						Name:    snapshot.Name,
						Version: snapshot.Version,
					}
					if snapshot.Author != nil {
						adminInfo.TaskPlugin.Author = &dto.TaskPluginAuthorInfo{
							Name: snapshot.Author.Name,
							URL:  snapshot.Author.URL,
						}
					}
				}
			}
			if adminInfo.RequestID != "" || adminInfo.RequestPath != "" || adminInfo.TaskPlugin != nil {
				item.AdminInfo = adminInfo
			}
		}
		if viewerRole >= common.RoleRootUser {
			rootInfo := &dto.TaskRootInfo{
				UpstreamTaskID: task.PrivateData.UpstreamTaskID,
				NodeName:       task.PrivateData.NodeName,
			}
			if execution := task.PrivateData.Execution; execution != nil {
				if snapshot := execution.TaskPlugin; snapshot != nil {
					rootInfo.TaskPlugin = &dto.TaskPluginRuntimeInfo{
						Key:        snapshot.Key,
						Version:    snapshot.Version,
						APIVersion: snapshot.APIVersion,
						Generation: snapshot.Generation,
					}
				}
			}
			if rootInfo.TaskPlugin != nil || rootInfo.UpstreamTaskID != "" || rootInfo.NodeName != "" {
				item.RootInfo = rootInfo
			}
		}
		result[i] = item
	}
	return result
}

// Only recognized video actions qualify; dimensions on image or arbitrary
// plugin requests must not manufacture video metadata.
func taskVideoRequestSnapshot(request any, action string) *dto.TaskVideoInfo {
	switch constant.NormalizeTaskAction(action) {
	case constant.TaskActionTextToVideo, constant.TaskActionImageToVideo,
		constant.TaskActionFirstTailToVideo, constant.TaskActionReferenceToVideo, constant.TaskActionRemix:
	default:
		return nil
	}
	info := &dto.TaskVideoInfo{}
	if request == nil {
		return info
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		return info
	}
	var body map[string]any
	if common.Unmarshal(encoded, &body) != nil {
		return info
	}
	// These are normalized provider containers (Ark metadata, Ali input and
	// parameters, and Veo parameters), not arbitrary user-defined subtrees.
	containers := []map[string]any{body}
	for i := 0; i < len(containers) && i < 16; i++ {
		for _, key := range []string{"metadata", "parameters", "input"} {
			if child, ok := containers[i][key].(map[string]any); ok && len(containers) < 16 {
				containers = append(containers, child)
			}
		}
	}
	for _, fields := range containers {
		if info.DurationSeconds == nil {
			for _, key := range []string{"seconds", "duration", "duration_seconds", "durationSeconds"} {
				if value, exists := fields[key]; exists {
					if n, ok := taskVideoDisplayNumber(value, relaycommon.MaxTaskDurationSeconds); ok {
						info.DurationSeconds = &n
					}
					break
				}
			}
		}
		if info.Resolution == "" {
			for _, key := range []string{"resolution", "size"} {
				if text, ok := fields[key].(string); ok {
					info.Resolution = taskVideoDisplayResolution(text)
					if info.Resolution != "" {
						break
					}
				}
			}
			if info.Resolution == "" {
				w, wok := taskVideoDisplayNumber(fields["width"], 16384)
				h, hok := taskVideoDisplayNumber(fields["height"], 16384)
				if wok && hok && w > 0 && h > 0 && w == math.Trunc(w) && h == math.Trunc(h) {
					info.Resolution = fmt.Sprintf("%.0fx%.0f", w, h)
				}
			}
		}
		for _, key := range []string{"video_url", "video_urls", "reference_video", "reference_videos", "reference_video_url", "reference_video_urls"} {
			if value, exists := fields[key]; exists {
				present, known := taskVideoReferencePresent(value)
				if known && (info.HasReferenceVideo == nil || present) {
					info.HasReferenceVideo = &present
				}
			}
		}
		for _, key := range []string{"content", "media"} {
			items, ok := fields[key].([]any)
			if !ok {
				continue
			}
			known, present := true, false
			for _, item := range items {
				entry, ok := item.(map[string]any)
				if !ok {
					known = false
					continue
				}
				typ, _ := entry["type"].(string)
				switch typ {
				case "video_url", "reference_video":
					value := entry[typ]
					if value == nil {
						value = entry["url"]
					}
					p, k := taskVideoReferencePresent(value)
					present = present || p
					known = known && k
				case "text", "image_url", "reference_image", "first_frame", "last_frame", "audio_url", "reference_audio":
				default:
					known = false
				}
			}
			if present || known && info.HasReferenceVideo == nil {
				info.HasReferenceVideo = &present
			}
		}
	}
	// The basic DTO accepts only an image reference, never a reference video.
	switch request.(type) {
	case dto.VideoRequest, *dto.VideoRequest:
		if info.HasReferenceVideo == nil {
			present := false
			info.HasReferenceVideo = &present
		}
	}
	return info
}

func taskVideoDisplayNumber(value any, limit float64) (float64, bool) {
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case string:
		var err error
		n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= limit
}

var taskVideoResolutionPattern = regexp.MustCompile(`^(?:[1-9][0-9]{1,3}[pP]|[1248][kK]|[1-9][0-9]{0,4}[xX*][1-9][0-9]{0,4})$`)

func taskVideoDisplayResolution(value string) string {
	value = strings.TrimSpace(value)
	if !taskVideoResolutionPattern.MatchString(value) {
		return ""
	}
	value = strings.ToLower(strings.ReplaceAll(value, "*", "x"))
	if w, h, ok := strings.Cut(value, "x"); ok {
		width, _ := strconv.Atoi(w)
		height, _ := strconv.Atoi(h)
		if width > 16384 || height > 16384 {
			return ""
		}
	}
	return value
}

func taskVideoReferencePresent(value any) (bool, bool) {
	switch v := value.(type) {
	case nil:
		return false, true
	case string:
		return strings.TrimSpace(v) != "", true
	case bool:
		return v, true
	case map[string]any:
		if url, exists := v["url"]; exists {
			return taskVideoReferencePresent(url)
		}
		return false, false
	case []any:
		known := true
		for _, item := range v {
			present, valid := taskVideoReferencePresent(item)
			if present {
				return true, true
			}
			known = known && valid
		}
		return false, known
	default:
		return false, false
	}
}

func taskVideoLogInfo(task *model.Task) *dto.TaskVideoInfo {
	snapshot := task.PrivateData.VideoInfo
	if snapshot == nil {
		snapshot = taskVideoRequestSnapshot(nil, task.Action)
		if snapshot == nil {
			return nil
		}
	}
	info := &dto.TaskVideoInfo{Resolution: taskVideoDisplayResolution(snapshot.Resolution)}
	if snapshot.DurationSeconds != nil {
		if n, ok := taskVideoDisplayNumber(*snapshot.DurationSeconds, relaycommon.MaxTaskDurationSeconds); ok {
			info.DurationSeconds = &n
		}
	}
	if snapshot.HasReferenceVideo != nil {
		present := *snapshot.HasReferenceVideo
		info.HasReferenceVideo = &present
	}
	// Do not trust stored estimates, plugin usage units or credits. Only the
	// succeeded upstream response's explicit usage token counts are actual.
	if task.Status == model.TaskStatusSuccess {
		var response struct {
			Resolution string         `json:"resolution"`
			Duration   any            `json:"duration"`
			Usage      map[string]any `json:"usage"`
		}
		if common.Unmarshal(task.Data, &response) == nil {
			if info.Resolution == "" {
				info.Resolution = taskVideoDisplayResolution(response.Resolution)
			}
			if info.DurationSeconds == nil {
				if n, ok := taskVideoDisplayNumber(response.Duration, relaycommon.MaxTaskDurationSeconds); ok {
					info.DurationSeconds = &n
				}
			}
			for _, key := range []string{"total_tokens", "completion_tokens"} {
				value, ok := response.Usage[key].(float64)
				if !ok {
					continue
				}
				if n, valid := taskVideoDisplayNumber(value, math.MaxInt32); valid && n == math.Trunc(n) {
					tokens := common.QuotaFromFloat(n)
					info.ConsumedTokens = &tokens
					break
				}
			}
		}
	}
	return info
}

func taskFailReasonIsLegacyResultURL(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= len("https://") && strings.EqualFold(value[:len("https://")], "https://") ||
		len(value) >= len("http://") && strings.EqualFold(value[:len("http://")], "http://") ||
		len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:")
}
