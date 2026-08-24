package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const stickySessionPrefix = "sticky_session:"
const openAIResponsesSessionWindowPrefix = "openai_responses_session_window:"
const liveCallPrefix = "live:call:"

const deepSeekFileInventoryPrefix = "deepseek_file_inventory:"

const (
	deepSeekFileQuotaPrefix               = "deepseek_file_quota:"
	deepSeekFileQuotaReservationsKey      = deepSeekFileQuotaPrefix + "reservations"
	deepSeekFileQuotaReservationExpiryKey = deepSeekFileQuotaPrefix + "reservation_expiry"
)

type gatewayCache struct {
	rdb *redis.Client
}

func NewGatewayCache(rdb *redis.Client) service.GatewayCache {
	return &gatewayCache{rdb: rdb}
}

// buildSessionKey 构建 session key，包含 groupID 实现分组隔离
// 格式: sticky_session:{groupID}:{sessionHash}
func buildSessionKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", stickySessionPrefix, groupID, sessionHash)
}

func deepSeekFileInventoryKeys(groupID, userID int64) (recordsKey, createdAtKey string) {
	tenantKey := fmt.Sprintf("%s%d:%d", deepSeekFileInventoryPrefix, groupID, userID)
	return tenantKey + ":records", tenantKey + ":created_at"
}

func deepSeekFileTenantQuotaKeys(groupID, userID int64) (usageKey, filesKey string) {
	tenantKey := fmt.Sprintf("%stenant:%d:%d", deepSeekFileQuotaPrefix, groupID, userID)
	return tenantKey + ":usage", tenantKey + ":files"
}

func deepSeekFileAccountQuotaUsageKey(accountID int64) string {
	return fmt.Sprintf("%saccount:%d:usage", deepSeekFileQuotaPrefix, accountID)
}

func deepSeekFileQuotaReservationPayload(tenantUsageKey, accountUsageKey string, sizeBytes int64) string {
	return fmt.Sprintf("%s|%s|%d", tenantUsageKey, accountUsageKey, sizeBytes)
}

func deepSeekFileQuotaMetadata(accountID, sizeBytes int64) string {
	return fmt.Sprintf("%d:%d", accountID, sizeBytes)
}

func validateDeepSeekFileTenant(groupID, userID int64) error {
	if groupID <= 0 || userID <= 0 {
		return errors.New("invalid DeepSeek file inventory tenant")
	}
	return nil
}

func validateDeepSeekFileID(fileID string) (string, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", errors.New("invalid DeepSeek file ID")
	}
	return fileID, nil
}

const (
	deepSeekFileQuotaReserveStatusReserved = iota + 1
	deepSeekFileQuotaReserveStatusAffinityConflict
	deepSeekFileQuotaReserveStatusTenantExceeded
	deepSeekFileQuotaReserveStatusAccountExceeded
)

