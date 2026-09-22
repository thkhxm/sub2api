CREATE TABLE vpn_user_groups (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    quota_bytes BIGINT NOT NULL CHECK (quota_bytes > 0 AND quota_bytes <= 9007199254740991),
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vpn_user_groups_one_default ON vpn_user_groups(is_default) WHERE is_default;
INSERT INTO vpn_user_groups(name,quota_bytes,is_default) VALUES('默认用户组',85899345920,true);
CREATE TABLE vpn_user_group_members (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    group_id BIGINT NOT NULL REFERENCES vpn_user_groups(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX vpn_user_group_members_group ON vpn_user_group_members(group_id);
INSERT INTO vpn_user_group_members(user_id,group_id) SELECT DISTINCT s.user_id,g.id FROM vpn_subscriptions s CROSS JOIN vpn_user_groups g WHERE g.is_default;
ALTER TABLE vpn_subscriptions ADD COLUMN group_id BIGINT REFERENCES vpn_user_groups(id);
UPDATE vpn_subscriptions s SET group_id=m.group_id FROM vpn_user_group_members m WHERE m.user_id=s.user_id;
ALTER TABLE vpn_subscriptions ALTER COLUMN group_id SET NOT NULL;
ALTER TABLE vpn_subscriptions ALTER COLUMN quota_bytes SET DEFAULT 85899345920;
-- 既有额度原样保留，只有显式改组月额或成员归属才触发批量同步。
ALTER TABLE vpn_subscriptions ADD COLUMN quota_sync_needed BOOLEAN NOT NULL DEFAULT false;
