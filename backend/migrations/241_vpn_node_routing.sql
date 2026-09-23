-- 旧版本没有发出标记，保守视为已经触及远端，避免重分配后遗留有效账号。
ALTER TABLE vpn_operations ADD COLUMN dispatched BOOLEAN NOT NULL DEFAULT false;
UPDATE vpn_operations SET dispatched=true WHERE action IN ('create','revoke') AND status IN ('pending','running','failed');
ALTER TABLE vpn_operations DROP CONSTRAINT vpn_operations_action_check;
ALTER TABLE vpn_operations ADD CONSTRAINT vpn_operations_action_check CHECK (action IN ('create','update','revoke','delete','migrate'));

-- 迁移只替换当前绑定；历史 owner 保留，用于按用户查询真实节点流量。
CREATE TABLE vpn_subscription_binding_history (
    id BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES vpn_subscriptions(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    server_id BIGINT NOT NULL REFERENCES vpn_servers(id),
    owner_ref VARCHAR(36) NOT NULL,
    remote_username VARCHAR(32) NOT NULL,
    operation_id VARCHAR(36) NOT NULL REFERENCES vpn_operations(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(server_id,owner_ref)
);
CREATE INDEX vpn_binding_history_user ON vpn_subscription_binding_history(user_id,server_id);
CREATE INDEX vpn_binding_history_operation ON vpn_subscription_binding_history(operation_id);
