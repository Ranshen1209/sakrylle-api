package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ErrDeepSeekFileRecordNotFound identifies a tenant inventory miss. The
// inventory is authoritative for gateway ownership: an upstream file ID that
// is not present here must never be routed on behalf of a caller.
var ErrDeepSeekFileRecordNotFound = errors.New("deepseek file record not found")

// ErrDeepSeekFilesListInvalid identifies caller pagination/filter errors.
var ErrDeepSeekFilesListInvalid = errors.New("invalid deepseek files list request")

const (
	// DeepSeekFileMaxUploadBytes is the provider's limit for the file part. The
	// multipart envelope is deliberately excluded from quota accounting.
	DeepSeekFileMaxUploadBytes int64 = 64 << 20

	deepSeekFileQuotaReservationTTL = 30 * time.Minute
	defaultDeepSeekTenantMaxFiles   = int64(1000)
	defaultDeepSeekTenantMaxBytes   = int64(2 * 1024 * 1024 * 1024)
	defaultDeepSeekAccountMaxFiles  = int64(10000)
	defaultDeepSeekAccountMaxBytes  = int64(25 * 1024 * 1024 * 1024)
)

// DeepSeekFileRecord is the protocol-neutral metadata persisted for each file
// uploaded through the gateway. AccountID is internal routing state and is
// never serialized to clients.
type DeepSeekFileRecord struct {
	ID        string     `json:"id"`
	Filename  string     `json:"filename"`
	SizeBytes int64      `json:"size_bytes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Purpose   string     `json:"purpose,omitempty"`
	MimeType  string     `json:"mime_type,omitempty"`
	AccountID int64      `json:"account_id"`
}

// DeepSeekFileInventoryStore is an optional capability implemented by the
// production gateway cache. Keeping it separate from GatewayCache avoids
// forcing unrelated test caches to provide file inventory behavior.
type DeepSeekFileInventoryStore interface {
	StoreDeepSeekFileRecord(ctx context.Context, groupID, userID int64, fileID string, record []byte, createdAt int64) error
	GetDeepSeekFileRecord(ctx context.Context, groupID, userID int64, fileID string) ([]byte, error)
	ListDeepSeekFileRecords(ctx context.Context, groupID, userID int64) ([][]byte, error)
	DeleteDeepSeekFileRecord(ctx context.Context, groupID, userID int64, fileID string) error
}

// DeepSeekFileQuotaReservation is the durable capacity claim made before an
// upload is sent upstream. Its random ID is safe to use as the global Redis
// reservation key; the remaining fields are verified again at commit time.
type DeepSeekFileQuotaReservation struct {
	ID            string
	GroupID       int64
	UserID        int64
	AccountID     int64
	SizeBytes     int64
	SessionHash   string
	ExpiresAtUnix int64
}

type DeepSeekFileQuotaLimits struct {
	TenantMaxFiles  int64
	TenantMaxBytes  int64
	AccountMaxFiles int64
	AccountMaxBytes int64
}

type DeepSeekFileQuotaReservationRequest struct {
	Reservation DeepSeekFileQuotaReservation
	Limits      DeepSeekFileQuotaLimits
	NowUnix     int64
}

type DeepSeekFileQuotaReservationResult struct {
	Reserved             bool
	AffinityAccountID    int64
	AffinityExisted      bool
	TenantQuotaExceeded  bool
	AccountQuotaExceeded bool
}

type DeepSeekFileQuotaCommitRequest struct {
	Reservation DeepSeekFileQuotaReservation
	FileID      string
	Record      []byte
	CreatedAt   int64
}

// DeepSeekFileQuotaStore is optional so existing GatewayCache test doubles do
// not need quota behavior. Production admission fails closed when unavailable.
type DeepSeekFileQuotaStore interface {
	ReserveDeepSeekFileUpload(ctx context.Context, req DeepSeekFileQuotaReservationRequest) (DeepSeekFileQuotaReservationResult, error)
	CommitDeepSeekFileUploadReservation(ctx context.Context, req DeepSeekFileQuotaCommitRequest) error
	ReleaseDeepSeekFileUploadReservation(ctx context.Context, reservationID string) error
}

func (s *OpenAIGatewayService) deepSeekFileInventory() (DeepSeekFileInventoryStore, error) {
	if s == nil || s.cache == nil {
		return nil, deepSeekFileAffinityCacheUnavailableError()
	}
	store, ok := s.cache.(DeepSeekFileInventoryStore)
	if !ok || store == nil {
		return nil, fmt.Errorf("%w: DeepSeek tenant file inventory is unavailable", ErrNoAvailableAccounts)
	}
	return store, nil
}

func (s *OpenAIGatewayService) deepSeekFileQuotaStore() (DeepSeekFileQuotaStore, error) {
	if s == nil || s.cache == nil {
		return nil, deepSeekFileAffinityCacheUnavailableError()
	}
	store, ok := s.cache.(DeepSeekFileQuotaStore)
	if !ok || store == nil {
		return nil, fmt.Errorf("%w: DeepSeek Files quota store is unavailable", ErrNoAvailableAccounts)
	}
	return store, nil
}

func (s *OpenAIGatewayService) deepSeekFileQuotaLimits() (DeepSeekFileQuotaLimits, error) {
	limits := DeepSeekFileQuotaLimits{
		TenantMaxFiles:  defaultDeepSeekTenantMaxFiles,
		TenantMaxBytes:  defaultDeepSeekTenantMaxBytes,
		AccountMaxFiles: defaultDeepSeekAccountMaxFiles,
		AccountMaxBytes: defaultDeepSeekAccountMaxBytes,
	}
	if s != nil && s.cfg != nil {
		cfg := s.cfg.Gateway.DeepSeekFiles
		allZero := cfg.TenantMaxFiles == 0 && cfg.TenantMaxBytes == 0 && cfg.AccountMaxFiles == 0 && cfg.AccountMaxBytes == 0
		if !allZero {
			limits = DeepSeekFileQuotaLimits{
				TenantMaxFiles:  int64(cfg.TenantMaxFiles),
				TenantMaxBytes:  cfg.TenantMaxBytes,
				AccountMaxFiles: int64(cfg.AccountMaxFiles),
				AccountMaxBytes: cfg.AccountMaxBytes,
			}
		}
	}
	if limits.TenantMaxFiles <= 0 || limits.TenantMaxBytes <= 0 ||
		limits.AccountMaxFiles <= 0 || limits.AccountMaxBytes <= 0 ||
		limits.TenantMaxFiles >= limits.AccountMaxFiles || limits.TenantMaxBytes >= limits.AccountMaxBytes {
		return DeepSeekFileQuotaLimits{}, fmt.Errorf("invalid DeepSeek Files quota configuration")
	}
	return limits, nil
}

// ReserveDeepSeekFileUpload atomically reserves both tenant and upstream
// account capacity and claims the tenant's upload shard. Redis errors fail
// closed before any upload bytes are sent upstream.
func (s *OpenAIGatewayService) ReserveDeepSeekFileUpload(
	ctx context.Context,
	groupID *int64,
	userID, accountID, sizeBytes int64,
	sessionHash string,
) (DeepSeekFileQuotaReservation, DeepSeekFileQuotaReservationResult, error) {
	group := derefGroupID(groupID)
	if group <= 0 || userID <= 0 || accountID <= 0 || sizeBytes <= 0 || sizeBytes > DeepSeekFileMaxUploadBytes ||
		strings.TrimSpace(sessionHash) != s.DeepSeekFileUploadSessionHash(userID) {
		return DeepSeekFileQuotaReservation{}, DeepSeekFileQuotaReservationResult{}, fmt.Errorf("invalid DeepSeek Files quota reservation")
	}
	store, err := s.deepSeekFileQuotaStore()
	if err != nil {
		return DeepSeekFileQuotaReservation{}, DeepSeekFileQuotaReservationResult{}, err
	}
	limits, err := s.deepSeekFileQuotaLimits()
	if err != nil {
		return DeepSeekFileQuotaReservation{}, DeepSeekFileQuotaReservationResult{}, err
	}
	reservationID, err := newDeepSeekFileReservationID()
	if err != nil {
		return DeepSeekFileQuotaReservation{}, DeepSeekFileQuotaReservationResult{}, err
	}
	now := time.Now()
	reservation := DeepSeekFileQuotaReservation{
		ID:            reservationID,
		GroupID:       group,
		UserID:        userID,
		AccountID:     accountID,
		SizeBytes:     sizeBytes,
		SessionHash:   strings.TrimSpace(sessionHash),
		ExpiresAtUnix: now.Add(deepSeekFileQuotaReservationTTL).Unix(),
	}
	result, err := store.ReserveDeepSeekFileUpload(ctx, DeepSeekFileQuotaReservationRequest{
		Reservation: reservation,
		Limits:      limits,
		NowUnix:     now.Unix(),
	})
	return reservation, result, err
}

func newDeepSeekFileReservationID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate DeepSeek Files quota reservation: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

func (s *OpenAIGatewayService) ReleaseDeepSeekFileUploadReservation(ctx context.Context, reservationID string) error {
	store, err := s.deepSeekFileQuotaStore()
	if err != nil {
		return err
	}
	if strings.TrimSpace(reservationID) == "" {
		return nil
	}
	releaseCtx, cancel := deepSeekFileOperationContext(ctx, deepSeekFileInventoryStoreTimeout)
	defer cancel()
	return store.ReleaseDeepSeekFileUploadReservation(releaseCtx, strings.TrimSpace(reservationID))
}

// CommitReservedDeepSeekFileUpload moves reserved capacity to used capacity
// in the same Redis transaction that makes the tenant inventory record visible.
// On a persistent store failure, it compensates the exact upstream file.
func (s *OpenAIGatewayService) CommitReservedDeepSeekFileUpload(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	reservation DeepSeekFileQuotaReservation,
	record DeepSeekFileRecord,
) error {
	if account == nil || account.ID != reservation.AccountID || record.AccountID != reservation.AccountID ||
		reservation.ID == "" || record.ID == "" || record.CreatedAt.IsZero() {
		return fmt.Errorf("invalid reserved DeepSeek file upload commit")
	}
	store, err := s.deepSeekFileQuotaStore()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	request := DeepSeekFileQuotaCommitRequest{
		Reservation: reservation,
		FileID:      record.ID,
		Record:      payload,
		CreatedAt:   record.CreatedAt.Unix(),
	}
	persistCtx, cancelPersist := deepSeekFileOperationContext(ctx, deepSeekFileInventoryStoreTimeout)
	defer cancelPersist()

	var storeErr error
	attempts := 0
	for attempt := 0; attempt < deepSeekFileInventoryStoreAttempts; attempt++ {
		attempts++
		storeErr = store.CommitDeepSeekFileUploadReservation(persistCtx, request)
		if storeErr == nil {
			return nil
		}
		if attempt+1 >= deepSeekFileInventoryStoreAttempts || persistCtx.Err() != nil {
			break
		}
		if err := waitDeepSeekFileRetry(persistCtx, time.Duration(attempt+1)*deepSeekFileInventoryStoreBackoff); err != nil {
			break
		}
	}

	compensationCtx, cancelCompensation := deepSeekFileOperationContext(ctx, deepSeekFileCompensationTimeout)
	defer cancelCompensation()
	compensationErr := s.deleteDeepSeekFileOnAccount(compensationCtx, c, account, record.ID)
	cleanupErr := s.ReleaseDeepSeekFileUploadReservation(compensationCtx, reservation.ID)
	if compensationErr == nil {
		cleanupErr = errors.Join(
			cleanupErr,
			s.DeleteDeepSeekFileAccount(compensationCtx, &reservation.GroupID, reservation.UserID, record.ID),
			s.DeleteDeepSeekFileRecord(compensationCtx, &reservation.GroupID, reservation.UserID, record.ID),
		)
	}
	return fmt.Errorf("persist reserved DeepSeek file inventory after %d attempts: %w; compensation: %v",
		attempts, storeErr, errors.Join(compensationErr, cleanupErr))
}

// EnsureDeepSeekFileInventory lets handlers fail before creating an upstream
// file when the tenant ownership store is unavailable. Listing the raw tenant
// index performs a real Redis round trip; a type assertion alone would not
// detect a disconnected store and could leave an untracked upstream file.
func (s *OpenAIGatewayService) EnsureDeepSeekFileInventory(ctx context.Context, groupID *int64, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("invalid DeepSeek file inventory owner")
	}
	store, err := s.deepSeekFileInventory()
	if err != nil {
		return err
	}
	payloads, err := store.ListDeepSeekFileRecords(ctx, derefGroupID(groupID), userID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, payload := range payloads {
		var record DeepSeekFileRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return fmt.Errorf("decode DeepSeek file inventory record: %w", err)
		}
		if record.ExpiresAt != nil && !now.Before(*record.ExpiresAt) {
			if err := store.DeleteDeepSeekFileRecord(ctx, derefGroupID(groupID), userID, record.ID); err != nil {
				return err
			}
			if err := s.DeleteDeepSeekFileAccount(ctx, groupID, userID, record.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseDeepSeekFileRecord normalizes either official Files response family.
func ParseDeepSeekFileRecord(body []byte, nativeAnthropic bool, fallbackMimeType string, accountID int64) (*DeepSeekFileRecord, error) {
	var wire struct {
		ID        string          `json:"id"`
		Filename  string          `json:"filename"`
		Bytes     int64           `json:"bytes"`
		SizeBytes int64           `json:"size_bytes"`
		CreatedAt json.RawMessage `json:"created_at"`
		ExpiresAt json.RawMessage `json:"expires_at"`
		Purpose   string          `json:"purpose"`
		MimeType  string          `json:"mime_type"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("parse DeepSeek file response: %w", err)
	}
	id, err := validateDeepSeekFileID(wire.ID)
	if err != nil {
		return nil, fmt.Errorf("parse DeepSeek file response id: %w", err)
	}
	createdAt, err := parseDeepSeekFileTime(wire.CreatedAt, nativeAnthropic)
	if err != nil {
		return nil, fmt.Errorf("parse DeepSeek file created_at: %w", err)
	}
	var expiresAt *time.Time
	if len(wire.ExpiresAt) > 0 && string(wire.ExpiresAt) != "null" {
		parsed, parseErr := parseDeepSeekFileTime(wire.ExpiresAt, nativeAnthropic)
		if parseErr != nil {
			return nil, fmt.Errorf("parse DeepSeek file expires_at: %w", parseErr)
		}
		expiresAt = &parsed
	}
	size := wire.Bytes
	if nativeAnthropic {
		size = wire.SizeBytes
	}
	purpose := strings.TrimSpace(wire.Purpose)
	if purpose == "" {
		purpose = "user_data"
	}
	mimeType := strings.TrimSpace(wire.MimeType)
	if mimeType == "" {
		mimeType = strings.TrimSpace(fallbackMimeType)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return &DeepSeekFileRecord{
		ID:        id,
		Filename:  wire.Filename,
		SizeBytes: size,
		CreatedAt: createdAt.UTC(),
		ExpiresAt: expiresAt,
		Purpose:   purpose,
		MimeType:  mimeType,
		AccountID: accountID,
	}, nil
}

