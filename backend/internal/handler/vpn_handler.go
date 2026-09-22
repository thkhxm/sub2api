package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type VPNHandler struct{ svc *service.VPNService }

func NewVPNHandler(s *service.VPNService) *VPNHandler { return &VPNHandler{svc: s} }
func vpnReply(c *gin.Context, value any, err error) {
	c.Header("Cache-Control", "no-store")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func vpnAccepted(c *gin.Context, value any, err error) {
	c.Header("Cache-Control", "no-store")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, value)
}
func vpnID(c *gin.Context) int64 {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil || id <= 0 {
		response.ErrorFrom(c, service.ErrVPNInvalid)
		return 0
	}
	return id
}
func vpnActor(c *gin.Context) int64 {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return subject.UserID
}
func (h *VPNHandler) Mine(c *gin.Context) {
	id := vpnActor(c)
	if id == 0 {
		response.Unauthorized(c, "请先登录")
		return
	}
	v, e := h.svc.Mine(c.Request.Context(), id)
	vpnReply(c, v, e)
}
func (h *VPNHandler) CreateMine(c *gin.Context) {
	id := vpnActor(c)
	if id == 0 {
		response.Unauthorized(c, "请先登录")
		return
	}
	v, e := h.svc.Create(c.Request.Context(), id, false, id)
	vpnAccepted(c, v, e)
}
func (h *VPNHandler) RefreshMine(c *gin.Context) {
	id := vpnActor(c)
	if id == 0 {
		response.Unauthorized(c, "请先登录")
		return
	}
	mine, e := h.svc.Mine(c.Request.Context(), id)
	if e == nil && mine.Subscription != nil {
		_, e = h.svc.Refresh(c.Request.Context(), mine.Subscription.ID)
	}
	if e != nil {
		vpnReply(c, nil, e)
		return
	}
	v, e := h.svc.Mine(c.Request.Context(), id)
	vpnReply(c, v, e)
}
func (h *VPNHandler) Servers(c *gin.Context) {
	v, e := h.svc.Servers(c.Request.Context())
	vpnReply(c, v, e)
}
func (h *VPNHandler) SaveServer(c *gin.Context) {
	var in service.VPNServerInput
	if e := c.ShouldBindJSON(&in); e != nil {
		vpnReply(c, nil, service.ErrVPNInvalid)
		return
	}
	var id int64
	if c.Param("id") != "" {
		id = vpnID(c)
		if id == 0 {
			return
		}
	}
	v, e := h.svc.SaveServer(c.Request.Context(), id, in)
	vpnReply(c, v, e)
}
func (h *VPNHandler) Probe(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	v, e := h.svc.Probe(c.Request.Context(), id)
	vpnReply(c, v, e)
}
func (h *VPNHandler) AdminList(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("page_size"))
	server, _ := strconv.ParseInt(c.Query("server_id"), 10, 64)
	v, e := h.svc.List(c.Request.Context(), service.VPNFilter{Page: page, PageSize: size, Query: c.Query("q"), Status: c.Query("status"), ServerID: server})
	vpnReply(c, v, e)
}
func (h *VPNHandler) AdminCreate(c *gin.Context) {
	var in struct {
		UserID int64 `json:"user_id"`
	}
	if c.ShouldBindJSON(&in) != nil || in.UserID <= 0 {
		vpnReply(c, nil, service.ErrVPNInvalid)
		return
	}
	v, e := h.svc.Create(c.Request.Context(), in.UserID, true, vpnActor(c))
	vpnAccepted(c, v, e)
}
func (h *VPNHandler) AdminUpdate(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	var in service.VPNUpdate
	if c.ShouldBindJSON(&in) != nil {
		vpnReply(c, nil, service.ErrVPNInvalid)
		return
	}
	v, e := h.svc.Update(c.Request.Context(), id, vpnActor(c), in)
	vpnAccepted(c, v, e)
}
func (h *VPNHandler) AdminRevoke(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	v, e := h.svc.Revoke(c.Request.Context(), id, vpnActor(c))
	vpnAccepted(c, v, e)
}
func (h *VPNHandler) AdminRefresh(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	v, e := h.svc.Refresh(c.Request.Context(), id)
	vpnReply(c, v, e)
}
func (h *VPNHandler) AdminRetry(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	v, e := h.svc.Retry(c.Request.Context(), id)
	vpnAccepted(c, v, e)
}
func (h *VPNHandler) Summary(c *gin.Context) {
	v, e := h.svc.Summary(c.Request.Context())
	vpnReply(c, v, e)
}

func (h *VPNHandler) AdminDelete(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	v, e := h.svc.Delete(c.Request.Context(), id, vpnActor(c))
	vpnAccepted(c, v, e)
}
func (h *VPNHandler) DailyTraffic(c *gin.Context) {
	var server, user int64
	for key, dest := range map[string]*int64{"server_id": &server, "user_id": &user} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				vpnReply(c, nil, service.ErrVPNInvalid)
				return
			}
			*dest = value
		}
	}
	v, e := h.svc.DailyTraffic(c.Request.Context(), service.VPNTrafficFilter{StartDate: c.Query("start_date"), EndDate: c.Query("end_date"), ServerID: server, UserID: user})
	vpnReply(c, v, e)
}

func (h *VPNHandler) Groups(c *gin.Context) {
	v, e := h.svc.Groups(c.Request.Context())
	vpnReply(c, v, e)
}
func (h *VPNHandler) SaveGroup(c *gin.Context) {
	var in service.VPNGroupInput
	if c.ShouldBindJSON(&in) != nil {
		vpnReply(c, nil, service.ErrVPNInvalid)
		return
	}
	var id int64
	if c.Param("id") != "" {
		id = vpnID(c)
		if id == 0 {
			return
		}
	}
	v, e := h.svc.SaveGroup(c.Request.Context(), id, in)
	vpnReply(c, v, e)
}
func (h *VPNHandler) SetUserGroup(c *gin.Context) {
	id := vpnID(c)
	if id == 0 {
		return
	}
	var in struct {
		GroupID int64 `json:"group_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		vpnReply(c, nil, service.ErrVPNInvalid)
		return
	}
	v, e := h.svc.SetUserGroup(c.Request.Context(), id, in.GroupID)
	vpnReply(c, v, e)
}
