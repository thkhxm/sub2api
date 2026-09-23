package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type vpnManualTestRepo struct {
	*vpnServiceTestRepo
	queued    []VPNOperationPayload
	filter    VPNFilter
	listCalls int
}

func (r *vpnManualTestRepo) Queue(_ context.Context, _, _ int64, payload VPNOperationPayload) error {
	r.queued = append(r.queued, payload)
	return nil
}

func (r *vpnManualTestRepo) List(_ context.Context, filter VPNFilter) ([]VPNSubscription, int, error) {
	r.filter = filter
	r.listCalls++
	return []VPNSubscription{}, 0, nil
}

func TestVPNManualServiceRequiresTargetAndKeepsChoice(t *testing.T) {
	svc, base, _ := vpnServiceFixture(t)
	repo := &vpnManualTestRepo{vpnServiceTestRepo: base}
	svc.repo = repo
	for _, id := range []int64{0, -1} {
		_, err := svc.Rotate(context.Background(), 1, 41, id)
		require.ErrorIs(t, err, ErrVPNInvalid)
	}
	require.Empty(t, repo.queued)
	_, err := svc.Rotate(context.Background(), 1, 41, 7)
	require.NoError(t, err)
	require.Equal(t, []VPNOperationPayload{{Action: "revoke", TargetServerID: 7}}, repo.queued)
}

func TestVPNManualRevokeDoesNotLeakSchedulingFieldsToNode(t *testing.T) {
	svc, repo, remote := vpnServiceFixture(t)
	payload, err := json.Marshal(VPNOperationPayload{OperationID: remote.snapshot.LastOperationID, OwnerRef: repo.sub.OwnerRef, Action: "revoke", TargetServerID: repo.sub.ServerID})
	require.NoError(t, err)
	op := &VPNOperation{ID: remote.snapshot.LastOperationID, SubscriptionID: repo.sub.ID, Payload: payload}
	svc.process(context.Background(), op)
	require.Equal(t, "succeeded", repo.finishState)
	require.Len(t, remote.payloads, 1)
	var wire map[string]any
	require.NoError(t, json.Unmarshal([]byte(remote.payloads[0]), &wire))
	require.Equal(t, map[string]any{"operation_id": op.ID, "owner_ref": repo.sub.OwnerRef, "action": "revoke"}, wire)
	require.Equal(t, string(payload), string(op.Payload))
}

func TestVPNManualDisabledOrUnhealthyTargetNeverRevokesSourceOrFallback(t *testing.T) {
	for _, condition := range []string{"disabled_before_revoke", "disabled_before_create", "unhealthy_before_revoke"} {
		t.Run(condition, func(t *testing.T) {
			svc, repo, remote := vpnMigrationFixture(t)
			m := repo.migration(t)
			m.ManualTarget = true
			if condition == "disabled_before_create" {
				m.Stage = "create_target"
				var err error
				m.InitialUsage, err = migrationUsage(&repo.sub.Snapshot, nil)
				require.NoError(t, err)
			}
			if condition == "unhealthy_before_revoke" {
				remote.metadata.Healthy = false
			} else {
				repo.servers[m.TargetServerID].Enabled = false
			}
			repo.persist(repo.operation, m)
			svc.process(context.Background(), repo.operation)
			require.Equal(t, "pending", repo.finishState)
			require.Empty(t, remote.calls)
			require.Zero(t, repo.retargetCount)
			require.Zero(t, repo.completeCount)
			require.Equal(t, int64(2), repo.migration(t).TargetServerID)
			require.True(t, repo.migration(t).ManualTarget)
		})
	}
}

func TestVPNUsageSortServiceValidationAndForwarding(t *testing.T) {
	svc, base, _ := vpnServiceFixture(t)
	repo := &vpnManualTestRepo{vpnServiceTestRepo: base}
	svc.repo = repo
	for _, filter := range []VPNFilter{{SortBy: "allocation"}, {SortBy: "used_bytes", SortOrder: "ASC"}, {SortBy: "used_bytes", SortOrder: "asc;SELECT 1"}} {
		_, err := svc.List(context.Background(), filter)
		require.ErrorIs(t, err, ErrVPNInvalid)
	}
	require.Zero(t, repo.listCalls)
	for _, order := range []string{"asc", "desc"} {
		filter := VPNFilter{Page: 3, PageSize: 7, SortBy: "used_bytes", SortOrder: order}
		_, err := svc.List(context.Background(), filter)
		require.NoError(t, err)
		require.Equal(t, filter, repo.filter)
	}
}