func parseDeepSeekFileTime(raw json.RawMessage, nativeAnthropic bool) (time.Time, error) {
	if nativeAnthropic {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return time.Time{}, err
		}
		return time.Parse(time.RFC3339, value)
	}
	var unixSeconds int64
	if err := json.Unmarshal(raw, &unixSeconds); err != nil {
		return time.Time{}, err
	}
	return time.Unix(unixSeconds, 0).UTC(), nil
}

// RenderDeepSeekFileRecord emits metadata in the caller's Files API family.
func RenderDeepSeekFileRecord(record DeepSeekFileRecord, nativeAnthropic bool) ([]byte, error) {
	if nativeAnthropic {
		return json.Marshal(struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			SizeBytes int64  `json:"size_bytes"`
			CreatedAt string `json:"created_at"`
			Filename  string `json:"filename"`
			MimeType  string `json:"mime_type"`
		}{
			ID:        record.ID,
			Type:      "file",
			SizeBytes: record.SizeBytes,
			CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339),
			Filename:  record.Filename,
			MimeType:  record.MimeType,
		})
	}
	return json.Marshal(struct {
		ID        string `json:"id"`
		Object    string `json:"object"`
		Bytes     int64  `json:"bytes"`
		CreatedAt int64  `json:"created_at"`
		Filename  string `json:"filename"`
		Purpose   string `json:"purpose"`
		ExpiresAt *int64 `json:"expires_at,omitempty"`
	}{
		ID:        record.ID,
		Object:    "file",
		Bytes:     record.SizeBytes,
		CreatedAt: record.CreatedAt.Unix(),
		Filename:  record.Filename,
		Purpose:   record.Purpose,
		ExpiresAt: unixTimePointer(record.ExpiresAt),
	})
}

