//go:build vpnacceptance && vpnintegration

package repository

import (
	"context"
	"github.com/stretchr/testify/require"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestVPNRecoveryLiveOfflineMigrationAndDeferredRevocation(t *testing.T) {
	f := newVPNLiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	user := vpnTestUser(t, f.db, 1)
	original, err := f.svc.Create(ctx, user, false, user)
	require.NoError(t, err)
	created := f.waitApplied(t, ctx, original.ID)
	oldURL := created.SubscriptionURLs["base64"]
	oldPassword := f.password(t, 0, oldURL)
	stream, err := f.connect(0, oldPassword)
	require.NoError(t, err)
	defer stream.conn.Close()
	body := []byte(strings.Repeat("offline-migration-real-echo", 4096))
	require.NoError(t, stream.echo(body))
	vpnLiveWait(t, ctx, "真实上传与下载采样", func() bool {
		x, e := f.remote.User(ctx, f.nodes[0], f.creds[0], original.RemoteUsername)
		return e == nil && x.UploadBytes >= int64(len(body)) && x.DownloadBytes >= int64(len(body))
	})
	before, err := f.svc.Refresh(ctx, original.ID)
	require.NoError(t, err)
	f.enable(t, 1, true)
	f.enable(t, 0, false)
	name := f.access[0].Container
	require.True(t, strings.HasPrefix(name, "vpn-worker-integration-"))
	require.NoError(t, exec.CommandContext(ctx, "docker", "stop", "--time", "5", name).Run())
	defer func() { _ = exec.Command("docker", "start", name).Run() }()
	_, err = f.svc.Probe(ctx, original.ServerID)
	require.NoError(t, err)
	_, err = f.svc.RotateWithOptions(ctx, original.ID, user, f.nodes[1].ID, true)
	require.NoError(t, err)
	migrated := f.waitApplied(t, ctx, original.ID)
	require.Equal(t, f.nodes[1].ID, migrated.ServerID)
	require.Equal(t, before.QuotaBytes, migrated.QuotaBytes)
	require.GreaterOrEqual(t, migrated.UsedBytes, before.UsedBytes)
	require.True(t, migrated.SourceRevocationPending)
	password := f.password(t, 1, migrated.SubscriptionURLs["base64"])
	fresh, err := f.connect(1, password)
	require.NoError(t, err)
	defer fresh.conn.Close()
	require.NoError(t, fresh.echo([]byte("target-active-while-source-offline")))
	require.NoError(t, exec.CommandContext(ctx, "docker", "start", name).Run())
	vpnLiveWait(t, ctx, "源节点恢复", func() bool { x, e := f.remote.Server(ctx, f.nodes[0], f.creds[0]); return e == nil && x.Healthy })
	require.NoError(t, exec.CommandContext(ctx, "docker", "exec", "-d", name, "python", "/test/origin.py").Run())
	vpnLiveWait(t, ctx, "恢复后独立补撤销", func() bool {
		_, e := f.db.ExecContext(ctx, `UPDATE vpn_credential_revocations SET available_at=now() WHERE status='pending'`)
		require.NoError(t, e)
		f.svc.Tick(ctx)
		x, e := f.svc.Get(ctx, original.ID)
		return e == nil && !x.SourceRevocationPending
	})
	f.assertRetired(t, ctx, original, oldURL, oldPassword, stream)
	after, err := f.svc.Get(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, migrated.OwnerRef, after.OwnerRef)
	require.Equal(t, "succeeded", after.OperationStatus)
	require.GreaterOrEqual(t, after.UsedBytes, before.UsedBytes)
	t.Log("真实源节点断网后迁移成功；新节点双向回显可用；源节点恢复后旧URL、旧Trojan凭据与旧连接均失效；用户额度与累计用量保留")
}
