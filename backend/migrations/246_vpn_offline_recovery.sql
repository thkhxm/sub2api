-- 节点容量使用主机收发量；原始采样、用户额度与历史不回填、不翻倍。
ALTER TABLE vpn_servers ADD COLUMN traffic_multiplier SMALLINT NOT NULL DEFAULT 2 CHECK (traffic_multiplier IN (1,2));

-- 离线迁移的旧凭据回收独立排队，不能阻塞用户的新订阅操作。
CREATE TABLE vpn_credential_revocations (
 id VARCHAR(36) PRIMARY KEY,
 subscription_id BIGINT NOT NULL REFERENCES vpn_subscriptions(id),
 server_id BIGINT NOT NULL REFERENCES vpn_servers(id),
 owner_ref VARCHAR(36) NOT NULL,
 remote_username VARCHAR(32) NOT NULL,
 status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','succeeded')),
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 lease_until TIMESTAMPTZ,
 lease_token VARCHAR(36),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(server_id,owner_ref)
);
CREATE INDEX vpn_credential_revocations_poll ON vpn_credential_revocations(available_at) WHERE status<>'succeeded';
CREATE INDEX vpn_credential_revocations_subscription ON vpn_credential_revocations(subscription_id);
