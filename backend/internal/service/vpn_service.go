package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type VPNService struct {
	repo   VPNRepository
	cipher SecretEncryptor
	remote VPNRemote
	cancel context.CancelFunc
	wg     sync.WaitGroup
	start  sync.Once
}

func NewVPNService(r VPNRepository, c SecretEncryptor, remote VPNRemote) *VPNService {
	return &VPNService{repo: r, cipher: c, remote: remote}
}
func ProvideVPNService(r VPNRepository, c SecretEncryptor, remote VPNRemote) *VPNService {
	s := NewVPNService(r, c, remote)
	s.Start()
	return s
}
func (s *VPNService) Start() {
	s.start.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.Tick(ctx)
				}
			}
		}()
	})
}
func (s *VPNService) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.wg.Wait()
	}
}
func (s *VPNService) credentials(v *VPNServer) (VPNCredentials, error) {
	var k VPNCredentials
	raw, err := s.cipher.Decrypt(v.CredentialsEncrypted)
	if err != nil || !strings.HasPrefix(raw, "vpn-credentials:") {
		return k, fmt.Errorf("VPN凭据解密失败")
	}
	err = json.Unmarshal([]byte(strings.TrimPrefix(raw, "vpn-credentials:")), &k)
	return k, err
}
func (s *VPNService) SaveServer(ctx context.Context, id int64, in VPNServerInput) (*VPNServer, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.AdminUsername = strings.TrimSpace(in.AdminUsername)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if len(in.Name) == 0 || len(in.Name) > 100 || len(in.AdminUsername) == 0 || len(in.AdminUsername) > 128 || len(in.BaseURL) > 512 || len(in.AdminPassword) > 4096 || (in.CAPEM != nil && len(*in.CAPEM) > 65536) {
		return nil, ErrVPNInvalid
	}
	if _, err := validateVPNURL(in.BaseURL); err != nil {
		return nil, err
	}
	k := VPNCredentials{}
	if id > 0 {
		old, err := s.repo.GetServer(ctx, id)
		if err != nil {
			return nil, err
		}
		k, err = s.credentials(old)
		if err != nil {
			return nil, err
		}
		if old.AdminUsername != in.AdminUsername && in.AdminPassword == "" {
			return nil, ErrVPNInvalid
		}
	}
	if in.AdminPassword != "" {
		k.Password = in.AdminPassword
	}
	if in.CAPEM != nil {
		k.CAPEM = *in.CAPEM
	}
	if k.Password == "" {
		return nil, ErrVPNInvalid
	}
	client, err := vpnHTTPClient(k)
	if err != nil {
		return nil, err
	}
	client.CloseIdleConnections()
	raw, _ := json.Marshal(k)
	encrypted, err := s.cipher.Encrypt("vpn-credentials:" + string(raw))
	if err != nil {
		return nil, err
	}
	v, err := s.repo.SaveServer(ctx, &VPNServer{ID: id, Name: in.Name, BaseURL: in.BaseURL, AdminUsername: in.AdminUsername, CredentialsEncrypted: encrypted, Enabled: in.Enabled})
	if err != nil {
		return nil, err
	}
	return s.Probe(ctx, v.ID)
}
func (s *VPNService) Servers(ctx context.Context) ([]VPNServer, error) {
	return s.repo.ListServers(ctx)
}
func (s *VPNService) Probe(ctx context.Context, id int64) (*VPNServer, error) {
	v, err := s.repo.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	k, err := s.credentials(v)
	var meta *VPNServerMeta
	if err == nil {
		meta, err = s.remote.Server(ctx, v, k)
	}
	message := ""
	if err != nil {
		message = err.Error()
		meta = nil
	} else if meta.APIVersion != "sub2api-v1" || meta.Protocol != "trojan" || meta.InboundTag != "TROJAN_TLS" || meta.PersonalUserCount < 0 {
		meta = nil
		message = "节点不支持当前VPN集成协议"
	} else if !meta.Healthy || meta.AccountingStatus != "ok" || meta.SampledAt == nil || time.Since(*meta.SampledAt) > 2*time.Minute {
		meta.Healthy = false
		message = "节点核心或流量采样暂不可用"
	}
	if err = s.repo.UpdateServerHealth(ctx, id, meta, message); err != nil {
		return nil, err
	}
	return s.repo.GetServer(ctx, id)
}
func (s *VPNService) hydrate(sub *VPNSubscription) error {
	x := sub.Snapshot
	sub.UploadBytes = x.UploadBytes
	sub.DownloadBytes = x.DownloadBytes
	sub.UsedBytes = x.UsedTraffic
	if x.DataLimit > 0 {
		sub.QuotaBytes = x.DataLimit
	}
	sub.RemainingBytes = sub.QuotaBytes - sub.UsedBytes
	if sub.RemainingBytes < 0 {
		sub.RemainingBytes = 0
	}
	sub.PeriodStart = x.PeriodStart
	sub.PeriodEnd = x.PeriodEnd
	sub.NextResetAt = x.NextResetAt
	sub.SampledAt = x.SampledAt
	sub.AccountingStatus = x.AccountingStatus
	if sub.AccountingStatus == "" || sub.SampledAt == nil {
		sub.AccountingStatus = "stale"
	} else if sub.AccountingStatus == "ok" && (time.Since(*sub.SampledAt) > 30*time.Second || sub.SyncedAt == nil || time.Since(*sub.SyncedAt) > time.Minute) {
		sub.AccountingStatus = "stale"
	}
	if sub.Status == "active" && sub.ApplyStatus == "applied" && sub.AccessState == "allowed" && sub.URLEncrypted != "" {
		raw, err := s.cipher.Decrypt(sub.URLEncrypted)
		if err != nil || !strings.HasPrefix(raw, "vpn-url:") {
			return fmt.Errorf("VPN订阅链接解密失败")
		}
		base := strings.TrimRight(strings.TrimPrefix(raw, "vpn-url:"), "/")
		sub.SubscriptionURLs = map[string]string{"clash": base + "/clash-meta", "base64": base + "/v2ray"}
	}
	return nil
}
func (s *VPNService) Get(ctx context.Context, id int64) (*VPNSubscription, error) {
	v, err := s.repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	return v, s.hydrate(v)
}
func (s *VPNService) Mine(ctx context.Context, userID int64) (*VPNMyResponse, error) {
	r := &VPNMyResponse{DefaultQuotaBytes: VPNDefaultQuota}
	v, err := s.repo.GetUserSubscription(ctx, userID)
	if err == nil {
		r.Subscription = v
		r.IneligibleReason = "already_exists"
		return r, s.hydrate(v)
	}
	if !errors.Is(err, ErrVPNNotFound) {
		return nil, err
	}
	r.CanCreate, r.IneligibleReason, err = s.repo.Eligibility(ctx, userID)
	return r, err
}
func (s *VPNService) Create(ctx context.Context, userID int64, admin bool, actor int64) (*VPNSubscription, error) {
	v, err := s.repo.Reserve(ctx, userID, admin, actor, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return v, s.hydrate(v)
}
func (s *VPNService) Update(ctx context.Context, id, actor int64, in VPNUpdate) (*VPNSubscription, error) {
	if in.QuotaBytes == nil && in.Enabled == nil {
		return nil, ErrVPNInvalid
	}
	if in.QuotaBytes != nil && (*in.QuotaBytes <= 0 || *in.QuotaBytes > VPNMaxQuota) {
		return nil, ErrVPNInvalid
	}
	err := s.repo.Queue(ctx, id, actor, VPNOperationPayload{Action: "update", DataLimit: in.QuotaBytes, Enabled: in.Enabled})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}
func (s *VPNService) Revoke(ctx context.Context, id, actor int64) (*VPNSubscription, error) {
	if err := s.repo.Queue(ctx, id, actor, VPNOperationPayload{Action: "revoke"}); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}
func (s *VPNService) Retry(ctx context.Context, id int64) (*VPNSubscription, error) {
	if err := s.repo.Retry(ctx, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}
func (s *VPNService) List(ctx context.Context, f VPNFilter) (*VPNListResponse, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 100 {
		f.PageSize = 20
	}
	if len(f.Query) > 128 {
		return nil, ErrVPNInvalid
	}
	list, total, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err = s.hydrate(&list[i]); err != nil {
			return nil, err
		}
	}
	return &VPNListResponse{Items: list, Total: total, Page: f.Page, PageSize: f.PageSize}, nil
}
func (s *VPNService) Summary(ctx context.Context) (*VPNSummary, error) { return s.repo.Summary(ctx) }
func (s *VPNService) Refresh(ctx context.Context, id int64) (*VPNSubscription, error) {
	allowed, err := s.repo.RequestRefresh(ctx, id)
	if err != nil {
		return nil, err
	}
	if allowed {
		if err = s.sync(ctx, id); err != nil {
			_ = s.repo.MarkSyncError(ctx, id, err.Error())
		}
	}
	return s.Get(ctx, id)
}
func (s *VPNService) sync(ctx context.Context, id int64) error {
	sub, err := s.repo.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	server, err := s.repo.GetServer(ctx, sub.ServerID)
	if err != nil {
		return err
	}
	cred, err := s.credentials(server)
	if err != nil {
		return err
	}
	x, err := s.remote.User(ctx, server, cred, sub.RemoteUsername)
	if err != nil {
		return err
	}
	if x.OwnerRef != sub.OwnerRef || x.Username != sub.RemoteUsername || x.DataLimit <= 0 || x.DataLimit > VPNMaxQuota || x.UploadBytes < 0 || x.DownloadBytes < 0 || x.UsedTraffic < 0 {
		return fmt.Errorf("VPN节点返回的账户归属或流量数据无效")
	}
	encrypted := ""
	if x.SubscriptionURL != "" {
		u, e := url.Parse(x.SubscriptionURL)
		base, _ := validateVPNURL(server.BaseURL)
		if e != nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/sub/") {
			return fmt.Errorf("VPN节点返回的订阅地址无效")
		}
		encrypted, err = s.cipher.Encrypt("vpn-url:" + x.SubscriptionURL)
		if err != nil {
			return err
		}
	}
	return s.repo.SaveSnapshot(ctx, id, *x, encrypted)
}
func (s *VPNService) process(ctx context.Context, o *VPNOperation) {
	state, message := "pending", ""
	defer func() {
		if err := s.repo.Finish(ctx, o, state, message); err != nil && ctx.Err() == nil {
			slog.Warn("VPN操作状态持久化失败", "operation_id", o.ID)
		}
	}()
	sub, err := s.repo.GetSubscription(ctx, o.SubscriptionID)
	if err != nil {
		message = "订阅记录读取失败"
		return
	}
	server, err := s.repo.GetServer(ctx, sub.ServerID)
	if err != nil {
		message = "节点记录读取失败"
		return
	}
	cred, err := s.credentials(server)
	if err != nil {
		message = err.Error()
		state = "failed"
		return
	}
	result, err := s.remote.Submit(ctx, server, cred, sub.RemoteUsername, o.Payload)
	if err != nil {
		message = err.Error()
		if e, ok := err.(*vpnHTTPError); ok && e.Status >= 400 && e.Status < 500 && e.Status != 429 {
			state = "failed"
		}
		return
	}
	if result.OperationID != o.ID {
		state = "failed"
		message = "VPN操作编号不匹配"
		return
	}
	if result.Status != "succeeded" && result.Status != "failed" {
		result, err = s.remote.Operation(ctx, server, cred, o.ID)
		if err != nil {
			message = err.Error()
			return
		}
	}
	if result.OperationID != o.ID || (result.Status != "pending" && result.Status != "succeeded" && result.Status != "failed") {
		state, message = "failed", "VPN操作查询返回了不匹配的编号或状态"
		return
	}
	if result.Status == "failed" {
		state = "failed"
		message = "节点配置应用失败，可在修复节点后重试原操作"
		_ = s.sync(ctx, sub.ID)
		return
	}
	if err = s.sync(ctx, sub.ID); err != nil {
		message = err.Error()
		return
	}
	if result.Status == "succeeded" {
		fresh, e := s.repo.GetSubscription(ctx, sub.ID)
		if e != nil {
			message = "读取同步结果失败"
			return
		}
		if fresh.Snapshot.LastOperationID == o.ID && fresh.ApplyStatus == "applied" {
			state = "succeeded"
		} else {
			message = "等待节点实际配置生效"
		}
	}
}

// Tick 将远端操作与用量同步从页面生命周期中解耦。租约保证多实例不会同时投递同一操作。
func (s *VPNService) Tick(ctx context.Context) {
	servers, err := s.repo.ListServers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("VPN节点列表读取失败")
		}
		return
	}
	for _, v := range servers {
		if ctx.Err() != nil {
			return
		}
		if v.Enabled && (v.LastCheckedAt == nil || time.Since(*v.LastCheckedAt) > 30*time.Second) {
			_, _ = s.Probe(ctx, v.ID)
		}
	}
	for i := 0; i < 10; i++ {
		if ctx.Err() != nil {
			return
		}
		op, e := s.repo.Claim(ctx)
		if e != nil || op == nil {
			break
		}
		s.process(ctx, op)
	}
	ids, _ := s.repo.InactiveUserSubscriptions(ctx)
	for _, id := range ids {
		off := false
		_, _ = s.Update(ctx, id, 0, VPNUpdate{Enabled: &off})
	}
	ids, _ = s.repo.SyncCandidates(ctx)
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err = s.sync(ctx, id); err != nil {
			_ = s.repo.MarkSyncError(ctx, id, err.Error())
		}
	}
}
