package repository

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newDeepSeekFileQuotaTestCache(t *testing.T) (*gatewayCache, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return &gatewayCache{rdb: client}, client, server
}

func deepSeekFileQuotaTestReservation(
	id string,
	groupID, userID, accountID, sizeBytes, expiresAtUnix int64,
) service.DeepSeekFileQuotaReservation {
	return service.DeepSeekFileQuotaReservation{
		ID:            id,
		GroupID:       groupID,
		UserID:        userID,
		AccountID:     accountID,
		SizeBytes:     sizeBytes,
		SessionHash:   fmt.Sprintf("deepseek-file-upload:test:user:%d", userID),
		ExpiresAtUnix: expiresAtUnix,
	}
}

func deepSeekFileQuotaTestLimits(
	tenantFiles, tenantBytes, accountFiles, accountBytes int64,
) service.DeepSeekFileQuotaLimits {
	return service.DeepSeekFileQuotaLimits{
		TenantMaxFiles:  tenantFiles,
		TenantMaxBytes:  tenantBytes,
		AccountMaxFiles: accountFiles,
		AccountMaxBytes: accountBytes,
	}
}

func deepSeekFileQuotaCounter(t *testing.T, client *redis.Client, key, field string) int64 {
	t.Helper()
	values, err := client.HGetAll(context.Background(), key).Result()
	require.NoError(t, err)
	raw := values[field]
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	require.NoError(t, err)
	return value
}

func requireDeepSeekFileQuotaUsage(
	t *testing.T,
	client *redis.Client,
	key string,
	usedFiles, usedBytes, reservedFiles, reservedBytes int64,
) {
	t.Helper()
	require.Equal(t, usedFiles, deepSeekFileQuotaCounter(t, client, key, "used_files"))
	require.Equal(t, usedBytes, deepSeekFileQuotaCounter(t, client, key, "used_bytes"))
	require.Equal(t, reservedFiles, deepSeekFileQuotaCounter(t, client, key, "reserved_files"))
	require.Equal(t, reservedBytes, deepSeekFileQuotaCounter(t, client, key, "reserved_bytes"))
}

func TestGatewayCacheDeepSeekFileInventory(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()

	recordsKey, createdAtKey := deepSeekFileInventoryKeys(10, 20)
	require.NoError(t, cache.StoreDeepSeekFileRecord(ctx, 10, 20, "file_later", []byte(`{"id":"file_later"}`), 200))
	require.NoError(t, cache.StoreDeepSeekFileRecord(ctx, 10, 20, "file_earlier", []byte(`{"id":"file_earlier"}`), 100))
	require.Zero(t, redisServer.TTL(recordsKey))
	require.Zero(t, redisServer.TTL(createdAtKey))

	got, err := cache.GetDeepSeekFileRecord(ctx, 10, 20, " file_later ")
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"file_later"}`, string(got))

	listed, err := cache.ListDeepSeekFileRecords(ctx, 10, 20)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.JSONEq(t, `{"id":"file_earlier"}`, string(listed[0]))
	require.JSONEq(t, `{"id":"file_later"}`, string(listed[1]))

	// Re-storing a file atomically updates both its payload and ordering score.
	require.NoError(t, cache.StoreDeepSeekFileRecord(ctx, 10, 20, "file_later", []byte(`{"id":"file_later","updated":true}`), 50))
	listed, err = cache.ListDeepSeekFileRecords(ctx, 10, 20)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"file_later","updated":true}`, string(listed[0]))

	// The same file ID in another user tenant remains isolated.
	require.NoError(t, cache.StoreDeepSeekFileRecord(ctx, 10, 21, "file_later", []byte(`{"id":"other_tenant"}`), 1))
	got, err = cache.GetDeepSeekFileRecord(ctx, 10, 21, "file_later")
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"other_tenant"}`, string(got))
	require.NoError(t, cache.StoreDeepSeekFileRecord(ctx, 11, 20, "file_later", []byte(`{"id":"other_group"}`), 1))
	got, err = cache.GetDeepSeekFileRecord(ctx, 11, 20, "file_later")
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"other_group"}`, string(got))

	require.NoError(t, cache.DeleteDeepSeekFileRecord(ctx, 10, 20, "file_later"))
	_, err = cache.GetDeepSeekFileRecord(ctx, 10, 20, "file_later")
	require.ErrorIs(t, err, service.ErrDeepSeekFileRecordNotFound)
	_, err = cache.GetDeepSeekFileRecord(ctx, 10, 21, "file_later")
	require.NoError(t, err)
	_, err = cache.GetDeepSeekFileRecord(ctx, 11, 20, "file_later")
	require.NoError(t, err)
	listed, err = cache.ListDeepSeekFileRecords(ctx, 10, 20)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.JSONEq(t, `{"id":"file_earlier"}`, string(listed[0]))
}

