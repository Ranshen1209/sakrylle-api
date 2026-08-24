package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	// DeepSeek documents a 64 MiB file limit. Leave room for multipart framing
	// and form fields while keeping the gateway request bounded.
	deepSeekFilesMaxRequestBytes  int64 = 68 << 20
	deepSeekFilesMaxResponseBytes int64 = 8 << 20
	deepSeekFilesMinExpirySeconds int64 = 60 * 60
	deepSeekFilesMaxExpirySeconds int64 = 30 * 24 * 60 * 60
)

// Files proxies both DeepSeek Files API families behind a tenant inventory.
//
// Upstream API keys are shared gateway resources, so their native list is not
// a security boundary. Uploads create user-scoped records; retrieve, delete,
// list, and model references all require that ownership record.
func (h *OpenAIGatewayHandler) Files(c *gin.Context) {
	streamStarted := false
	setOpenAIClientTransportHTTP(c)
	nativeAnthropic := service.DeepSeekFilesUseNativeAnthropicIngress(c)
	setDeepSeekFilesNativeAnthropicErrors(c, nativeAnthropic)
	if nativeAnthropic {
		defer h.recoverAnthropicMessagesPanic(c, &streamStarted)
	} else {
		defer h.recoverResponsesPanic(c, &streamStarted)
	}
	filesError := func(status int, errType, message string) {
		if nativeAnthropic {
			h.anthropicErrorResponse(c, status, errType, message)
			return
		}
		h.errorResponse(c, status, errType, message)
	}

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		filesError(http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if !deepSeekFilesPlatformAllowed(c, apiKey) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		filesError(http.StatusNotFound, "not_found_error", "Files API is only available for DeepSeek groups")
		return
	}
	if h == nil || h.gatewayService == nil || h.concurrencyHelper == nil {
		filesError(http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		filesError(http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(c.Request.Method))
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodDelete {
		filesError(http.StatusMethodNotAllowed, "invalid_request_error", "Unsupported Files API method")
		return
	}

	fileID := strings.TrimSpace(c.Param("file_id"))
	if method == http.MethodPost && fileID != "" {
		filesError(http.StatusBadRequest, "invalid_request_error", "POST /files does not accept a file id")
		return
	}
	if method == http.MethodDelete && fileID == "" {
		filesError(http.StatusBadRequest, "invalid_request_error", "DELETE /files/:file_id requires a file id")
		return
	}
	if fileID != "" {
		if err := service.ValidateDeepSeekFileID(fileID); err != nil {
			filesError(http.StatusBadRequest, "invalid_request_error", "Invalid file id")
			return
		}
	}

	reqLog := requestLogger(
		c,
		"handler.openai_gateway.files",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("method", method),
		zap.String("file_id", fileID),
	)

	body, err := readDeepSeekFilesRequestBody(c)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			filesError(http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		if errors.Is(err, errDeepSeekFilesBodyTooLarge) {
			filesError(http.StatusRequestEntityTooLarge, "invalid_request_error", "Files API request exceeds the 64 MiB file limit")
			return
		}
		filesError(http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	var uploadMetadata deepSeekUploadedFileMetadata
	if method == http.MethodPost {
		uploadMetadata, err = parseDeepSeekUploadedFileMetadata(c.GetHeader("Content-Type"), body)
		if err != nil {
			if errors.Is(err, errDeepSeekFilesFileTooLarge) {
				filesError(http.StatusRequestEntityTooLarge, "invalid_request_error", "Files API file part exceeds the 64 MiB limit")
				return
			}
			filesError(http.StatusBadRequest, "invalid_request_error", "Invalid Files API multipart upload")
			return
		}
	}

	setOpsRequestContext(c, "", false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	if method == http.MethodPost {
		if inventoryErr := h.gatewayService.EnsureDeepSeekFileInventory(c.Request.Context(), apiKey.GroupID, subject.UserID); inventoryErr != nil {
			reqLog.Error("deepseek_files.inventory_unavailable", zap.Error(inventoryErr))
			filesError(http.StatusServiceUnavailable, "api_error", "File inventory temporarily unavailable")
			return
		}
	}

	// A shared DeepSeek API key is not a tenant boundary. List from the
	// gateway's user-scoped inventory instead of exposing the upstream key's
	// aggregate file list.
	if method == http.MethodGet && fileID == "" {
		responseBody, listErr := h.gatewayService.ListDeepSeekFileRecords(
			c.Request.Context(), apiKey.GroupID, subject.UserID, c.Request.URL.Query(), nativeAnthropic,
		)
		if listErr != nil {
			if errors.Is(listErr, service.ErrDeepSeekFilesListInvalid) {
				filesError(http.StatusBadRequest, "invalid_request_error", listErr.Error())
				return
			}
			reqLog.Error("deepseek_files.inventory_list_failed", zap.Error(listErr))
			filesError(http.StatusServiceUnavailable, "api_error", "File inventory temporarily unavailable")
			return
		}
		c.Data(http.StatusOK, "application/json", responseBody)
		return
	}

	var existingRecord *service.DeepSeekFileRecord
	if fileID != "" {
		existingRecord, err = h.gatewayService.GetDeepSeekFileRecord(c.Request.Context(), apiKey.GroupID, subject.UserID, fileID)
		if err != nil {
			if errors.Is(err, service.ErrDeepSeekFileRecordNotFound) {
				filesError(http.StatusNotFound, "not_found_error", "File not found")
				return
			}
			reqLog.Error("deepseek_files.inventory_lookup_failed", zap.Error(err))
			filesError(http.StatusServiceUnavailable, "api_error", "File inventory temporarily unavailable")
			return
		}
	}

	// Item operations use the file owner's binding. Uploads use a stable tenant
	// shard so every file later referenced by one request belongs to one upstream
	// Files namespace.
	sessionHash := h.gatewayService.DeepSeekFileSessionHash(subject.UserID, fileID)
	if method == http.MethodPost {
		sessionHash = h.gatewayService.DeepSeekFileUploadSessionHash(subject.UserID)
	}

	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}

	failedAccountIDs := make(map[int64]struct{})
	maxSwitches := h.maxAccountSwitches
	if maxSwitches <= 0 {
		maxSwitches = 3
	}
	activeReservationID := ""
	releaseActiveReservation := func(event string) {
		if activeReservationID == "" {
			return
		}
		reservationID := activeReservationID
		activeReservationID = ""
		if releaseErr := h.gatewayService.ReleaseDeepSeekFileUploadReservation(c.Request.Context(), reservationID); releaseErr != nil {
			reqLog.Error("deepseek_files.quota_release_failed", zap.String("event", event), zap.Error(releaseErr))
		}
	}
	defer releaseActiveReservation("handler_exit")

	switchCount := 0
	for {
		selection, _, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapability(
			c.Request.Context(),
			apiKey.GroupID,
			"",
			sessionHash,
			"",
			failedAccountIDs,
			service.OpenAIUpstreamTransportHTTPSSE,
			"",
			false,
			false,
			false,
			service.PlatformDeepseek,
		)
		if selectErr != nil || selection == nil || selection.Account == nil {
			reqLog.Warn("deepseek_files.account_select_failed", zap.Error(selectErr), zap.Int("excluded_account_count", len(failedAccountIDs)))
			markOpsRoutingCapacityLimited(c)
			filesError(http.StatusServiceUnavailable, "api_error", "No available accounts")
			return
		}

		account := selection.Account
		if existingRecord != nil && existingRecord.AccountID != account.ID {
			reqLog.Error("deepseek_files.owner_selection_mismatch",
				zap.Int64("inventory_account_id", existingRecord.AccountID),
				zap.Int64("selected_account_id", account.ID),
			)
			filesError(http.StatusServiceUnavailable, "api_error", "File owner account is unavailable")
			return
		}
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountRelease, slotResult := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, false, &streamStarted, reqLog)
		if slotResult == openAISlotAcquireProfitVetoed {
			failedAccountIDs[account.ID] = struct{}{}
			if switchCount >= maxSwitches {
				h.handleOpenAIProfitVetoExhausted(c, streamStarted, reqLog, switchCount+1)
				return
			}
			switchCount++
			continue
		}
		if slotResult != openAISlotAcquireOK {
			return
		}
		if method == http.MethodPost && uploadMetadata.hasExpiresAfter && service.DeepSeekFilesUseNativeAnthropicUpstream(c, account) {
			if accountRelease != nil {
				accountRelease()
			}
			failedAccountIDs[account.ID] = struct{}{}
			reqLog.Info("deepseek_files.upstream_protocol_mismatch", zap.Int64("account_id", account.ID))
			if switchCount >= maxSwitches {
				filesError(http.StatusServiceUnavailable, "api_error", "File upload protocol is unavailable for this tenant")
				return
			}
			switchCount++
			continue
		}

		var reservation service.DeepSeekFileQuotaReservation
		if method == http.MethodPost {
			var quotaResult service.DeepSeekFileQuotaReservationResult
			var quotaErr error
			reservation, quotaResult, quotaErr = h.gatewayService.ReserveDeepSeekFileUpload(
				c.Request.Context(), apiKey.GroupID, subject.UserID, account.ID, uploadMetadata.sizeBytes, sessionHash,
			)
			if quotaErr != nil {
				if accountRelease != nil {
					accountRelease()
				}
				reqLog.Error("deepseek_files.quota_reserve_failed", zap.Error(quotaErr), zap.Int64("account_id", account.ID))
				filesError(http.StatusServiceUnavailable, "api_error", "File inventory temporarily unavailable")
				return
			}
			if !quotaResult.Reserved {
				if accountRelease != nil {
					accountRelease()
				}
				switch {
				case quotaResult.TenantQuotaExceeded:
					filesError(http.StatusTooManyRequests, "rate_limit_error", "DeepSeek Files tenant storage quota exceeded")
					return
				case quotaResult.AccountQuotaExceeded && quotaResult.AffinityExisted:
					markOpsRoutingCapacityLimited(c)
					filesError(http.StatusServiceUnavailable, "api_error", "File storage account is full")
					return
				case quotaResult.AccountQuotaExceeded:
					failedAccountIDs[account.ID] = struct{}{}
				case quotaResult.AffinityAccountID > 0 && quotaResult.AffinityAccountID != account.ID:
					failedAccountIDs[account.ID] = struct{}{}
				default:
					filesError(http.StatusServiceUnavailable, "api_error", "File capacity could not be reserved safely")
					return
				}
				if switchCount >= maxSwitches {
					markOpsRoutingCapacityLimited(c)
					filesError(http.StatusServiceUnavailable, "api_error", "No file storage account has available capacity")
					return
				}
				switchCount++
				continue
			}
			activeReservationID = reservation.ID
		}

		resp, forwardErr := func() (*http.Response, error) {
			if accountRelease != nil {
				defer accountRelease()
			}
			return h.gatewayService.ForwardDeepSeekFiles(c.Request.Context(), c, account, method, fileID, body)
		}()
		if forwardErr != nil {
			// Multipart normalization runs before the upstream HTTP request. A
			// valid OpenAI expires_after upload cannot use a fixed Anthropic
			// Files account, but it can safely try another protocol-capable
			// account because no upload bytes have been sent yet.
			if errors.Is(forwardErr, service.ErrDeepSeekFilesUpstreamProtocolUnsupported) {
				releaseActiveReservation("protocol_mismatch")
				failedAccountIDs[account.ID] = struct{}{}
				reqLog.Info("deepseek_files.upstream_protocol_mismatch", zap.Int64("account_id", account.ID), zap.Error(forwardErr))
				continue
			}
			if errors.Is(forwardErr, service.ErrDeepSeekFilesInvalidUpload) {
				filesError(http.StatusBadRequest, "invalid_request_error", "Invalid Files API multipart upload")
				return
			}
			var failoverErr *service.UpstreamFailoverError
			// A Files item operation is scoped to the account that owns the ID;
			// retrying it on another account can turn a valid reference into a
			// misleading 404 (or, worse, expose another tenant's metadata). POST
			// uploads are also never replayed: a transport failure after sending
			// the body is ambiguous and could create duplicate permanent files.
			if errors.As(forwardErr, &failoverErr) && deepSeekFilesMayFailoverAcrossAccounts(method, fileID) && switchCount < maxSwitches && !failoverClientGone(c) {
				failedAccountIDs[account.ID] = struct{}{}
				reqLog.Warn("deepseek_files.upstream_transport_failover", zap.Int64("account_id", account.ID), zap.Error(forwardErr))
				switchCount++
				continue
			}
			reqLog.Warn("deepseek_files.forward_failed", zap.Int64("account_id", account.ID), zap.Error(forwardErr))
			filesError(http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return
		}
		if resp == nil {
			filesError(http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return
		}

		responseBody, readErr := readDeepSeekFilesResponseBody(resp)
		if readErr != nil {
			reqLog.Warn("deepseek_files.response_read_failed", zap.Int64("account_id", account.ID), zap.Error(readErr))
			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, "", false, nil)
			filesError(http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
			return
		}
		upstreamSuccess := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
		idempotentDelete := method == http.MethodDelete && resp.StatusCode == http.StatusNotFound
		scheduleSuccess := h.gatewayService.HandleDeepSeekFilesUpstreamResult(
			c.Request.Context(), account, resp.StatusCode, resp.Header, responseBody,
		)
		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, "", scheduleSuccess, nil)
		normalizeDeepSeekFilesRedirectResponse(resp)
		upstreamNativeAnthropic := service.DeepSeekFilesUseNativeAnthropicUpstream(c, account)
		if method == http.MethodGet && fileID != "" && resp.StatusCode == http.StatusNotFound {
			cleanupErr := errors.Join(
				h.gatewayService.DeleteDeepSeekFileAccount(c.Request.Context(), apiKey.GroupID, subject.UserID, fileID),
				h.gatewayService.DeleteDeepSeekFileRecord(c.Request.Context(), apiKey.GroupID, subject.UserID, fileID),
			)
			if cleanupErr != nil {
				reqLog.Error("deepseek_files.stale_inventory_delete_failed", zap.Error(cleanupErr), zap.Int64("account_id", account.ID))
			}
		}
		if upstreamSuccess || idempotentDelete {
			if resp.Header == nil {
				resp.Header = make(http.Header)
			}
			switch method {
			case http.MethodDelete:
				cleanupErr := errors.Join(
					h.gatewayService.DeleteDeepSeekFileAccount(c.Request.Context(), apiKey.GroupID, subject.UserID, fileID),
					h.gatewayService.DeleteDeepSeekFileRecord(c.Request.Context(), apiKey.GroupID, subject.UserID, fileID),
				)
				if cleanupErr != nil {
					// Upstream deletion is complete (or already complete). Retrying it
					// cannot repair Redis, so preserve idempotent success and alert.
					reqLog.Error("deepseek_files.inventory_delete_failed", zap.Error(cleanupErr), zap.Int64("account_id", account.ID))
				}
				responseBody, _ = service.RenderDeepSeekFileDelete(fileID, nativeAnthropic)
				resp.StatusCode = http.StatusOK
				resp.Header.Set("Content-Type", "application/json")

			case http.MethodPost:
				record, parseErr := service.ParseDeepSeekFileRecord(
					responseBody, upstreamNativeAnthropic, uploadMetadata.mimeType, account.ID,
				)
				if parseErr != nil {
					reqLog.Error("deepseek_files.upload_response_invalid", zap.Error(parseErr), zap.Int64("account_id", account.ID))
					h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, "", false, nil)
					if uploadedFileID, ok := service.DeepSeekFileIDFromUploadResponse(responseBody); ok {
						if compensateErr := h.gatewayService.CompensateDeepSeekFileUpload(
							c.Request.Context(), c, account, uploadedFileID,
						); compensateErr != nil {
							reqLog.Error("deepseek_files.invalid_upload_response_compensation_failed",
								zap.Error(compensateErr), zap.Int64("account_id", account.ID), zap.String("file_id", uploadedFileID))
						}
					}
					filesError(http.StatusBadGateway, "upstream_error", "Invalid upstream file response")
					return
				}
				if record.ExpiresAt == nil && uploadMetadata.expiresAfterSeconds > 0 {
					expiresAt := record.CreatedAt.Add(time.Duration(uploadMetadata.expiresAfterSeconds) * time.Second)
					record.ExpiresAt = &expiresAt
				}
				if storeErr := h.gatewayService.CommitReservedDeepSeekFileUpload(
					c.Request.Context(), c, account, reservation, *record,
				); storeErr != nil {
					reqLog.Error("deepseek_files.inventory_store_failed", zap.Error(storeErr), zap.Int64("account_id", account.ID))
					filesError(http.StatusServiceUnavailable, "api_error", "File upload could not be completed safely; retry later")
					return
				}
				activeReservationID = ""
				// The tenant inventory is authoritative and already makes the file
				// safe to route. This sticky key is only a scheduler fast path, so a
				// transient write failure must not turn a completed upload into an
				// ambiguous client-visible failure.
				if bindErr := h.gatewayService.BindDeepSeekFileAccount(
					c.Request.Context(), apiKey.GroupID, subject.UserID, record.ID, account.ID,
				); bindErr != nil {
					reqLog.Warn("deepseek_files.affinity_bind_failed", zap.Error(bindErr), zap.Int64("account_id", account.ID))
				}
				responseBody, _ = service.RenderDeepSeekFileRecord(*record, nativeAnthropic)
				resp.Header.Set("Content-Type", "application/json")

			case http.MethodGet:
				fallbackMime := ""
				if existingRecord != nil {
					fallbackMime = existingRecord.MimeType
				}
				record, parseErr := service.ParseDeepSeekFileRecord(responseBody, upstreamNativeAnthropic, fallbackMime, account.ID)
				if parseErr != nil || record.ID != fileID {
					reqLog.Error("deepseek_files.retrieve_response_invalid", zap.Error(parseErr), zap.Int64("account_id", account.ID))
					h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, "", false, nil)
					filesError(http.StatusBadGateway, "upstream_error", "Invalid upstream file response")
					return
				}
				preserveDeepSeekFileRefreshExpiry(record, existingRecord)
				if storeErr := h.gatewayService.StoreDeepSeekFileRecord(c.Request.Context(), apiKey.GroupID, subject.UserID, *record); storeErr != nil {
					reqLog.Error("deepseek_files.inventory_refresh_failed", zap.Error(storeErr), zap.Int64("account_id", account.ID))
					filesError(http.StatusServiceUnavailable, "api_error", "File inventory temporarily unavailable")
					return
				}
				responseBody, _ = service.RenderDeepSeekFileRecord(*record, nativeAnthropic)
				resp.Header.Set("Content-Type", "application/json")
			}
		}
		if !upstreamSuccess && !idempotentDelete {
			if resp.Header == nil {
				resp.Header = make(http.Header)
			}
			renderedError, renderErr := service.RenderDeepSeekFilesError(resp.StatusCode, responseBody, nativeAnthropic)
			if renderErr != nil {
				reqLog.Error("deepseek_files.error_response_render_failed", zap.Error(renderErr), zap.Int64("account_id", account.ID))
				filesError(http.StatusBadGateway, "upstream_error", "Upstream request failed")
				return
			}
			responseBody = renderedError
			resp.Header.Set("Content-Type", "application/json")
		}
		writeDeepSeekFilesResponse(c, h.gatewayService, resp, responseBody)
		return
	}
}

var (
	errDeepSeekFilesBodyTooLarge = errors.New("deepseek files request body too large")
	errDeepSeekFilesFileTooLarge = errors.New("deepseek files file part too large")
)

func readDeepSeekFilesRequestBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, nil
	}
	limited := io.LimitReader(c.Request.Body, deepSeekFilesMaxRequestBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > deepSeekFilesMaxRequestBytes {
		return nil, errDeepSeekFilesBodyTooLarge
	}
	return body, nil
}

func readDeepSeekFilesResponseBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, deepSeekFilesMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > deepSeekFilesMaxResponseBytes {
		return nil, fmt.Errorf("DeepSeek Files response exceeds %d bytes", deepSeekFilesMaxResponseBytes)
	}
	return body, nil
}