var reserveDeepSeekFileUploadScript = redis.NewScript(`
local tenant_usage = KEYS[1]
local account_usage = KEYS[2]
local reservations = KEYS[3]
local reservation_expiry = KEYS[4]
local affinity_key = KEYS[5]

local reservation_id = ARGV[1]
local requested_account = tonumber(ARGV[2])
local size_bytes = tonumber(ARGV[3])
local expires_at = tonumber(ARGV[4])
local now = tonumber(ARGV[5])
local tenant_max_files = tonumber(ARGV[6])
local tenant_max_bytes = tonumber(ARGV[7])
local account_max_files = tonumber(ARGV[8])
local account_max_bytes = tonumber(ARGV[9])

local function require_positive_integer(value, name)
  if value == nil or value <= 0 or value ~= math.floor(value) then
    error('invalid ' .. name)
  end
  return value
end

local function require_nonnegative_integer(value, name)
  if value == nil or value < 0 or value ~= math.floor(value) then
    error('invalid ' .. name)
  end
  return value
end

if reservation_id == nil or reservation_id == '' then
  error('invalid reservation id')
end
requested_account = require_positive_integer(requested_account, 'account id')
size_bytes = require_positive_integer(size_bytes, 'size bytes')
expires_at = require_positive_integer(expires_at, 'reservation expiry')
now = require_nonnegative_integer(now, 'current time')
tenant_max_files = require_positive_integer(tenant_max_files, 'tenant max files')
tenant_max_bytes = require_positive_integer(tenant_max_bytes, 'tenant max bytes')
account_max_files = require_positive_integer(account_max_files, 'account max files')
account_max_bytes = require_positive_integer(account_max_bytes, 'account max bytes')
if expires_at <= now then
  error('reservation expiry must be in the future')
end

local function counter(key, field)
  local raw = redis.call('HGET', key, field)
  if raw == false then
    return 0
  end
  local value = tonumber(raw)
  if value == nil or value ~= math.floor(value) then
    error('invalid quota counter ' .. field)
  end
  if value < 0 then
    return 0
  end
  return value
end

local function read_usage(key)
  return {
    counter(key, 'used_files'),
    counter(key, 'used_bytes'),
    counter(key, 'reserved_files'),
    counter(key, 'reserved_bytes')
  }
end

local function parse_reservation(payload)
  local tenant_key, account_key, raw_size = string.match(payload, '^([^|]+)|([^|]+)|([0-9]+)$')
  local parsed_size = tonumber(raw_size)
  if tenant_key == nil or account_key == nil or parsed_size == nil or
      parsed_size <= 0 or parsed_size ~= math.floor(parsed_size) then
    error('invalid DeepSeek file quota reservation payload')
  end
  return tenant_key, account_key, parsed_size
end

local expected_payload = tenant_usage .. '|' .. account_usage .. '|' .. tostring(size_bytes)
local existing_payload = redis.call('HGET', reservations, reservation_id)
local existing_expiry = redis.call('ZSCORE', reservation_expiry, reservation_id)
local affinity = redis.call('GET', affinity_key)
local affinity_existed = 0
local affinity_account = requested_account
if affinity ~= false then
  affinity_existed = 1
  affinity_account = tonumber(affinity)
  if affinity_account == nil or affinity_account <= 0 or affinity_account ~= math.floor(affinity_account) then
    error('invalid DeepSeek file upload affinity')
  end
  if affinity_account ~= requested_account then
    return {2, affinity_account, 1}
  end
end

if existing_payload ~= false then
  if existing_expiry == false then
    error('DeepSeek file quota reservation is missing its expiry')
  end
  local existing_expiry_number = tonumber(existing_expiry)
  if existing_expiry_number == nil then
    error('invalid DeepSeek file quota reservation expiry')
  end
  if existing_expiry_number > now then
    if existing_payload ~= expected_payload then
      error('DeepSeek file quota reservation id collision')
    end
    if affinity == false then
      redis.call('SET', affinity_key, tostring(requested_account))
    end
    return {1, requested_account, affinity_existed}
  end
elseif existing_expiry ~= false and tonumber(existing_expiry) > now then
  error('DeepSeek file quota reservation expiry has no payload')
end

-- Validate every expired entry and all affected counters before mutating any
-- state. Lua errors do not roll back prior writes, so validation must be a
-- separate pass.
local expired_ids = redis.call('ZRANGEBYSCORE', reservation_expiry, '-inf', now)
local expired = {}
for _, expired_id in ipairs(expired_ids) do
  local payload = redis.call('HGET', reservations, expired_id)
  if payload ~= false then
    local expired_tenant, expired_account, expired_size = parse_reservation(payload)
    read_usage(expired_tenant)
    read_usage(expired_account)
    expired[#expired + 1] = {
      id = expired_id,
      tenant = expired_tenant,
      account = expired_account,
      size = expired_size
    }
  else
    expired[#expired + 1] = {id = expired_id}
  end
end

-- Validate the target hashes before the cleanup pass writes anything.
read_usage(tenant_usage)
read_usage(account_usage)

local function decrement_reserved(key, size)
  local files = redis.call('HINCRBY', key, 'reserved_files', -1)
  local bytes = redis.call('HINCRBY', key, 'reserved_bytes', -size)
  if files < 0 then
    redis.call('HSET', key, 'reserved_files', 0)
  end
  if bytes < 0 then
    redis.call('HSET', key, 'reserved_bytes', 0)
  end
end

for _, item in ipairs(expired) do
  if item.tenant ~= nil then
    decrement_reserved(item.tenant, item.size)
    decrement_reserved(item.account, item.size)
    redis.call('HDEL', reservations, item.id)
  end
  redis.call('ZREM', reservation_expiry, item.id)
end

local tenant = read_usage(tenant_usage)
local account = read_usage(account_usage)
if tenant[1] + tenant[3] + 1 > tenant_max_files or
    tenant[2] + tenant[4] + size_bytes > tenant_max_bytes then
  return {3, affinity_account, affinity_existed}
end
if account[1] + account[3] + 1 > account_max_files or
    account[2] + account[4] + size_bytes > account_max_bytes then
  return {4, affinity_account, affinity_existed}
end

redis.call('HSET', tenant_usage,
  'used_files', tenant[1],
  'used_bytes', tenant[2],
  'reserved_files', tenant[3] + 1,
  'reserved_bytes', tenant[4] + size_bytes)
redis.call('HSET', account_usage,
  'used_files', account[1],
  'used_bytes', account[2],
  'reserved_files', account[3] + 1,
  'reserved_bytes', account[4] + size_bytes)
redis.call('HSET', reservations, reservation_id, expected_payload)
redis.call('ZADD', reservation_expiry, expires_at, reservation_id)
if affinity == false then
  redis.call('SET', affinity_key, tostring(requested_account))
end
return {1, requested_account, affinity_existed}
`)

