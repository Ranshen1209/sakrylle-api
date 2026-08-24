package service

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeepSeekFileInventoryScopesListAndLookupByTenant(t *testing.T) {
	cache := &schedulerTestGatewayCache{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(42)

	require.NoError(t, svc.StoreDeepSeekFileRecord(context.Background(), &groupID, 7, deepSeekInventoryTestRecord("file-user-7", 10, 1)))
	require.NoError(t, svc.StoreDeepSeekFileRecord(context.Background(), &groupID, 8, deepSeekInventoryTestRecord("file-user-8", 20, 1)))
	require.NoError(t, svc.StoreDeepSeekFileRecord(context.Background(), int64PtrForDeepSeekAffinityTest(43), 7, deepSeekInventoryTestRecord("file-group-43", 30, 1)))

	body, err := svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, nil, false)
	require.NoError(t, err)
	require.Equal(t, "list", gjson.GetBytes(body, "object").String())
	require.Equal(t, []string{"file-user-7"}, deepSeekInventoryListIDs(body))
	require.False(t, gjson.GetBytes(body, "data.0.account_id").Exists())

	_, err = svc.GetDeepSeekFileRecord(context.Background(), &groupID, 7, "file-user-8")
	require.ErrorIs(t, err, ErrDeepSeekFileRecordNotFound)
	_, err = svc.GetDeepSeekFileRecord(context.Background(), &groupID, 8, "file-user-7")
	require.ErrorIs(t, err, ErrDeepSeekFileRecordNotFound)
}

func TestReserveDeepSeekFileUploadUsesOperationalDefaultsAndFailsClosedWithoutQuotaStore(t *testing.T) {
	groupID := int64(42)
	baseCache := &schedulerTestGatewayCache{}
	svc := &OpenAIGatewayService{cache: baseCache}
	session := svc.DeepSeekFileUploadSessionHash(7)

	_, _, err := svc.ReserveDeepSeekFileUpload(context.Background(), &groupID, 7, 9, 123, session)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)

	quotaCache := &deepSeekQuotaTestCache{
		schedulerTestGatewayCache: baseCache,
		reserveResult:             DeepSeekFileQuotaReservationResult{Reserved: true, AffinityAccountID: 9},
	}
	svc.cache = quotaCache
	reservation, result, err := svc.ReserveDeepSeekFileUpload(context.Background(), &groupID, 7, 9, 123, session)
	require.NoError(t, err)
	require.True(t, result.Reserved)
	require.NotEmpty(t, reservation.ID)
	require.Equal(t, int64(42), quotaCache.reserveRequest.Reservation.GroupID)
	require.Equal(t, int64(123), quotaCache.reserveRequest.Reservation.SizeBytes)
	require.Equal(t, defaultDeepSeekTenantMaxFiles, quotaCache.reserveRequest.Limits.TenantMaxFiles)
	require.Equal(t, defaultDeepSeekTenantMaxBytes, quotaCache.reserveRequest.Limits.TenantMaxBytes)
	require.Equal(t, defaultDeepSeekAccountMaxFiles, quotaCache.reserveRequest.Limits.AccountMaxFiles)
	require.Equal(t, defaultDeepSeekAccountMaxBytes, quotaCache.reserveRequest.Limits.AccountMaxBytes)
}