func TestGatewayCacheDeepSeekFileInventoryValidationAndMisses(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()

	_, err := cache.GetDeepSeekFileRecord(ctx, 1, 1, "missing")
	require.ErrorIs(t, err, service.ErrDeepSeekFileRecordNotFound)

	empty, err := cache.ListDeepSeekFileRecords(ctx, 1, 1)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NotNil(t, empty)

	require.Error(t, cache.StoreDeepSeekFileRecord(ctx, 0, 1, "file_a", []byte(`{}`), 1))
	require.Error(t, cache.StoreDeepSeekFileRecord(ctx, 1, 0, "file_a", []byte(`{}`), 1))
	require.Error(t, cache.StoreDeepSeekFileRecord(ctx, 1, 1, "", []byte(`{}`), 1))
	require.Error(t, cache.StoreDeepSeekFileRecord(ctx, 1, 1, "file_a", nil, 1))
	require.Error(t, cache.StoreDeepSeekFileRecord(ctx, 1, 1, "file_a", []byte(`{}`), 0))
	require.Error(t, cache.DeleteDeepSeekFileRecord(ctx, 1, 1, " "))
	_, err = cache.ListDeepSeekFileRecords(ctx, -1, 1)
	require.Error(t, err)
}

func TestGatewayCacheDeepSeekFileQuotaClaimsStableAffinity(t *testing.T) {
	cache, client, _ := newDeepSeekFileQuotaTestCache(t)
	ctx := context.Background()
	limits := deepSeekFileQuotaTestLimits(2, 100, 10, 1000)
	first := deepSeekFileQuotaTestReservation("reservation-first", 10, 20, 101, 40, 200)

	result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: first,
		Limits:      limits,
		NowUnix:     100,
	})
	require.NoError(t, err)
	require.True(t, result.Reserved)
	require.Equal(t, int64(101), result.AffinityAccountID)
	require.False(t, result.AffinityExisted)

	accountID, err := client.Get(ctx, buildSessionKey(first.GroupID, first.SessionHash)).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(101), accountID)
	tenantUsageKey, _ := deepSeekFileTenantQuotaKeys(first.GroupID, first.UserID)
	requireDeepSeekFileQuotaUsage(t, client, tenantUsageKey, 0, 0, 1, 40)

	// A retry with the same reservation is idempotent and does not reserve twice.
	retry, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: first,
		Limits:      limits,
		NowUnix:     100,
	})
	require.NoError(t, err)
	require.True(t, retry.Reserved)
	require.True(t, retry.AffinityExisted)
	requireDeepSeekFileQuotaUsage(t, client, tenantUsageKey, 0, 0, 1, 40)

	conflicting := deepSeekFileQuotaTestReservation("reservation-conflict", 10, 20, 102, 10, 200)
	conflicting.SessionHash = first.SessionHash
	result, err = cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: conflicting,
		Limits:      limits,
		NowUnix:     100,
	})
	require.NoError(t, err)
	require.False(t, result.Reserved)
	require.True(t, result.AffinityExisted)
	require.Equal(t, int64(101), result.AffinityAccountID)
	require.False(t, result.TenantQuotaExceeded)
	require.False(t, result.AccountQuotaExceeded)
	requireDeepSeekFileQuotaUsage(t, client, deepSeekFileAccountQuotaUsageKey(102), 0, 0, 0, 0)
}

