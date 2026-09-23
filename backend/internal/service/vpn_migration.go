package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 迁移额度只用于个人账期续接，不计入目标节点的真实传输量。
type VPNInitialUsage struct {
	UploadBytes      int64     `json:"upload_bytes"`
	DownloadBytes    int64     `json:"download_bytes"`
	UsedTraffic      int64     `json:"used_traffic"`
	PeriodStart      time.Time `json:"period_start"`
	PeriodEnd        time.Time `json:"period_end"`
	AccountingStatus string    `json:"accounting_status"`
}

// 迁移阶段与操作一同持久化；任何远端请求重放都使用原来的操作编号。
type VPNMigration struct {
	ManualTarget     bool             `json:"manual_target,omitempty"`
	SourceServerID   int64            `json:"source_server_id"`
	SourceOwnerRef   string           `json:"source_owner_ref"`
	SourceUsername   string           `json:"source_username"`
	TargetServerID   int64            `json:"target_server_id"`
	TargetOwnerRef   string           `json:"target_owner_ref"`
	TargetUsername   string           `json:"target_username"`
	Stage            string           `json:"stage"`
	QuotaBytes       int64            `json:"quota_bytes"`
	Enabled          bool             `json:"enabled"`
	InitialUsage     *VPNInitialUsage `json:"initial_usage,omitempty"`
	TargetDispatched bool             `json:"target_dispatched"`
	TargetRetired    bool             `json:"target_retired"`
}

func migrationOperationID(parent, step string) string {
	return uuid.NewSHA1(uuid.MustParse(parent), []byte(step)).String()
}

func (s *VPNService) saveMigration(ctx context.Context, o *VPNOperation, m *VPNMigration) error {
	if err := s.repo.CheckpointMigration(ctx, o, m); err != nil {
		return err
	}
	var payload VPNOperationPayload
	if err := json.Unmarshal(o.Payload, &payload); err != nil {
		return err
	}
	payload.Migration = m
	o.Payload, _ = json.Marshal(payload)
	return nil
}

func validVPNMigrationSnapshot(x *VPNSnapshot, owner, username, operationID string) bool {
	return x != nil && x.OwnerRef == owner && x.Username == username && x.LastOperationID == operationID &&
		x.DataLimit > 0 && x.DataLimit <= VPNMaxQuota && x.UsedTraffic >= 0 && x.UsedTraffic <= VPNMaxQuota &&
		x.UploadBytes >= 0 && x.DownloadBytes >= 0 && x.UploadBytes <= VPNMaxQuota && x.DownloadBytes <= VPNMaxQuota &&
		x.UploadBytes+x.DownloadBytes <= VPNMaxQuota
}

// 子操作保持固定编号；结果未知时只重放原请求，不能另选节点创建第二份账号。
func (s *VPNService) migrationRemote(ctx context.Context, o *VPNOperation, server *VPNServer, username string, payload VPNOperationPayload) (*VPNSnapshot, error) {
	cred, err := s.credentials(server)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(payload)
	if err := s.repo.MarkOperationDispatched(ctx, o); err != nil {
		return nil, err
	}
	op, err := s.remote.Submit(ctx, server, cred, username, body)
	if err != nil {
		return nil, err
	}
	if op.OperationID != payload.OperationID {
		return nil, fmt.Errorf("迁移子操作编号不匹配")
	}
	if op.Status != "succeeded" && op.Status != "failed" {
		op, err = s.remote.Operation(ctx, server, cred, payload.OperationID)
		if err != nil {
			return nil, err
		}
	}
	if op.OperationID != payload.OperationID || (op.Status != "pending" && op.Status != "succeeded" && op.Status != "failed") {
		return nil, fmt.Errorf("迁移子操作状态无效")
	}
	if op.Status != "succeeded" {
		return nil, fmt.Errorf("等待节点完成迁移子操作：%s", op.Status)
	}
	x, err := s.remote.User(ctx, server, cred, username)
	if err != nil {
		return nil, err
	}
	if !validVPNMigrationSnapshot(x, payload.OwnerRef, username, payload.OperationID) || x.ApplyStatus != "applied" {
		return nil, fmt.Errorf("迁移账户归属或实际配置尚未确认")
	}
	if payload.Action == "delete" && (x.Status != "deleted" || x.AccessState != "blocked") {
		return nil, fmt.Errorf("旧节点凭据尚未确认失效")
	}
	return x, nil
}

