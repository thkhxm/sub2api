package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 只模拟持久化边界与故障；迁移调度、远端请求及公开响应仍走真实服务代码。
type vpnMigrationTestRepo struct {
	*vpnServiceTestRepo
	servers       map[int64]*VPNServer
	operation     *VPNOperation
	events        []string
	checkpointErr error
	retargetErr   error
	completeErr   error
	dispatchErr   error
	completeCount int
	retargetCount int
}

func (r *vpnMigrationTestRepo) GetServer(_ context.Context, id int64) (*VPNServer, error) {
	server := r.servers[id]
	if server == nil {
		return nil, ErrVPNNotFound
	}
	copy := *server
	return &copy, nil
}

func (r *vpnMigrationTestRepo) PrepareOperation(_ context.Context, _ *VPNOperation) (*VPNOperation, error) {
	copy := *r.operation
	copy.Payload = append(json.RawMessage(nil), r.operation.Payload...)
	return &copy, nil
}

func (r *vpnMigrationTestRepo) MarkOperationDispatched(_ context.Context, _ *VPNOperation) error {
	return r.dispatchErr
}

func (r *vpnMigrationTestRepo) persist(o *VPNOperation, migration *VPNMigration) {
	var payload VPNOperationPayload
	_ = json.Unmarshal(o.Payload, &payload)
	payload.Migration = migration
	o.Payload, _ = json.Marshal(payload)
	copy := *o
	copy.Payload = append(json.RawMessage(nil), o.Payload...)
	r.operation = &copy
}

func (r *vpnMigrationTestRepo) CheckpointMigration(_ context.Context, o *VPNOperation, migration *VPNMigration) error {
	if r.checkpointErr != nil {
		return r.checkpointErr
	}
	r.events = append(r.events, "checkpoint")
	r.persist(o, migration)
	return nil
}

func (r *vpnMigrationTestRepo) RetargetMigration(_ context.Context, o *VPNOperation, migration *VPNMigration) (*VPNMigration, error) {
	r.events = append(r.events, "retarget")
	r.retargetCount++
	if r.retargetErr != nil {
		return nil, r.retargetErr
	}
	next := *migration
	next.TargetServerID, next.TargetOwnerRef, next.TargetUsername = 3, uuid.NewString(), "pc_reselected"
	next.TargetDispatched, next.TargetRetired = false, false
	r.persist(o, &next)
	return &next, nil
}

func (r *vpnMigrationTestRepo) CompleteMigration(_ context.Context, o *VPNOperation, migration *VPNMigration, snapshot VPNSnapshot, encrypted string) error {
	r.completeCount++
	if r.completeErr != nil {
		return r.completeErr
	}
	r.events = append(r.events, "complete")
	r.sub.ServerID, r.sub.OwnerRef, r.sub.RemoteUsername = migration.TargetServerID, migration.TargetOwnerRef, migration.TargetUsername
	r.sub.Status, r.sub.ApplyStatus, r.sub.AccessState = snapshot.Status, snapshot.ApplyStatus, snapshot.AccessState
	snapshot.SubscriptionURL = ""
	r.sub.Snapshot, r.sub.URLEncrypted = snapshot, encrypted
	migration.Stage = "completed"
	r.persist(o, migration)
	return nil
}

func (r *vpnMigrationTestRepo) Finish(ctx context.Context, o *VPNOperation, state, message string) error {
	r.sub.OperationStatus = state
	return r.vpnServiceTestRepo.Finish(ctx, o, state, message)
}

func (r *vpnMigrationTestRepo) migration(t *testing.T) *VPNMigration {
	t.Helper()
	var payload VPNOperationPayload
	require.NoError(t, json.Unmarshal(r.operation.Payload, &payload))
	return payload.Migration
}

type vpnMigrationCall struct {
	serverID int64
	username string
	payload  VPNOperationPayload
	raw      string
}

