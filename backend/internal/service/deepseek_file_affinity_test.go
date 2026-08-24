package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekFileUploadSessionHashIsStablePerTenant(t *testing.T) {
	svc := &OpenAIGatewayService{}
	first := svc.DeepSeekFileUploadSessionHash(7)

	require.Equal(t, first, svc.DeepSeekFileUploadSessionHash(7))
	require.NotEmpty(t, first)
	require.NotEqual(t, first, svc.DeepSeekFileUploadSessionHash(8))
	require.Empty(t, svc.DeepSeekFileUploadSessionHash(0))
}

func TestDeepSeekFileUploadAffinityCanOnlyBeClaimedByQuotaReservation(t *testing.T) {
	cache := &deepSeekAffinityTTLCache{schedulerTestGatewayCache: &schedulerTestGatewayCache{}}
	svc := &OpenAIGatewayService{cache: cache}
	session := svc.DeepSeekFileUploadSessionHash(7)

	require.NoError(t, svc.BindStickySession(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), session, 9,
	))
	require.Empty(t, cache.sessionBindings)
	require.Empty(t, cache.setTTLs)
}

func TestDeepSeekFileUploadAffinityPinsOwnerAndFailsClosedWhenExcluded(t *testing.T) {
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
		"deepseek-file-upload:v1:user:7": 1,
	}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{
			{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}},
			{ID: 2, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}},
		}},
		cache:              cache,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	session := svc.DeepSeekFileUploadSessionHash(7)

	selection, err := svc.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		session, "deepseek-v4-flash-vision-exp", nil, false, "", false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	selection, err = svc.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		session, "deepseek-v4-flash-vision-exp", map[int64]struct{}{1: {}}, false, "", false,
	)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
}

func TestDeepSeekFileUploadAffinityAllowsCacheMissButFailsClosedOnCacheError(t *testing.T) {
	account := Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	base := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	session := base.DeepSeekFileUploadSessionHash(7)
	selection, err := base.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		session, "", nil, false, "", false,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	base.cache = &deepSeekAffinityErrorCache{
		schedulerTestGatewayCache: &schedulerTestGatewayCache{},
		err:                       fmt.Errorf("redis unavailable"),
	}
	selection, err = base.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		session, "", nil, false, "", false,
	)
	require.Error(t, err)
	require.Nil(t, selection)
}

func TestDeepSeekFileAffinityFailsClosedOnConflictingOwners(t *testing.T) {
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
		"deepseek-file:v2:user:7:files:file-a": 1,
		"deepseek-file:v2:user:7:files:file-b": 2,
	}, deepSeekRecords: map[string][]byte{
		"42:7:file-a": deepSeekAffinityTestRecord(t, "file-a", 1),
		"42:7:file-b": deepSeekAffinityTestRecord(t, "file-b", 2),
	}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{
			{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}},
			{ID: 2, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}},
		}},
		cache:              cache,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, err := svc.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		svc.DeepSeekFileSessionHash(7, "file-a", "file-b"), "deepseek-v4-flash-vision-exp", nil, false, "", false,
	)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
}

func TestDeepSeekFileAffinityPinsSameOwnerAcrossMultipleFiles(t *testing.T) {
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
		"deepseek-file:v2:user:7:files:file-a": 1,
		"deepseek-file:v2:user:7:files:file-b": 1,
	}, deepSeekRecords: map[string][]byte{
		"42:7:file-a": deepSeekAffinityTestRecord(t, "file-a", 1),
		"42:7:file-b": deepSeekAffinityTestRecord(t, "file-b", 1),
	}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{
			{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}},
			{ID: 2, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{42}},
		}},
		cache:              cache,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, err := svc.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		svc.DeepSeekFileSessionHash(7, "file-a", "file-b"), "deepseek-v4-flash-vision-exp", nil, false, "", false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID)
}

func TestDeepSeekFileAffinityFailsClosedWhenFileOwnerIsUnknown(t *testing.T) {
	svc := &OpenAIGatewayService{
		cache: &schedulerTestGatewayCache{sessionBindings: map[string]int64{}},
	}

	owner, err := svc.resolveDeepSeekFileAffinityAccountID(
		context.Background(), int64PtrForDeepSeekAffinityTest(42),
		svc.DeepSeekFileSessionHash(7, "file-unknown"),
	)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Zero(t, owner)
}

func TestDeepSeekFileAffinityRejectsAnotherTenantRecord(t *testing.T) {
	cache := &schedulerTestGatewayCache{deepSeekRecords: map[string][]byte{
		"42:8:file-private": deepSeekAffinityTestRecord(t, "file-private", 9),
	}}
	svc := &OpenAIGatewayService{cache: cache}

	owner, err := svc.resolveDeepSeekFileAffinityAccountID(
		context.Background(), int64PtrForDeepSeekAffinityTest(42),
		svc.DeepSeekFileSessionHash(7, "file-private"),
	)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Zero(t, owner)
}

func TestDeepSeekFileAffinityUsesPersistentBindingAndExplicitDelete(t *testing.T) {
	cache := &deepSeekAffinityTTLCache{schedulerTestGatewayCache: &schedulerTestGatewayCache{}}
	svc := &OpenAIGatewayService{cache: cache}
	session := svc.DeepSeekFileSessionHash(7, "file-permanent")

	require.NoError(t, svc.BindDeepSeekFileAccount(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), 7, "file-permanent", 9,
	))
	require.Equal(t, time.Duration(0), cache.setTTLs[session])

	// Generic scheduler refreshes must not turn a persistent file binding into
	// an expiring key.
	require.NoError(t, svc.refreshStickySessionTTL(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), session, 10*time.Minute,
	))
	require.Empty(t, cache.refreshTTLs)

	require.NoError(t, svc.DeleteDeepSeekFileAccount(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), 7, "file-permanent",
	))
	require.Equal(t, 1, cache.deletedSessions[session])
}

