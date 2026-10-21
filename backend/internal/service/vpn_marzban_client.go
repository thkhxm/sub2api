package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type vpnToken struct {
	value   string
	expires time.Time
}
type MarzbanVPNClient struct {
	mu      sync.Mutex
	tokens  map[string]vpnToken
	flights map[string]chan struct{}
}

func NewMarzbanVPNClient() VPNRemote { return &MarzbanVPNClient{tokens: make(map[string]vpnToken)} }

func validateVPNURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrVPNInvalid
	}
	return u, nil
}
func vpnHTTPClient(c VPNCredentials) (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if c.CAPEM != "" && !roots.AppendCertsFromPEM([]byte(c.CAPEM)) {
		return nil, ErrVPNInvalid
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("VPN节点不允许重定向") }}, nil
}

var ErrVPNConnection = errors.New("VPN节点连接失败")

type vpnHTTPError struct{ Status int }

func (e *vpnHTTPError) Error() string { return fmt.Sprintf("VPN节点返回HTTP %d", e.Status) }
func vpnRequest(ctx context.Context, client *http.Client, method, endpoint, token, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrVPNInvalid
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ErrVPNConnection
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &vpnHTTPError{Status: resp.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(b) > 4*1024*1024 {
		return fmt.Errorf("VPN节点响应无效")
	}
	if out == nil {
		return nil
	}
	if err = json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("VPN节点JSON响应无效")
	}
	return nil
}
func (c *MarzbanVPNClient) call(ctx context.Context, s *VPNServer, cred VPNCredentials, method, path string, body []byte, out any) error {
	if _, err := validateVPNURL(s.BaseURL); err != nil {
		return err
	}
	client, err := vpnHTTPClient(cred)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	h := sha256.Sum256([]byte(s.BaseURL + "\x00" + s.AdminUsername + "\x00" + cred.Password))
	key := hex.EncodeToString(h[:])
	login := func(force bool) (string, error) {
		// 只合并同一节点的登录请求；网络等待不持有跨节点互斥锁。
		for {
			c.mu.Lock()
			if cached := c.tokens[key]; !force && cached.expires.After(time.Now()) {
				c.mu.Unlock()
				return cached.value, nil
			}
			if wait := c.flights[key]; wait != nil {
				c.mu.Unlock()
				select {
				case <-wait:
					force = false
					continue
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			if c.flights == nil {
				c.flights = make(map[string]chan struct{})
			}
			done := make(chan struct{})
			c.flights[key] = done
			c.mu.Unlock()
			var result struct {
				AccessToken string `json:"access_token"`
			}
			form := url.Values{"username": {s.AdminUsername}, "password": {cred.Password}}.Encode()
			err := vpnRequest(ctx, client, "POST", strings.TrimRight(s.BaseURL, "/")+"/api/admin/token", "", "application/x-www-form-urlencoded", []byte(form), &result)
			if err == nil && result.AccessToken == "" {
				err = fmt.Errorf("VPN节点未返回登录令牌")
			}
			c.mu.Lock()
			if err == nil {
				c.tokens[key] = vpnToken{result.AccessToken, time.Now().Add(30 * time.Minute)}
			}
			delete(c.flights, key)
			close(done)
			c.mu.Unlock()
			return result.AccessToken, err
		}
	}

	token, err := login(false)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(s.BaseURL, "/") + path
	err = vpnRequest(ctx, client, method, endpoint, token, "application/json", body, out)
	if e, ok := err.(*vpnHTTPError); ok && e.Status == 401 {
		token, err = login(true)
		if err != nil {
			return err
		}
		return vpnRequest(ctx, client, method, endpoint, token, "application/json", body, out)
	}
	return err
}
func (c *MarzbanVPNClient) Server(ctx context.Context, s *VPNServer, k VPNCredentials) (*VPNServerMeta, error) {
	var r VPNServerMeta
	err := c.call(ctx, s, k, "GET", "/api/integration/server", nil, &r)
	return &r, err
}
func (c *MarzbanVPNClient) Submit(ctx context.Context, s *VPNServer, k VPNCredentials, name string, p json.RawMessage) (*VPNRemoteOperation, error) {
	var r VPNRemoteOperation
	err := c.call(ctx, s, k, "POST", "/api/integration/users/"+url.PathEscape(name)+"/operations", p, &r)
	return &r, err
}
func (c *MarzbanVPNClient) Operation(ctx context.Context, s *VPNServer, k VPNCredentials, id string) (*VPNRemoteOperation, error) {
	var r VPNRemoteOperation
	err := c.call(ctx, s, k, "GET", "/api/integration/operations/"+url.PathEscape(id), nil, &r)
	return &r, err
}
func (c *MarzbanVPNClient) User(ctx context.Context, s *VPNServer, k VPNCredentials, name string) (*VPNSnapshot, error) {
	var r VPNSnapshot
	err := c.call(ctx, s, k, "GET", "/api/integration/users/"+url.PathEscape(name), nil, &r)
	return &r, err
}

func (c *MarzbanVPNClient) Traffic(ctx context.Context, s *VPNServer, k VPNCredentials, f VPNTrafficFilter, refs []string) (*VPNRemoteTraffic, error) {
	query := url.Values{"start_date": {f.StartDate}, "end_date": {f.EndDate}}
	if len(refs) > 0 {
		query.Set("owner_refs", strings.Join(refs, ","))
	}
	var result VPNRemoteTraffic
	err := c.call(ctx, s, k, "GET", "/api/integration/traffic/daily?"+query.Encode(), nil, &result)
	return &result, err
}