type vpnMigrationTestRemote struct {
	VPNRemote
	repo       *vpnMigrationTestRepo
	metadata   VPNServerMeta
	metaErr    error
	submitErr  map[int64]error
	userErr    map[int64]error
	status     map[int64]string
	wrongID    map[int64]bool
	calls      []vpnMigrationCall
	last       map[int64]VPNOperationPayload
	mutateUser func(int64, *VPNSnapshot)
}

func (r *vpnMigrationTestRemote) Server(context.Context, *VPNServer, VPNCredentials) (*VPNServerMeta, error) {
	copy := r.metadata
	return &copy, r.metaErr
}

func (r *vpnMigrationTestRemote) Submit(_ context.Context, server *VPNServer, _ VPNCredentials, username string, body json.RawMessage) (*VPNRemoteOperation, error) {
	var payload VPNOperationPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	r.last[server.ID] = payload
	r.calls = append(r.calls, vpnMigrationCall{server.ID, username, payload, string(body)})
	r.repo.events = append(r.repo.events, fmt.Sprintf("remote:%d:%s", server.ID, payload.Action))
	if err := r.submitErr[server.ID]; err != nil {
		return nil, err
	}
	return r.operation(server.ID, payload.OperationID), nil
}

func (r *vpnMigrationTestRemote) operation(serverID int64, id string) *VPNRemoteOperation {
	if r.wrongID[serverID] {
		id = uuid.NewString()
	}
	state := r.status[serverID]
	if state == "" {
		state = "succeeded"
	}
	return &VPNRemoteOperation{OperationID: id, Status: state}
}

func (r *vpnMigrationTestRemote) Operation(_ context.Context, server *VPNServer, _ VPNCredentials, id string) (*VPNRemoteOperation, error) {
	return r.operation(server.ID, id), nil
}

func (r *vpnMigrationTestRemote) User(_ context.Context, server *VPNServer, _ VPNCredentials, username string) (*VPNSnapshot, error) {
	if err := r.userErr[server.ID]; err != nil {
		return nil, err
	}
	payload := r.last[server.ID]
	start, end := vpnPeriodBounds(time.Now())
	x := &VPNSnapshot{Username: username, OwnerRef: payload.OwnerRef, LastOperationID: payload.OperationID,
		Status: "deleted", ApplyStatus: "applied", AccessState: "blocked", DataLimit: 100,
		UploadBytes: 20, DownloadBytes: 30, UsedTraffic: 70, PeriodStart: &start, PeriodEnd: &end, AccountingStatus: "ok"}
	if payload.Action == "create" {
		x.DataLimit = *payload.DataLimit
		x.UploadBytes, x.DownloadBytes, x.UsedTraffic = payload.InitialUsage.UploadBytes, payload.InitialUsage.DownloadBytes, payload.InitialUsage.UsedTraffic
		x.Status, x.AccessState = "active", "allowed"
		if !*payload.Enabled {
			x.Status, x.AccessState = "disabled", "blocked"
		} else if x.UsedTraffic >= x.DataLimit {
			x.Status, x.AccessState = "limited", "blocked"
		}
		x.SubscriptionURL = server.BaseURL + "/sub/new-private-token"
	}
	if r.mutateUser != nil {
		r.mutateUser(server.ID, x)
	}
	return x, nil
}