func maxVPNUsage(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func migrationUsage(x *VPNSnapshot, previous *VPNInitialUsage) (*VPNInitialUsage, error) {
	if x.PeriodStart == nil || x.PeriodEnd == nil || !x.PeriodStart.Before(*x.PeriodEnd) {
		return nil, fmt.Errorf("源节点缺少有效账期，不能迁移额度")
	}
	usage := &VPNInitialUsage{UploadBytes: x.UploadBytes, DownloadBytes: x.DownloadBytes, UsedTraffic: x.UsedTraffic,
		PeriodStart: *x.PeriodStart, PeriodEnd: *x.PeriodEnd, AccountingStatus: x.AccountingStatus}
	if previous != nil && usage.PeriodStart.Equal(previous.PeriodStart) && usage.PeriodEnd.Equal(previous.PeriodEnd) {
		usage.UploadBytes = maxVPNUsage(usage.UploadBytes, previous.UploadBytes)
		usage.DownloadBytes = maxVPNUsage(usage.DownloadBytes, previous.DownloadBytes)
		usage.UsedTraffic = maxVPNUsage(usage.UsedTraffic, previous.UsedTraffic)
		if previous.AccountingStatus == "gap_detected" {
			usage.AccountingStatus = previous.AccountingStatus
		}
	}
	usage.UsedTraffic = maxVPNUsage(usage.UsedTraffic, usage.UploadBytes+usage.DownloadBytes)
	if usage.UsedTraffic > VPNMaxQuota {
		return nil, ErrVPNInvalid
	}
	return usage, nil
}

func (s *VPNService) encryptVPNURL(server *VPNServer, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	base, baseErr := validateVPNURL(server.BaseURL)
	if err != nil || baseErr != nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/sub/") || strings.Trim(strings.TrimPrefix(u.Path, "/sub/"), "/") == "" {
		return "", fmt.Errorf("VPN节点返回的订阅地址无效")
	}
	return s.cipher.Encrypt("vpn-url:" + raw)
}

