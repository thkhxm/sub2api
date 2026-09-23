package repository

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type vpnNodeRequest struct {
	SourceID, ExcludedID, RequiredQuota, RequestedID int64
	OwnerRef, IgnoreOperationID                      string
}

type vpnAllocationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadVPNAllocationDesired(ctx context.Context, q vpnAllocationQueryer, servers []service.VPNServer, ignoreOperationID string) (map[int64]map[string]int64, error) {
	desired := make(map[int64]map[string]int64, len(servers))
	ids := make([]int64, 0, len(servers))
	for _, server := range servers {
		ids = append(ids, server.ID)
		desired[server.ID] = make(map[string]int64)
	}
	if len(ids) == 0 {
		return desired, nil
	}
	// 节点汇总、账号采样和排队操作异步更新；降额确认前保留各处已知的较大额度。
	rows, err := q.QueryContext(ctx, `SELECT s.server_id,s.owner_ref,GREATEST(s.quota_bytes,COALESCE((s.snapshot->>'data_limit')::bigint,0))
FROM vpn_subscriptions s
WHERE s.server_id=ANY($1) AND s.deleted_at IS NULL AND NOT EXISTS (
 SELECT 1 FROM vpn_operations o WHERE o.subscription_id=s.id AND o.action='migrate'
 AND o.status IN ('pending','running','failed') AND o.payload->'migration'->>'stage'='create_target'
 AND (o.payload->'migration'->>'source_server_id')::bigint=s.server_id
 AND o.payload->'migration'->>'source_owner_ref'=s.owner_ref)
UNION ALL
SELECT s.server_id,s.owner_ref,(o.payload->>'data_limit')::bigint
FROM vpn_operations o JOIN vpn_subscriptions s ON s.id=o.subscription_id
WHERE s.server_id=ANY($1) AND s.deleted_at IS NULL AND o.action IN ('create','update')
 AND o.status IN ('pending','running','failed') AND o.payload->>'owner_ref'=s.owner_ref
 AND o.payload->>'data_limit' IS NOT NULL
UNION ALL
SELECT (o.payload->'migration'->>'target_server_id')::bigint,
 o.payload->'migration'->>'target_owner_ref',GREATEST((o.payload->'migration'->>'quota_bytes')::bigint,s.quota_bytes)
FROM vpn_operations o JOIN vpn_subscriptions s ON s.id=o.subscription_id
WHERE s.deleted_at IS NULL AND o.action='migrate' AND o.status IN ('pending','running','failed')
 AND o.payload->'migration'->>'stage'<>'completed'
 AND NOT COALESCE((o.payload->'migration'->>'target_retired')::boolean,false)
 AND (o.payload->'migration'->>'target_server_id')::bigint=ANY($1)
 AND ($2='' OR o.id<>$2)`, pq.Array(ids), ignoreOperationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, quota int64
		var owner string
		if err := rows.Scan(&id, &owner, &quota); err != nil {
			return nil, err
		}
		if quota > desired[id][owner] {
			desired[id][owner] = quota
		}
	}
	return desired, rows.Err()
}

// 远端总额保留非平台账号和待确认的旧额度，本地只补尚未落地的正向差额。
func applyVPNAllocations(servers []service.VPNServer, desired map[int64]map[string]int64) {
	for i := range servers {
		server := &servers[i]
		server.AllocationQuotaBytes, server.AllocationAvailableBytes, server.AllocationRatio = nil, nil, nil
		if !server.AllocationSnapshot.Valid() {
			continue
		}
		allocated := server.AllocationSnapshot.AllocatedQuotaBytes
		valid := true
		for owner, quota := range desired[server.ID] {
			delta := quota - server.AllocationSnapshot.ManagedQuotaBytes[owner]
			if delta <= 0 {
				continue
			}
			if delta > service.VPNMaxQuota-allocated {
				valid = false
				break
			}
			allocated += delta
		}
		if !valid {
			continue
		}
		server.AllocationQuotaBytes = &allocated
		if server.TrafficQuotaBytes > 0 {
			available := max(server.TrafficQuotaBytes-allocated, 0)
			ratio := float64(allocated) / float64(server.TrafficQuotaBytes)
			server.AllocationAvailableBytes, server.AllocationRatio = &available, &ratio
		}
	}
}

func hydrateVPNAllocations(ctx context.Context, q vpnAllocationQueryer, servers []service.VPNServer, ignoreOperationID string) error {
	desired, err := loadVPNAllocationDesired(ctx, q, servers, ignoreOperationID)
	if err != nil {
		return err
	}
	applyVPNAllocations(servers, desired)
	return nil
}

func lessVPNAllocation(a, b *service.VPNServer) bool {
	ratio := new(big.Rat).SetFrac64(*a.AllocationQuotaBytes, a.TrafficQuotaBytes).Cmp(new(big.Rat).SetFrac64(*b.AllocationQuotaBytes, b.TrafficQuotaBytes))
	if ratio != 0 {
		return ratio < 0
	}
	if a.LastAssignedAt == nil || b.LastAssignedAt == nil {
		if a.LastAssignedAt != b.LastAssignedAt {
			return a.LastAssignedAt == nil
		}
	} else if !a.LastAssignedAt.Equal(*b.LastAssignedAt) {
		return a.LastAssignedAt.Before(*b.LastAssignedAt)
	}
	return a.ID < b.ID
}

// 调用者持有统一分配锁；锁定候选后，读取最新预占并核验完整额度能否放入。
func selectVPNServerByQuota(ctx context.Context, tx *sql.Tx, req vpnNodeRequest) (int64, error) {
	if req.RequiredQuota <= 0 || req.RequiredQuota > service.VPNMaxQuota {
		return 0, service.ErrVPNInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+vpnServerColumns+` FROM vpn_servers v
WHERE v.enabled AND v.healthy AND v.last_checked_at>now()-interval '2 minutes'
AND v.traffic_quota_bytes>0 AND v.id<>$1 AND v.id<>$2 AND ($3::bigint=0 OR v.id=$3)
ORDER BY v.id FOR UPDATE OF v`, req.SourceID, req.ExcludedID, req.RequestedID)
	if err != nil {
		return 0, err
	}
	servers := []service.VPNServer{}
	for rows.Next() {
		server, err := scanVPNServer(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		servers = append(servers, *server)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	desired, err := loadVPNAllocationDesired(ctx, tx, servers, req.IgnoreOperationID)
	if err != nil {
		return 0, err
	}
	applyVPNAllocations(servers, desired)
	var selected *service.VPNServer
	for i := range servers {
		server := &servers[i]
		if server.AllocationQuotaBytes == nil || *server.AllocationQuotaBytes > server.TrafficQuotaBytes {
			continue
		}
		alreadyReserved := int64(0)
		if req.OwnerRef != "" {
			alreadyReserved = max(server.AllocationSnapshot.ManagedQuotaBytes[req.OwnerRef], desired[server.ID][req.OwnerRef])
		}
		additional := max(req.RequiredQuota-alreadyReserved, 0)
		if additional > server.TrafficQuotaBytes-*server.AllocationQuotaBytes {
			continue
		}
		if selected == nil || lessVPNAllocation(server, selected) {
			selected = server
		}
	}
	if selected == nil {
		return 0, service.ErrVPNNoServer
	}
	return selected.ID, nil
}
