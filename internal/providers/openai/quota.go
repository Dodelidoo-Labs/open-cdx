package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

type quotaWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds"`
	ResetAt            *int64   `json:"reset_at"`
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

// QuotaWindow is one independently enforced window returned by the Codex
// allowance service. Windows are optional and must never be inferred from an
// account's plan.
type QuotaWindow struct {
	Role        string
	UsedPercent float64
	Duration    time.Duration
	ResetAt     time.Time
}

// QuotaPace compares allowance remaining with time remaining in a window. A
// small tolerance prevents insignificant service rounding from flipping an
// account between on-pace and too-fast states.
type QuotaPace struct {
	Available                bool
	Status                   string
	RequiredRemainingPercent float64
	BufferPercent            float64
}

const paceTolerancePercent = 2.0

func ParseQuota(raw []byte) (providers.Quota, error) {
	var payload quotaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return providers.Quota{}, errors.New("quota response was invalid")
	}
	quota := providers.Quota{Plan: payload.Plan, Raw: append(json.RawMessage(nil), raw...)}
	if payload.RateLimit != nil {
		now := time.Now().UTC()
		quota.LimitReached = payload.RateLimit.LimitReached || !payload.RateLimit.Allowed
		if used, resetAt, present := quotaWindowSnapshot(payload.RateLimit.Primary, now); present {
			quota.UsedPercent = used
			quota.ResetAt = resetAt
		}
		if used, resetAt, present := quotaWindowSnapshot(payload.RateLimit.Secondary, now); present && used > quota.UsedPercent {
			quota.UsedPercent = used
			quota.ResetAt = resetAt
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

// ParseQuotaWindows preserves every primary/secondary window actually present
// in the main Codex rate limit. Known durations sort longest-first so callers
// can give the long-horizon allowance modestly greater prominence. A present
// window with missing duration or reset data is retained, but its pace will be
// unavailable.
func ParseQuotaWindows(raw []byte, now time.Time) ([]QuotaWindow, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload quotaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, errors.New("quota response was invalid")
	}
	if payload.RateLimit == nil {
		return nil, nil
	}
	windows := make([]QuotaWindow, 0, 2)
	for _, candidate := range []struct {
		role   string
		window *quotaWindow
	}{{role: "primary", window: payload.RateLimit.Primary}, {role: "secondary", window: payload.RateLimit.Secondary}} {
		if candidate.window == nil || candidate.window.UsedPercent == nil {
			continue
		}
		window := QuotaWindow{Role: candidate.role, UsedPercent: clampPercent(*candidate.window.UsedPercent)}
		if seconds := candidate.window.LimitWindowSeconds; validDurationSeconds(seconds) {
			window.Duration = time.Duration(*seconds) * time.Second
		}
		window.ResetAt = quotaWindowResetTime(candidate.window, now)
		windows = append(windows, window)
	}
	sort.SliceStable(windows, func(left, right int) bool {
		leftKnown, rightKnown := windows[left].Duration > 0, windows[right].Duration > 0
		if leftKnown != rightKnown {
			return leftKnown
		}
		return windows[left].Duration > windows[right].Duration
	})
	return windows, nil
}

func (window QuotaWindow) RemainingPercent() float64 {
	return clampPercent(100 - window.UsedPercent)
}

func (window QuotaWindow) Label() string {
	if window.Duration <= 0 {
		return "Allowance"
	}
	minutes := int64(window.Duration / time.Minute)
	switch {
	case minutes == 7*24*60:
		return "Weekly"
	case minutes%1440 == 0:
		days := minutes / 1440
		if days == 1 {
			return "24 hours"
		}
		return fmt.Sprintf("%d days", days)
	case minutes%60 == 0:
		hours := minutes / 60
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	case minutes == 1:
		return "1 minute"
	default:
		return fmt.Sprintf("%d minutes", minutes)
	}
}

func (window QuotaWindow) Pace(now time.Time) QuotaPace {
	if window.Duration <= 0 || window.ResetAt.IsZero() || !now.Before(window.ResetAt) {
		return QuotaPace{}
	}
	timeRemaining := window.ResetAt.Sub(now)
	if timeRemaining > window.Duration {
		return QuotaPace{}
	}
	required := clampPercent(100 * float64(timeRemaining) / float64(window.Duration))
	buffer := window.RemainingPercent() - required
	status := "on_pace"
	if buffer < -paceTolerancePercent {
		status = "too_fast"
	}
	return QuotaPace{
		Available: true, Status: status,
		RequiredRemainingPercent: roundPercent(required), BufferPercent: roundPercent(buffer),
	}
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
	now := time.Now().UTC()
	for _, additional := range payload.AdditionalRateLimits {
		name := strings.TrimSpace(additional.Name)
		feature := strings.TrimSpace(additional.MeteredFeature)
		if additional.RateLimit == nil || (name == "" && feature == "") {
			continue
		}
		used, resetAt, present := restrictiveQuotaWindow(additional.RateLimit, now)
		if !present {
			continue
		}
		quotas = append(quotas, AdditionalQuota{
			Name: name, MeteredFeature: feature, UsedPercent: clampPercent(used), ResetAt: resetAt,
		})
	}
	sort.SliceStable(quotas, func(left, right int) bool {
		return strings.ToLower(quotas[left].Name+quotas[left].MeteredFeature) < strings.ToLower(quotas[right].Name+quotas[right].MeteredFeature)
	})
	return quotas, nil
}

func restrictiveQuotaWindow(limit *quotaRateLimit, now time.Time) (float64, time.Time, bool) {
	if limit == nil {
		return 0, time.Time{}, false
	}
	var used float64
	var resetAt time.Time
	present := false
	for _, window := range []*quotaWindow{limit.Primary, limit.Secondary} {
		windowUsed, windowResetAt, windowPresent := quotaWindowSnapshot(window, now)
		if !windowPresent {
			continue
		}
		if !present || windowUsed > used {
			used, resetAt = windowUsed, windowResetAt
		}
		present = true
	}
	return used, resetAt, present
}

func quotaWindowSnapshot(window *quotaWindow, now time.Time) (float64, time.Time, bool) {
	if window == nil || window.UsedPercent == nil {
		return 0, time.Time{}, false
	}
	return *window.UsedPercent, quotaWindowResetTime(window, now), true
}

func quotaWindowResetTime(window *quotaWindow, now time.Time) time.Time {
	if window == nil {
		return time.Time{}
	}
	if window.ResetAt != nil {
		if resetAt := quotaResetTime(*window.ResetAt); !resetAt.IsZero() {
			return resetAt
		}
	}
	if seconds := window.ResetAfterSeconds; validDurationSeconds(seconds) && !now.IsZero() {
		return now.Add(time.Duration(*seconds) * time.Second).UTC()
	}
	return time.Time{}
}

func validDurationSeconds(value *int64) bool {
	const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))
	return value != nil && *value > 0 && *value <= maxDurationSeconds
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

func roundPercent(value float64) float64 {
	return math.Round(value*10) / 10
}

func quotaResetTime(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}