func (s *VPNService) processMigration(ctx context.Context, o *VPNOperation, m *VPNMigration) (string, string) {
	fail := func(err error) (string, string) { return "pending", err.Error() }
	if m == nil || m.SourceServerID == m.TargetServerID || m.SourceOwnerRef == m.TargetOwnerRef {
		return "failed", "VPN迁移内容无效"
	}
	if _, err := uuid.Parse(o.ID); err != nil {
		return "failed", "VPN迁移编号无效"
	}
	if m.Stage == "completed" {
		return "succeeded", ""
	}
	target, err := s.repo.GetServer(ctx, m.TargetServerID)
	if err != nil {
		return fail(err)
	}
	if !target.Enabled || m.TargetRetired {
		if m.ManualTarget && !target.Enabled && !m.TargetDispatched {
			return "pending", "所选目标节点已禁用，等待该节点恢复后继续"
		}
		if m.TargetDispatched && !m.TargetRetired {
			x, err := s.migrationRemote(ctx, o, target, m.TargetUsername, VPNOperationPayload{
				OperationID: migrationOperationID(o.ID, "retire-"+m.TargetOwnerRef), OwnerRef: m.TargetOwnerRef, Action: "delete"})
			if err != nil {
				return fail(err)
			}
			m.InitialUsage, err = migrationUsage(x, m.InitialUsage)
			if err != nil {
				return fail(err)
			}
			m.TargetRetired = true
			if err := s.saveMigration(ctx, o, m); err != nil {
				return fail(err)
			}
		}
		_, err = s.repo.RetargetMigration(ctx, o, m)
		if err != nil {
			return fail(err)
		}
		return "pending", "目标节点已停用，正在改派到可用节点"
	}
	cred, err := s.credentials(target)
	if err != nil {
		return fail(err)
	}
	meta, err := s.remote.Server(ctx, target, cred)
	if err != nil {
		return fail(err)
	}
	if !meta.Healthy || !slices.Contains(meta.Capabilities, "initial-usage-v1") {
		return "pending", "目标节点尚未就绪或不支持保留已用流量的迁移"
	}
	if m.Stage == "revoke_source" {
		source, err := s.repo.GetServer(ctx, m.SourceServerID)
		if err != nil {
			return fail(err)
		}
		x, err := s.migrationRemote(ctx, o, source, m.SourceUsername, VPNOperationPayload{
			OperationID: migrationOperationID(o.ID, "retire-source"), OwnerRef: m.SourceOwnerRef, Action: "delete"})
		if err != nil {
			return fail(err)
		}
		sub, err := s.repo.GetSubscription(ctx, o.SubscriptionID)
		if err != nil {
			return fail(err)
		}
		previous, _ := migrationUsage(&sub.Snapshot, nil)
		m.InitialUsage, err = migrationUsage(x, previous)
		if err != nil {
			return fail(err)
		}
		m.Stage = "create_target"
		if err := s.saveMigration(ctx, o, m); err != nil {
			return fail(err)
		}
		return "pending", "旧节点凭据已失效，正在续接本期额度"
	}
	if m.Stage != "create_target" || m.InitialUsage == nil {
		return "failed", "VPN迁移阶段无效"
	}
	if !m.TargetDispatched {
		m.TargetDispatched = true
		if err := s.saveMigration(ctx, o, m); err != nil {
			return fail(err)
		}
	}
	x, err := s.migrationRemote(ctx, o, target, m.TargetUsername, VPNOperationPayload{OperationID: o.ID,
		OwnerRef: m.TargetOwnerRef, Action: "create", DataLimit: &m.QuotaBytes, Enabled: &m.Enabled, InitialUsage: m.InitialUsage})
	if err != nil {
		return fail(err)
	}
	start, end := vpnPeriodBounds(time.Now())
	if x.DataLimit != m.QuotaBytes || x.PeriodStart == nil || x.PeriodEnd == nil || !x.PeriodStart.Equal(start) || !x.PeriodEnd.Equal(end) {
		return "pending", "目标节点额度或账期尚未确认一致"
	}
	if m.InitialUsage.PeriodStart.Equal(start) && (x.UsedTraffic < m.InitialUsage.UsedTraffic || x.UploadBytes < m.InitialUsage.UploadBytes || x.DownloadBytes < m.InitialUsage.DownloadBytes) {
		return "pending", "目标节点尚未完整保留本期已用流量"
	}
	if !m.Enabled && x.Status != "disabled" || m.Enabled && x.UsedTraffic >= x.DataLimit && x.Status != "limited" || m.Enabled && x.UsedTraffic < x.DataLimit && x.Status != "active" {
		return "pending", "目标节点启停或限额状态尚未确认一致"
	}
	if (x.Status == "active" && x.AccessState != "allowed") || (x.Status != "active" && x.AccessState != "blocked") {
		return "pending", "等待目标节点访问状态与额度一致"
	}
	encrypted, err := s.encryptVPNURL(target, x.SubscriptionURL)
	if err != nil {
		return fail(err)
	}
	if x.Status == "active" && encrypted == "" {
		return "pending", "目标节点尚未返回有效订阅地址"
	}
	if err := s.repo.CompleteMigration(ctx, o, m, *x, encrypted); err != nil {
		return fail(err)
	}
	return "succeeded", ""
}