var commitDeepSeekFileUploadReservationScript = redis.NewScript(`
local records = KEYS[1]
local created_at_index = KEYS[2]
local tenant_usage = KEYS[3]
local tenant_files = KEYS[4]
local account_usage = KEYS[5]
local reservations = KEYS[6]
local reservation_expiry = KEYS[7]

local reservation_id = ARGV[1]
local file_id = ARGV[2]
local record = ARGV[3]
local created_at = tonumber(ARGV[4])
local expected_reservation = ARGV[5]
local expected_file_quota = ARGV[6]
local size_bytes = tonumber(ARGV[7])

if reservation_id == nil or reservation_id == '' or file_id == nil or file_id == '' or
    record == nil or record == '' or created_at == nil or created_at <= 0 or
    created_at ~= math.floor(created_at) or expected_reservation == nil or
    expected_reservation == '' or expected_file_quota == nil or
    expected_file_quota == '' or size_bytes == nil or size_bytes <= 0 or
    size_bytes ~= math.floor(size_bytes) then
  error('invalid DeepSeek file quota commit input')
end

local function counter(key, field)
  local raw = redis.call('HGET', key, field)
  if raw == false then
    return 0
  end
  local value = tonumber(raw)
  if value == nil or value ~= math.floor(value) then
    error('invalid quota counter ' .. field)
  end
  if value < 0 then
    return 0
  end
  return value
end

local function read_usage(key)
  return {
    counter(key, 'used_files'),
    counter(key, 'used_bytes'),
    counter(key, 'reserved_files'),
    counter(key, 'reserved_bytes')
  }
end

-- Read every destination before any writes, both to validate Redis types and
-- to make timeout retries distinguish an already-committed reservation.
local reservation = redis.call('HGET', reservations, reservation_id)
redis.call('ZSCORE', reservation_expiry, reservation_id)
local existing_record = redis.call('HGET', records, file_id)
local existing_score = redis.call('ZSCORE', created_at_index, file_id)
local existing_file_quota = redis.call('HGET', tenant_files, file_id)

if reservation == false then
  if existing_record == record and existing_file_quota == expected_file_quota and
      existing_score ~= false and tonumber(existing_score) == created_at then
    return 1
  end
  return 0
end
if reservation ~= expected_reservation then
  error('DeepSeek file quota reservation does not match commit')
end

local tenant = read_usage(tenant_usage)
local account = read_usage(account_usage)
local already_committed = existing_record == record and
  existing_file_quota == expected_file_quota and existing_score ~= false and
  tonumber(existing_score) == created_at
if existing_record ~= false or existing_score ~= false or existing_file_quota ~= false then
  if not already_committed then
    error('DeepSeek file inventory id already exists')
  end
end

local tenant_reserved_files = tenant[3] - 1
local tenant_reserved_bytes = tenant[4] - size_bytes
local account_reserved_files = account[3] - 1
local account_reserved_bytes = account[4] - size_bytes
if tenant_reserved_files < 0 then tenant_reserved_files = 0 end
if tenant_reserved_bytes < 0 then tenant_reserved_bytes = 0 end
if account_reserved_files < 0 then account_reserved_files = 0 end
if account_reserved_bytes < 0 then account_reserved_bytes = 0 end

local tenant_used_files = tenant[1]
local tenant_used_bytes = tenant[2]
local account_used_files = account[1]
local account_used_bytes = account[2]
if not already_committed then
  tenant_used_files = tenant_used_files + 1
  tenant_used_bytes = tenant_used_bytes + size_bytes
  account_used_files = account_used_files + 1
  account_used_bytes = account_used_bytes + size_bytes
end

redis.call('HSET', tenant_usage,
  'used_files', tenant_used_files,
  'used_bytes', tenant_used_bytes,
  'reserved_files', tenant_reserved_files,
  'reserved_bytes', tenant_reserved_bytes)
redis.call('HSET', account_usage,
  'used_files', account_used_files,
  'used_bytes', account_used_bytes,
  'reserved_files', account_reserved_files,
  'reserved_bytes', account_reserved_bytes)
if not already_committed then
  redis.call('HSET', records, file_id, record)
  redis.call('ZADD', created_at_index, created_at, file_id)
  redis.call('HSET', tenant_files, file_id, expected_file_quota)
end
redis.call('HDEL', reservations, reservation_id)
redis.call('ZREM', reservation_expiry, reservation_id)
return 1
`)

var releaseDeepSeekFileUploadReservationScript = redis.NewScript(`
local reservations = KEYS[1]
local reservation_expiry = KEYS[2]
local reservation_id = ARGV[1]

if reservation_id == nil or reservation_id == '' then
  error('invalid reservation id')
end

local payload = redis.call('HGET', reservations, reservation_id)
redis.call('ZSCORE', reservation_expiry, reservation_id)
if payload == false then
  redis.call('ZREM', reservation_expiry, reservation_id)
  return 0
end

local tenant_usage, account_usage, raw_size = string.match(payload, '^([^|]+)|([^|]+)|([0-9]+)$')
local size_bytes = tonumber(raw_size)
if tenant_usage == nil or account_usage == nil or size_bytes == nil or
    size_bytes <= 0 or size_bytes ~= math.floor(size_bytes) then
  error('invalid DeepSeek file quota reservation payload')
end

local function counter(key, field)
  local raw = redis.call('HGET', key, field)
  if raw == false then
    return 0
  end
  local value = tonumber(raw)
  if value == nil or value ~= math.floor(value) then
    error('invalid quota counter ' .. field)
  end
  if value < 0 then
    return 0
  end
  return value
end

local tenant_reserved_files = counter(tenant_usage, 'reserved_files') - 1
local tenant_reserved_bytes = counter(tenant_usage, 'reserved_bytes') - size_bytes
local account_reserved_files = counter(account_usage, 'reserved_files') - 1
local account_reserved_bytes = counter(account_usage, 'reserved_bytes') - size_bytes
if tenant_reserved_files < 0 then tenant_reserved_files = 0 end
if tenant_reserved_bytes < 0 then tenant_reserved_bytes = 0 end
if account_reserved_files < 0 then account_reserved_files = 0 end
if account_reserved_bytes < 0 then account_reserved_bytes = 0 end

-- Counter reads above validate both hashes before the first write.
redis.call('HSET', tenant_usage,
  'reserved_files', tenant_reserved_files,
  'reserved_bytes', tenant_reserved_bytes)
redis.call('HSET', account_usage,
  'reserved_files', account_reserved_files,
  'reserved_bytes', account_reserved_bytes)
redis.call('HDEL', reservations, reservation_id)
redis.call('ZREM', reservation_expiry, reservation_id)
return 1
`)

