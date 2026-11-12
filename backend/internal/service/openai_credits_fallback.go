package service

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

const (
	OpenAICreditsFallbackEnabledExtraKey = "openai_credits_fallback_enabled"
	openAICreditsSnapshotTTL             = 2 * time.Minute
	openAICreditsRefreshInterval         = time.Minute
)

// credits 续用必须单独授权；它与兑换重置卡、清除上游限流是不同操作。
func OpenAICreditsFallbackEnabled(account *Account) bool {
	return isOpenAIAutoResetCreditAccount(account) && resolveAccountExtraBool(account.Extra, OpenAICreditsFallbackEnabledExtraKey)
}

func openAICreditsSnapshotFromExtra(extra map[string]any) *openAICreditsSnapshot {
	raw, ok := extra[openaiQuotaCreditsKey]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot openAICreditsSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.FetchedAt <= 0 {
		return nil
	}
	return &snapshot
}

func openAICreditsRefreshDue(extra map[string]any, now time.Time) bool {
	snapshot := openAICreditsSnapshotFromExtra(extra)
	if snapshot == nil {
		return true
	}
	age := now.Sub(time.Unix(snapshot.FetchedAt, 0))
	return age < 0 || age >= openAICreditsRefreshInterval
}

func openAIHasFreshSpendableCredits(account *Account, now time.Time) bool {
	snapshot := openAICreditsSnapshotFromExtra(account.Extra)
	if snapshot == nil || snapshot.ErrorCode != "" || snapshot.Credits == nil {
		return false
	}
	age := now.Sub(time.Unix(snapshot.FetchedAt, 0))
	if age < 0 || age >= openAICreditsSnapshotTTL {
		return false
	}
	credits := snapshot.Credits
	// 只允许消费明确的有限余额，不能把 unlimited 当作零余额或未知余额的豁免。
	if credits.Unlimited || !credits.HasCredits || credits.Balance == nil {
		return false
	}
	// 保留上游十进制精度，同时拒绝非十进制、无限值和异常长输入。
	value := strings.TrimSpace(*credits.Balance)
	if value == "" || len(value) > 128 || strings.Count(value, ".") > 1 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, ch := range value {
		if ch != '.' && (ch < '0' || ch > '9') {
			return false
		}
	}
	balance, ok := new(big.Rat).SetString(value)
	return ok && balance.Sign() > 0
}

func openAICreditsCoverExhaustedQuota(ctx context.Context, account *Account, now time.Time) bool {
	if !OpenAICreditsFallbackEnabled(account) {
		return false
	}
	if openAICreditsRefreshDue(account.Extra, now) {
		notifyOpenAIAutoResetFromScheduler(account.ID)
	}
	used5h, has5h := resolveOpenAIQuotaUtilization(account.Extra, "5h", now)
	used7d, has7d := resolveOpenAIQuotaUtilization(account.Extra, "7d", now)
	if (!has5h || used5h < 1) && (!has7d || used7d < 1) {
		return false
	}
	// 明确设置的提前暂停/用卡阈值仍生效，不能借另一个已耗尽窗口绕过它。
	pause5h, pause7d := resolveOpenAIQuotaAutoPauseThresholds(ctx, account)
	reset := ResolveOpenAIAutoResetCreditConfig(account)
	for _, window := range []struct {
		name        string
		used, pause float64
		reset       float64
	}{{"5h", used5h, pause5h, reset.Threshold5h}, {"7d", used7d, pause7d, reset.Threshold7d}} {
		if window.used >= 1 {
			continue
		}
		if !resolveAccountExtraBool(account.Extra, "auto_pause_"+window.name+"_disabled") && window.pause > 0 && window.used >= window.pause {
			return false
		}
		if reset.Enabled && window.used >= window.reset {
			return false
		}
	}
	return openAIHasFreshSpendableCredits(account, now)
}

func (s *OpenAIQuotaAutoResetService) refreshCreditsOnly(ctx context.Context, account *Account, now time.Time) error {
	if !openAICreditsRefreshDue(account.Extra, now) {
		return nil
	}
	usage, err := s.quota.QueryUsage(ctx, account.ID)
	if err != nil || usage == nil {
		if cacheErr := s.invalidateCreditsFallbackSnapshot(ctx, account.ID, now); cacheErr != nil {
			return cacheErr
		}
		return err
	}
	return s.persistFreshUsage(ctx, account.ID, usage, now)
}

func (s *OpenAIQuotaAutoResetService) invalidateCreditsFallbackSnapshot(ctx context.Context, accountID int64, now time.Time) error {
	return s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		openaiQuotaCreditsKey: openAICreditsSnapshot{FetchedAt: now.Unix(), ErrorCode: "CREDITS_QUERY_FAILED"},
	})
}