func vpnMigrationFixture(t *testing.T) (*VPNService, *vpnMigrationTestRepo, *vpnMigrationTestRemote) {
	t.Helper()
	svc, base, _ := vpnServiceFixture(t)
	servers := make(map[int64]*VPNServer)
	for id, host := range map[int64]string{1: "source.invalid", 2: "target.invalid", 3: "replacement.invalid"} {
		server := *base.server
		server.ID, server.BaseURL = id, "https://"+host
		servers[id] = &server
	}
	start, end := vpnPeriodBounds(time.Now())
	base.sub.Status, base.sub.ApplyStatus, base.sub.AccessState, base.sub.OperationStatus = "active", "applied", "allowed", "pending"
	base.sub.Snapshot = VPNSnapshot{DataLimit: 100, UploadBytes: 20, DownloadBytes: 30, UsedTraffic: 70, PeriodStart: &start, PeriodEnd: &end, AccountingStatus: "ok"}
	base.sub.URLEncrypted, _ = svc.cipher.Encrypt("vpn-url:https://source.invalid/sub/old-private-token")
	operationID := uuid.NewString()
	migration := &VPNMigration{SourceServerID: 1, SourceOwnerRef: base.sub.OwnerRef, SourceUsername: base.sub.RemoteUsername,
		TargetServerID: 2, TargetOwnerRef: uuid.NewString(), TargetUsername: "pc_target", Stage: "revoke_source", QuotaBytes: 100, Enabled: true}
	payload, err := json.Marshal(VPNOperationPayload{OperationID: operationID, OwnerRef: base.sub.OwnerRef, Action: "migrate", Migration: migration})
	require.NoError(t, err)
	repo := &vpnMigrationTestRepo{vpnServiceTestRepo: base, servers: servers, operation: &VPNOperation{ID: operationID, SubscriptionID: 1, Payload: payload}}
	remote := &vpnMigrationTestRemote{repo: repo, metadata: VPNServerMeta{Healthy: true, Capabilities: []string{"initial-usage-v1"}},
		submitErr: make(map[int64]error), userErr: make(map[int64]error), status: make(map[int64]string), wrongID: make(map[int64]bool), last: make(map[int64]VPNOperationPayload)}
	svc.repo, svc.remote = repo, remote
	return svc, repo, remote
}

func requireVPNMigrationHidden(t *testing.T, svc *VPNService, repo *vpnMigrationTestRepo) {
	t.Helper()
	public, err := svc.Get(context.Background(), repo.sub.ID)
	require.NoError(t, err)
	require.Empty(t, public.SubscriptionURLs)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-token")
}

func TestVPNMigrationSourceRetirementMustBeVerified(t *testing.T) {
	cases := map[string]func(*vpnMigrationTestRepo, *vpnMigrationTestRemote){
		"source_timeout": func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) {
			remote.submitErr[1] = errors.New("timeout")
		},
		"source_pending":  func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) { remote.status[1] = "pending" },
		"source_failed":   func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) { remote.status[1] = "failed" },
		"wrong_operation": func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) { remote.wrongID[1] = true },
		"source_read_error": func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) {
			remote.userErr[1] = errors.New("read unavailable")
		},
		"still_active": func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) {
			remote.mutateUser = func(_ int64, x *VPNSnapshot) { x.Status, x.AccessState = "active", "allowed" }
		},
		"not_applied": func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) {
			remote.mutateUser = func(_ int64, x *VPNSnapshot) { x.ApplyStatus = "pending" }
		},
		"wrong_owner": func(_ *vpnMigrationTestRepo, remote *vpnMigrationTestRemote) {
			remote.mutateUser = func(_ int64, x *VPNSnapshot) { x.OwnerRef = uuid.NewString() }
		},
		"lost_lease": func(repo *vpnMigrationTestRepo, _ *vpnMigrationTestRemote) { repo.dispatchErr = ErrVPNBusy },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, remote := vpnMigrationFixture(t)
			change(repo, remote)
			svc.process(context.Background(), repo.operation)
			require.NotEqual(t, "succeeded", repo.finishState)
			require.Zero(t, repo.completeCount)
			for _, call := range remote.calls {
				require.Equal(t, int64(1), call.serverID, "源撤销未证实前不能触及目标账号")
				require.Equal(t, "delete", call.payload.Action)
			}
			if name == "lost_lease" {
				require.Empty(t, remote.calls)
			}
			requireVPNMigrationHidden(t, svc, repo)
		})
	}
}

