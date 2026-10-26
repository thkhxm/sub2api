-- 用户已要求现有订阅统一为 40 GiB；保留原始用量、账期、凭据和历史订阅。
-- 复用既有额度同步任务，只更新节点 data_limit，不重置用量或重新创建账号。
SELECT pg_advisory_xact_lock(821407238);

ALTER TABLE vpn_subscriptions ALTER COLUMN quota_bytes SET DEFAULT 42949672960;

UPDATE vpn_user_groups
SET quota_bytes = 42949672960, updated_at = now()
WHERE is_default AND quota_bytes <> 42949672960;

UPDATE vpn_subscriptions
SET quota_bytes = 42949672960, quota_sync_needed = true, updated_at = now()
WHERE deleted_at IS NULL AND delete_requested_at IS NULL
  AND quota_bytes <> 42949672960;
