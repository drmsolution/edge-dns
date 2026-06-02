package analytics

import (
	"fmt"
	"time"
)

type LogStatus string

const (
	StatusAllowed  LogStatus = "ALLOWED"
	StatusBlocked  LogStatus = "BLOCKED"
	StatusRedirect LogStatus = "REDIRECTED"
)

type DNSLog struct {
	Timestamp    time.Time     `json:"timestamp"`
	UserID       string        `json:"user_id"`
	ClientIP     string        `json:"client_ip"`
	Domain       string        `json:"domain"`
	QueryType    string        `json:"query_type"`
	Status       LogStatus     `json:"status"`
	ResponseTime time.Duration `json:"response_time_ns"`
}

func (l DNSLog) String() string {
	return fmt.Sprintf("DNSLog{user=%s domain=%s type=%s status=%s elapsed=%v}",
		l.UserID, l.Domain, l.QueryType, l.Status, l.ResponseTime)
}
