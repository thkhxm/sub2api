package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSignAndParseReauthToken 验证签名 token 可被同一服务正确验签。
func TestSignAndParseReauthToken(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil)

	token, err := svc.SignReauthToken(ctx, 1001, "Owner@Example.com")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := svc.ParseReauthToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, int64(1001), claims.AccountID)
	require.Equal(t, "owner@example.com", claims.OwnerEmail)
	require.Greater(t, claims.Exp, time.Now().Unix())
	require.NotEmpty(t, claims.Nonce)
}

// TestParseReauthTokenRejectsTampered 验证篡改的 token 被拒。
func TestParseReauthTokenRejectsTampered(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil)

	token, err := svc.SignReauthToken(ctx, 1001, "owner@example.com")
	require.NoError(t, err)

	// 篡改 payload（破坏签名）。
	tampered := token + "x"
	_, err = svc.ParseReauthToken(ctx, tampered)
	require.Error(t, err)

	// 无效格式。
	_, err = svc.ParseReauthToken(ctx, "not-a-token")
	require.Error(t, err)
}

// TestReauthTokenOneTimeConsumption 验证 token 一次性消费后再次解析被拒。
func TestReauthTokenOneTimeConsumption(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil)

	token, err := svc.SignReauthToken(ctx, 2002, "a@b.com")
	require.NoError(t, err)

	claims, err := svc.ParseReauthToken(ctx, token)
	require.NoError(t, err)

	// 模拟成功重授权后的一次性消费。
	require.NoError(t, svc.markConsumed(ctx, claims))

	// 再次解析应被拒（已消费）。
	_, err = svc.ParseReauthToken(ctx, token)
	require.Error(t, err)
}

// TestReauthTokenDistinctNonces 验证同账号多次签发的 token 不同（nonce 隔离）。
func TestReauthTokenDistinctNonces(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewAccountReauthService(nil, repo, nil, nil)

	t1, err := svc.SignReauthToken(ctx, 3003, "x@y.com")
	require.NoError(t, err)
	t2, err := svc.SignReauthToken(ctx, 3003, "x@y.com")
	require.NoError(t, err)
	require.NotEqual(t, t1, t2)

	// 消费 t1 不应影响 t2。
	c1, err := svc.ParseReauthToken(ctx, t1)
	require.NoError(t, err)
	require.NoError(t, svc.markConsumed(ctx, c1))

	_, err = svc.ParseReauthToken(ctx, t1)
	require.Error(t, err)
	_, err = svc.ParseReauthToken(ctx, t2)
	require.NoError(t, err)
}

func TestMaskReauthEmail(t *testing.T) {
	require.Equal(t, "a***@example.com", maskReauthEmail("alice@example.com"))
	require.Equal(t, "", maskReauthEmail(""))
	require.Equal(t, "***", maskReauthEmail("noatsign"))
}