func TestCommitReservedDeepSeekFileUploadUsesAtomicQuotaStore(t *testing.T) {
	cache := &deepSeekQuotaTestCache{schedulerTestGatewayCache: &schedulerTestGatewayCache{}}
	svc := &OpenAIGatewayService{cache: cache}
	reservation := DeepSeekFileQuotaReservation{
		ID: "reservation-a", GroupID: 42, UserID: 7, AccountID: 9, SizeBytes: 3,
		SessionHash: svc.DeepSeekFileUploadSessionHash(7), ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	record := deepSeekInventoryTestRecord("file-a", 3, 9)

	err := svc.CommitReservedDeepSeekFileUpload(context.Background(), nil, &Account{ID: 9}, reservation, record)
	require.NoError(t, err)
	require.Equal(t, "reservation-a", cache.commitRequest.Reservation.ID)
	require.Equal(t, "file-a", cache.commitRequest.FileID)
	require.NotEmpty(t, cache.commitRequest.Record)
}

func TestDeepSeekFileInventoryExpiryRemovesPersistentAffinity(t *testing.T) {
	cache := &schedulerTestGatewayCache{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(42)
	userID := int64(7)
	expiredAt := time.Now().Add(-time.Minute)

	lookupRecord := deepSeekInventoryTestRecord("file-expired-lookup", 10, 9)
	lookupRecord.ExpiresAt = &expiredAt
	require.NoError(t, svc.StoreDeepSeekFileRecord(context.Background(), &groupID, userID, lookupRecord))
	require.NoError(t, svc.BindDeepSeekFileAccount(context.Background(), &groupID, userID, lookupRecord.ID, lookupRecord.AccountID))

	_, err := svc.GetDeepSeekFileRecord(context.Background(), &groupID, userID, lookupRecord.ID)
	require.ErrorIs(t, err, ErrDeepSeekFileRecordNotFound)
	lookupSession := svc.DeepSeekFileSessionHash(userID, lookupRecord.ID)
	require.Equal(t, 1, cache.deletedSessions[lookupSession])
	_, exists := cache.deepSeekRecords[schedulerDeepSeekRecordKey(groupID, userID, lookupRecord.ID)]
	require.False(t, exists)

	listRecord := deepSeekInventoryTestRecord("file-expired-list", 20, 9)
	listRecord.ExpiresAt = &expiredAt
	require.NoError(t, svc.StoreDeepSeekFileRecord(context.Background(), &groupID, userID, listRecord))
	require.NoError(t, svc.BindDeepSeekFileAccount(context.Background(), &groupID, userID, listRecord.ID, listRecord.AccountID))

	body, err := svc.ListDeepSeekFileRecords(context.Background(), &groupID, userID, nil, false)
	require.NoError(t, err)
	require.Empty(t, deepSeekInventoryListIDs(body))
	listSession := svc.DeepSeekFileSessionHash(userID, listRecord.ID)
	require.Equal(t, 1, cache.deletedSessions[listSession])
	_, exists = cache.deepSeekRecords[schedulerDeepSeekRecordKey(groupID, userID, listRecord.ID)]
	require.False(t, exists)
}

func TestDeepSeekFileInventoryOpenAIPaginationAndSchema(t *testing.T) {
	cache := &schedulerTestGatewayCache{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(42)
	for index, fileID := range []string{"file-a", "file-b", "file-c"} {
		require.NoError(t, svc.StoreDeepSeekFileRecord(
			context.Background(), &groupID, 7, deepSeekInventoryTestRecord(fileID, int64(index+1), 9),
		))
	}

	body, err := svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, url.Values{
		"order": []string{"desc"},
		"limit": []string{"2"},
	}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"file-c", "file-b"}, deepSeekInventoryListIDs(body))
	require.True(t, gjson.GetBytes(body, "has_more").Bool())
	require.Equal(t, "file-c", gjson.GetBytes(body, "first_id").String())
	require.Equal(t, "file-b", gjson.GetBytes(body, "last_id").String())
	require.Equal(t, "file", gjson.GetBytes(body, "data.0.object").String())
	require.Equal(t, int64(3), gjson.GetBytes(body, "data.0.bytes").Int())
	require.Equal(t, int64(3), gjson.GetBytes(body, "data.0.created_at").Int())
	require.False(t, gjson.GetBytes(body, "data.0.size_bytes").Exists())

	body, err = svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, url.Values{
		"order": []string{"desc"},
		"after": []string{"file-b"},
	}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"file-a"}, deepSeekInventoryListIDs(body))
}

