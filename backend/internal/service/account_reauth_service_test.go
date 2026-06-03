package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSignAndParseReauthToken 验证签名 token 可被同一服务正确验签。
func TestSignAndParseReauthToken(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil, nil)

	token, err := svc.SignReauthToken(ctx, 1001, "Owner@Example.com")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := svc.ParseReauthToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, int64(1001), claims.AccountID)
	require.Equal(t, "owner@example.com", claims.OwnerEmail)
	require.Greater(t, claims.Exp, time.Now().Unix())
	require.NotEmpty(t, claims.Nonce)
}

// TestParseReauthTokenRejectsTampered 验证篡改的 token 被拒。
func TestParseReauthTokenRejectsTampered(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil, nil)

	token, err := svc.SignReauthToken(ctx, 1001, "owner@example.com")
	require.NoError(t, err)

	// 篡改 payload（破坏签名）。
	tampered := token + "x"
	_, err = svc.ParseReauthToken(ctx, tampered)
	require.Error(t, err)

	// 无效格式。
	_, err = svc.ParseReauthToken(ctx, "not-a-token")
	require.Error(t, err)
}

// TestReauthTokenOneTimeConsumption 验证 token 一次性消费后再次解析被拒。
func TestReauthTokenOneTimeConsumption(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil, nil)

	token, err := svc.SignReauthToken(ctx, 2002, "a@b.com")
	require.NoError(t, err)

	claims, err := svc.ParseReauthToken(ctx, token)
	require.NoError(t, err)

	// 模拟成功重授权后的一次性消费。
	require.NoError(t, svc.markConsumed(ctx, claims))

	// 再次解析应被拒（已消费）。
	_, err = svc.ParseReauthToken(ctx, token)
	require.Error(t, err)
}

// TestReauthTokenDistinctNonces 验证同账号多次签发的 token 不同（nonce 隔离）。
func TestReauthTokenDistinctNonces(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil, nil)

	t1, err := svc.SignReauthToken(ctx, 3003, "x@y.com")
	require.NoError(t, err)
	t2, err := svc.SignReauthToken(ctx, 3003, "x@y.com")
	require.NoError(t, err)
	require.NotEqual(t, t1, t2)

	// 消费 t1 不应影响 t2。
	c1, err := svc.ParseReauthToken(ctx, t1)
	require.NoError(t, err)
	require.NoError(t, svc.markConsumed(ctx, c1))

	_, err = svc.ParseReauthToken(ctx, t1)
	require.Error(t, err)
	_, err = svc.ParseReauthToken(ctx, t2)
	require.NoError(t, err)
}

func TestMaskReauthEmail(t *testing.T) {
	require.Equal(t, "a***@example.com", maskReauthEmail("alice@example.com"))
	require.Equal(t, "", maskReauthEmail(""))
	require.Equal(t, "***", maskReauthEmail("noatsign"))
}

// fakeReauthCache 是 AccountReauthCache 的内存实现，用于单测 Redis 路径。
type fakeReauthCache struct {
	mu          sync.Mutex
	keys        map[string]struct{}
	lockHeld    bool
	failConsume bool // IsConsumed/MarkConsumed 返回错误（模拟 Redis 故障）
	failNotify  bool // IsNotified/MarkNotified 返回错误
}

func newFakeReauthCache() *fakeReauthCache {
	return &fakeReauthCache{keys: make(map[string]struct{})}
}

func (c *fakeReauthCache) AcquireSecretLock(_ context.Context, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lockHeld {
		return false, nil
	}
	c.lockHeld = true
	return true, nil
}

func (c *fakeReauthCache) ReleaseSecretLock(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lockHeld = false
	return nil
}

func (c *fakeReauthCache) MarkConsumed(_ context.Context, key string, _ time.Duration) error {
	if c.failConsume {
		return errors.New("redis down")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys["c:"+key] = struct{}{}
	return nil
}

func (c *fakeReauthCache) IsConsumed(_ context.Context, key string) (bool, error) {
	if c.failConsume {
		return false, errors.New("redis down")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.keys["c:"+key]
	return ok, nil
}

func (c *fakeReauthCache) MarkNotified(_ context.Context, key string, _ time.Duration) error {
	if c.failNotify {
		return errors.New("redis down")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys["n:"+key] = struct{}{}
	return nil
}

func (c *fakeReauthCache) IsNotified(_ context.Context, key string) (bool, error) {
	if c.failNotify {
		return false, errors.New("redis down")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.keys["n:"+key]
	return ok, nil
}

// TestReauthConsumedViaCache 验证 consumed 标记走 Redis cache 时的判定（P2-3）。
func TestReauthConsumedViaCache(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	cache := newFakeReauthCache()
	svc := NewAccountReauthService(nil, repo, nil, nil, cache)

	token, err := svc.SignReauthToken(ctx, 4004, "c@d.com")
	require.NoError(t, err)
	claims, err := svc.ParseReauthToken(ctx, token)
	require.NoError(t, err)

	// 消费写入 cache，再次解析被拒。
	require.NoError(t, svc.markConsumed(ctx, claims))
	_, err = svc.ParseReauthToken(ctx, token)
	require.Error(t, err)
}

// TestReauthConsumedFailClose 验证 cache 故障时 consumed 降级到 settings（fail-close 不放过重放，P2-3）。
func TestReauthConsumedFailClose(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	cache := newFakeReauthCache()
	svc := NewAccountReauthService(nil, repo, nil, nil, cache)

	token, err := svc.SignReauthToken(ctx, 5005, "e@f.com")
	require.NoError(t, err)
	claims, err := svc.ParseReauthToken(ctx, token)
	require.NoError(t, err)

	// 先在 cache 正常时消费（写 cache）。
	require.NoError(t, svc.markConsumed(ctx, claims))

	// 模拟 Redis 故障：cache 不可用。markConsumed 会回退写 settings；本例先手动写 settings 模拟历史标记。
	require.NoError(t, repo.Set(ctx, svc.consumedKey(claims), "consumed"))
	cache.failConsume = true

	// cache 故障 → 降级 settings 复查 → 命中历史标记 → 仍判已消费（fail-close）。
	consumed, err := svc.isConsumed(ctx, claims)
	require.NoError(t, err)
	require.True(t, consumed)
}

// TestReauthSecretLockNoDuplicate 验证 secret 首次生成走锁，并发不产生覆盖（P2-2）。
func TestReauthSecretLockNoDuplicate(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	cache := newFakeReauthCache()
	svc := NewAccountReauthService(nil, repo, nil, nil, cache)

	// 首次读触发生成 + 落库。
	s1, err := svc.reauthSecret(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, s1)

	// 二次读应命中已落库 secret，绝不重新生成。
	s2, err := svc.reauthSecret(ctx)
	require.NoError(t, err)
	require.Equal(t, s1, s2)
}