func TestVPNMigrationTargetCapabilityIsCheckedBeforeSourceDeletion(t *testing.T) {
	for _, condition := range []string{"old_node", "unhealthy", "unreachable"} {
		t.Run(condition, func(t *testing.T) {
			svc, repo, remote := vpnMigrationFixture(t)
			switch condition {
			case "old_node":
				remote.metadata.Capabilities = nil
			case "unhealthy":
				remote.metadata.Healthy = false
			case "unreachable":
				remote.metaErr = errors.New("unreachable")
			}
			svc.process(context.Background(), repo.operation)
			require.Equal(t, "pending", repo.finishState)
			require.Empty(t, remote.calls)
			require.Equal(t, int64(1), repo.sub.ServerID)
			requireVPNMigrationHidden(t, svc, repo)
		})
	}
}

func TestVPNMigrationRetriesPreserveIdentitiesQuotaUsageAndHideOldURL(t *testing.T) {
	svc, repo, remote := vpnMigrationFixture(t)
	ctx := context.Background()
	repo.checkpointErr = errors.New("checkpoint unavailable")
	svc.process(ctx, repo.operation)
	requireVPNMigrationHidden(t, svc, repo)
	repo.checkpointErr = nil
	svc.process(ctx, repo.operation)
	require.Equal(t, "create_target", repo.migration(t).Stage)
	require.Equal(t, remote.calls[0].raw, remote.calls[1].raw, "已撤源但本地持久化失败须重放同一撤销")
	remote.submitErr[2] = errors.New("target outcome unknown")
	svc.process(ctx, repo.operation)
	requireVPNMigrationHidden(t, svc, repo)
	remote.submitErr[2] = nil
	svc.process(ctx, repo.operation)
	require.Equal(t, "succeeded", repo.finishState)
	require.Equal(t, remote.calls[2].raw, remote.calls[3].raw, "未知结果不能换编号再次创建")
	create := remote.calls[3].payload
	require.Equal(t, repo.operation.ID, create.OperationID)
	require.Equal(t, int64(100), *create.DataLimit, "应续接原月额而非把剩余额度当新月额")
	require.True(t, *create.Enabled)
	require.Equal(t, int64(20), create.InitialUsage.UploadBytes)
	require.Equal(t, int64(30), create.InitialUsage.DownloadBytes)
	require.Equal(t, int64(70), create.InitialUsage.UsedTraffic)
	require.Equal(t, int64(2), repo.sub.ServerID)
	require.NotContains(t, repo.sub.URLEncrypted, "new-private-token")
	public, err := svc.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(70), public.UsedBytes)
	require.Equal(t, int64(30), public.RemainingBytes)
	require.Equal(t, "https://target.invalid/sub/new-private-token/clash-meta", public.SubscriptionURLs["clash"])
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "old-private-token")
}

func TestVPNMigrationRetiredTargetCannotRecreateAfterReenabled(t *testing.T) {
	svc, repo, remote := vpnMigrationFixture(t)
	ctx := context.Background()
	svc.process(ctx, repo.operation)
	remote.status[2] = "pending"
	svc.process(ctx, repo.operation)
	require.True(t, repo.migration(t).TargetDispatched)
	repo.servers[2].Enabled = false
	repo.retargetErr = ErrVPNNoServer
	svc.process(ctx, repo.operation)
	require.Zero(t, repo.retargetCount, "目标撤销仍在处理中时不能提前改派")
	require.False(t, repo.migration(t).TargetRetired)
	requireVPNMigrationHidden(t, svc, repo)
	retirement := remote.calls[len(remote.calls)-1].raw
	remote.status[2] = "succeeded"
	svc.process(ctx, repo.operation)
	require.Equal(t, retirement, remote.calls[len(remote.calls)-1].raw, "撤销未知结果须复用原操作编号和请求")
	require.True(t, repo.migration(t).TargetRetired, "目标撤销必须先持久化，即使暂时没有新节点")
	requireVPNMigrationHidden(t, svc, repo)
	require.Equal(t, []string{"remote:2:delete", "checkpoint", "retarget"}, repo.events[len(repo.events)-3:])
	calls := len(remote.calls)
	repo.servers[2].Enabled = true
	svc.process(ctx, repo.operation)
	require.Len(t, remote.calls, calls, "已退役目标重新启用也不能重放原create")
	require.Equal(t, "pending", repo.finishState)
	repo.retargetErr = nil
	svc.process(ctx, repo.operation)
	require.Equal(t, int64(3), repo.migration(t).TargetServerID)
	requireVPNMigrationHidden(t, svc, repo)
	svc.process(ctx, repo.operation)
	require.Equal(t, "succeeded", repo.finishState)
	require.Equal(t, int64(3), repo.sub.ServerID)
	for _, call := range remote.calls[calls:] {
		require.Equal(t, int64(3), call.serverID)
	}
}

