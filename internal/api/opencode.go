package api

import "time"

type OpenCodeQuotaFormat string

const (
	OpenCodeQuotaFormatCurrency OpenCodeQuotaFormat = "currency"
	OpenCodeQuotaFormatPercent  OpenCodeQuotaFormat = "percent"
)

type OpenCodeQuota struct {
	Name        string
	Used        float64
	Limit       float64
	Utilization float64
	Format      OpenCodeQuotaFormat
	// Status is the upstream window status ("ok", "exhausted", ...) from the
	// key-mode usage API. Empty for cookie mode, which reports no status.
	// A value other than "ok" means the window is blocked whatever its percent.
	Status   string
	ResetsAt *time.Time
}

type OpenCodeAccountType string

const (
	OpenCodeAccountTypePro OpenCodeAccountType = "pro"
)

type OpenCodeSnapshot struct {
	ID          int64
	CapturedAt  time.Time
	RawJSON     string
	AccountType OpenCodeAccountType
	PlanName    string
	Quotas      []OpenCodeQuota
}
