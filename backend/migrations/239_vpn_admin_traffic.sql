-- 删除在节点撤销完成后解除有效绑定，历史订阅和用量归属继续保留。
ALTER TABLE vpn_subscriptions ADD COLUMN delete_requested_at TIMESTAMPTZ;
ALTER TABLE vpn_subscriptions ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE vpn_subscriptions DROP CONSTRAINT vpn_subscriptions_user_id_key;
CREATE UNIQUE INDEX vpn_subscriptions_one_current_user ON vpn_subscriptions(user_id) WHERE deleted_at IS NULL;
ALTER TABLE vpn_operations DROP CONSTRAINT vpn_operations_action_check;
ALTER TABLE vpn_operations ADD CONSTRAINT vpn_operations_action_check CHECK (action IN ('create','update','revoke','delete'));

-- 未配置额度或节点不支持遥测时不能把未知用量当作零。
ALTER TABLE vpn_servers ADD COLUMN traffic_quota_bytes BIGINT NOT NULL DEFAULT 0 CHECK (traffic_quota_bytes BETWEEN 0 AND 9007199254740991);
ALTER TABLE vpn_servers ADD COLUMN traffic_used_offset_bytes BIGINT NOT NULL DEFAULT 0 CHECK (traffic_used_offset_bytes BETWEEN 0 AND 9007199254740991);
ALTER TABLE vpn_servers ADD COLUMN traffic_offset_period_start TIMESTAMPTZ;
ALTER TABLE vpn_servers ADD COLUMN traffic_snapshot JSONB;
