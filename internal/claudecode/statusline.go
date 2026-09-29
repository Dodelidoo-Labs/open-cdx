package claudecode

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// StatusLineReading is the plan allowance Claude Code gives its status line
// command. Claude Code includes it for Pro and Max subscribers after the first
// API response of a session.
type StatusLineReading struct {
	SessionID string
	Windows   []Window
}

var statusLineWindows = map[string]int64{
	"five_hour": 5 * 60 * 60,
	"seven_day": 7 * 24 * 60 * 60,
}

// ParseStatusLine extracts only the session ID and rate-limit windows. Paths,
// the workspace, and cost fields in the same JSON are ignored.
func ParseStatusLine(body []byte, now time.Time) (StatusLineReading, error) {
	var payload struct {
		SessionID  string `json:"session_id"`
		RateLimits map[string]*struct {
			UsedPercentage *float64 `json:"used_percentage"`
			ResetsAt       *int64   `json:"resets_at"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return StatusLineReading{}, errors.New("status line input is not valid JSON")
	}
	reading := StatusLineReading{SessionID: payload.SessionID}
	for name, seconds := range statusLineWindows {
		window := payload.RateLimits[name]
		if window == nil || window.UsedPercentage == nil || window.ResetsAt == nil {
			continue
		}
		reset := time.Unix(*window.ResetsAt, 0).UTC()
		used := *window.UsedPercentage
		// Claude Code drops a window once it resets; guard stale values anyway.
		if !reset.After(now) || reset.Sub(now) > time.Duration(seconds)*time.Second+5*time.Minute || used < 0 || used > 100 {
			continue
		}
		reading.Windows = append(reading.Windows, Window{Seconds: seconds, UsedPercent: used, ResetAt: reset.Format(time.RFC3339)})
	}
	sort.Slice(reading.Windows, func(i, j int) bool { return reading.Windows[i].Seconds > reading.Windows[j].Seconds })
	return reading, nil
}