func TestGatewayCacheDeepSeekFileQuotaLimits(t *testing.T) {
	t.Run("tenant file count", func(t *testing.T) {
		cache, _, _ := newDeepSeekFileQuotaTestCache(t)
		ctx := context.Background()
		limits := deepSeekFileQuotaTestLimits(1, 100, 2, 200)
		first := deepSeekFileQuotaTestReservation("tenant-count-1", 1, 1, 11, 10, 200)
		result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
			Reservation: first,
			Limits:      limits,
			NowUnix:     100,
		})
		require.NoError(t, err)
		require.True(t, result.Reserved)

		second := deepSeekFileQuotaTestReservation("tenant-count-2", 1, 1, 11, 10, 200)
		result, err = cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
			Reservation: second,
			Limits:      limits,
			NowUnix:     100,
		})
		require.NoError(t, err)
		require.True(t, result.TenantQuotaExceeded)
		require.False(t, result.Reserved)
	})

	t.Run("tenant bytes", func(t *testing.T) {
		cache, _, _ := newDeepSeekFileQuotaTestCache(t)
		ctx := context.Background()
		limits := deepSeekFileQuotaTestLimits(2, 50, 4, 200)
		for index, size := range []int64{40, 11} {
			reservation := deepSeekFileQuotaTestReservation(fmt.Sprintf("tenant-bytes-%d", index), 1, 1, 11, size, 200)
			result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
				Reservation: reservation,
				Limits:      limits,
				NowUnix:     100,
			})
			require.NoError(t, err)
			if index == 0 {
				require.True(t, result.Reserved)
			} else {
				require.True(t, result.TenantQuotaExceeded)
			}
		}
	})

	t.Run("account file count across tenants", func(t *testing.T) {
		cache, client, _ := newDeepSeekFileQuotaTestCache(t)
		ctx := context.Background()
		limits := deepSeekFileQuotaTestLimits(1, 100, 2, 300)
		for userID := int64(1); userID <= 3; userID++ {
			reservation := deepSeekFileQuotaTestReservation(fmt.Sprintf("account-count-%d", userID), 1, userID, 11, 10, 200)
			result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
				Reservation: reservation,
				Limits:      limits,
				NowUnix:     100,
			})
			require.NoError(t, err)
			if userID <= 2 {
				require.True(t, result.Reserved)
			} else {
				require.True(t, result.AccountQuotaExceeded)
				require.False(t, result.AffinityExisted)
				_, err = client.Get(ctx, buildSessionKey(reservation.GroupID, reservation.SessionHash)).Result()
				require.ErrorIs(t, err, redis.Nil)
			}
		}
	})

	t.Run("account bytes across tenants", func(t *testing.T) {
		cache, _, _ := newDeepSeekFileQuotaTestCache(t)
		ctx := context.Background()
		limits := deepSeekFileQuotaTestLimits(2, 50, 10, 90)
		for userID, size := range []int64{45, 45, 1} {
			reservation := deepSeekFileQuotaTestReservation(fmt.Sprintf("account-bytes-%d", userID), 1, int64(userID+1), 11, size, 200)
			result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
				Reservation: reservation,
				Limits:      limits,
				NowUnix:     100,
			})
			require.NoError(t, err)
			if userID < 2 {
				require.True(t, result.Reserved)
			} else {
				require.True(t, result.AccountQuotaExceeded)
			}
		}
	})
}

func TestGatewayCacheDeepSeekFileQuotaReleaseIsIdempotent(t *testing.T) {
	cache, client, _ := newDeepSeekFileQuotaTestCache(t)
	ctx := context.Background()
	reservation := deepSeekFileQuotaTestReservation("release", 1, 2, 11, 33, 200)
	result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: reservation,
		Limits:      deepSeekFileQuotaTestLimits(2, 100, 10, 1000),
		NowUnix:     100,
	})
	require.NoError(t, err)
	require.True(t, result.Reserved)

	require.NoError(t, cache.ReleaseDeepSeekFileUploadReservation(ctx, reservation.ID))
	require.NoError(t, cache.ReleaseDeepSeekFileUploadReservation(ctx, reservation.ID))
	tenantUsageKey, _ := deepSeekFileTenantQuotaKeys(reservation.GroupID, reservation.UserID)
	requireDeepSeekFileQuotaUsage(t, client, tenantUsageKey, 0, 0, 0, 0)
	requireDeepSeekFileQuotaUsage(t, client, deepSeekFileAccountQuotaUsageKey(reservation.AccountID), 0, 0, 0, 0)
	require.False(t, client.HExists(ctx, deepSeekFileQuotaReservationsKey, reservation.ID).Val())
}

