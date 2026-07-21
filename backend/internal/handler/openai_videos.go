package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Videos handles Agnes async video generation. POST /v1/videos
func (h *OpenAIGatewayHandler) Videos(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.videos",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	parsed, err := service.ParseOpenAIVideosRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	requestModel := parsed.Model

	reqLog = reqLog.With(
		zap.String("model", requestModel),
	)
	setOpsRequestContext(c, requestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(false, false)))
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, body); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestModel)

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai.videos.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)

	selection, _, err := h.gatewayService.SelectAccountWithSchedulerForImages(
		c.Request.Context(),
		apiKey.GroupID,
		sessionHash,
		requestModel,
		map[int64]struct{}{},
		service.OpenAIImagesCapabilityBasic,
	)
	if err != nil || selection == nil || selection.Account == nil {
		markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available compatible accounts")
		return
	}

	account := selection.Account
	reqLog.Debug("openai.videos.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
	setOpsSelectedAccount(c, account.ID, account.Platform)

	if !account.IsVideoEnabled() || !account.IsDeclaredVideoModel(requestModel) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is not a supported video model for this account")
		return
	}

	upstreamModel := account.GetMappedModel(requestModel)

	result, err := h.gatewayService.SubmitVideo(c.Request.Context(), c, account, parsed, requestModel, upstreamModel, time.Now())
	if err != nil {
		var imageUpstreamErr *service.OpenAIImagesUpstreamError
		if errors.As(err, &imageUpstreamErr) {
			reqLog.Warn("openai.videos.upstream_user_error",
				zap.Int64("account_id", account.ID),
				zap.Int("status_code", imageUpstreamErr.StatusCode),
				zap.Error(err),
			)
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
		reqLog.Error("openai.videos.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		h.ensureForwardErrorResponse(c, streamStarted)
		return
	}

	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)

	upstreamModelResult := ""
	if result != nil {
		upstreamModelResult = result.UpstreamModel
	}

	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			ChannelUsageFields: channelMapping.ToUsageFields(requestModel, upstreamModelResult),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.videos"),
				zap.Int64("user_id", subject.UserID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("model", requestModel),
				zap.Int64("account_id", account.ID),
			).Error("openai.videos.record_usage_failed", zap.Error(err))
		}
	})

	reqLog.Debug("openai.videos.request_completed", zap.Int64("account_id", account.ID))
}

// RetrieveVideo handles GET /v1/videos/{id} — proxies the Agnes task status. No billing.
func (h *OpenAIGatewayHandler) RetrieveVideo(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.videos.retrieve",
		zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", apiKey.ID), zap.Any("group_id", apiKey.GroupID))
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	taskID := strings.TrimSpace(c.Param("id"))
	if taskID == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "missing video id")
		return
	}

	// NOTE: retrieve correctness assumes single-account groups — the task lives only on
	// the upstream account that submitted it. group 23 (Agnes) is single-account. A future
	// multi-account video group would need the submitting account pinned into the task id.
	selection, _, err := h.gatewayService.SelectAccountWithSchedulerForImages(
		c.Request.Context(), apiKey.GroupID, "", "", map[int64]struct{}{}, service.OpenAIImagesCapabilityBasic)
	if err != nil || selection == nil || selection.Account == nil {
		markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available compatible accounts")
		return
	}
	account := selection.Account
	setOpsSelectedAccount(c, account.ID, account.Platform)

	if err := h.gatewayService.RetrieveVideo(c.Request.Context(), c, account, taskID); err != nil {
		var imageUpstreamErr *service.OpenAIImagesUpstreamError
		if errors.As(err, &imageUpstreamErr) {
			reqLog.Warn("openai.videos.retrieve_upstream_error", zap.Int("status_code", imageUpstreamErr.StatusCode), zap.Error(err))
			return // asyncFail already wrote the response
		}
		if c.Request.Context().Err() != nil {
			return
		}
		reqLog.Error("openai.videos.retrieve_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		h.ensureForwardErrorResponse(c, streamStarted)
		return
	}
	reqLog.Debug("openai.videos.retrieve_completed", zap.Int64("account_id", account.ID), zap.String("task_id", taskID))
}
