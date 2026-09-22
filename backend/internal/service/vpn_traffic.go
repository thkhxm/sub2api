package service

import (
	"context"
	"fmt"
	"time"
)

var vpnTimezone = time.FixedZone("Asia/Shanghai", 8*60*60)

func vpnPeriodBounds(now time.Time) (time.Time, time.Time) {
	local := now.In(vpnTimezone)
	start := time.Date(local.Year(), local.Month(), 22, 0, 0, 0, 0, vpnTimezone)
	if local.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start.UTC(), start.AddDate(0, 1, 0).UTC()
}

func validVPNNodeTraffic(v *VPNNodeTraffic) bool {
	if v.UsedBytes == nil || *v.UsedBytes < 0 || *v.UsedBytes > VPNMaxQuota || v.PeriodStart == nil || v.PeriodEnd == nil || v.SampledAt == nil {
		return false
	}
	start, end := vpnPeriodBounds(*v.SampledAt)
	return v.PeriodStart.Equal(start) && v.PeriodEnd.Equal(end) && !v.SampledAt.After(time.Now().Add(time.Minute)) && (v.AvailableFrom == nil || !v.AvailableFrom.After(*v.SampledAt))
}

func hydrateVPNServer(v *VPNServer, now time.Time) {
	v.TrafficAccountingStatus = "unavailable"
	v.TrafficRemainingBytes = nil
	v.TrafficUsedBytes = nil
	currentStart, _ := vpnPeriodBounds(now)
	if v.TrafficOffsetPeriodStart == nil || !v.TrafficOffsetPeriodStart.Equal(currentStart) {
		// 编辑表单展示本期有效值，换月后允许重新填入相同数额而不误用旧补充值。
		v.TrafficUsedOffsetBytes = 0
	}
	x := v.TrafficSnapshot
	if x == nil || !validVPNNodeTraffic(x) {
		return
	}
	v.TrafficPeriodStart, v.TrafficPeriodEnd = x.PeriodStart, x.PeriodEnd
	v.TrafficSampledAt, v.TrafficAvailableFrom = x.SampledAt, x.AvailableFrom
	used := *x.UsedBytes
	v.TrafficUsedBytes = &used
	if v.TrafficOffsetPeriodStart != nil && v.TrafficOffsetPeriodStart.Equal(*x.PeriodStart) {
		if v.TrafficUsedOffsetBytes > VPNMaxQuota-used {
			v.TrafficAccountingStatus = "invalid"
			return
		}
		used += v.TrafficUsedOffsetBytes
	}
	v.TrafficAccountingStatus = x.AccountingStatus
	if (x.AccountingStatus == "ok" || x.AccountingStatus == "partial_history") && (now.Sub(*x.SampledAt) > 2*time.Minute || now.Before(*x.PeriodStart) || !now.Before(*x.PeriodEnd) || v.LastCheckedAt == nil || now.Sub(*v.LastCheckedAt) > 2*time.Minute || !v.Healthy) {
		v.TrafficAccountingStatus = "stale"
	}
	if v.TrafficQuotaBytes > 0 && (v.TrafficAccountingStatus == "ok" || v.TrafficAccountingStatus == "partial_history") {
		remaining := v.TrafficQuotaBytes - used
		if remaining < 0 {
			remaining = 0
		}
		v.TrafficRemainingBytes = &remaining
	}
}

func vpnTrafficDates(f VPNTrafficFilter) ([]string, error) {
	start, err := time.ParseInLocation("2006-01-02", f.StartDate, vpnTimezone)
	if err != nil {
		return nil, ErrVPNInvalid
	}
	end, err := time.ParseInLocation("2006-01-02", f.EndDate, vpnTimezone)
	if err != nil || start.Year() < 1970 || end.Before(start) || end.Sub(start) > 92*24*time.Hour || f.ServerID < 0 || f.UserID < 0 {
		return nil, ErrVPNInvalid
	}
	result := []string{}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		result = append(result, day.Format("2006-01-02"))
	}
	return result, nil
}