func TestGatewayCacheDeepSeekFileQuotaCommitAndDelete(t *testing.T) {
	cache, client, _ := newDeepSeekFileQuotaTestCache(t)
	ctx := context.Background()
	reservation := deepSeekFileQuotaTestReservation("commit", 1, 2, 11, 42, 200)
	result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: reservation,
		Limits:      deepSeekFileQuotaTestLimits(2, 100, 10, 1000),
		NowUnix:     100,
	})
	require.NoError(t, err)
	require.True(t, result.Reserved)

	commit := service.DeepSeekFileQuotaCommitRequest{
		Reservation: reservation,
		FileID:      "file_commit",
		Record:      []byte(`{"id":"file_commit","account_id":11}`),
		CreatedAt:   123,
	}
	require.NoError(t, cache.CommitDeepSeekFileUploadReservation(ctx, commit))
	// Retrying after a client timeout sees the exact inventory and succeeds.
	require.NoError(t, cache.CommitDeepSeekFileUploadReservation(ctx, commit))

	tenantUsageKey, tenantFilesKey := deepSeekFileTenantQuotaKeys(reservation.GroupID, reservation.UserID)
	requireDeepSeekFileQuotaUsage(t, client, tenantUsageKey, 1, 42, 0, 0)
	requireDeepSeekFileQuotaUsage(t, client, deepSeekFileAccountQuotaUsageKey(reservation.AccountID), 1, 42, 0, 0)
	require.Equal(t, "11:42", client.HGet(ctx, tenantFilesKey, commit.FileID).Val())
	got, err := cache.GetDeepSeekFileRecord(ctx, reservation.GroupID, reservation.UserID, commit.FileID)
	require.NoError(t, err)
	require.Equal(t, commit.Record, got)

	// Used inventory and in-flight reservations are checked as one total.
	afterCommit := deepSeekFileQuotaTestReservation("after-commit", 1, 2, 11, 59, 250)
	result, err = cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: afterCommit,
		Limits:      deepSeekFileQuotaTestLimits(2, 100, 10, 1000),
		NowUnix:     150,
	})
	require.NoError(t, err)
	require.True(t, result.TenantQuotaExceeded)

	require.NoError(t, cache.DeleteDeepSeekFileRecord(ctx, reservation.GroupID, reservation.UserID, commit.FileID))
	require.NoError(t, cache.DeleteDeepSeekFileRecord(ctx, reservation.GroupID, reservation.UserID, commit.FileID))
	requireDeepSeekFileQuotaUsage(t, client, tenantUsageKey, 0, 0, 0, 0)
	requireDeepSeekFileQuotaUsage(t, client, deepSeekFileAccountQuotaUsageKey(reservation.AccountID), 0, 0, 0, 0)
	require.False(t, client.HExists(ctx, tenantFilesKey, commit.FileID).Val())
	_, err = cache.GetDeepSeekFileRecord(ctx, reservation.GroupID, reservation.UserID, commit.FileID)
	require.ErrorIs(t, err, service.ErrDeepSeekFileRecordNotFound)
}