func deepSeekFilesPlatformAllowed(c *gin.Context, apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.Group == nil {
		return false
	}
	if apiKey.Group.Platform == service.PlatformDeepseek {
		return true
	}
	if apiKey.Group.Platform == service.PlatformComposite && c != nil && c.Request != nil {
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		return ok && platform == service.PlatformDeepseek
	}
	return false
}

type deepSeekUploadedFileMetadata struct {
	mimeType            string
	expiresAfterSeconds int64
	sizeBytes           int64
	hasExpiresAfter     bool
}

func parseDeepSeekUploadedFileMetadata(contentType string, body []byte) (deepSeekUploadedFileMetadata, error) {
	return parseDeepSeekUploadedFileMetadataWithLimit(contentType, body, service.DeepSeekFileMaxUploadBytes)
}

func parseDeepSeekUploadedFileMetadataWithLimit(contentType string, body []byte, maxFileBytes int64) (deepSeekUploadedFileMetadata, error) {
	var metadata deepSeekUploadedFileMetadata
	if maxFileBytes <= 0 {
		return metadata, fmt.Errorf("invalid file size limit")
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || strings.TrimSpace(params["boundary"]) == "" {
		return metadata, fmt.Errorf("invalid multipart content type")
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var expiryAnchor, expirySeconds string
	var expiryAnchorParts, expirySecondsParts int
	fileParts := 0
	for {
		// Match the forwarding normalizer's raw-part semantics so both the 64 MiB
		// limit and quota charge use the exact bytes sent upstream.
		part, partErr := reader.NextRawPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			return metadata, fmt.Errorf("read multipart upload: %w", partErr)
		}
		switch part.FormName() {
		case "file":
			fileParts++
			if fileParts > 1 {
				_ = part.Close()
				return metadata, fmt.Errorf("multipart upload must contain exactly one file")
			}
			metadata.mimeType = strings.TrimSpace(part.Header.Get("Content-Type"))
			inferredType := mime.TypeByExtension(filepath.Ext(part.FileName()))
			if metadata.mimeType == "" || (metadata.mimeType == "application/octet-stream" && inferredType != "") {
				metadata.mimeType = inferredType
			}
			size, readErr := io.Copy(io.Discard, io.LimitReader(part, maxFileBytes+1))
			if readErr != nil {
				_ = part.Close()
				return metadata, fmt.Errorf("read multipart file: %w", readErr)
			}
			if size > maxFileBytes {
				_ = part.Close()
				return metadata, errDeepSeekFilesFileTooLarge
			}
			metadata.sizeBytes = size
		case "expires_after[anchor]", "expires_after[seconds]":
			metadata.hasExpiresAfter = true
			value, readErr := io.ReadAll(io.LimitReader(part, 129))
			if readErr != nil {
				_ = part.Close()
				return metadata, fmt.Errorf("read multipart expiry field: %w", readErr)
			}
			if len(value) > 128 {
				_ = part.Close()
				return metadata, fmt.Errorf("multipart expiry field is too large")
			}
			if part.FormName() == "expires_after[anchor]" {
				expiryAnchorParts++
				expiryAnchor = strings.TrimSpace(string(value))
			} else {
				expirySecondsParts++
				expirySeconds = strings.TrimSpace(string(value))
			}
		}
		if closeErr := part.Close(); closeErr != nil {
			return metadata, fmt.Errorf("close multipart part: %w", closeErr)
		}
	}
	if fileParts != 1 || metadata.sizeBytes <= 0 {
		return metadata, fmt.Errorf("multipart upload must contain one non-empty file")
	}
	if !metadata.hasExpiresAfter {
		return metadata, nil
	}
	if expiryAnchorParts != 1 || expirySecondsParts != 1 || expiryAnchor != "created_at" {
		return metadata, fmt.Errorf("expires_after requires one created_at anchor and one seconds value")
	}
	seconds, err := strconv.ParseInt(expirySeconds, 10, 64)
	if err != nil || seconds < deepSeekFilesMinExpirySeconds || seconds > deepSeekFilesMaxExpirySeconds {
		return metadata, fmt.Errorf("expires_after seconds must be between %d and %d", deepSeekFilesMinExpirySeconds, deepSeekFilesMaxExpirySeconds)
	}
	metadata.expiresAfterSeconds = seconds
	return metadata, nil
}

func writeDeepSeekFilesResponse(c *gin.Context, gatewayService *service.OpenAIGatewayService, resp *http.Response, body []byte) {
	if c == nil || resp == nil {
		return
	}
	if gatewayService != nil {
		gatewayService.WriteDeepSeekFilesResponseHeaders(c.Writer.Header(), resp.Header)
	}
	if strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Status(resp.StatusCode)
	if len(body) > 0 {
		_, _ = c.Writer.Write(body)
	}
}

func deepSeekFilesMayFailoverAcrossAccounts(method, fileID string) bool {
	// Only a key-wide read can be replayed safely on a different account. The
	// handler serves list requests from tenant inventory, but keeping the rule
	// explicit here prevents a future proxy path from treating POST as safe.
	return strings.EqualFold(strings.TrimSpace(method), http.MethodGet) && strings.TrimSpace(fileID) == ""
}

func preserveDeepSeekFileRefreshExpiry(record, existing *service.DeepSeekFileRecord) {
	if record == nil || existing == nil || record.ExpiresAt != nil || existing.ExpiresAt == nil {
		return
	}
	expiresAt := *existing.ExpiresAt
	record.ExpiresAt = &expiresAt
}

func normalizeDeepSeekFilesRedirectResponse(resp *http.Response) {
	if resp == nil || resp.StatusCode < http.StatusMultipleChoices || resp.StatusCode >= http.StatusBadRequest {
		return
	}
	resp.StatusCode = http.StatusBadGateway
	for key := range resp.Header {
		if strings.EqualFold(key, "location") {
			delete(resp.Header, key)
		}
	}
}

const deepSeekFilesNativeAnthropicErrorsKey = "deepseek_files_native_anthropic_errors"

func setDeepSeekFilesNativeAnthropicErrors(c *gin.Context, enabled bool) {
	if c != nil && enabled {
		c.Set(deepSeekFilesNativeAnthropicErrorsKey, true)
	}
}

func useDeepSeekFilesNativeAnthropicErrors(c *gin.Context) bool {
	if c == nil {
		return false
	}
	enabled, _ := c.Get(deepSeekFilesNativeAnthropicErrorsKey)
	value, _ := enabled.(bool)
	return value
}
