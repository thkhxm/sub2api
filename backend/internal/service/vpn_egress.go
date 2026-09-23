package service

import (
	"context"
	"fmt"
	"strings"
)

type VPNEgress struct {
	Username         string            `json:"username"`
	Status           string            `json:"status"`
	ApplyStatus      string            `json:"apply_status"`
	AccessState      string            `json:"access_state"`
	Unlimited        bool              `json:"unlimited"`
	SubscriptionURL  string            `json:"-"`
	SubscriptionURLs map[string]string `json:"subscription_urls"`
}

type vpnEgressRemote interface {
	Egress(context.Context, *VPNServer, VPNCredentials, bool) (*VPNEgress, error)
}

func (s *VPNService) Egress(ctx context.Context, serverID int64, ensure bool) (*VPNEgress, error) {
	server, err := s.repo.GetServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	cred, err := s.credentials(server)
	if err != nil {
		return nil, err
	}
	remote, ok := s.remote.(vpnEgressRemote)
	if !ok {
		return nil, fmt.Errorf("节点尚未支持运维订阅")
	}
	result, err := remote.Egress(ctx, server, cred, ensure)
	if err != nil {
		return nil, err
	}
	if result.Username != "sub2api" || !result.Unlimited {
		return nil, fmt.Errorf("节点返回的运维账号类型无效")
	}
	result.SubscriptionURLs = nil
	if result.Status == "active" && result.ApplyStatus == "applied" && result.AccessState == "allowed" {
		if result.SubscriptionURL == "" {
			return nil, fmt.Errorf("节点未返回有效运维订阅")
		}
		if _, err := s.encryptVPNURL(server, result.SubscriptionURL); err != nil {
			return nil, err
		}
		base := strings.TrimRight(result.SubscriptionURL, "/")
		result.SubscriptionURLs = map[string]string{"clash": base + "/clash-meta", "base64": base + "/v2ray", "singbox": base + "/sing-box"}
	}
	return result, nil
}

func (c *MarzbanVPNClient) Egress(ctx context.Context, server *VPNServer, cred VPNCredentials, ensure bool) (*VPNEgress, error) {
	var result struct {
		Username        string `json:"username"`
		Status          string `json:"status"`
		ApplyStatus     string `json:"apply_status"`
		AccessState     string `json:"access_state"`
		Unlimited       bool   `json:"unlimited"`
		SubscriptionURL string `json:"subscription_url"`
	}
	method := "GET"
	if ensure {
		method = "POST"
	}
	if err := c.call(ctx, server, cred, method, "/api/integration/egress", nil, &result); err != nil {
		return nil, err
	}
	return &VPNEgress{Username: result.Username, Status: result.Status, ApplyStatus: result.ApplyStatus,
		AccessState: result.AccessState, Unlimited: result.Unlimited, SubscriptionURL: result.SubscriptionURL}, nil
}