func unixTimePointer(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	seconds := value.Unix()
	return &seconds
}

func (s *OpenAIGatewayService) StoreDeepSeekFileRecord(ctx context.Context, groupID *int64, userID int64, record DeepSeekFileRecord) error {
	if userID <= 0 || record.AccountID <= 0 {
		return fmt.Errorf("invalid DeepSeek file inventory owner")
	}
	store, err := s.deepSeekFileInventory()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return store.StoreDeepSeekFileRecord(ctx, derefGroupID(groupID), userID, record.ID, payload, record.CreatedAt.Unix())
}

func (s *OpenAIGatewayService) GetDeepSeekFileRecord(ctx context.Context, groupID *int64, userID int64, fileID string) (*DeepSeekFileRecord, error) {
	if userID <= 0 {
		return nil, ErrDeepSeekFileRecordNotFound
	}
	id, err := validateDeepSeekFileID(fileID)
	if err != nil {
		return nil, err
	}
	store, err := s.deepSeekFileInventory()
	if err != nil {
		return nil, err
	}
	payload, err := store.GetDeepSeekFileRecord(ctx, derefGroupID(groupID), userID, id)
	if err != nil {
		return nil, err
	}
	var record DeepSeekFileRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("decode DeepSeek file inventory record: %w", err)
	}
	if record.ID != id || record.AccountID <= 0 {
		return nil, ErrDeepSeekFileRecordNotFound
	}
	if record.ExpiresAt != nil && !time.Now().Before(*record.ExpiresAt) {
		s.purgeExpiredDeepSeekFileRecord(ctx, store, groupID, userID, id)
		return nil, ErrDeepSeekFileRecordNotFound
	}
	return &record, nil
}

