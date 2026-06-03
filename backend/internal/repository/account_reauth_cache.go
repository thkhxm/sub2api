package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	// accountReauthSecretLockKey secret 首次生成的分布式锁 key（set-if-absent）。
	accountReauthSecretLockKey = "account_reauth:secret_lock"
	// accountReauthCacheKeyPrefix consumed / notify 标记的 Redis key 前缀。
	// 业务层已传入带自身前缀的 key（account_reauth_consumed: / account_revoke_notify:），
	// 这里再加一层命名空间，避免与 settings 路径混淆。
	accountReauthCacheKeyPrefix = "reauth:"
)

// accountReauthCache 用带 TTL 的 Redis key 实现 service.AccountReauthCache。
//
// 解决两个问题：
//   - consumed / notify 去重标记若写 settings 表则无 TTL，长期累积膨胀（P2-3）；
//   - secret 首次生成需 set-if-absent 锁防并发覆盖（P2-2）。
type accountReauthCache struct {
	rdb *redis.Client
}

// NewAccountReauthCache 创建重授权 Redis 缓存。
func NewAccountReauthCache(rdb *redis.Client) service.AccountReauthCache {
	return &accountReauthCache{rdb: rdb}
}

func (c *accountReauthCache) nsKey(key string) string {
	return accountReauthCacheKeyPrefix + key
}

// AcquireSecretLock 用 SETNX 获取 secret 首次生成锁，成功返回 true。
func (c *accountReauthCache) AcquireSecretLock(ctx context.Context, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, accountReauthSecretLockKey, 1, ttl).Result()
}

// ReleaseSecretLock 删除 secret 首次生成锁。
func (c *accountReauthCache) ReleaseSecretLock(ctx context.Context) error {
	return c.rdb.Del(ctx, accountReauthSecretLockKey).Err()
}

// MarkConsumed 写入一次性消费标记，附 TTL（到期自动清理）。
func (c *accountReauthCache) MarkConsumed(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Set(ctx, c.nsKey(key), 1, ttl).Err()
}

// IsConsumed 判断 token 是否已被消费。key 不存在返回 false。
func (c *accountReauthCache) IsConsumed(ctx context.Context, key string) (bool, error) {
	exists, err := c.rdb.Exists(ctx, c.nsKey(key)).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

// MarkNotified 写入 revoke 告警去重标记，附去重窗口 TTL。
func (c *accountReauthCache) MarkNotified(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Set(ctx, c.nsKey(key), 1, ttl).Err()
}

// IsNotified 判断某 revoke 告警 key 是否在去重窗口内已发送。
func (c *accountReauthCache) IsNotified(ctx context.Context, key string) (bool, error) {
	exists, err := c.rdb.Exists(ctx, c.nsKey(key)).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}
