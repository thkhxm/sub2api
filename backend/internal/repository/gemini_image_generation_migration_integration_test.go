//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 验证真实迁移和既有缓存失效触发器，不用静态 SQL 字符串替代数据效果。
func TestGeminiImageGenerationMigrationScopeAndOutbox(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance) VALUES($1,'fixture',123.456) RETURNING id`, uuid.NewString()+"@migration.invalid").Scan(&userID))
	cases := []struct {
		platform, status           string
		deleted, initial, expected bool
	}{
		{"gemini", "active", false, false, true},
		{"antigravity", "active", false, false, true},
		{"gemini", "active", true, false, true},
		{"antigravity", "disabled", false, false, true},
		{"gemini", "active", false, true, true},
		{"composite", "active", false, false, false},
		{"openai", "active", false, false, false},
	}
	ids := make([]int64, len(cases))
	for i, item := range cases {
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status,allow_image_generation,deleted_at) VALUES($1,$2,$3,$4,CASE WHEN $5 THEN now() ELSE NULL END) RETURNING id`, uuid.NewString(), item.platform, item.status, item.initial, item.deleted).Scan(&ids[i]))
		_, err := tx.ExecContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,$2,'fixture',$3)`, userID, "sk-migration-"+uuid.NewString(), ids[i])
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id,deleted_at) VALUES($1,$2,'deleted-fixture',$3,now())`, userID, "sk-deleted-"+uuid.NewString(), ids[i])
		require.NoError(t, err)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'','empty-fixture',$2)`, userID, ids[0])
	require.NoError(t, err)
	var before int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_cache_invalidation_outbox`).Scan(&before))
	migration, err := os.ReadFile("../../migrations/246_enable_gemini_image_generation_groups.sql")
	require.NoError(t, err)
	result, err := tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	changed, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(4), changed)
	for i, item := range cases {
		var enabled bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT allow_image_generation FROM groups WHERE id=$1`, ids[i]).Scan(&enabled))
		require.Equal(t, item.expected, enabled, item.platform)
	}
	var after int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_cache_invalidation_outbox`).Scan(&after))
	require.Equal(t, before+4, after, "仅为受影响分组中的非删除、非空Key发出缓存失效通知")
	result, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	changed, err = result.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, changed)
	var repeated int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_cache_invalidation_outbox`).Scan(&repeated))
	require.Equal(t, after, repeated)
	var balance float64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance))
	require.InDelta(t, 123.456, balance, 0.000001)
}