var deleteDeepSeekFileRecordScript = redis.NewScript(`
local records = KEYS[1]
local created_at_index = KEYS[2]
local tenant_usage = KEYS[3]
local tenant_files = KEYS[4]
local file_id = ARGV[1]
local account_usage_prefix = ARGV[2]

if file_id == nil or file_id == '' or account_usage_prefix == nil or
    account_usage_prefix == '' then
  error('invalid DeepSeek file quota delete input')
end

-- These reads validate the inventory key types before the first write.
redis.call('HGET', records, file_id)
redis.call('ZSCORE', created_at_index, file_id)
local file_quota = redis.call('HGET', tenant_files, file_id)
if file_quota == false then
  redis.call('HDEL', records, file_id)
  redis.call('ZREM', created_at_index, file_id)
  return 0
end

local raw_account_id, raw_size = string.match(file_quota, '^([0-9]+):([0-9]+)$')
local account_id = tonumber(raw_account_id)
local size_bytes = tonumber(raw_size)
if account_id == nil or account_id <= 0 or account_id ~= math.floor(account_id) or
    size_bytes == nil or size_bytes <= 0 or size_bytes ~= math.floor(size_bytes) then
  error('invalid DeepSeek file quota metadata')
end
local account_usage = account_usage_prefix .. tostring(account_id) .. ':usage'

local function counter(key, field)
  local raw = redis.call('HGET', key, field)
  if raw == false then
    return 0
  end
  local value = tonumber(raw)
  if value == nil or value ~= math.floor(value) then
    error('invalid quota counter ' .. field)
  end
  if value < 0 then
    return 0
  end
  return value
end

local tenant_used_files = counter(tenant_usage, 'used_files') - 1
local tenant_used_bytes = counter(tenant_usage, 'used_bytes') - size_bytes
local account_used_files = counter(account_usage, 'used_files') - 1
local account_used_bytes = counter(account_usage, 'used_bytes') - size_bytes
if tenant_used_files < 0 then tenant_used_files = 0 end
if tenant_used_bytes < 0 then tenant_used_bytes = 0 end
if account_used_files < 0 then account_used_files = 0 end
if account_used_bytes < 0 then account_used_bytes = 0 end

redis.call('HDEL', records, file_id)
redis.call('ZREM', created_at_index, file_id)
redis.call('HDEL', tenant_files, file_id)
redis.call('HSET', tenant_usage,
  'used_files', tenant_used_files,
  'used_bytes', tenant_used_bytes)
redis.call('HSET', account_usage,
  'used_files', account_used_files,
  'used_bytes', account_used_bytes)
return 1
`)

func validateDeepSeekFileQuotaReservation(reservation service.DeepSeekFileQuotaReservation) error {
	if strings.TrimSpace(reservation.ID) == "" || reservation.ID != strings.TrimSpace(reservation.ID) ||
		reservation.GroupID <= 0 || reservation.UserID <= 0 || reservation.AccountID <= 0 ||
		reservation.SizeBytes <= 0 || reservation.ExpiresAtUnix <= 0 ||
		strings.TrimSpace(reservation.SessionHash) == "" ||
		reservation.SessionHash != strings.TrimSpace(reservation.SessionHash) {
		return errors.New("invalid DeepSeek file quota reservation")
	}
	return nil
}

func validateDeepSeekFileQuotaLimits(limits service.DeepSeekFileQuotaLimits) error {
	if limits.TenantMaxFiles <= 0 || limits.TenantMaxBytes <= 0 ||
		limits.AccountMaxFiles <= 0 || limits.AccountMaxBytes <= 0 {
		return errors.New("invalid DeepSeek file quota limits")
	}
	return nil
}

func deepSeekFileQuotaScriptInt64At(result any, index int) (int64, error) {
	values, ok := result.([]any)
	if !ok || index < 0 || index >= len(values) {
		return 0, fmt.Errorf("invalid DeepSeek file quota script result %T at index %d", result, index)
	}
	switch value := values[index].(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case string:
		return strconv.ParseInt(value, 10, 64)
	case []byte:
		return strconv.ParseInt(string(value), 10, 64)
	default:
		return 0, fmt.Errorf("invalid DeepSeek file quota script value %T", value)
	}
}

