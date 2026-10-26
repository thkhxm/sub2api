//go:build vpnacceptance && vpnintegration

package repository

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestVPNQuotaLiveOvercommitAndReductionPreserveTrafficAndCredentials(t *testing.T) {
	f := newVPNLiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	oldQuota := int64(80 * vpnAllocationGiB)
	_, err := f.db.Exec(`UPDATE vpn_user_groups SET quota_bytes=$1 WHERE is_default`, oldQuota)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE vpn_servers SET traffic_quota_bytes=$2,traffic_multiplier=2 WHERE id=$1`, f.nodes[0].ID, int64(100*vpnAllocationGiB))
	require.NoError(t, err)
	user := vpnTestUser(t, f.db, 0)
	_, err = f.svc.CreateForAdmin(ctx, user, user, service.VPNCreateOptions{})
	require.ErrorIs(t, err, service.ErrVPNOvercommitRequired)
	created, err := f.svc.CreateForAdmin(ctx, user, user, service.VPNCreateOptions{AllowOvercommit: true, TargetServerID: f.nodes[0].ID, ExpectedQuotaBytes: oldQuota})
	require.NoError(t, err)
	active := f.waitApplied(t, ctx, created.ID)
	oldURL := active.SubscriptionURLs["base64"]
	password := f.password(t, 0, oldURL)
	stream, err := f.connect(0, password)
	require.NoError(t, err)
	body := make([]byte, 65536)
	_, err = rand.Read(body)
	require.NoError(t, err)
	require.NoError(t, stream.echo(body))
	_ = stream.conn.Close()
	var before *service.VPNSnapshot
	vpnLiveWait(t, ctx, "采样真实上传和下载", func() bool {
		before, err = f.remote.User(ctx, f.nodes[0], f.creds[0], created.RemoteUsername)
		return err == nil && before.UploadBytes >= int64(len(body)) && before.DownloadBytes >= int64(len(body))
	})
	require.Equal(t, before.UploadBytes+before.DownloadBytes, before.UsedTraffic, "用户按代理上传加下载计量，不再乘节点倍率")
	migration, err := os.ReadFile("../../migrations/247_vpn_default_quota_40g.sql")
	require.NoError(t, err)
	_, err = f.db.Exec(string(migration))
	require.NoError(t, err)
	updated := f.waitApplied(t, ctx, created.ID)
	require.Equal(t, service.VPNDefaultQuota, updated.QuotaBytes)
	require.Equal(t, oldURL, updated.SubscriptionURLs["base64"])
	require.GreaterOrEqual(t, updated.UsedBytes, before.UsedTraffic)
	require.True(t, updated.PeriodStart.Equal(*before.PeriodStart))
	require.True(t, updated.PeriodEnd.Equal(*before.PeriodEnd))
	after, err := f.remote.User(ctx, f.nodes[0], f.creds[0], created.RemoteUsername)
	require.NoError(t, err)
	require.Equal(t, service.VPNDefaultQuota, after.DataLimit)
	require.GreaterOrEqual(t, after.UploadBytes, before.UploadBytes)
	require.GreaterOrEqual(t, after.DownloadBytes, before.DownloadBytes)
	same, err := f.connect(0, password)
	require.NoError(t, err)
	defer same.conn.Close()
	require.NoError(t, same.echo([]byte("same-credentials-after-quota-update")))
	t.Logf("真实管理员超配开通成功；80 GiB -> 40 GiB 已同步，原上传/下载至少 %d/%d 字节保留，URL、密码、账期不变且原凭据可连接", before.UploadBytes, before.DownloadBytes)
}
