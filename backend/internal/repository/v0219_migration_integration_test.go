//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 在隔离数据库事务中还原升级前的两处结构，验证新增字段的默认值和重复执行。
func TestV0219MigrationPreservesExistingData(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	var uid, gid int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance) VALUES($1,'fixture',123.456) RETURNING id`, uuid.NewString()+"@migration.invalid").Scan(&uid))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,rate_multiplier,allow_image_generation) VALUES($1,'openai',1.75,true) RETURNING id`, uuid.NewString()).Scan(&gid))
	var before, after string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT (to_jsonb(g)-'image_count_billing')::text FROM groups g WHERE id=$1`, gid).Scan(&before))
	_, err := tx.ExecContext(ctx, `DROP TABLE scoped_admin_api_keys; ALTER TABLE groups DROP COLUMN image_count_billing`)
	require.NoError(t, err)
	for repeat := 0; repeat < 2; repeat++ {
		for _, name := range []string{"247_scoped_admin_api_keys.sql", "248_group_image_count_billing.sql"} {
			migration, err := os.ReadFile("../../migrations/" + name)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, string(migration))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT (to_jsonb(g)-'image_count_billing')::text FROM groups g WHERE id=$1`, gid).Scan(&after))
	require.Equal(t, before, after)
	var enabled bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT image_count_billing FROM groups WHERE id=$1`, gid).Scan(&enabled))
	require.False(t, enabled)
	var keys int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM scoped_admin_api_keys`).Scan(&keys))
	require.Zero(t, keys, "升级不会自动创建管理密钥")
	var balance float64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.InDelta(t, 123.456, balance, 0.000001)
}
