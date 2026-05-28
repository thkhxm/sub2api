#!/bin/sh
# =============================================================================
# PunkcodeAI Settings Seed
# =============================================================================
# 在 sub2api 容器健康后执行，往 settings 表写入 PunkcodeAI 桌面端依赖的开关：
#   - registration_enabled    = true   开启注册（CLI register 必须）
#   - email_verify_enabled    = false  关闭邮件验证（企业内部用）
#   - invitation_code_enabled = false  关闭邀请码
#
# 幂等：使用 ON CONFLICT (key) DO UPDATE，多次执行结果一致。
# =============================================================================

set -e

echo "=== PunkcodeAI Settings Seed ==="
echo "PGHOST=${PGHOST}  PGUSER=${PGUSER}  PGDATABASE=${PGDATABASE}"

# 等 sub2api 完成 ent migration（健康检查能确保 server 起来，但表创建可能稍晚）
echo "wait 3s for migrations to settle..."
sleep 3

# 反复尝试，避免 settings 表此刻还没创建的极端情况
i=0
until psql -At -c "SELECT 1 FROM information_schema.tables WHERE table_name='settings';" | grep -q 1; do
  i=$((i+1))
  if [ "$i" -gt 30 ]; then
    echo "ERROR: settings table did not appear within 60s"
    exit 1
  fi
  echo "  settings table not ready yet, retry $i/30..."
  sleep 2
done

# 写入三个 setting（幂等）
psql -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO settings (key, value, updated_at) VALUES
  ('registration_enabled',    'true',  NOW()),
  ('email_verify_enabled',    'false', NOW()),
  ('invitation_code_enabled', 'false', NOW())
ON CONFLICT (key) DO UPDATE
  SET value = EXCLUDED.value,
      updated_at = NOW();

SELECT key, value FROM settings
WHERE key IN ('registration_enabled', 'email_verify_enabled', 'invitation_code_enabled')
ORDER BY key;
SQL

echo "=== Seed done. PunkcodeAI desktop can now register/login. ==="
