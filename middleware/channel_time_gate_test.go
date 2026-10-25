package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelTimeGateRejectsExplicitSelectionBeforeInstallingCredentials(t *testing.T) {
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "6,7,14,18")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenSpecificChannelId, "6")
	channel := &model.Channel{Id: 6, Status: common.ChannelStatusEnabled, Key: "test-only-key"}
	err := setupContextForSelectedChannelAt(ctx, channel, "glm-5.3", time.Date(2026, 10, 18, 5, 30, 0, 0, time.UTC))
	require.NotNil(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	assert.Zero(t, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
	assert.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
}
