package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type VPNCredentialRevocation struct {
	ID, LeaseToken           string
	SubscriptionID, ServerID int64
	OwnerRef, Username       string
}
type vpnRecoveryRepository interface {
	ClaimRevocation(context.Context) (*VPNCredentialRevocation, error)
	FinishRevocation(context.Context, *VPNCredentialRevocation, string) error
}

func VPNSourceRevocationID(parent string) string {
	return migrationOperationID(parent, "retire-source")
}

func (s *VPNService) revokeOfflineSource(ctx context.Context, item *VPNCredentialRevocation) error {
	server, err := s.repo.GetServer(ctx, item.ServerID)
	if err != nil {
		return err
	}
	cred, err := s.credentials(server)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(VPNOperationPayload{OperationID: item.ID, OwnerRef: item.OwnerRef, Action: "delete"})
	result, err := s.remote.Submit(ctx, server, cred, item.Username, body)
	if err != nil {
		return err
	}
	if result.OperationID != item.ID {
		return fmt.Errorf("旧凭据回收操作编号不匹配")
	}
	if result.Status != "succeeded" {
		result, err = s.remote.Operation(ctx, server, cred, item.ID)
		if err != nil {
			return err
		}
	}
	if result.OperationID != item.ID || result.Status != "succeeded" {
		return fmt.Errorf("等待旧节点完成凭据撤销")
	}
	snapshot, err := s.remote.User(ctx, server, cred, item.Username)
	if err != nil {
		return err
	}
	if snapshot.OwnerRef != item.OwnerRef || snapshot.Username != item.Username || snapshot.LastOperationID != item.ID || snapshot.Status != "deleted" || snapshot.ApplyStatus != "applied" || snapshot.AccessState != "blocked" {
		return fmt.Errorf("旧凭据尚未确认失效")
	}
	return nil
}

func (s *VPNService) processRevocations(ctx context.Context) {
	repo, ok := s.repo.(vpnRecoveryRepository)
	if !ok {
		return
	}
	// 清理队列独立重试，不改写当前订阅状态和新节点用量。
	for i := 0; i < 4 && ctx.Err() == nil; i++ {
		item, err := repo.ClaimRevocation(ctx)
		if err != nil || item == nil {
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err = s.revokeOfflineSource(callCtx, item)
		cancel()
		message := ""
		if err != nil {
			message = err.Error()
		}
		saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = repo.FinishRevocation(saveCtx, item, message)
		saveCancel()
	}
}
