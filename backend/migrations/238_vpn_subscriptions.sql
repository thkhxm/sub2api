-- VPN 为独立业务模块，不修改已有余额、订阅或用量数据。
CREATE TABLE vpn_servers (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    base_url VARCHAR(512) NOT NULL UNIQUE,
    admin_username VARCHAR(128) NOT NULL,
    credentials_encrypted TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    healthy BOOLEAN NOT NULL DEFAULT false,
    health_error TEXT NOT NULL DEFAULT '',
    personal_user_count INTEGER NOT NULL DEFAULT 0 CHECK (personal_user_count >= 0),
    known_owner_refs JSONB NOT NULL DEFAULT '[]',
    last_checked_at TIMESTAMPTZ,
    last_assigned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE vpn_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id),
    server_id BIGINT NOT NULL REFERENCES vpn_servers(id),
    owner_ref VARCHAR(36) NOT NULL UNIQUE,
    remote_username VARCHAR(32) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    quota_bytes BIGINT NOT NULL DEFAULT 32212254720 CHECK (quota_bytes > 0 AND quota_bytes <= 9007199254740991),
    status VARCHAR(32) NOT NULL DEFAULT 'provisioning',
    apply_status VARCHAR(16) NOT NULL DEFAULT 'pending',
    access_state VARCHAR(16) NOT NULL DEFAULT 'unknown',
    snapshot JSONB NOT NULL DEFAULT '{}',
    url_encrypted TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    synced_at TIMESTAMPTZ,
    sync_attempted_at TIMESTAMPTZ,
    refresh_requested_at TIMESTAMPTZ,
    operation_status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(server_id, remote_username)
);
CREATE TABLE vpn_operations (
    id VARCHAR(36) PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES vpn_subscriptions(id),
    action VARCHAR(16) NOT NULL CHECK (action IN ('create','update','revoke')),
    payload JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
    actor_id BIGINT,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    lease_token VARCHAR(36),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vpn_operations_one_pending ON vpn_operations(subscription_id) WHERE status IN ('pending','running');
CREATE INDEX vpn_operations_poll ON vpn_operations(available_at) WHERE status IN ('pending','running');
CREATE INDEX vpn_subscriptions_server ON vpn_subscriptions(server_id);
