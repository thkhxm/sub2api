-- =============================================================================
-- 144_balance_requests.sql
-- =============================================================================
-- PunkcodeAI 桌面端：用户余额申请 + 管理员审批
--
-- 表 balance_requests 字段对应 ent/schema/balance_request.go。
-- 当 ent generate 后可由 ent migration 同步；本文件保证不依赖 ent 工具链。
-- =============================================================================

CREATE TABLE IF NOT EXISTS balance_requests (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT          NOT NULL,
    amount_usd          DECIMAL(20, 8)  NOT NULL CHECK (amount_usd > 0),
    approved_amount_usd DECIMAL(20, 8)  NULL,
    note                VARCHAR(1000)   NOT NULL DEFAULT '',
    status              VARCHAR(20)     NOT NULL DEFAULT 'pending',
    reviewer_id         BIGINT          NULL,
    reviewed_at         TIMESTAMPTZ     NULL,
    reject_reason       VARCHAR(500)    NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS balancerequest_user_id_status
    ON balance_requests (user_id, status);

CREATE INDEX IF NOT EXISTS balancerequest_status_created_at
    ON balance_requests (status, created_at);