func TestVPNMigrationRejectsInconsistentTargetAndPersistenceFailure(t *testing.T) {
	cases := map[string]func(*VPNSnapshot){
		"quota_changed":   func(x *VPNSnapshot) { x.DataLimit++ },
		"total_lost":      func(x *VPNSnapshot) { x.UsedTraffic-- },
		"upload_lost":     func(x *VPNSnapshot) { x.UploadBytes-- },
		"download_lost":   func(x *VPNSnapshot) { x.DownloadBytes-- },
		"period_missing":  func(x *VPNSnapshot) { x.PeriodStart = nil },
		"not_applied":     func(x *VPNSnapshot) { x.ApplyStatus = "pending" },
		"wrong_owner":     func(x *VPNSnapshot) { x.OwnerRef = uuid.NewString() },
		"wrong_operation": func(x *VPNSnapshot) { x.LastOperationID = uuid.NewString() },
		"active_blocked":  func(x *VPNSnapshot) { x.AccessState = "blocked" },
		"false_limited":   func(x *VPNSnapshot) { x.Status, x.AccessState = "limited", "blocked" },
		"foreign_url":     func(x *VPNSnapshot) { x.SubscriptionURL = "https://evil.invalid/sub/secret" },
		"no_url":          func(x *VPNSnapshot) { x.SubscriptionURL = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, remote := vpnMigrationFixture(t)
			svc.process(context.Background(), repo.operation)
			remote.mutateUser = func(_ int64, x *VPNSnapshot) { change(x) }
			svc.process(context.Background(), repo.operation)
			require.Equal(t, "pending", repo.finishState)
			require.Zero(t, repo.completeCount, "未经核验的目标不能进入完成写入")
			requireVPNMigrationHidden(t, svc, repo)
		})
	}
	for _, failure := range []string{"submit", "read", "remote_failed", "complete"} {
		t.Run(failure, func(t *testing.T) {
			svc, repo, remote := vpnMigrationFixture(t)
			svc.process(context.Background(), repo.operation)
			switch failure {
			case "submit":
				remote.submitErr[2] = errors.New("unavailable")
			case "read":
				remote.userErr[2] = errors.New("unavailable")
			case "remote_failed":
				remote.status[2] = "failed"
			case "complete":
				repo.completeErr = errors.New("commit failed")
			}
			svc.process(context.Background(), repo.operation)
			require.NotEqual(t, "succeeded", repo.finishState)
			require.Equal(t, int64(1), repo.sub.ServerID)
			requireVPNMigrationHidden(t, svc, repo)
		})
	}
}

func TestVPNMigrationPendingRunningAndFailedNeverExposeCachedURL(t *testing.T) {
	for _, state := range []string{"pending", "running", "failed"} {
		t.Run(state, func(t *testing.T) {
			svc, repo, _ := vpnMigrationFixture(t)
			repo.sub.OperationStatus = state
			repo.sub.SubscriptionURLs = map[string]string{"clash": "https://source.invalid/sub/old-private-token/clash-meta"}
			requireVPNMigrationHidden(t, svc, repo)
		})
	}
}