func (s *OpenAIGatewayService) DeleteDeepSeekFileRecord(ctx context.Context, groupID *int64, userID int64, fileID string) error {
	store, err := s.deepSeekFileInventory()
	if err != nil {
		return err
	}
	id, err := validateDeepSeekFileID(fileID)
	if err != nil {
		return err
	}
	return store.DeleteDeepSeekFileRecord(ctx, derefGroupID(groupID), userID, id)
}

func (s *OpenAIGatewayService) purgeExpiredDeepSeekFileRecord(
	ctx context.Context,
	store DeepSeekFileInventoryStore,
	groupID *int64,
	userID int64,
	fileID string,
) {
	_ = store.DeleteDeepSeekFileRecord(ctx, derefGroupID(groupID), userID, fileID)
	_ = s.DeleteDeepSeekFileAccount(ctx, groupID, userID, fileID)
}

// ListDeepSeekFileRecords applies the official family-specific pagination to
// the caller's local tenant inventory, never to a shared upstream API key.
func (s *OpenAIGatewayService) ListDeepSeekFileRecords(ctx context.Context, groupID *int64, userID int64, query url.Values, nativeAnthropic bool) ([]byte, error) {
	store, err := s.deepSeekFileInventory()
	if err != nil {
		return nil, err
	}
	payloads, err := store.ListDeepSeekFileRecords(ctx, derefGroupID(groupID), userID)
	if err != nil {
		return nil, err
	}
	records := make([]DeepSeekFileRecord, 0, len(payloads))
	for _, payload := range payloads {
		var record DeepSeekFileRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return nil, fmt.Errorf("decode DeepSeek file inventory record: %w", err)
		}
		records = append(records, record)
	}
	now := time.Now()
	filtered := records[:0]
	for _, record := range records {
		if record.ExpiresAt != nil && !now.Before(*record.ExpiresAt) {
			s.purgeExpiredDeepSeekFileRecord(ctx, store, groupID, userID, record.ID)
			continue
		}
		if !nativeAnthropic {
			if purpose := strings.TrimSpace(query.Get("purpose")); purpose != "" && record.Purpose != purpose {
				continue
			}
		}
		filtered = append(filtered, record)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})

	limitDefault := 1000
	if nativeAnthropic {
		limitDefault = 20
	}
	limit, err := parseDeepSeekFilesListLimit(query.Get("limit"), limitDefault)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeepSeekFilesListInvalid, err)
	}
	if !nativeAnthropic {
		purpose := strings.TrimSpace(query.Get("purpose"))
		if purpose != "" && purpose != "user_data" {
			return nil, fmt.Errorf("%w: purpose must be user_data", ErrDeepSeekFilesListInvalid)
		}
	}
	if !nativeAnthropic && strings.EqualFold(strings.TrimSpace(query.Get("order")), "desc") {
		reverseDeepSeekFileRecords(filtered)
	} else if !nativeAnthropic {
		order := strings.ToLower(strings.TrimSpace(query.Get("order")))
		if order != "" && order != "asc" {
			return nil, fmt.Errorf("%w: order must be asc or desc", ErrDeepSeekFilesListInvalid)
		}
	}

	after := strings.TrimSpace(query.Get("after"))
	before := ""
	if nativeAnthropic {
		if after != "" || query.Get("order") != "" || query.Get("purpose") != "" {
			return nil, fmt.Errorf("%w: Anthropic Files list does not support after, order, or purpose", ErrDeepSeekFilesListInvalid)
		}
		after = strings.TrimSpace(query.Get("after_id"))
		before = strings.TrimSpace(query.Get("before_id"))
		if after != "" && before != "" {
			return nil, fmt.Errorf("%w: after_id and before_id are mutually exclusive", ErrDeepSeekFilesListInvalid)
		}
		if before != "" {
			filtered = recordsBeforeDeepSeekCursor(filtered, before)
		}
	} else if query.Get("after_id") != "" || query.Get("before_id") != "" {
		return nil, fmt.Errorf("%w: OpenAI Files list uses the after cursor", ErrDeepSeekFilesListInvalid)
	}
	if after != "" {
		filtered = recordsAfterDeepSeekCursor(filtered, after)
	}

	hasMore := len(filtered) > limit
	if len(filtered) > limit {
		if nativeAnthropic && before != "" {
			filtered = filtered[len(filtered)-limit:]
		} else {
			filtered = filtered[:limit]
		}
	}
	return renderDeepSeekFileList(filtered, hasMore, nativeAnthropic)
}