func TestDeepSeekFileAffinityFailsClosedWhenCacheIsUnavailable(t *testing.T) {
	svc := &OpenAIGatewayService{}

	owner, err := svc.resolveDeepSeekFileAffinityAccountID(
		context.Background(), int64PtrForDeepSeekAffinityTest(42),
		svc.DeepSeekFileSessionHash(7, "file-a"),
	)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Zero(t, owner)

	selection, err := svc.selectAccountWithLoadAwareness(
		context.Background(), int64PtrForDeepSeekAffinityTest(42), PlatformDeepseek,
		svc.DeepSeekFileSessionHash(7, "file-a"), "", nil, false, "", false,
	)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
}

func TestDeepSeekFileAffinityFailsClosedOnCacheError(t *testing.T) {
	svc := &OpenAIGatewayService{
		cache: &deepSeekAffinityErrorCache{
			schedulerTestGatewayCache: &schedulerTestGatewayCache{deepSeekRecords: map[string][]byte{
				"42:7:file-known": deepSeekAffinityTestRecord(t, "file-known", 9),
			}},
			err: fmt.Errorf("redis unavailable"),
		},
	}

	owner, err := svc.resolveDeepSeekFileAffinityAccountID(
		context.Background(), int64PtrForDeepSeekAffinityTest(42),
		svc.DeepSeekFileSessionHash(7, "file-known"),
	)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Zero(t, owner)
}

type deepSeekAffinityErrorCache struct {
	*schedulerTestGatewayCache
	err error
}

func (c *deepSeekAffinityErrorCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, c.err
}

func TestDeepSeekFileSessionSeedFromBodyCollectsOnlySupportedChatMedia(t *testing.T) {
	body := []byte(`{
		"metadata":{"file_id":"metadata-file"},
		"tools":[{"type":"function","parameters":{"file_id":"schema-file"}}],
		"messages":[
			{"role":"system","content":[{"type":"file","file_id":"system-file"}]},
			{"role":"assistant","content":[{"type":"image","source":{"type":"file","file_id":"assistant-file"}}]},
			{"role":"user","content":[
				{"type":"text","text":"inspect"},
				{"type":"text","text":"[{\"type\":\"file\",\"file_id\":\"text-file\"}]"},
				{"type":"file","file_id":"user-file"},
				{"type":"image","source":{"type":"file","file_id":"anthropic-user-file"}}
			]}
		]
	}`)

	require.Equal(t,
		"anthropic-user-file,user-file",
		deepSeekFileSessionSeedFromBody(body),
	)
}

func TestDeepSeekFileSessionSeedFromBodyPinsChatDeveloperMediaForProtocolBridge(t *testing.T) {
	body := []byte(`{
		"messages":[
			{"role":"developer","content":[
				{"type":"input_image","file_id":"developer-file"}
			]},
			{"role":"assistant","content":[
				{"type":"file","file_id":"assistant-file"}
			]}
		]
	}`)

	// A fixed Responses/Anthropic account converts the developer image into a
	// supported upstream role, so scheduling must honor its owning API key.
	require.Equal(t, "developer-file", deepSeekFileSessionSeedFromBody(body))
}

func TestDeepSeekFileSessionSeedFromBodySupportsResponsesRolesAndToolOutput(t *testing.T) {
	body := []byte(`{
		"metadata":{"file_id":"metadata-file"},
		"tools":[{"type":"function","parameters":{"file_id":"schema-file"}}],
		"input":[
			{"role":"system","content":[{"type":"input_image","file_id":"system-file"}]},
			{"role":"assistant","content":[{"type":"input_image","file_id":"assistant-file"}]},
			{"role":"developer","content":[{"type":"input_image","file_id":"developer-file"}]},
			{"role":"user","content":[{"type":"input_image","file":{"file_id":"user-file"}}]},
			{"type":"function_call_output","output":[{"type":"input_image","file_id":"output-file"}]}
		]
	}`)

	require.Equal(t,
		"developer-file,output-file,user-file",
		deepSeekFileSessionSeedFromBody(body),
	)
}

func int64PtrForDeepSeekAffinityTest(value int64) *int64 { return &value }

func deepSeekAffinityTestRecord(t *testing.T, fileID string, accountID int64) []byte {
	t.Helper()
	record, err := json.Marshal(DeepSeekFileRecord{
		ID: fileID, Filename: fileID + ".png", SizeBytes: 1,
		CreatedAt: time.Unix(1, 0).UTC(), Purpose: "user_data", MimeType: "image/png", AccountID: accountID,
	})
	require.NoError(t, err)
	return record
}

type deepSeekAffinityTTLCache struct {
	*schedulerTestGatewayCache
	setTTLs     map[string]time.Duration
	refreshTTLs map[string]time.Duration
}

func (c *deepSeekAffinityTTLCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if c.setTTLs == nil {
		c.setTTLs = make(map[string]time.Duration)
	}
	c.setTTLs[sessionHash] = ttl
	return c.schedulerTestGatewayCache.SetSessionAccountID(ctx, groupID, sessionHash, accountID, ttl)
}

func (c *deepSeekAffinityTTLCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	if c.refreshTTLs == nil {
		c.refreshTTLs = make(map[string]time.Duration)
	}
	c.refreshTTLs[sessionHash] = ttl
	return c.schedulerTestGatewayCache.RefreshSessionTTL(ctx, groupID, sessionHash, ttl)
}
