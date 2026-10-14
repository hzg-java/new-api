package controller

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogStatsTotalTokensFollowFilters(t *testing.T) {
	setupManageUserTestDB(t)
	now := time.Now().Unix()
	logs := []model.Log{
		{Type: model.LogTypeConsume, CreatedAt: now - 3600, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 100, CompletionTokens: 50, Quota: 10},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 20, CompletionTokens: 30, Quota: 5},
		{Type: model.LogTypeConsume, CreatedAt: now - 7200, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 900},
		{Type: model.LogTypeConsume, CreatedAt: now - 10, Username: "another", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 800},
		{Type: model.LogTypeTopup, CreatedAt: now - 10, Username: "owner", ModelName: "model-a", TokenName: "key-a", ChannelId: 2, Group: "default", PromptTokens: 700},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	for _, self := range []bool{false, true} {
		for _, tc := range []struct {
			name, model     string
			total, rpm, tpm int
		}{
			{"matched", "model-a", 200, 1, 50},
			{"empty", "missing", 0, 0, 0},
		} {
			t.Run(tc.name+map[bool]string{false: "/admin", true: "/self"}[self], func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("GET", "/?start_timestamp="+strconv.FormatInt(now-4000, 10)+"&end_timestamp="+strconv.FormatInt(now+1, 10)+"&username=owner&model_name="+tc.model+"&token_name=key-a&channel=2", nil)
				c.Set("username", "owner")
				if self {
					GetLogsSelfStat(c)
				} else {
					GetLogsStat(c)
				}
				var response struct {
					Success bool           `json:"success"`
					Data    map[string]int `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.True(t, response.Success)
				assert.Equal(t, tc.total, response.Data["total_tokens"])
				assert.Contains(t, response.Data, "total_tokens")
				assert.Equal(t, tc.rpm, response.Data["rpm"])
				assert.Equal(t, tc.tpm, response.Data["tpm"])
			})
		}
	}
}