func validVPNRemoteTraffic(v *VPNRemoteTraffic, f VPNTrafficFilter, dates []string) bool {
	if v == nil || v.Timezone != "Asia/Shanghai" || v.StartDate != f.StartDate || v.EndDate != f.EndDate || len(v.Days) != len(dates) || v.TotalBytes < 0 {
		return false
	}
	var total int64
	for i, day := range v.Days {
		if day.Date != dates[i] {
			return false
		}
		if day.UsedBytes != nil {
			if *day.UsedBytes < 0 || *day.UsedBytes > VPNMaxQuota-total {
				return false
			}
			total += *day.UsedBytes
		}
	}
	return total == v.TotalBytes
}

func (s *VPNService) DailyTraffic(ctx context.Context, f VPNTrafficFilter) (*VPNTrafficResponse, error) {
	dates, err := vpnTrafficDates(f)
	if err != nil {
		return nil, err
	}
	response := &VPNTrafficResponse{Timezone: "Asia/Shanghai", StartDate: f.StartDate, EndDate: f.EndDate, Days: make([]VPNTrafficDay, len(dates))}
	for i, date := range dates {
		response.Days[i].Date = date
	}
	servers, err := s.repo.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	var refs map[int64][]string
	if f.UserID > 0 {
		refs, err = s.repo.UserOwnerRefs(ctx, f.UserID)
		if err != nil {
			return nil, err
		}
	}
	selected := []VPNServer{}
	serverFound := f.ServerID == 0
	for _, server := range servers {
		if f.ServerID > 0 && server.ID != f.ServerID {
			continue
		}
		serverFound = true
		if f.UserID > 0 && len(refs[server.ID]) == 0 {
			continue
		}
		selected = append(selected, server)
	}
	if !serverFound {
		return nil, ErrVPNNotFound
	}
	if len(selected) == 0 {
		if f.UserID > 0 {
			for i := range response.Days {
				value := int64(0)
				response.Days[i].UsedBytes = &value
			}
		}
		return response, nil
	}
	type nodeResult struct {
		traffic *VPNRemoteTraffic
		err     error
	}
	results := make(chan nodeResult, len(selected))
	limit := make(chan struct{}, 4)
	for _, server := range selected {
		go func(server VPNServer) {
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				results <- nodeResult{err: ctx.Err()}
				return
			}
			defer func() { <-limit }()
			credentials, e := s.credentials(&server)
			if e != nil {
				results <- nodeResult{err: e}
				return
			}
			traffic, e := s.remote.Traffic(ctx, &server, credentials, f, refs[server.ID])
			if e == nil && !validVPNRemoteTraffic(traffic, f, dates) {
				e = fmt.Errorf("VPN节点每日流量响应无效")
			}
			results <- nodeResult{traffic, e}
		}(server)
	}
	for range selected {
		r := <-results
		if r.err != nil {
			response.UnavailableServers++
			response.Partial = true
			continue
		}
		x := r.traffic
		if x.AccountingStatus != "ok" || x.SampledAt == nil || time.Since(*x.SampledAt) > 2*time.Minute {
			response.Partial = true
		}
		if x.AvailableFrom != nil && (response.AvailableFrom == nil || x.AvailableFrom.Before(*response.AvailableFrom)) {
			response.AvailableFrom = x.AvailableFrom
		}
		if x.SampledAt != nil && (response.SyncedAt == nil || x.SampledAt.Before(*response.SyncedAt)) {
			response.SyncedAt = x.SampledAt
		}
		for i, day := range x.Days {
			if day.UsedBytes == nil {
				response.Partial = true
				continue
			}
			if response.Days[i].UsedBytes == nil {
				value := int64(0)
				response.Days[i].UsedBytes = &value
			}
			if *day.UsedBytes > VPNMaxQuota-response.TotalBytes {
				return nil, ErrVPNInvalid
			}
			*response.Days[i].UsedBytes += *day.UsedBytes
			response.TotalBytes += *day.UsedBytes
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if response.UnavailableServers == len(selected) {
		return nil, ErrVPNNoServer
	}
	return response, nil
}