func parseDeepSeekFilesListLimit(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("limit must be between 1 and 1000")
	}
	return limit, nil
}

func recordsAfterDeepSeekCursor(records []DeepSeekFileRecord, cursor string) []DeepSeekFileRecord {
	for i := range records {
		if records[i].ID == cursor {
			return records[i+1:]
		}
	}
	return nil
}

func recordsBeforeDeepSeekCursor(records []DeepSeekFileRecord, cursor string) []DeepSeekFileRecord {
	for i := range records {
		if records[i].ID == cursor {
			return records[:i]
		}
	}
	return nil
}

func reverseDeepSeekFileRecords(records []DeepSeekFileRecord) {
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
}

func renderDeepSeekFileList(records []DeepSeekFileRecord, hasMore, nativeAnthropic bool) ([]byte, error) {
	data := make([]json.RawMessage, 0, len(records))
	for _, record := range records {
		body, err := RenderDeepSeekFileRecord(record, nativeAnthropic)
		if err != nil {
			return nil, err
		}
		data = append(data, body)
	}
	response := map[string]any{"data": data, "has_more": hasMore}
	if !nativeAnthropic {
		response["object"] = "list"
	}
	if len(records) > 0 {
		response["first_id"] = records[0].ID
		response["last_id"] = records[len(records)-1].ID
	}
	return json.Marshal(response)
}

func RenderDeepSeekFileDelete(fileID string, nativeAnthropic bool) ([]byte, error) {
	if nativeAnthropic {
		return json.Marshal(map[string]any{"id": fileID, "type": "file_deleted"})
	}
	return json.Marshal(map[string]any{"id": fileID, "object": "file", "deleted": true})
}
