package openai

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

type quotaWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
}

type quotaRateLimit struct {
	Allowed      bool         `json:"allowed"`
	LimitReached bool         `json:"limit_reached"`
	Primary      *quotaWindow `json:"primary_window"`
	Secondary    *quotaWindow `json:"secondary_window"`
}

type quotaPayload struct {
	Plan         string          `json:"plan_type"`
	RateLimit    *quotaRateLimit `json:"rate_limit"`
	SpendControl *struct {
		Reached         bool `json:"reached"`
		IndividualLimit *struct {
			UsedPercent float64 `json:"used_percent"`
			ResetAt     int64   `json:"reset_at"`
		} `json:"individual_limit"`
	} `json:"spend_control"`
	RateLimitReachedType *struct {
		Type string `json:"type"`
	} `json:"rate_limit_reached_type"`
	ResetCredits *struct {
		Available int `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
	AdditionalRateLimits []struct {
		Name           string          `json:"limit_name"`
		MeteredFeature string          `json:"metered_feature"`
		RateLimit      *quotaRateLimit `json:"rate_limit"`
	} `json:"additional_rate_limits"`
}

// AdditionalQuota is a separately metered Codex quota bucket, such as the
// research-preview Spark allowance. The raw response remains the source of
// truth; callers should only surface buckets that are actually present.
type AdditionalQuota struct {
	Name           string
	MeteredFeature string
	UsedPercent    float64
	ResetAt        time.Time
}

func ParseQuota(raw []byte) (providers.Quota, error) {
	var payload quotaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return providers.Quota{}, errors.New("quota response was invalid")
	}
	quota := providers.Quota{Plan: payload.Plan, Raw: append(json.RawMessage(nil), raw...)}
	if payload.RateLimit != nil {
		quota.LimitReached = payload.RateLimit.LimitReached || !payload.RateLimit.Allowed
		if payload.RateLimit.Primary != nil {
			quota.UsedPercent = payload.RateLimit.Primary.UsedPercent
			quota.ResetAt = quotaResetTime(payload.RateLimit.Primary.ResetAt)
		}
		if payload.RateLimit.Secondary != nil && payload.RateLimit.Secondary.UsedPercent > quota.UsedPercent {
			quota.UsedPercent = payload.RateLimit.Secondary.UsedPercent
			quota.ResetAt = quotaResetTime(payload.RateLimit.Secondary.ResetAt)
		}
	}
	if payload.SpendControl != nil {
		quota.LimitReached = quota.LimitReached || payload.SpendControl.Reached
		if limit := payload.SpendControl.IndividualLimit; limit != nil && limit.UsedPercent > quota.UsedPercent {
			quota.UsedPercent = limit.UsedPercent
			quota.ResetAt = quotaResetTime(limit.ResetAt)
		}
	}
	if payload.RateLimitReachedType != nil && payload.RateLimitReachedType.Type != "" && payload.RateLimitReachedType.Type != "unknown" {
		quota.LimitReached = true
	}
	if quota.LimitReached && quota.UsedPercent < 100 {
		quota.UsedPercent = 100
	}
	if quota.UsedPercent < 0 {
		quota.UsedPercent = 0
	} else if quota.UsedPercent > 100 {
		quota.UsedPercent = 100
	}
	if payload.ResetCredits != nil {
		quota.ResetCredits = payload.ResetCredits.Available
	}
	return quota, nil
}

// ParseAdditionalQuotas returns independently metered quota buckets without
// inferring availability from the account plan or model catalog.
func ParseAdditionalQuotas(raw []byte) ([]AdditionalQuota, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload quotaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, errors.New("quota response was invalid")
	}
	quotas := make([]AdditionalQuota, 0, len(payload.AdditionalRateLimits))
	for _, additional := range payload.AdditionalRateLimits {
		name := strings.TrimSpace(additional.Name)
		feature := strings.TrimSpace(additional.MeteredFeature)
		if additional.RateLimit == nil || (name == "" && feature == "") {
			continue
		}
		used, resetAt, present := restrictiveQuotaWindow(additional.RateLimit)
		if !present {
			continue
		}
		quotas = append(quotas, AdditionalQuota{
			Name: name, MeteredFeature: feature, UsedPercent: clampPercent(used), ResetAt: quotaResetTime(resetAt),
		})
	}
	sort.SliceStable(quotas, func(left, right int) bool {
		return strings.ToLower(quotas[left].Name+quotas[left].MeteredFeature) < strings.ToLower(quotas[right].Name+quotas[right].MeteredFeature)
	})
	return quotas, nil
}

func restrictiveQuotaWindow(limit *quotaRateLimit) (float64, int64, bool) {
	if limit == nil {
		return 0, 0, false
	}
	var used float64
	var resetAt int64
	present := false
	for _, window := range []*quotaWindow{limit.Primary, limit.Secondary} {
		if window == nil {
			continue
		}
		if !present || window.UsedPercent > used {
			used, resetAt = window.UsedPercent, window.ResetAt
		}
		present = true
	}
	return used, resetAt, present
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func quotaResetTime(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}
