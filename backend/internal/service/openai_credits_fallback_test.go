package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func creditsFallbackTestAccount(now time.Time) *Account {
	balance := "62500.0000000000"
	return &Account{ID: 701, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		OpenAICreditsFallbackEnabledExtraKey: true,
		"auto_pause_5h_threshold":            1.0,
		"auto_pause_7d_threshold":            1.0,
		"codex_5h_used_percent":              0.0,
		"codex_7d_used_percent":              100.0,
		"codex_5h_reset_at":                  now.Add(time.Hour).Format(time.RFC3339),
		"codex_7d_reset_at":                  now.Add(24 * time.Hour).Format(time.RFC3339),
		"codex_usage_updated_at":             now.Format(time.RFC3339),
		openaiQuotaCreditsKey: openAICreditsSnapshot{
			Credits: &OpenAICredits{HasCredits: true, Balance: &balance}, FetchedAt: now.Unix(),
		},
	}}
}

func TestOpenAICreditsFallbackQuotaGate(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		change func(*Account)
		paused bool
	}{
		{"有积分的已耗尽账号放行", func(*Account) {}, false},
		{"默认关闭", func(a *Account) { delete(a.Extra, OpenAICreditsFallbackEnabledExtraKey) }, true},
		{"显式关闭立即恢复暂停", func(a *Account) { a.Extra[OpenAICreditsFallbackEnabledExtraKey] = false }, true},
		{"快照缺失", func(a *Account) { delete(a.Extra, openaiQuotaCreditsKey) }, true},
		{"快照过期", func(a *Account) {
			v, ok := a.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
			require.True(t, ok)
			v.FetchedAt = now.Add(-3 * time.Minute).Unix()
			a.Extra[openaiQuotaCreditsKey] = v
		}, true},
		{"未来快照不可信", func(a *Account) {
			v, ok := a.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
			require.True(t, ok)
			v.FetchedAt = now.Add(time.Hour).Unix()
			a.Extra[openaiQuotaCreditsKey] = v
		}, true},
		{"查询失败使旧积分失效", func(a *Account) {
			v, ok := a.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
			require.True(t, ok)
			v.ErrorCode = "CREDITS_QUERY_FAILED"
			a.Extra[openaiQuotaCreditsKey] = v
		}, true},
		{"未知积分", func(a *Account) {
			v, ok := a.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
			require.True(t, ok)
			v.Credits = nil
			a.Extra[openaiQuotaCreditsKey] = v
		}, true},
		{"无重置卡不影响积分续用", func(a *Account) {
			a.Extra[OpenAIAutoResetCreditEnabledExtraKey] = true
			a.Extra[OpenAIAutoResetCreditStateExtraKey] = OpenAIAutoResetCreditState{Status: OpenAIAutoResetStatusNoCredit, AvailableCount: 0}
		}, false},
		{"保留当前窗口提前暂停", func(a *Account) { a.Extra["codex_7d_used_percent"] = 92.0; a.Extra["auto_pause_7d_threshold"] = 0.9 }, true},
		{"保留另一窗口提前暂停", func(a *Account) { a.Extra["codex_5h_used_percent"] = 90.0; a.Extra["auto_pause_5h_threshold"] = 0.8 }, true},
		{"保留提前用卡阈值", func(a *Account) {
			a.Extra[OpenAIAutoResetCreditEnabledExtraKey] = true
			a.Extra[OpenAIAutoResetCredit5hThresholdExtraKey] = 0.8
			a.Extra["codex_5h_used_percent"] = 90.0
		}, true},
		{"窗口自然重置不依赖积分", func(a *Account) {
			a.Extra["codex_7d_reset_at"] = now.Add(-time.Minute).Format(time.RFC3339)
			delete(a.Extra, openaiQuotaCreditsKey)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := creditsFallbackTestAccount(now)
			tc.change(a)
			paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), a)
			require.Equal(t, tc.paused, paused)
		})
	}
}

func TestOpenAICreditsFallbackBalanceValidation(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		value     string
		available bool
	}{
		{"0", false}, {"0.0000", false}, {"-2", false}, {"NaN", false}, {"Infinity", false}, {"1/2", false}, {"1e20", false}, {".5", false}, {"1.", false}, {strings.Repeat("9", 129), false},
		{"0.000000000000000001", true}, {"12345678901234567890.0123", true},
	} {
		a := creditsFallbackTestAccount(now)
		v, ok := a.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
		require.True(t, ok)
		v.Credits.Balance = &tc.value
		a.Extra[openaiQuotaCreditsKey] = v
		require.Equal(t, tc.available, openAIHasFreshSpendableCredits(a, now), tc.value)
	}
	a := creditsFallbackTestAccount(now)
	a.Extra[openaiQuotaCreditsKey] = openAICreditsSnapshot{Credits: &OpenAICredits{Unlimited: true}, FetchedAt: now.Unix()}
	require.True(t, openAIHasFreshSpendableCredits(a, now))
	a.Extra[openaiQuotaCreditsKey] = openAICreditsSnapshot{Credits: &OpenAICredits{HasCredits: true}, FetchedAt: now.Unix()}
	require.False(t, openAIHasFreshSpendableCredits(a, now))
}