func TestDeepSeekFileInventoryAnthropicPaginationAndSchema(t *testing.T) {
	cache := &schedulerTestGatewayCache{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(42)
	for index, fileID := range []string{"file-a", "file-b", "file-c"} {
		require.NoError(t, svc.StoreDeepSeekFileRecord(
			context.Background(), &groupID, 7, deepSeekInventoryTestRecord(fileID, int64(index+1), 9),
		))
	}

	body, err := svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, url.Values{
		"after_id": []string{"file-a"},
		"limit":    []string{"1"},
	}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"file-b"}, deepSeekInventoryListIDs(body))
	require.True(t, gjson.GetBytes(body, "has_more").Bool())
	require.False(t, gjson.GetBytes(body, "object").Exists())
	require.Equal(t, "file", gjson.GetBytes(body, "data.0.type").String())
	require.Equal(t, int64(2), gjson.GetBytes(body, "data.0.size_bytes").Int())
	require.Equal(t, "image/png", gjson.GetBytes(body, "data.0.mime_type").String())
	require.Equal(t, time.Unix(2, 0).UTC().Format(time.RFC3339), gjson.GetBytes(body, "data.0.created_at").String())
	require.False(t, gjson.GetBytes(body, "data.0.bytes").Exists())

	body, err = svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, url.Values{
		"before_id": []string{"file-c"},
		"limit":     []string{"1"},
	}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"file-b"}, deepSeekInventoryListIDs(body),
		"before_id must return the page immediately preceding the cursor")
	require.True(t, gjson.GetBytes(body, "has_more").Bool())

	_, err = svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, url.Values{
		"after_id":  []string{"file-a"},
		"before_id": []string{"file-c"},
	}, true)
	require.ErrorIs(t, err, ErrDeepSeekFilesListInvalid)
	_, err = svc.ListDeepSeekFileRecords(context.Background(), &groupID, 7, url.Values{
		"order": []string{"desc"},
	}, true)
	require.ErrorIs(t, err, ErrDeepSeekFilesListInvalid)
}

func TestDeepSeekFileRecordConvertsBetweenOfficialFamilies(t *testing.T) {
	record, err := ParseDeepSeekFileRecord([]byte(`{
		"id":"file-cross-family","object":"file","bytes":4096,
		"created_at":1700000000,"filename":"photo.webp","purpose":"user_data","expires_at":1700003600
	}`), false, "image/webp", 17)
	require.NoError(t, err)
	require.Equal(t, int64(17), record.AccountID)

	body, err := RenderDeepSeekFileRecord(*record, true)
	require.NoError(t, err)
	require.Equal(t, "file", gjson.GetBytes(body, "type").String())
	require.Equal(t, int64(4096), gjson.GetBytes(body, "size_bytes").Int())
	require.Equal(t, "image/webp", gjson.GetBytes(body, "mime_type").String())
	require.Equal(t, time.Unix(1700000000, 0).UTC().Format(time.RFC3339), gjson.GetBytes(body, "created_at").String())
	require.False(t, gjson.GetBytes(body, "account_id").Exists())
	require.False(t, gjson.GetBytes(body, "expires_at").Exists())

	deleted, err := RenderDeepSeekFileDelete("file-cross-family", true)
	require.NoError(t, err)
	require.Equal(t, "file_deleted", gjson.GetBytes(deleted, "type").String())
}

func deepSeekInventoryTestRecord(fileID string, unixSeconds, accountID int64) DeepSeekFileRecord {
	return DeepSeekFileRecord{
		ID:        fileID,
		Filename:  fileID + ".png",
		SizeBytes: unixSeconds,
		CreatedAt: time.Unix(unixSeconds, 0).UTC(),
		Purpose:   "user_data",
		MimeType:  "image/png",
		AccountID: accountID,
	}
}

func deepSeekInventoryListIDs(body []byte) []string {
	values := gjson.GetBytes(body, "data.#.id").Array()
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.String())
	}
	return ids
}

type deepSeekQuotaTestCache struct {
	*schedulerTestGatewayCache
	reserveRequest DeepSeekFileQuotaReservationRequest
	reserveResult  DeepSeekFileQuotaReservationResult
	reserveErr     error
	commitRequest  DeepSeekFileQuotaCommitRequest
	commitErr      error
	released       []string
}

func (c *deepSeekQuotaTestCache) ReserveDeepSeekFileUpload(_ context.Context, req DeepSeekFileQuotaReservationRequest) (DeepSeekFileQuotaReservationResult, error) {
	c.reserveRequest = req
	return c.reserveResult, c.reserveErr
}

func (c *deepSeekQuotaTestCache) CommitDeepSeekFileUploadReservation(_ context.Context, req DeepSeekFileQuotaCommitRequest) error {
	c.commitRequest = req
	return c.commitErr
}

func (c *deepSeekQuotaTestCache) ReleaseDeepSeekFileUploadReservation(_ context.Context, reservationID string) error {
	c.released = append(c.released, reservationID)
	return nil
}