func TestGatewayCacheDeepSeekFileQuotaReserveSweepsExpiredReservations(t *testing.T) {
	cache, client, _ := newDeepSeekFileQuotaTestCache(t)
	ctx := context.Background()
	limits := deepSeekFileQuotaTestLimits(2, 100, 10, 1000)
	expired := deepSeekFileQuotaTestReservation("expired", 1, 1, 11, 30, 150)
	result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: expired,
		Limits:      limits,
		NowUnix:     100,
	})
	require.NoError(t, err)
	require.True(t, result.Reserved)

	fresh := deepSeekFileQuotaTestReservation("fresh", 1, 2, 11, 20, 250)
	result, err = cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
		Reservation: fresh,
		Limits:      limits,
		NowUnix:     151,
	})
	require.NoError(t, err)
	require.True(t, result.Reserved)

	expiredTenantUsage, _ := deepSeekFileTenantQuotaKeys(expired.GroupID, expired.UserID)
	freshTenantUsage, _ := deepSeekFileTenantQuotaKeys(fresh.GroupID, fresh.UserID)
	requireDeepSeekFileQuotaUsage(t, client, expiredTenantUsage, 0, 0, 0, 0)
	requireDeepSeekFileQuotaUsage(t, client, freshTenantUsage, 0, 0, 1, 20)
	requireDeepSeekFileQuotaUsage(t, client, deepSeekFileAccountQuotaUsageKey(11), 0, 0, 1, 20)
	require.False(t, client.HExists(ctx, deepSeekFileQuotaReservationsKey, expired.ID).Val())
	require.True(t, client.HExists(ctx, deepSeekFileQuotaReservationsKey, fresh.ID).Val())
}

func TestGatewayCacheDeepSeekFileQuotaDeleteClampsCorruptNegativeUsage(t *testing.T) {
	cache, client, _ := newDeepSeekFileQuotaTestCache(t)
	ctx := context.Background()
	const (
		groupID   = int64(1)
		userID    = int64(2)
		accountID = int64(11)
		fileID    = "file_negative_usage"
	)
	require.NoError(t, cache.StoreDeepSeekFileRecord(ctx, groupID, userID, fileID, []byte(`{"id":"file_negative_usage"}`), 100))
	tenantUsageKey, tenantFilesKey := deepSeekFileTenantQuotaKeys(groupID, userID)
	accountUsageKey := deepSeekFileAccountQuotaUsageKey(accountID)
	require.NoError(t, client.HSet(ctx, tenantFilesKey, fileID, deepSeekFileQuotaMetadata(accountID, 42)).Err())
	require.NoError(t, client.HSet(ctx, tenantUsageKey, "used_files", -3, "used_bytes", -7).Err())
	require.NoError(t, client.HSet(ctx, accountUsageKey, "used_files", -4, "used_bytes", -9).Err())

	require.NoError(t, cache.DeleteDeepSeekFileRecord(ctx, groupID, userID, fileID))
	requireDeepSeekFileQuotaUsage(t, client, tenantUsageKey, 0, 0, 0, 0)
	requireDeepSeekFileQuotaUsage(t, client, accountUsageKey, 0, 0, 0, 0)
}

func TestGatewayCacheDeepSeekFileQuotaConcurrentReserveDoesNotExceedAccountLimit(t *testing.T) {
	cache, client, _ := newDeepSeekFileQuotaTestCache(t)
	ctx := context.Background()
	limits := deepSeekFileQuotaTestLimits(1, 100, 5, 1000)

	type reserveOutcome struct {
		result service.DeepSeekFileQuotaReservationResult
		err    error
	}
	outcomes := make(chan reserveOutcome, 20)
	var wg sync.WaitGroup
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			reservation := deepSeekFileQuotaTestReservation(
				fmt.Sprintf("concurrent-%d", index),
				1,
				int64(index+1),
				11,
				1,
				200,
			)
			result, err := cache.ReserveDeepSeekFileUpload(ctx, service.DeepSeekFileQuotaReservationRequest{
				Reservation: reservation,
				Limits:      limits,
				NowUnix:     100,
			})
			outcomes <- reserveOutcome{result: result, err: err}
		}(index)
	}
	wg.Wait()
	close(outcomes)

	reserved := 0
	accountExceeded := 0
	for outcome := range outcomes {
		require.NoError(t, outcome.err)
		switch {
		case outcome.result.Reserved:
			reserved++
		case outcome.result.AccountQuotaExceeded:
			accountExceeded++
		default:
			t.Fatalf("unexpected concurrent reserve result: %+v", outcome.result)
		}
	}
	require.Equal(t, 5, reserved)
	require.Equal(t, 15, accountExceeded)
	requireDeepSeekFileQuotaUsage(t, client, deepSeekFileAccountQuotaUsageKey(11), 0, 0, 5, 5)
}