func TestOpenAICreditsFallbackPreservesSchedulerGuards(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	for _, tc := range []struct {
		name    string
		change  func(*Account)
		allowed bool
	}{
		{"有新鲜积分可以调度", func(*Account) {}, true},
		{"关闭开关后不再调度", func(a *Account) { a.Extra[OpenAICreditsFallbackEnabledExtraKey] = false }, false},
		{"管理员禁用", func(a *Account) { a.Status = StatusDisabled }, false},
		{"停止调度", func(a *Account) { a.Schedulable = false }, false},
		{"真实上游限流", func(a *Account) { a.RateLimitResetAt = &future }, false},
		{"过载保护", func(a *Account) { a.OverloadUntil = &future }, false},
		{"临时熔断", func(a *Account) { a.TempUnschedulableUntil = &future }, false},
		{"鉴权错误账号", func(a *Account) { a.Status = StatusError }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := creditsFallbackTestAccount(now)
			tc.change(a)
			require.Equal(t, tc.allowed, isOpenAICompatibleAccountEligibleForRequest(context.Background(), a, PlatformOpenAI, "", false, ""))
		})
	}
}

func TestOpenAICreditsFallbackConfigAndManagedSnapshot(t *testing.T) {
	extra, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{
		OpenAICreditsFallbackEnabledExtraKey: true,
		openaiQuotaCreditsKey:                map[string]any{"credits": map[string]any{"unlimited": true}},
	})
	require.NoError(t, err)
	require.Equal(t, true, extra[OpenAICreditsFallbackEnabledExtraKey])
	require.NotContains(t, extra, openaiQuotaCreditsKey, "客户端不能伪造可消费积分")
	for _, value := range []any{"true", 1, nil} {
		_, err = normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{OpenAICreditsFallbackEnabledExtraKey: value})
		require.Error(t, err)
	}
	_, err = normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeAPIKey, false, extra)
	require.Error(t, err)
	_, err = normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, true, extra)
	require.Error(t, err)
	require.NotContains(t, stripOpenAIAutoResetCreditManagedExtra(extra, true), OpenAICreditsFallbackEnabledExtraKey)
}

func TestOpenAICreditsFallbackRefreshWithoutRedeemingResetCard(t *testing.T) {
	now := time.Now()
	a := creditsFallbackTestAccount(now)
	delete(a.Extra, openaiQuotaCreditsKey)
	repo := &autoResetTestAccountRepo{account: a}
	balance := "100.50"
	quota := &autoResetTestQuota{usage: &OpenAIQuotaUsage{FetchedAt: now.Unix(), Credits: &OpenAICredits{HasCredits: true, Balance: &balance}, RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}}}
	svc := &OpenAIQuotaAutoResetService{accountRepo: repo, quota: quota}
	require.NoError(t, svc.evaluateAccount(context.Background(), a.ID))
	require.EqualValues(t, 1, quota.queryCalls.Load())
	require.Zero(t, quota.resetCalls.Load())
	updated, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), updated)
	require.False(t, paused)
	require.NotContains(t, updated.Extra, OpenAIAutoResetCreditStateExtraKey)
	require.NoError(t, svc.evaluateAccount(context.Background(), a.ID))
	require.EqualValues(t, 1, quota.queryCalls.Load(), "短时间通知复用新鲜快照")
	// 后续读取为零时重新暂停，不重置订阅窗口，也不兑换剩余重置卡。
	repo.account.Extra[openaiQuotaCreditsKey] = openAICreditsSnapshot{FetchedAt: now.Add(-3 * time.Minute).Unix()}
	zero := "0"
	quota.usage.Credits = &OpenAICredits{HasCredits: false, Balance: &zero}
	require.NoError(t, svc.evaluateAccount(context.Background(), a.ID))
	paused, _ = shouldAutoPauseOpenAIAccountByQuota(context.Background(), repo.account)
	require.True(t, paused)
	require.Equal(t, 100.0, repo.account.Extra["codex_7d_used_percent"])
	require.Zero(t, quota.resetCalls.Load())
	// 查询失败失效旧余额，且不会在下一次通知时立刻重复查询。
	repo.account.Extra[openaiQuotaCreditsKey] = openAICreditsSnapshot{FetchedAt: now.Add(-3 * time.Minute).Unix()}
	quota.queryErr = errors.New("本地测试查询失败")
	require.Error(t, svc.evaluateAccount(context.Background(), a.ID))
	snapshot := openAICreditsSnapshotFromExtra(repo.account.Extra)
	require.Equal(t, "CREDITS_QUERY_FAILED", snapshot.ErrorCode)
	require.Nil(t, snapshot.Credits)
	before := quota.queryCalls.Load()
	require.NoError(t, svc.evaluateAccount(context.Background(), a.ID))
	require.Equal(t, before, quota.queryCalls.Load())
}

func TestOpenAICreditsFallbackSchedulerSelection(t *testing.T) {
	for _, advanced := range []string{"true", "false"} {
		for _, enabled := range []bool{true, false} {
			t.Run(advanced+fmt.Sprint(enabled), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				ctx := context.Background()
				groupID := int64(101201)
				account := creditsFallbackTestAccount(time.Now())
				account.Concurrency = 1
				account.Extra[OpenAICreditsFallbackEnabledExtraKey] = enabled
				account.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false)
				if enabled {
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.Equal(t, account.ID, selection.Account.ID)
				} else {
					require.ErrorIs(t, err, ErrNoAvailableAccounts)
					require.Nil(t, selection)
				}
			})
		}
	}
}
