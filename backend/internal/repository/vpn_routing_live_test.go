//go:build vpnacceptance && vpnintegration

package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 仅显式提供本机烟测节点和隔离库后执行；不使用默认环境或生产接入信息。
// go test -tags=vpnacceptance,vpnintegration ./internal/repository -run '^TestVPNRoutingLive' -count=1 -v
type vpnLiveAccess struct {
	Container     string `json:"container"`
	BaseURL       string `json:"base_url"`
	ProxyPort     int    `json:"proxy_port"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
	CAPEM         string `json:"ca_pem"`
}

type vpnLiveFixture struct {
	r       *vpnRepository
	db      *sql.DB
	svc     *service.VPNService
	remote  service.VPNRemote
	nodes   [2]*service.VPNServer
	creds   [2]service.VPNCredentials
	access  [2]vpnLiveAccess
	tls     [2]*tls.Config
	clients [2]*http.Client
}

func newVPNLiveFixture(t *testing.T) *vpnLiveFixture {
	t.Helper()
	require.Equal(t, "1", os.Getenv("VPN_ACCEPTANCE_LOCAL_ONLY"), "必须显式授权本机隔离验收")
	dsn, err := url.Parse(os.Getenv("VPN_TEST_DSN"))
	require.NoError(t, err)
	require.True(t, dsn.Hostname() == "127.0.0.1" && dsn.Port() == "18701" && dsn.Path == "/vpn_test", "验收仅允许指定本机隔离 PostgreSQL")
	f := &vpnLiveFixture{remote: service.NewMarzbanVPNClient()}
	f.r, f.db = vpnTestRepository(t)
	f.svc = service.NewVPNService(f.r, &AESEncryptor{key: []byte("0123456789abcdef0123456789abcdef")}, f.remote)
	for i, variable := range []string{"VPN_ACCEPTANCE_NODE1", "VPN_ACCEPTANCE_NODE2"} {
		path := os.Getenv(variable)
		require.NotEmpty(t, path, variable)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &f.access[i]))
		a := f.access[i]
		u, err := url.Parse(a.BaseURL)
		require.NoError(t, err)
		require.True(t, u.Scheme == "https" && u.Hostname() == "127.0.0.1" && u.Port() != "", "节点必须是本机独立 HTTPS 实例")
		require.True(t, strings.HasPrefix(a.Container, "vpn-worker-integration-"), "只允许已声明的独立烟测实例")
		ca, err := os.ReadFile(a.CAPEM)
		require.NoError(t, err)
		roots := x509.NewCertPool()
		require.True(t, roots.AppendCertsFromPEM(ca))
		f.tls[i] = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "127.0.0.1"}
		f.clients[i] = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{TLSClientConfig: f.tls[i], Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		f.creds[i] = service.VPNCredentials{Password: a.AdminPassword, CAPEM: string(ca)}
		capacity := 20 * service.VPNDefaultQuota
		f.nodes[i], err = f.svc.SaveServer(context.Background(), 0, service.VPNServerInput{Name: fmt.Sprintf("真实迁移验收节点%d", i+1), BaseURL: a.BaseURL, AdminUsername: a.AdminUsername, AdminPassword: a.AdminPassword, CAPEM: &f.creds[i].CAPEM, Enabled: i == 0, TrafficQuotaBytes: &capacity})
		require.NoError(t, err)
		require.True(t, f.nodes[i].Healthy, "本机节点必须已完成真实健康探测")
	}
	require.NotEqual(t, f.access[0].Container, f.access[1].Container)
	require.NotEqual(t, f.access[0].BaseURL, f.access[1].BaseURL)
	t.Cleanup(func() {
		// 仅撤销本 schema 创建的 owner，不触碰同节点其他烟测账号和状态目录。
		for _, client := range f.clients {
			defer client.CloseIdleConnections()
		}
		rows, err := f.db.Query(`SELECT server_id,owner_ref,remote_username FROM vpn_subscriptions UNION SELECT server_id,owner_ref,remote_username FROM vpn_subscription_binding_history UNION SELECT (payload->'migration'->>'target_server_id')::bigint,payload->'migration'->>'target_owner_ref',payload->'migration'->>'target_username' FROM vpn_operations WHERE action='migrate'`)
		if err != nil {
			t.Error("无法读取本次验收账号进行清理")
			return
		}
		type binding struct {
			server          int64
			owner, username string
		}
		var bindings []binding
		for rows.Next() {
			var b binding
			if err := rows.Scan(&b.server, &b.owner, &b.username); err != nil {
				t.Error("读取验收账号失败")
				break
			}
			bindings = append(bindings, b)
		}
		_ = rows.Close()
		for _, b := range bindings {
			for i, node := range f.nodes {
				if b.server != node.ID {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				snap, err := f.remote.User(ctx, node, f.creds[i], b.username)
				if err == nil && snap.Status != "deleted" {
					body, _ := json.Marshal(service.VPNOperationPayload{OperationID: uuid.NewString(), OwnerRef: b.owner, Action: "delete"})
					_, err = f.remote.Submit(ctx, node, f.creds[i], b.username, body)
					if err == nil {
						vpnLiveWait(t, ctx, "清理本次验收账号", func() bool {
							x, e := f.remote.User(ctx, node, f.creds[i], b.username)
							return e == nil && x.Status == "deleted" && x.ApplyStatus == "applied" && x.AccessState == "blocked"
						})
					} else {
						t.Error("本次验收账号撤销失败")
					}
				}
				cancel()
			}
		}
	})
	return f
}

func vpnLiveWait(t *testing.T, ctx context.Context, condition string, check func() bool) {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s 未在期限内完成", condition)
		case <-ticker.C:
		}
	}
}

func (f *vpnLiveFixture) enable(t *testing.T, i int, enabled bool) {
	t.Helper()
	a := f.access[i]
	var err error
	f.nodes[i], err = f.svc.SaveServer(context.Background(), f.nodes[i].ID, service.VPNServerInput{Name: f.nodes[i].Name, BaseURL: a.BaseURL, AdminUsername: a.AdminUsername, Enabled: enabled})
	require.NoError(t, err)
}

func (f *vpnLiveFixture) waitApplied(t *testing.T, ctx context.Context, subID int64) *service.VPNSubscription {
	t.Helper()
	var result *service.VPNSubscription
	vpnLiveWait(t, ctx, "平台操作与实际节点配置生效", func() bool {
		// 只推进隔离 schema 的重试时间，不改变操作状态、租约或远端结果。
		_, err := f.db.ExecContext(ctx, `UPDATE vpn_operations SET available_at=now() WHERE status='pending'`)
		require.NoError(t, err)
		f.svc.Tick(ctx)
		result, err = f.svc.Get(ctx, subID)
		require.NoError(t, err)
		if result.OperationStatus != "succeeded" {
			require.Empty(t, result.SubscriptionURLs, "迁移完成前不能公开旧链接")
		}
		if result.OperationStatus == "failed" {
			t.Fatalf("平台操作失败：%s", result.LastError)
		}
		return result.OperationStatus == "succeeded" && result.ApplyStatus == "applied"
	})
	return result
}

func (f *vpnLiveFixture) password(t *testing.T, i int, subscriptionURL string) string {
	t.Helper()
	req, err := http.NewRequest("GET", subscriptionURL, nil)
	require.True(t, err == nil, "真实订阅地址格式无效")
	response, err := f.clients[i].Do(req)
	require.True(t, err == nil, "真实订阅下载失败")
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(string(body))
	require.NoError(t, err)
	for _, line := range strings.Split(string(decoded), "\n") {
		if !strings.HasPrefix(line, "trojan://") {
			continue
		}
		u, err := url.Parse(line)
		require.True(t, err == nil, "真实订阅中的 Trojan 地址格式无效")
		require.Equal(t, "127.0.0.1", u.Hostname())
		require.Equal(t, strconv.Itoa(f.access[i].ProxyPort), u.Port())
		return u.User.Username()
	}
	t.Fatal("真实订阅未返回 Trojan 配置")
	return ""
}

type vpnLiveStream struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (f *vpnLiveFixture) connect(i int, password string) (*vpnLiveStream, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 4 * time.Second}, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.access[i].ProxyPort)), f.tls[i])
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	hash := sha256.Sum224([]byte(password))
	header := append([]byte(hex.EncodeToString(hash[:])+"\r\n"), 1, 1, 127, 0, 0, 1, byte(19090>>8), byte(19090&255), '\r', '\n')
	if _, err = conn.Write(header); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &vpnLiveStream{conn: conn, reader: bufio.NewReader(conn)}, nil
}

func (s *vpnLiveStream) echo(body []byte) error {
	_ = s.conn.SetDeadline(time.Now().Add(4 * time.Second))
	request := fmt.Sprintf("POST /echo HTTP/1.1\r\nHost: origin\r\nContent-Length: %d\r\nConnection: keep-alive\r\n\r\n", len(body))
	if _, err := s.conn.Write(append([]byte(request), body...)); err != nil {
		return err
	}
	response, err := http.ReadResponse(s.reader, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != 200 || !bytes.Equal(got, body) {
		return fmt.Errorf("真实 Trojan 回显不一致")
	}
	return nil
}

func (f *vpnLiveFixture) assertRetired(t *testing.T, ctx context.Context, original *service.VPNSubscription, oldURL, oldPassword string, oldStream *vpnLiveStream) {
	t.Helper()
	x, err := f.remote.User(ctx, f.nodes[0], f.creds[0], original.RemoteUsername)
	require.NoError(t, err)
	require.Equal(t, "deleted", x.Status)
	require.Equal(t, "applied", x.ApplyStatus)
	require.Equal(t, "blocked", x.AccessState)
	require.Empty(t, x.SubscriptionURL)
	if oldURL != "" {
		response, err := f.clients[0].Get(oldURL)
		require.True(t, err == nil, "旧订阅 HTTP 验证失败")
		_ = response.Body.Close()
		require.Equal(t, http.StatusNotFound, response.StatusCode)
	}
	if oldStream != nil {
		require.Error(t, oldStream.echo([]byte("retired-stream")))
	}
	if oldPassword != "" {
		stream, err := f.connect(0, oldPassword)
		if err == nil {
			defer stream.conn.Close()
			err = stream.echo([]byte("retired-credential"))
		}
		require.Error(t, err, "旧 Trojan 凭据必须失效")
	}
}

func TestVPNRoutingLiveManualMigrationPreservesRealTrafficAndRevokesSource(t *testing.T) {
	f := newVPNLiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	user := vpnTestUser(t, f.db, 1)
	name, quota := "真实迁移额度组", int64(8*1024*1024)
	group, err := f.svc.SaveGroup(ctx, 0, service.VPNGroupInput{Name: &name, QuotaBytes: &quota})
	require.NoError(t, err)
	_, err = f.svc.SetUserGroup(ctx, user, group.ID)
	require.NoError(t, err)
	original, err := f.svc.Create(ctx, user, false, user)
	require.NoError(t, err)
	require.Equal(t, f.nodes[0].ID, original.ServerID)
	created := f.waitApplied(t, ctx, original.ID)
	oldURL := created.SubscriptionURLs["base64"]
	require.NotEmpty(t, oldURL)
	oldPassword := f.password(t, 0, oldURL)
	oldStream, err := f.connect(0, oldPassword)
	require.NoError(t, err)
	defer oldStream.conn.Close()
	body := make([]byte, 65536)
	_, err = rand.Read(body)
	require.NoError(t, err)
	require.NoError(t, oldStream.echo(body))
	var before *service.VPNSnapshot
	vpnLiveWait(t, ctx, "源节点真实流量采样", func() bool {
		before, err = f.remote.User(ctx, f.nodes[0], f.creds[0], original.RemoteUsername)
		return err == nil && before.UploadBytes >= int64(len(body)) && before.DownloadBytes >= int64(len(body))
	})
	_, err = f.svc.Refresh(ctx, original.ID)
	require.NoError(t, err)
	f.enable(t, 1, true)
	source, err := f.r.GetServer(ctx, original.ServerID)
	require.NoError(t, err)
	require.True(t, source.Enabled, "必须覆盖源节点仍启用时手选另一节点迁移")
	_, err = f.svc.Rotate(ctx, original.ID, user, f.nodes[1].ID)
	require.NoError(t, err)
	migrated := f.waitApplied(t, ctx, original.ID)
	require.Equal(t, original.ID, migrated.ID)
	require.Equal(t, f.nodes[1].ID, migrated.ServerID)
	require.NotEqual(t, original.OwnerRef, migrated.OwnerRef)
	require.Equal(t, group.ID, migrated.GroupID)
	require.Equal(t, quota, migrated.QuotaBytes)
	require.GreaterOrEqual(t, migrated.UsedBytes, before.UsedTraffic)
	require.GreaterOrEqual(t, migrated.UploadBytes, before.UploadBytes)
	require.GreaterOrEqual(t, migrated.DownloadBytes, before.DownloadBytes)
	require.True(t, migrated.PeriodStart.Equal(*before.PeriodStart) && migrated.PeriodEnd.Equal(*before.PeriodEnd))
	f.assertRetired(t, ctx, original, oldURL, oldPassword, oldStream)
	newPassword := f.password(t, 1, migrated.SubscriptionURLs["base64"])
	require.True(t, newPassword != oldPassword, "迁移必须生成新代理凭据")
	newStream, err := f.connect(1, newPassword)
	require.NoError(t, err)
	defer newStream.conn.Close()
	require.NoError(t, newStream.echo([]byte("new-node-real-traffic")))
	refs, err := f.r.UserOwnerRefs(ctx, user)
	require.NoError(t, err)
	require.Contains(t, refs[f.nodes[0].ID], original.OwnerRef)
	require.Contains(t, refs[f.nodes[1].ID], migrated.OwnerRef)
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
	traffic, err := f.svc.DailyTraffic(ctx, service.VPNTrafficFilter{StartDate: day, EndDate: day, UserID: user})
	require.NoError(t, err)
	require.GreaterOrEqual(t, traffic.TotalBytes, before.UsedTraffic)
	var raw []byte
	require.NoError(t, f.db.QueryRow(`SELECT payload FROM vpn_operations WHERE subscription_id=$1 AND action='migrate'`, original.ID).Scan(&raw))
	var operation service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(raw, &operation))
	require.NotNil(t, operation.Migration)
	require.True(t, operation.Migration.ManualTarget)
	require.Equal(t, f.nodes[1].ID, operation.Migration.TargetServerID)
	source, err = f.r.GetServer(ctx, original.ServerID)
	require.NoError(t, err)
	require.True(t, source.Enabled, "手动迁移不能顺带停用整个源节点")
	t.Logf("源节点保持启用且手选第二节点：真实双向流量至少 %d 字节；迁移后已用 %d；旧 URL、旧凭据、旧连接失效，指定节点真实代理回显通过", before.UsedTraffic, migrated.UsedBytes)
}

func TestVPNRoutingLiveRotateSameNodePreservesBindingAndTraffic(t *testing.T) {
	f := newVPNLiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	user := vpnTestUser(t, f.db, 1)
	original, err := f.svc.Create(ctx, user, false, user)
	require.NoError(t, err)
	created := f.waitApplied(t, ctx, original.ID)
	oldURL := created.SubscriptionURLs["base64"]
	require.NotEmpty(t, oldURL)
	oldPassword := f.password(t, 0, oldURL)
	oldStream, err := f.connect(0, oldPassword)
	require.NoError(t, err)
	defer oldStream.conn.Close()
	body := []byte("same-node-before-rotation-real-traffic")
	require.NoError(t, oldStream.echo(body))
	var before *service.VPNSnapshot
	vpnLiveWait(t, ctx, "同节点轮换前真实流量采样", func() bool {
		before, err = f.remote.User(ctx, f.nodes[0], f.creds[0], original.RemoteUsername)
		return err == nil && before.UploadBytes >= int64(len(body)) && before.DownloadBytes >= int64(len(body))
	})
	_, err = f.svc.Rotate(ctx, original.ID, user, original.ServerID)
	require.NoError(t, err)
	rotated := f.waitApplied(t, ctx, original.ID)
	require.Equal(t, original.ID, rotated.ID)
	require.Equal(t, original.ServerID, rotated.ServerID)
	require.Equal(t, original.OwnerRef, rotated.OwnerRef)
	require.Equal(t, original.RemoteUsername, rotated.RemoteUsername)
	require.Equal(t, original.GroupID, rotated.GroupID)
	require.Equal(t, original.QuotaBytes, rotated.QuotaBytes)
	require.GreaterOrEqual(t, rotated.UsedBytes, before.UsedTraffic)
	require.True(t, rotated.PeriodStart.Equal(*before.PeriodStart) && rotated.PeriodEnd.Equal(*before.PeriodEnd))
	response, err := f.clients[0].Get(oldURL)
	require.True(t, err == nil, "旧订阅 HTTP 验证失败")
	_ = response.Body.Close()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.Error(t, oldStream.echo([]byte("same-node-old-connection")))
	oldCredential, err := f.connect(0, oldPassword)
	if err == nil {
		defer oldCredential.conn.Close()
		err = oldCredential.echo([]byte("same-node-old-password"))
	}
	require.Error(t, err, "同节点轮换后旧代理凭据必须失效")
	newPassword := f.password(t, 0, rotated.SubscriptionURLs["base64"])
	require.True(t, newPassword != oldPassword, "同节点轮换必须生成新凭据")
	newStream, err := f.connect(0, newPassword)
	require.NoError(t, err)
	defer newStream.conn.Close()
	require.NoError(t, newStream.echo([]byte("same-node-new-password-real-traffic")))
	var action string
	require.NoError(t, f.db.QueryRow(`SELECT action FROM vpn_operations WHERE subscription_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, original.ID).Scan(&action))
	require.Equal(t, "revoke", action)
	var histories int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM vpn_subscription_binding_history WHERE subscription_id=$1`, original.ID).Scan(&histories))
	require.Zero(t, histories, "原地轮换不应制造迁移历史")
	t.Logf("手选当前节点原地轮换：节点/owner/账号/额度/账期保留，已用至少 %d 字节；旧 URL/凭据/连接失效，新凭据真实代理通过", before.UsedTraffic)
}

func TestVPNRoutingLiveDisabledBeforeDispatchReroutes(t *testing.T) {
	f := newVPNLiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	user := vpnTestUser(t, f.db, 1)
	original, err := f.svc.Create(ctx, user, false, user)
	require.NoError(t, err)
	f.enable(t, 1, true)
	f.enable(t, 0, false)
	created := f.waitApplied(t, ctx, original.ID)
	require.Equal(t, f.nodes[1].ID, created.ServerID)
	require.Equal(t, original.OwnerRef, created.OwnerRef)
	_, err = f.remote.User(ctx, f.nodes[0], f.creds[0], original.RemoteUsername)
	require.EqualError(t, err, "VPN节点返回HTTP 404", "从未派发的源节点不得出现该账号")
	password := f.password(t, 1, created.SubscriptionURLs["base64"])
	stream, err := f.connect(1, password)
	require.NoError(t, err)
	defer stream.conn.Close()
	require.NoError(t, stream.echo([]byte("rerouted-before-dispatch")))
	t.Log("Reserve 后停用源节点：同 owner 改派，源节点无账号，目标真实代理通过")
}

type vpnLiveDisableAfterSubmit struct {
	service.VPNRemote
	db           *sql.DB
	source       int64
	disabled     bool
	actualStatus string
}

func (r *vpnLiveDisableAfterSubmit) Submit(ctx context.Context, server *service.VPNServer, credentials service.VPNCredentials, username string, raw json.RawMessage) (*service.VPNRemoteOperation, error) {
	result, err := r.VPNRemote.Submit(ctx, server, credentials, username, raw)
	var p service.VPNOperationPayload
	if err == nil && json.Unmarshal(raw, &p) == nil && p.Action == "create" && server.ID == r.source && !r.disabled {
		r.actualStatus = result.Status
		_, err = r.db.ExecContext(ctx, `UPDATE vpn_servers SET enabled=false WHERE id=$1`, r.source)
		r.disabled = err == nil
	}
	return result, err
}

func TestVPNRoutingLiveDisabledAfterRemoteAcceptedMigrates(t *testing.T) {
	f := newVPNLiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	user := vpnTestUser(t, f.db, 1)
	original, err := f.svc.Create(ctx, user, false, user)
	require.NoError(t, err)
	f.enable(t, 1, true)
	decorator := &vpnLiveDisableAfterSubmit{VPNRemote: f.remote, db: f.db, source: f.nodes[0].ID}
	f.svc = service.NewVPNService(f.r, &AESEncryptor{key: []byte("0123456789abcdef0123456789abcdef")}, decorator)
	f.svc.Tick(ctx)
	require.True(t, decorator.disabled)
	require.Equal(t, "pending", decorator.actualStatus, "必须真实命中远端 create 已受理但尚未应用的窗口")
	intermediate, err := f.svc.Get(ctx, original.ID)
	require.NoError(t, err)
	require.NotEqual(t, "succeeded", intermediate.OperationStatus)
	require.Empty(t, intermediate.SubscriptionURLs)
	migrated := f.waitApplied(t, ctx, original.ID)
	require.Equal(t, f.nodes[1].ID, migrated.ServerID)
	require.NotEqual(t, original.OwnerRef, migrated.OwnerRef)
	f.assertRetired(t, ctx, original, "", "", nil)
	password := f.password(t, 1, migrated.SubscriptionURLs["base64"])
	stream, err := f.connect(1, password)
	require.NoError(t, err)
	defer stream.conn.Close()
	require.NoError(t, stream.echo([]byte("migrated-after-accepted-create")))
	var action string
	require.NoError(t, f.db.QueryRow(`SELECT action FROM vpn_operations WHERE subscription_id=$1`, original.ID).Scan(&action))
	require.Equal(t, "migrate", action)
	t.Log("真实 create pending 后停用源节点：父操作保持 pending，旧账号确认撤销后迁移，新节点真实代理通过")
}