func (c *gatewayCache) ReserveDeepSeekFileUpload(
	ctx context.Context,
	req service.DeepSeekFileQuotaReservationRequest,
) (service.DeepSeekFileQuotaReservationResult, error) {
	if c == nil || c.rdb == nil {
		return service.DeepSeekFileQuotaReservationResult{}, errors.New("gateway cache unavailable")
	}
	if err := validateDeepSeekFileQuotaReservation(req.Reservation); err != nil {
		return service.DeepSeekFileQuotaReservationResult{}, err
	}
	if err := validateDeepSeekFileQuotaLimits(req.Limits); err != nil {
		return service.DeepSeekFileQuotaReservationResult{}, err
	}
	if req.NowUnix < 0 || req.Reservation.ExpiresAtUnix <= req.NowUnix {
		return service.DeepSeekFileQuotaReservationResult{}, errors.New("invalid DeepSeek file quota reservation time")
	}

	reservation := req.Reservation
	tenantUsageKey, _ := deepSeekFileTenantQuotaKeys(reservation.GroupID, reservation.UserID)
	accountUsageKey := deepSeekFileAccountQuotaUsageKey(reservation.AccountID)
	affinityKey := buildSessionKey(reservation.GroupID, reservation.SessionHash)
	raw, err := reserveDeepSeekFileUploadScript.Run(
		ctx,
		c.rdb,
		[]string{
			tenantUsageKey,
			accountUsageKey,
			deepSeekFileQuotaReservationsKey,
			deepSeekFileQuotaReservationExpiryKey,
			affinityKey,
		},
		reservation.ID,
		reservation.AccountID,
		reservation.SizeBytes,
		reservation.ExpiresAtUnix,
		req.NowUnix,
		req.Limits.TenantMaxFiles,
		req.Limits.TenantMaxBytes,
		req.Limits.AccountMaxFiles,
		req.Limits.AccountMaxBytes,
	).Result()
	if err != nil {
		return service.DeepSeekFileQuotaReservationResult{}, err
	}
	status, err := deepSeekFileQuotaScriptInt64At(raw, 0)
	if err != nil {
		return service.DeepSeekFileQuotaReservationResult{}, fmt.Errorf("parse DeepSeek file quota reserve status: %w", err)
	}
	affinityAccountID, err := deepSeekFileQuotaScriptInt64At(raw, 1)
	if err != nil {
		return service.DeepSeekFileQuotaReservationResult{}, fmt.Errorf("parse DeepSeek file quota reserve affinity: %w", err)
	}
	affinityExisted, err := deepSeekFileQuotaScriptInt64At(raw, 2)
	if err != nil {
		return service.DeepSeekFileQuotaReservationResult{}, fmt.Errorf("parse DeepSeek file quota reserve affinity state: %w", err)
	}
	if affinityAccountID <= 0 || (affinityExisted != 0 && affinityExisted != 1) {
		return service.DeepSeekFileQuotaReservationResult{}, errors.New("invalid DeepSeek file quota reserve result")
	}

	result := service.DeepSeekFileQuotaReservationResult{
		AffinityAccountID: affinityAccountID,
		AffinityExisted:   affinityExisted == 1,
	}
	switch status {
	case deepSeekFileQuotaReserveStatusReserved:
		result.Reserved = true
	case deepSeekFileQuotaReserveStatusAffinityConflict:
		// The caller releases its current account slot and retries scheduling on
		// the account that won the first-upload affinity claim.
	case deepSeekFileQuotaReserveStatusTenantExceeded:
		result.TenantQuotaExceeded = true
	case deepSeekFileQuotaReserveStatusAccountExceeded:
		result.AccountQuotaExceeded = true
	default:
		return service.DeepSeekFileQuotaReservationResult{}, fmt.Errorf("unknown DeepSeek file quota reserve status %d", status)
	}
	return result, nil
}

func (c *gatewayCache) CommitDeepSeekFileUploadReservation(
	ctx context.Context,
	req service.DeepSeekFileQuotaCommitRequest,
) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	if err := validateDeepSeekFileQuotaReservation(req.Reservation); err != nil {
		return err
	}
	fileID, err := validateDeepSeekFileID(req.FileID)
	if err != nil {
		return err
	}
	if fileID != req.FileID || len(req.Record) == 0 || req.CreatedAt <= 0 {
		return errors.New("invalid DeepSeek file quota commit")
	}

	reservation := req.Reservation
	recordsKey, createdAtKey := deepSeekFileInventoryKeys(reservation.GroupID, reservation.UserID)
	tenantUsageKey, tenantFilesKey := deepSeekFileTenantQuotaKeys(reservation.GroupID, reservation.UserID)
	accountUsageKey := deepSeekFileAccountQuotaUsageKey(reservation.AccountID)
	expectedReservation := deepSeekFileQuotaReservationPayload(tenantUsageKey, accountUsageKey, reservation.SizeBytes)
	expectedFileQuota := deepSeekFileQuotaMetadata(reservation.AccountID, reservation.SizeBytes)
	committed, err := commitDeepSeekFileUploadReservationScript.Run(
		ctx,
		c.rdb,
		[]string{
			recordsKey,
			createdAtKey,
			tenantUsageKey,
			tenantFilesKey,
			accountUsageKey,
			deepSeekFileQuotaReservationsKey,
			deepSeekFileQuotaReservationExpiryKey,
		},
		reservation.ID,
		fileID,
		req.Record,
		req.CreatedAt,
		expectedReservation,
		expectedFileQuota,
		reservation.SizeBytes,
	).Int()
	if err != nil {
		return err
	}
	if committed != 1 {
		return errors.New("DeepSeek file quota reservation not found")
	}
	return nil
}

func (c *gatewayCache) ReleaseDeepSeekFileUploadReservation(ctx context.Context, reservationID string) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	reservationID = strings.TrimSpace(reservationID)
	if reservationID == "" {
		return errors.New("invalid DeepSeek file quota reservation id")
	}
	_, err := releaseDeepSeekFileUploadReservationScript.Run(
		ctx,
		c.rdb,
		[]string{deepSeekFileQuotaReservationsKey, deepSeekFileQuotaReservationExpiryKey},
		reservationID,
	).Int()
	return err
}

var _ service.DeepSeekFileQuotaStore = (*gatewayCache)(nil)

func (c *gatewayCache) StoreDeepSeekFileRecord(
	ctx context.Context,
	groupID, userID int64,
	fileID string,
	record []byte,
	createdAt int64,
) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	if err := validateDeepSeekFileTenant(groupID, userID); err != nil {
		return err
	}
	var err error
	if fileID, err = validateDeepSeekFileID(fileID); err != nil {
		return err
	}
	if len(record) == 0 || createdAt <= 0 {
		return errors.New("invalid DeepSeek file record")
	}

	recordsKey, createdAtKey := deepSeekFileInventoryKeys(groupID, userID)
	pipe := c.rdb.TxPipeline()
	pipe.HSet(ctx, recordsKey, fileID, record)
	pipe.ZAdd(ctx, createdAtKey, redis.Z{Score: float64(createdAt), Member: fileID})
	_, err = pipe.Exec(ctx)
	return err
}

