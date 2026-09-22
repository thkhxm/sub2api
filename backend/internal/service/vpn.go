package service

import (
	"context"
	"encoding/json"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const VPNDefaultQuota int64 = 80 * 1024 * 1024 * 1024
const VPNMaxQuota int64 = 9007199254740991

var (
	ErrVPNNotFound = infraerrors.NotFound("VPN_NOT_FOUND", "VPN订阅或节点不存在")
	ErrVPNNoServer = infraerrors.ServiceUnavailable("VPN_NO_SERVER", "暂无可用VPN节点")
	ErrVPNBalance  = infraerrors.Forbidden("VPN_BALANCE_REQUIRED", "可用余额必须大于0才能自助开通")
	ErrVPNBusy     = infraerrors.Conflict("VPN_BUSY", "订阅仍有未完成操作，请等待或重试原操作")
	ErrVPNInvalid  = infraerrors.BadRequest("VPN_INVALID", "VPN配置或请求参数无效")
)

type VPNServer struct {
	ID                       int64           `json:"id"`
	Name                     string          `json:"name"`
	BaseURL                  string          `json:"base_url"`
	AdminUsername            string          `json:"admin_username"`
	CredentialsEncrypted     string          `json:"-"`
	Enabled                  bool            `json:"enabled"`
	Healthy                  bool            `json:"healthy"`
	HealthError              string          `json:"health_error"`
	PersonalUserCount        int             `json:"personal_user_count"`
	KnownOwnerRefs           []string        `json:"-"`
	AssignedCount            int             `json:"assigned_count"`
	PendingCount             int             `json:"pending_count"`
	LastCheckedAt            *time.Time      `json:"last_checked_at"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"-"`
	TrafficQuotaBytes        int64           `json:"traffic_quota_bytes"`
	TrafficUsedOffsetBytes   int64           `json:"traffic_used_offset_bytes"`
	TrafficOffsetPeriodStart *time.Time      `json:"traffic_offset_period_start"`
	TrafficSnapshot          *VPNNodeTraffic `json:"-"`
	TrafficUsedBytes         *int64          `json:"traffic_used_bytes"`
	TrafficRemainingBytes    *int64          `json:"traffic_remaining_bytes"`
	TrafficPeriodStart       *time.Time      `json:"traffic_period_start"`
	TrafficPeriodEnd         *time.Time      `json:"traffic_period_end"`
	TrafficSampledAt         *time.Time      `json:"traffic_sampled_at"`
	TrafficAvailableFrom     *time.Time      `json:"traffic_available_from"`
	TrafficAccountingStatus  string          `json:"traffic_accounting_status"`
}
type VPNServerInput struct {
	Name                   string  `json:"name"`
	BaseURL                string  `json:"base_url"`
	AdminUsername          string  `json:"admin_username"`
	AdminPassword          string  `json:"admin_password"`
	CAPEM                  *string `json:"ca_pem"`
	Enabled                bool    `json:"enabled"`
	TrafficQuotaBytes      *int64  `json:"traffic_quota_bytes"`
	TrafficUsedOffsetBytes *int64  `json:"traffic_used_offset_bytes"`
}
type VPNCredentials struct {
	Password string `json:"password"`
	CAPEM    string `json:"ca_pem"`
}
type VPNServerMeta struct {
	APIVersion        string          `json:"api_version"`
	Healthy           bool            `json:"healthy"`
	PersonalUserCount int             `json:"personal_user_count"`
	ManagedOwnerRefs  []string        `json:"managed_owner_refs"`
	AccountingStatus  string          `json:"accounting_status"`
	SampledAt         *time.Time      `json:"sampled_at"`
	Protocol          string          `json:"protocol"`
	InboundTag        string          `json:"inbound_tag"`
	Traffic           *VPNNodeTraffic `json:"traffic,omitempty"`
}

type VPNNodeTraffic struct {
	UsedBytes        *int64     `json:"used_bytes"`
	PeriodStart      *time.Time `json:"period_start"`
	PeriodEnd        *time.Time `json:"period_end"`
	SampledAt        *time.Time `json:"sampled_at"`
	AvailableFrom    *time.Time `json:"available_from"`
	AccountingStatus string     `json:"accounting_status"`
}
type VPNTrafficDay struct {
	Date      string `json:"date"`
	UsedBytes *int64 `json:"used_bytes"`
}
type VPNRemoteTraffic struct {
	Timezone         string          `json:"timezone"`
	StartDate        string          `json:"start_date"`
	EndDate          string          `json:"end_date"`
	Days             []VPNTrafficDay `json:"days"`
	TotalBytes       int64           `json:"total_bytes"`
	AvailableFrom    *time.Time      `json:"available_from"`
	SampledAt        *time.Time      `json:"sampled_at"`
	AccountingStatus string          `json:"accounting_status"`
}
type VPNTrafficFilter struct {
	StartDate, EndDate string
	ServerID, UserID   int64
}
type VPNTrafficResponse struct {
	Timezone           string          `json:"timezone"`
	StartDate          string          `json:"start_date"`
	EndDate            string          `json:"end_date"`
	Days               []VPNTrafficDay `json:"days"`
	TotalBytes         int64           `json:"total_bytes"`
	AvailableFrom      *time.Time      `json:"available_from"`
	SyncedAt           *time.Time      `json:"synced_at"`
	Partial            bool            `json:"partial"`
	UnavailableServers int             `json:"unavailable_servers"`
}
type VPNSnapshot struct {
	Username         string     `json:"username"`
	OwnerRef         string     `json:"owner_ref"`
	Status           string     `json:"status"`
	ApplyStatus      string     `json:"apply_status"`
	AccessState      string     `json:"access_state"`
	SubscriptionURL  string     `json:"subscription_url,omitempty"`
	UploadBytes      int64      `json:"upload_bytes"`
	DownloadBytes    int64      `json:"download_bytes"`
	UsedTraffic      int64      `json:"used_traffic"`
	DataLimit        int64      `json:"data_limit"`
	PeriodStart      *time.Time `json:"period_start"`
	PeriodEnd        *time.Time `json:"period_end"`
	NextResetAt      *time.Time `json:"next_reset_at"`
	SampledAt        *time.Time `json:"sampled_at"`
	AccountingStatus string     `json:"accounting_status"`
	LastOperationID  string     `json:"last_operation_id"`
	LastError        *string    `json:"last_error"`
}
type VPNSubscription struct {
	ID                int64             `json:"id"`
	UserID            int64             `json:"user_id"`
	UserEmail         string            `json:"user_email"`
	ServerID          int64             `json:"server_id"`
	ServerName        string            `json:"server_name"`
	OwnerRef          string            `json:"-"`
	RemoteUsername    string            `json:"remote_username"`
	Enabled           bool              `json:"-"`
	QuotaBytes        int64             `json:"quota_bytes"`
	Status            string            `json:"status"`
	ApplyStatus       string            `json:"apply_status"`
	AccessState       string            `json:"access_state"`
	Snapshot          VPNSnapshot       `json:"-"`
	URLEncrypted      string            `json:"-"`
	UploadBytes       int64             `json:"upload_bytes"`
	DownloadBytes     int64             `json:"download_bytes"`
	UsedBytes         int64             `json:"used_bytes"`
	RemainingBytes    int64             `json:"remaining_bytes"`
	PeriodStart       *time.Time        `json:"period_start"`
	PeriodEnd         *time.Time        `json:"period_end"`
	NextResetAt       *time.Time        `json:"next_reset_at"`
	SampledAt         *time.Time        `json:"sampled_at"`
	SyncedAt          *time.Time        `json:"synced_at"`
	AccountingStatus  string            `json:"accounting_status"`
	LastError         string            `json:"last_error"`
	OperationStatus   string            `json:"operation_status"`
	CreatedAt         time.Time         `json:"created_at"`
	SubscriptionURLs  map[string]string `json:"subscription_urls"`
	DeleteRequestedAt *time.Time        `json:"delete_requested_at"`
	DeletedAt         *time.Time        `json:"deleted_at"`
	GroupID           int64             `json:"group_id"`
	GroupName         string            `json:"group_name"`
	GroupQuotaBytes   int64             `json:"group_quota_bytes"`
	QuotaSyncNeeded   bool              `json:"quota_sync_needed"`
}
type VPNOperationPayload struct {
	OperationID string `json:"operation_id"`
	OwnerRef    string `json:"owner_ref"`
	Action      string `json:"action"`
	DataLimit   *int64 `json:"data_limit,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}
type VPNOperation struct {
	ID             string
	SubscriptionID int64
	Payload        json.RawMessage
	LeaseToken     string
	Attempts       int
}
type VPNRemoteOperation struct {
	OperationID string  `json:"operation_id"`
	Status      string  `json:"status"`
	Error       *string `json:"error"`
}
type VPNUpdate struct {
	QuotaBytes *int64 `json:"quota_bytes"`
	Enabled    *bool  `json:"enabled"`
}
type VPNFilter struct {
	Page, PageSize int
	Query, Status  string
	ServerID       int64
}
type VPNMyResponse struct {
	Subscription      *VPNSubscription `json:"subscription"`
	CanCreate         bool             `json:"can_create"`
	IneligibleReason  string           `json:"ineligible_reason"`
	DefaultQuotaBytes int64            `json:"default_quota_bytes"`
}
type VPNListResponse struct {
	Items    []VPNSubscription `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}
type VPNSummary struct {
	Total          int   `json:"total"`
	Active         int   `json:"active"`
	Disabled       int   `json:"disabled"`
	Limited        int   `json:"limited"`
	Pending        int   `json:"pending"`
	Failed         int   `json:"failed"`
	UsedBytes      int64 `json:"used_bytes"`
	QuotaBytes     int64 `json:"quota_bytes"`
	HealthyServers int   `json:"healthy_servers"`
	TotalServers   int   `json:"total_servers"`
}

type VPNRepository interface {
	ListServers(context.Context) ([]VPNServer, error)
	GetServer(context.Context, int64) (*VPNServer, error)
	SaveServer(context.Context, *VPNServer) (*VPNServer, error)
	UpdateServerHealth(context.Context, int64, *VPNServerMeta, string) error
	GetSubscription(context.Context, int64) (*VPNSubscription, error)
	GetUserSubscription(context.Context, int64) (*VPNSubscription, error)
	Eligibility(context.Context, int64) (bool, string, error)
	Reserve(context.Context, int64, bool, int64, string) (*VPNSubscription, error)
	Queue(context.Context, int64, int64, VPNOperationPayload) error
	Retry(context.Context, int64) error
	Claim(context.Context) (*VPNOperation, error)
	Finish(context.Context, *VPNOperation, string, string) error
	SaveSnapshot(context.Context, int64, VPNSnapshot, string) error
	MarkSyncError(context.Context, int64, string) error
	List(context.Context, VPNFilter) ([]VPNSubscription, int, error)
	Summary(context.Context) (*VPNSummary, error)
	SyncCandidates(context.Context) ([]int64, error)
	InactiveUserSubscriptions(context.Context) ([]int64, error)
	RequestRefresh(context.Context, int64) (bool, error)
	UserOwnerRefs(context.Context, int64) (map[int64][]string, error)
	ListVPNGroups(context.Context) ([]VPNGroup, error)
	SaveVPNGroup(context.Context, int64, VPNGroupInput) (*VPNGroup, error)
	UserVPNGroup(context.Context, int64) (*VPNUserGroup, error)
	SetUserVPNGroup(context.Context, int64, int64) (*VPNUserGroup, error)
	QuotaSyncCandidates(context.Context) ([]int64, error)
	QueueQuotaSync(context.Context, int64) error
}

type VPNGroup struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	QuotaBytes        int64  `json:"quota_bytes"`
	IsDefault         bool   `json:"is_default"`
	MemberCount       int    `json:"member_count"`
	SubscriptionCount int    `json:"subscription_count"`
	PendingCount      int    `json:"pending_count"`
	FailedCount       int    `json:"failed_count"`
}
type VPNGroupInput struct {
	Name       *string `json:"name"`
	QuotaBytes *int64  `json:"quota_bytes"`
}
type VPNUserGroup struct {
	UserID     int64  `json:"user_id"`
	GroupID    int64  `json:"group_id"`
	GroupName  string `json:"group_name"`
	QuotaBytes int64  `json:"quota_bytes"`
}

type VPNRemote interface {
	Server(context.Context, *VPNServer, VPNCredentials) (*VPNServerMeta, error)
	Submit(context.Context, *VPNServer, VPNCredentials, string, json.RawMessage) (*VPNRemoteOperation, error)
	Operation(context.Context, *VPNServer, VPNCredentials, string) (*VPNRemoteOperation, error)
	User(context.Context, *VPNServer, VPNCredentials, string) (*VPNSnapshot, error)
	Traffic(context.Context, *VPNServer, VPNCredentials, VPNTrafficFilter, []string) (*VPNRemoteTraffic, error)
}
