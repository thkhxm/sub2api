//go:build vpnintegration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// 必须使用显式指定的隔离数据库；缺少数据库直接失败，不跳过验收。
func vpnTestRepository(t *testing.T) (*vpnRepository, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("VPN_TEST_DSN")
	require.NotEmpty(t, dsn, "设置 VPN_TEST_DSN 为隔离 PostgreSQL 测试库")
	admin, e := sql.Open("postgres", dsn)
	require.NoError(t, e)
	require.NoError(t, admin.Ping())
	schema := "vpn_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, e = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, e)
	parsed, e := url.Parse(dsn)
	require.NoError(t, e)
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, e := sql.Open("postgres", parsed.String())
	require.NoError(t, e)
	db.SetMaxOpenConns(24)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, admin.Close())
	})
	_, e = db.Exec(`CREATE TABLE users(id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',balance NUMERIC NOT NULL DEFAULT 1,deleted_at TIMESTAMPTZ)`)
	require.NoError(t, e)
	migration, e := os.ReadFile("../../migrations/238_vpn_subscriptions.sql")
	require.NoError(t, e)
	_, e = db.Exec(string(migration))
	require.NoError(t, e)
	return &vpnRepository{db: db}, db
}
func vpnTestUser(t *testing.T, db *sql.DB, balance int) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,balance) VALUES($1,$2) RETURNING id`, uuid.NewString()+"@vpn.test", balance).Scan(&id))
	return id
}
func vpnTestServer(t *testing.T, r *vpnRepository, count int) *service.VPNServer {
	t.Helper()
	ctx := context.Background()
	s, e := r.SaveServer(ctx, &service.VPNServer{Name: "test", BaseURL: "https://" + uuid.NewString() + ".invalid", AdminUsername: "integration", CredentialsEncrypted: "encrypted-fixture", Enabled: true})
	require.NoError(t, e)
	require.NoError(t, r.UpdateServerHealth(ctx, s.ID, &service.VPNServerMeta{Healthy: true, PersonalUserCount: count}, ""))
	return s
}
func vpnReserve(t *testing.T, r *vpnRepository, user int64) *service.VPNSubscription {
	t.Helper()
	s, e := r.Reserve(context.Background(), user, false, user, uuid.NewString())
	require.NoError(t, e)
	return s
}

func TestVPNRepositoryConcurrentReserveUnique(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	user := vpnTestUser(t, db, 10)
	var wg sync.WaitGroup
	ids := make(chan int64, 20)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := r.Reserve(ctx, user, false, user, uuid.NewString())
			if e != nil {
				errs <- e
				return
			}
			ids <- s.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var id int64
	for got := range ids {
		if id == 0 {
			id = got
		}
		require.Equal(t, id, got)
	}
	require.NotZero(t, id)
	var subs, ops, balance int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_subscriptions`).Scan(&subs))
	require.Equal(t, 1, subs)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_operations`).Scan(&ops))
	require.Equal(t, 1, ops)
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, 10, balance)
	for _, state := range []string{"disabled", "limited", "provisioning"} {
		_, e := db.Exec(`UPDATE vpn_subscriptions SET status=$2,apply_status='failed',operation_status='failed' WHERE id=$1`, id, state)
		require.NoError(t, e)
		again, e := r.Reserve(ctx, user, false, user, uuid.NewString())
		require.NoError(t, e)
		require.Equal(t, id, again.ID)
	}
}
func TestVPNRepositoryAllocationAndBalance(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	a := vpnTestServer(t, r, 0)
	b := vpnTestServer(t, r, 0)
	first := vpnReserve(t, r, vpnTestUser(t, db, 1))
	require.Equal(t, a.ID, first.ServerID)
	second := vpnReserve(t, r, vpnTestUser(t, db, 1))
	require.Equal(t, b.ID, second.ServerID)
	third := vpnReserve(t, r, vpnTestUser(t, db, 1))
	require.Equal(t, a.ID, third.ServerID)
	require.Equal(t, service.VPNDefaultQuota, third.QuotaBytes)
	require.LessOrEqual(t, len(third.RemoteUsername), 32)
	// 已存在远端的 owner 不应重复计入预占。
	require.NoError(t, r.UpdateServerHealth(ctx, a.ID, &service.VPNServerMeta{Healthy: true, PersonalUserCount: 2, ManagedOwnerRefs: []string{first.OwnerRef, third.OwnerRef}}, ""))
	require.NoError(t, r.UpdateServerHealth(ctx, b.ID, &service.VPNServerMeta{Healthy: true, PersonalUserCount: 3, ManagedOwnerRefs: []string{second.OwnerRef}}, ""))
	fourth := vpnReserve(t, r, vpnTestUser(t, db, 1))
	require.Equal(t, a.ID, fourth.ServerID)
	zero := vpnTestUser(t, db, 0)
	_, e := r.Reserve(ctx, zero, false, zero, uuid.NewString())
	require.ErrorIs(t, e, service.ErrVPNBalance)
	admin, e := r.Reserve(ctx, zero, true, 999, uuid.NewString())
	require.NoError(t, e)
	require.Equal(t, zero, admin.UserID)
	_, e = db.Exec(`UPDATE users SET balance=0 WHERE id=$1`, first.UserID)
	require.NoError(t, e)
	ids, e := r.InactiveUserSubscriptions(ctx)
	require.NoError(t, e)
	require.NotContains(t, ids, first.ID)
	_, e = db.Exec(`UPDATE vpn_servers SET last_checked_at=now()-interval '3 minutes'`)
	require.NoError(t, e)
	_, e = r.Reserve(ctx, vpnTestUser(t, db, 1), false, 1, uuid.NewString())
	require.ErrorIs(t, e, service.ErrVPNNoServer)
	ok, reason, e := r.Eligibility(ctx, zero)
	require.NoError(t, e)
	require.False(t, ok)
	require.Equal(t, "balance_required", reason)
	_, e = db.Exec(`UPDATE users SET status='disabled' WHERE id=$1`, zero)
	require.NoError(t, e)
	ok, reason, e = r.Eligibility(ctx, zero)
	require.NoError(t, e)
	require.False(t, ok)
	require.Equal(t, "user_inactive", reason)
}
func TestVPNRepositoryOperationLeaseAndBusy(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	s := vpnReserve(t, r, vpnTestUser(t, db, 1))
	off := false
	require.ErrorIs(t, r.Queue(ctx, s.ID, 1, service.VPNOperationPayload{Action: "update", Enabled: &off}), service.ErrVPNBusy)
	var wg sync.WaitGroup
	got := make(chan *service.VPNOperation, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := r.Claim(ctx)
			if e != nil {
				errs <- e
			}
			if o != nil {
				got <- o
			}
		}()
	}
	wg.Wait()
	close(got)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var old *service.VPNOperation
	count := 0
	for o := range got {
		old = o
		count++
	}
	require.Equal(t, 1, count)
	require.NotNil(t, old)
	var p service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(old.Payload, &p))
	require.Equal(t, old.ID, p.OperationID)
	require.Equal(t, s.OwnerRef, p.OwnerRef)
	require.Equal(t, service.VPNDefaultQuota, *p.DataLimit)
	_, e := db.Exec(`UPDATE vpn_operations SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID)
	require.NoError(t, e)
	newer, e := r.Claim(ctx)
	require.NoError(t, e)
	require.NotNil(t, newer)
	require.Equal(t, old.ID, newer.ID)
	require.NotEqual(t, old.LeaseToken, newer.LeaseToken)
	require.NoError(t, r.Finish(ctx, old, "succeeded", "stale worker"))
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM vpn_operations WHERE id=$1`, old.ID).Scan(&status))
	require.Equal(t, "running", status)
	require.NoError(t, r.Finish(ctx, newer, "failed", "retryable"))
	require.ErrorIs(t, r.Queue(ctx, s.ID, 1, service.VPNOperationPayload{Action: "revoke"}), service.ErrVPNBusy)
	require.NoError(t, r.Retry(ctx, s.ID))
	retry, e := r.Claim(ctx)
	require.NoError(t, e)
	require.Equal(t, old.ID, retry.ID)
	require.NoError(t, r.Finish(ctx, retry, "succeeded", ""))
	require.NoError(t, r.Queue(ctx, s.ID, 1, service.VPNOperationPayload{Action: "update", Enabled: &off}))
	updated, e := r.GetSubscription(ctx, s.ID)
	require.NoError(t, e)
	require.False(t, updated.Enabled)
	require.Equal(t, "pending", updated.ApplyStatus)
}
func TestVPNRepositorySnapshotSecurityAndBinding(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	server := vpnTestServer(t, r, 0)
	s := vpnReserve(t, r, vpnTestUser(t, db, 1))
	op, e := r.Claim(ctx)
	require.NoError(t, e)
	now := time.Now()
	x := service.VPNSnapshot{Username: s.RemoteUsername, OwnerRef: s.OwnerRef, Status: "active", ApplyStatus: "applied", AccessState: "allowed", SubscriptionURL: "https://node.invalid/sub/secret-token", DataLimit: service.VPNDefaultQuota, UploadBytes: 12, DownloadBytes: 34, UsedTraffic: 46, SampledAt: &now, AccountingStatus: "ok", LastOperationID: op.ID}
	require.NoError(t, r.SaveSnapshot(ctx, s.ID, x, "encrypted-url"))
	var raw, cipher string
	require.NoError(t, db.QueryRow(`SELECT snapshot::text,url_encrypted FROM vpn_subscriptions WHERE id=$1`, s.ID).Scan(&raw, &cipher))
	require.NotContains(t, raw, "secret-token")
	require.NotContains(t, raw, "subscription_url")
	require.Equal(t, "encrypted-url", cipher)
	fresh, e := r.GetSubscription(ctx, s.ID)
	require.NoError(t, e)
	require.Equal(t, "applied", fresh.ApplyStatus)
	public, e := json.Marshal(fresh)
	require.NoError(t, e)
	require.NotContains(t, string(public), "encrypted-url")
	require.NotContains(t, string(public), s.OwnerRef)
	server.BaseURL = "https://another.invalid"
	_, e = r.SaveServer(ctx, server)
	require.ErrorIs(t, e, service.ErrVPNInvalid)
	require.NoError(t, r.Finish(ctx, op, "succeeded", ""))
	allowed, e := r.RequestRefresh(ctx, s.ID)
	require.NoError(t, e)
	require.True(t, allowed)
	allowed, e = r.RequestRefresh(ctx, s.ID)
	require.NoError(t, e)
	require.False(t, allowed)
	summary, e := r.Summary(ctx)
	require.NoError(t, e)
	require.Equal(t, int64(46), summary.UsedBytes)
	require.Equal(t, 1, summary.Active)
	list, total, e := r.List(ctx, service.VPNFilter{Page: 1, PageSize: 20, Query: s.RemoteUsername, ServerID: s.ServerID, Status: "active"})
	require.NoError(t, e)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	x.OwnerRef = uuid.NewString()
	x.UsedTraffic = 999
	_ = r.SaveSnapshot(ctx, s.ID, x, "wrong-url")
	fresh, e = r.GetSubscription(ctx, s.ID)
	require.NoError(t, e)
	require.Equal(t, int64(46), fresh.Snapshot.UsedTraffic)
	require.Equal(t, "encrypted-url", fresh.URLEncrypted)
}
func TestVPNRepositoryConcurrentBalancedAllocation(t *testing.T) {
	r, db := vpnTestRepository(t)
	vpnTestServer(t, r, 0)
	vpnTestServer(t, r, 0)
	users := make([]int64, 16)
	for i := range users {
		users[i] = vpnTestUser(t, db, 1)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(users))
	for _, id := range users {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			_, e := r.Reserve(context.Background(), id, false, id, uuid.NewString())
			if e != nil {
				errs <- e
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	rows, e := db.Query(`SELECT server_id,count(*) FROM vpn_subscriptions GROUP BY server_id`)
	require.NoError(t, e)
	defer rows.Close()
	groups := 0
	for rows.Next() {
		var id int64
		var count int
		require.NoError(t, rows.Scan(&id, &count))
		require.Equal(t, 8, count)
		groups++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 2, groups)
}

func TestVPNRepositoryOldSnapshotCannotOverwriteNewOperation(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	s := vpnReserve(t, r, vpnTestUser(t, db, 1))
	old, e := r.Claim(ctx)
	require.NoError(t, e)
	now := time.Now().UTC()
	x := service.VPNSnapshot{Username: s.RemoteUsername, OwnerRef: s.OwnerRef, Status: "active", ApplyStatus: "applied", AccessState: "allowed", DataLimit: service.VPNDefaultQuota, UsedTraffic: 100, SampledAt: &now, LastOperationID: old.ID}
	require.NoError(t, r.SaveSnapshot(ctx, s.ID, x, "old-url"))
	require.NoError(t, r.Finish(ctx, old, "succeeded", ""))
	older := now.Add(-time.Minute)
	stale := x
	stale.SampledAt = &older
	stale.UsedTraffic = 10
	require.NoError(t, r.SaveSnapshot(ctx, s.ID, stale, "stale-url"))
	fresh, e := r.GetSubscription(ctx, s.ID)
	require.NoError(t, e)
	require.Equal(t, int64(100), fresh.Snapshot.UsedTraffic)
	require.Equal(t, "old-url", fresh.URLEncrypted)
	require.NoError(t, r.Queue(ctx, s.ID, 1, service.VPNOperationPayload{Action: "revoke"}))
	current, e := r.Claim(ctx)
	require.NoError(t, e)
	require.NotEqual(t, old.ID, current.ID)
	newer := now.Add(time.Minute)
	x.SampledAt = &newer
	x.UsedTraffic = 200
	require.NoError(t, r.SaveSnapshot(ctx, s.ID, x, "delayed-old-url"))
	fresh, e = r.GetSubscription(ctx, s.ID)
	require.NoError(t, e)
	require.Equal(t, "pending", fresh.ApplyStatus)
	require.Equal(t, int64(100), fresh.Snapshot.UsedTraffic)
	require.Equal(t, "old-url", fresh.URLEncrypted)
	x.LastOperationID = current.ID
	require.NoError(t, r.SaveSnapshot(ctx, s.ID, x, "rotated-url"))
	fresh, e = r.GetSubscription(ctx, s.ID)
	require.NoError(t, e)
	require.Equal(t, "applied", fresh.ApplyStatus)
	require.Equal(t, int64(200), fresh.Snapshot.UsedTraffic)
	require.Equal(t, "rotated-url", fresh.URLEncrypted)
}

type vpnRepositoryRemoteFixture struct {
	service.VPNRemote
	operation service.VPNOperationPayload
	base      string
}

func (r *vpnRepositoryRemoteFixture) Server(context.Context, *service.VPNServer, service.VPNCredentials) (*service.VPNServerMeta, error) {
	now := time.Now()
	return &service.VPNServerMeta{APIVersion: "sub2api-v1", Healthy: true, Protocol: "trojan", InboundTag: "TROJAN_TLS", AccountingStatus: "ok", SampledAt: &now}, nil
}
func (r *vpnRepositoryRemoteFixture) Submit(_ context.Context, _ *service.VPNServer, _ service.VPNCredentials, _ string, raw json.RawMessage) (*service.VPNRemoteOperation, error) {
	if e := json.Unmarshal(raw, &r.operation); e != nil {
		return nil, e
	}
	return &service.VPNRemoteOperation{OperationID: r.operation.OperationID, Status: "succeeded"}, nil
}
func (r *vpnRepositoryRemoteFixture) User(_ context.Context, _ *service.VPNServer, _ service.VPNCredentials, username string) (*service.VPNSnapshot, error) {
	now := time.Now()
	return &service.VPNSnapshot{Username: username, OwnerRef: r.operation.OwnerRef, Status: "active", ApplyStatus: "applied", AccessState: "allowed", DataLimit: *r.operation.DataLimit, UploadBytes: 12, DownloadBytes: 34, UsedTraffic: 46, SampledAt: &now, AccountingStatus: "ok", LastOperationID: r.operation.OperationID, SubscriptionURL: r.base + "/sub/test-secret-token"}, nil
}
func TestVPNRepositoryServiceEncryptionRoundTrip(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	cipher := &AESEncryptor{key: []byte("0123456789abcdef0123456789abcdef")}
	remote := &vpnRepositoryRemoteFixture{base: "https://vpn-encryption.invalid"}
	svc := service.NewVPNService(r, cipher, remote)
	server, e := svc.SaveServer(ctx, 0, service.VPNServerInput{Name: "encrypted", BaseURL: remote.base, AdminUsername: "integration", AdminPassword: "private-admin-password", Enabled: true})
	require.NoError(t, e)
	require.True(t, server.Healthy)
	var encrypted string
	require.NoError(t, db.QueryRow(`SELECT credentials_encrypted FROM vpn_servers WHERE id=$1`, server.ID).Scan(&encrypted))
	require.NotContains(t, encrypted, "private-admin-password")
	plain, e := cipher.Decrypt(encrypted)
	require.NoError(t, e)
	require.Contains(t, plain, "private-admin-password")
	user := vpnTestUser(t, db, 1)
	sub, e := svc.Create(ctx, user, false, user)
	require.NoError(t, e)
	require.Nil(t, sub.SubscriptionURLs)
	svc.Tick(ctx)
	result, e := svc.Mine(ctx, user)
	require.NoError(t, e)
	require.Equal(t, "succeeded", result.Subscription.OperationStatus)
	require.Equal(t, "applied", result.Subscription.ApplyStatus)
	require.Equal(t, int64(46), result.Subscription.UsedBytes)
	require.Equal(t, remote.base+"/sub/test-secret-token/clash-meta", result.Subscription.SubscriptionURLs["clash"])
	var raw string
	require.NoError(t, db.QueryRow(`SELECT snapshot::text,url_encrypted FROM vpn_subscriptions WHERE id=$1`, sub.ID).Scan(&raw, &encrypted))
	require.NotContains(t, raw, "test-secret-token")
	require.NotContains(t, encrypted, "test-secret-token")
	plain, e = cipher.Decrypt(encrypted)
	require.NoError(t, e)
	require.Equal(t, "vpn-url:"+remote.base+"/sub/test-secret-token", plain)
}