func (c *gatewayCache) GetDeepSeekFileRecord(
	ctx context.Context,
	groupID, userID int64,
	fileID string,
) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	if err := validateDeepSeekFileTenant(groupID, userID); err != nil {
		return nil, err
	}
	var err error
	if fileID, err = validateDeepSeekFileID(fileID); err != nil {
		return nil, err
	}

	recordsKey, _ := deepSeekFileInventoryKeys(groupID, userID)
	record, err := c.rdb.HGet(ctx, recordsKey, fileID).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, service.ErrDeepSeekFileRecordNotFound
		}
		return nil, err
	}
	return record, nil
}

func (c *gatewayCache) ListDeepSeekFileRecords(
	ctx context.Context,
	groupID, userID int64,
) ([][]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	if err := validateDeepSeekFileTenant(groupID, userID); err != nil {
		return nil, err
	}

	recordsKey, createdAtKey := deepSeekFileInventoryKeys(groupID, userID)
	fileIDs, err := c.rdb.ZRange(ctx, createdAtKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(fileIDs) == 0 {
		return [][]byte{}, nil
	}
	records, err := c.rdb.HMGet(ctx, recordsKey, fileIDs...).Result()
	if err != nil {
		return nil, err
	}

	result := make([][]byte, 0, len(records))
	for _, record := range records {
		// A concurrent delete can remove a hash field after ZRANGE. Omitting that
		// item keeps the list consistent with the records visible at read time.
		if record == nil {
			continue
		}
		value, ok := record.(string)
		if !ok {
			return nil, fmt.Errorf("invalid DeepSeek file record value %T", record)
		}
		result = append(result, []byte(value))
	}
	return result, nil
}

func (c *gatewayCache) DeleteDeepSeekFileRecord(
	ctx context.Context,
	groupID, userID int64,
	fileID string,
) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	if err := validateDeepSeekFileTenant(groupID, userID); err != nil {
		return err
	}
	var err error
	if fileID, err = validateDeepSeekFileID(fileID); err != nil {
		return err
	}

	recordsKey, createdAtKey := deepSeekFileInventoryKeys(groupID, userID)
	tenantUsageKey, tenantFilesKey := deepSeekFileTenantQuotaKeys(groupID, userID)
	_, err = deleteDeepSeekFileRecordScript.Run(
		ctx,
		c.rdb,
		[]string{recordsKey, createdAtKey, tenantUsageKey, tenantFilesKey},
		fileID,
		deepSeekFileQuotaPrefix+"account:",
	).Int()
	return err
}

func buildOpenAIResponsesSessionWindowKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", openAIResponsesSessionWindowPrefix, groupID, sessionHash)
}

func (c *gatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	key := buildSessionKey(groupID, sessionHash)
	accountID, err := c.rdb.Get(ctx, key).Int64()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, service.ErrStickySessionNotFound
		}
		return 0, err
	}
	return accountID, nil
}

func (c *gatewayCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Set(ctx, key, accountID, ttl).Err()
}

func (c *gatewayCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// DeleteSessionAccountID 删除粘性会话与账号的绑定关系。
// 当检测到绑定的账号不可用（如状态错误、禁用、不可调度等）时调用，
// 以便下次请求能够重新选择可用账号。
//
// DeleteSessionAccountID removes the sticky session binding for the given session.
// Called when the bound account becomes unavailable (e.g., error status, disabled,
// or unschedulable), allowing subsequent requests to select a new available account.
func (c *gatewayCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Del(ctx, key).Err()
}

var claimOpenAIResponsesSessionWindowScript = redis.NewScript(`
local previous = redis.call('GET', KEYS[1])
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return previous
`)

var compareAndRefreshOpenAIResponsesSessionWindowScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current == false or current ~= ARGV[1] then
  return 0
end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

var compareAndDeleteOpenAIResponsesSessionWindowScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current == false or current ~= ARGV[1] then
  return 0
end
redis.call('DEL', KEYS[1])
return 1
`)

func (c *gatewayCache) ClaimOpenAIResponsesSessionWindow(ctx context.Context, groupID int64, sessionHash string, owner []byte, ttl time.Duration) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	if len(owner) == 0 || strings.TrimSpace(sessionHash) == "" || ttl <= 0 {
		return nil, errors.New("invalid OpenAI Responses session-window claim")
	}
	result, err := claimOpenAIResponsesSessionWindowScript.Run(
		ctx,
		c.rdb,
		[]string{buildOpenAIResponsesSessionWindowKey(groupID, sessionHash)},
		owner,
		ttl.Milliseconds(),
	).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	switch value := result.(type) {
	case nil:
		return nil, nil
	case string:
		return []byte(value), nil
	case []byte:
		return append([]byte(nil), value...), nil
	default:
		return nil, fmt.Errorf("unexpected OpenAI Responses session-window claim result %T", result)
	}
}

func (c *gatewayCache) CompareAndRefreshOpenAIResponsesSessionWindow(ctx context.Context, groupID int64, sessionHash string, expected []byte, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("gateway cache unavailable")
	}
	if len(expected) == 0 || strings.TrimSpace(sessionHash) == "" || ttl <= 0 {
		return false, errors.New("invalid OpenAI Responses session-window refresh")
	}
	n, err := compareAndRefreshOpenAIResponsesSessionWindowScript.Run(
		ctx,
		c.rdb,
		[]string{buildOpenAIResponsesSessionWindowKey(groupID, sessionHash)},
		expected,
		ttl.Milliseconds(),
	).Int()
	return n == 1, err
}

func (c *gatewayCache) CompareAndDeleteOpenAIResponsesSessionWindow(ctx context.Context, groupID int64, sessionHash string, expected []byte) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("gateway cache unavailable")
	}
	if len(expected) == 0 || strings.TrimSpace(sessionHash) == "" {
		return false, errors.New("invalid OpenAI Responses session-window delete")
	}
	n, err := compareAndDeleteOpenAIResponsesSessionWindowScript.Run(
		ctx,
		c.rdb,
		[]string{buildOpenAIResponsesSessionWindowKey(groupID, sessionHash)},
		expected,
	).Int()
	return n == 1, err
}

var _ service.OpenAIWSSessionPreemptionCache = (*gatewayCache)(nil)

const (
	grokVideoPendingBillingPrefix = "grok_video_pending:"
	grokVideoBilledPrefix         = "grok_video_billed:"
)

func (c *gatewayCache) SetGrokVideoPendingBilling(ctx context.Context, key string, payload []byte, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" || len(payload) == 0 {
		return errors.New("invalid grok video pending billing payload")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return c.rdb.Set(ctx, grokVideoPendingBillingPrefix+key, payload, ttl).Err()
}

func (c *gatewayCache) GetGrokVideoPendingBilling(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("invalid grok video pending billing key")
	}
	val, err := c.rdb.Get(ctx, grokVideoPendingBillingPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	return val, nil
}

func (c *gatewayCache) ClaimGrokVideoBilled(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, errors.New("invalid grok video billed key")
	}
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	return c.rdb.SetNX(ctx, grokVideoBilledPrefix+key, "1", ttl).Result()
}

func (c *gatewayCache) ReleaseGrokVideoBilled(ctx context.Context, key string) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("invalid grok video billed key")
	}
	return c.rdb.Del(ctx, grokVideoBilledPrefix+key).Err()
}

// Compile-time assertion: gatewayCache must implement CyberSessionBlockStore.
var _ service.CyberSessionBlockStore = (*gatewayCache)(nil)
var _ service.LiveCallStore = (*gatewayCache)(nil)

const reasoningContentPrefix = "reasoning_content:"

// reasoningContentDefaultTTL 是 reasoning 缓存的默认过期时间。Codex 会话可能
// 跨多天恢复，取 7 天；调用方传入非正 TTL 时兜底。
const reasoningContentDefaultTTL = 7 * 24 * time.Hour

// SetReasoningContent 按 reasoning item id 缓存 reasoning 全文。
// itemID 或 content 为空时直接返回 nil（无可缓存内容，属正常情况而非错误）。
func (c *gatewayCache) SetReasoningContent(ctx context.Context, itemID string, content string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" || content == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = reasoningContentDefaultTTL
	}
	return c.rdb.Set(ctx, reasoningContentPrefix+itemID, content, ttl).Err()
}

// GetReasoningContent 返回缓存的 reasoning 全文；未命中返回
// service.ErrReasoningContentNotFound。
func (c *gatewayCache) GetReasoningContent(ctx context.Context, itemID string) (string, error) {
	if c == nil || c.rdb == nil {
		return "", errors.New("gateway cache unavailable")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return "", service.ErrReasoningContentNotFound
	}
	val, err := c.rdb.Get(ctx, reasoningContentPrefix+itemID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", service.ErrReasoningContentNotFound
		}
		return "", err
	}
	return val, nil
}

const (
	cyberSessionBlockPrefix         = "cyber_session_block:"
	cyberSessionScopePrefix         = "cyber_session_scope:"
	cyberSessionRedisCommandMaxKeys = 128
)

// SetCyberSessionBlocked writes exact blocks in bounded transactions. The
// coarse scope is activated only after all exact blocks have been stored.
func (c *gatewayCache) SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error {
	if len(keys) == 0 {
		return nil
	}
	exactKeys := make([]string, 0, cyberSessionRedisCommandMaxKeys)
	flush := func() error {
		if len(exactKeys) == 0 {
			return nil
		}
		pipe := c.rdb.TxPipeline()
		for _, key := range exactKeys {
			pipe.Set(ctx, cyberSessionBlockPrefix+key, "1", ttl)
		}
		_, err := pipe.Exec(ctx)
		exactKeys = exactKeys[:0]
		return err
	}
	for _, key := range keys {
		if key != "" {
			exactKeys = append(exactKeys, key)
			if len(exactKeys) == cyberSessionRedisCommandMaxKeys {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if scopeKey != "" {
		return c.rdb.Set(ctx, cyberSessionScopePrefix+scopeKey, "1", ttl).Err()
	}
	return nil
}

func (c *gatewayCache) IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error) {
	n, err := c.rdb.Exists(ctx, cyberSessionScopePrefix+scopeKey).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// FindCyberSessionBlocked checks bounded batches in caller order and stops at
// the first blocked key, preserving the original earliest-match behavior.
func (c *gatewayCache) FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error) {
	if len(keys) == 0 {
		return "", nil
	}
	for start := 0; start < len(keys); start += cyberSessionRedisCommandMaxKeys {
		end := start + cyberSessionRedisCommandMaxKeys
		if end > len(keys) {
			end = len(keys)
		}
		redisKeys := make([]string, end-start)
		for i, key := range keys[start:end] {
			redisKeys[i] = cyberSessionBlockPrefix + key
		}
		values, err := c.rdb.MGet(ctx, redisKeys...).Result()
		if err != nil {
			return "", err
		}
		for i, value := range values {
			if value != nil {
				return keys[start+i], nil
			}
		}
	}
	return "", nil
}

var claimLiveControllerScript = redis.NewScript(`
	local key = KEYS[1]
	local target = ARGV[1]
	local owner = ARGV[2]
	local current = redis.call('HGET', key, 'controller')
	if current == false or current == 'closed' then
		return 0
	end
	if target == 'observer' and current ~= 'pending' then
		return 0
	end
	if target == 'proxy' and current ~= 'pending' and current ~= 'observer' and
		(current ~= 'proxy' or redis.call('HGET', key, 'controller_owner') ~= owner) then
		return 0
	end
	redis.call('HSET', key, 'controller', target, 'controller_owner', owner)
	return 1
`)

var markLiveCallClosedScript = redis.NewScript(`
	local key = KEYS[1]
	if redis.call('EXISTS', key) == 0 then
		return 0
	end
	if redis.call('HGET', key, 'controller') == 'closed' then
		return 0
	end
	redis.call('HSET', key, 'controller', 'closed', 'controller_owner', '')
	redis.call('EXPIRE', key, ARGV[1])
	return 1
`)

var releaseLiveControllerScript = redis.NewScript(`
	local key = KEYS[1]
	if redis.call('HGET', key, 'controller') ~= 'proxy' or
		redis.call('HGET', key, 'controller_owner') ~= ARGV[1] then
		return 0
	end
	redis.call('HSET', key, 'controller', 'pending', 'controller_owner', '')
	return 1
`)

func liveCallKey(callHash string) string {
	return liveCallPrefix + callHash
}

func HashLiveCallID(callID string) string {
	sum := sha256.Sum256([]byte(callID))
	return hex.EncodeToString(sum[:])
}

func (c *gatewayCache) SaveLiveCall(ctx context.Context, record *service.LiveCallRecord, ttl time.Duration) error {
	if record == nil || record.CallHash == "" || record.CallID == "" {
		return fmt.Errorf("invalid live call record")
	}
	values := map[string]any{
		"call_id":          record.CallID,
		"account_id":       record.AccountID,
		"api_key_id":       record.APIKeyID,
		"user_id":          record.UserID,
		"group_id":         record.GroupID,
		"subscription_id":  record.SubscriptionID,
		"lease_id":         record.LeaseID,
		"model":            record.Model,
		"created_at":       record.CreatedAt.UnixMilli(),
		"expires_at":       record.ExpiresAt.UnixMilli(),
		"controller":       record.Controller,
		"controller_owner": record.ControllerOwner,
		"user_agent":       record.UserAgent,
		"ip_address":       record.IPAddress,
		"inbound_endpoint": record.InboundEndpoint,
		"attestation":      record.AttestationCiphertext,
	}
	key := liveCallKey(record.CallHash)
	pipe := c.rdb.TxPipeline()
	pipe.HSet(ctx, key, values)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *gatewayCache) GetLiveCall(ctx context.Context, callHash string) (*service.LiveCallRecord, error) {
	values, err := c.rdb.HGetAll(ctx, liveCallKey(callHash)).Result()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, service.ErrLiveCallNotFound
	}
	parseInt := func(field string) int64 {
		value, _ := strconv.ParseInt(values[field], 10, 64)
		return value
	}
	createdAt := time.UnixMilli(parseInt("created_at"))
	expiresAt := time.UnixMilli(parseInt("expires_at"))
	return &service.LiveCallRecord{
		CallID:                values["call_id"],
		CallHash:              callHash,
		AccountID:             parseInt("account_id"),
		APIKeyID:              parseInt("api_key_id"),
		UserID:                parseInt("user_id"),
		GroupID:               parseInt("group_id"),
		SubscriptionID:        parseInt("subscription_id"),
		LeaseID:               values["lease_id"],
		Model:                 values["model"],
		CreatedAt:             createdAt,
		ExpiresAt:             expiresAt,
		Controller:            values["controller"],
		ControllerOwner:       values["controller_owner"],
		UserAgent:             values["user_agent"],
		IPAddress:             values["ip_address"],
		InboundEndpoint:       values["inbound_endpoint"],
		AttestationCiphertext: values["attestation"],
	}, nil
}

func (c *gatewayCache) ClaimLiveController(ctx context.Context, callHash, controller, owner string) (bool, error) {
	result, err := claimLiveControllerScript.Run(ctx, c.rdb, []string{liveCallKey(callHash)}, controller, owner).Int()
	return result == 1, err
}

func (c *gatewayCache) GetLiveController(ctx context.Context, callHash string) (string, error) {
	value, err := c.rdb.HGet(ctx, liveCallKey(callHash), "controller").Result()
	if err == redis.Nil {
		return "", service.ErrLiveCallNotFound
	}
	return value, err
}

func (c *gatewayCache) ReleaseLiveController(ctx context.Context, callHash, owner string) (bool, error) {
	result, err := releaseLiveControllerScript.Run(ctx, c.rdb, []string{liveCallKey(callHash)}, owner).Int()
	return result == 1, err
}

func (c *gatewayCache) MarkLiveCallClosed(ctx context.Context, callHash string, ttl time.Duration) (bool, error) {
	result, err := markLiveCallClosedScript.Run(ctx, c.rdb, []string{liveCallKey(callHash)}, int64(ttl.Seconds())).Int()
	return result == 1, err
}
