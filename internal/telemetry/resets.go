package telemetry

import (
	"sort"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

type AllowanceCycleUsage struct {
	DeviceID string `json:"device_id"`
	Tokens   int64  `json:"tokens"`
	Requests int64  `json:"requests"`
	Untimed  bool   `json:"untimed"`
}

type AllowanceReset struct {
	Source            string                `json:"source"`
	AccountID         string                `json:"account_id,omitempty"`
	DeviceID          string                `json:"device_id,omitempty"`
	Label             string                `json:"label"`
	At                time.Time             `json:"at"`
	After             time.Time             `json:"after"`
	ObservedAt        time.Time             `json:"observed_at"`
	Scheduled         bool                  `json:"scheduled"`
	ObservedRemaining float64               `json:"observed_remaining"`
	Until             *time.Time            `json:"until,omitempty"`
	Usage             []AllowanceCycleUsage `json:"usage"`
}

type allowanceStream struct{ source, account, device string }

// BuildAllowanceResets detects changes of weekly windows, not mere drops in
// utilization. History has no account identity: its transitions remain inferred
// and must not be summed across machines as distinct account resets.
func BuildAllowanceResets(observations []storage.AllowanceObservation, usage []storage.UsageAggregate, now time.Time) []AllowanceReset {
	streams := make(map[allowanceStream][]storage.AllowanceObservation)
	for _, o := range observations {
		if o.ObservedAt.After(now) || o.Validate(now) != nil {
			continue
		}
		key := allowanceStream{o.Source, o.AccountID, o.DeviceID}
		streams[key] = append(streams[key], o)
	}
	markers := make(map[allowanceStream][]AllowanceReset)
	for key, rows := range streams {
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].ObservedAt.Equal(rows[j].ObservedAt) {
				return rows[i].ObservedAt.Before(rows[j].ObservedAt)
			}
			return rows[i].ResetAt.Before(rows[j].ResetAt)
		})
		previous := rows[0]
		for _, row := range rows[1:] {
			shift := row.ResetAt.Sub(previous.ResetAt)
			if shift < -time.Minute {
				continue
			} // stale rebroadcast or account switch back
			if shift <= time.Minute {
				if !row.ObservedAt.Before(previous.ResetAt) {
					continue
				} // expired-window rebroadcast
				// Keep the window's original deadline to prevent accumulated jitter.
				previous.ObservedAt, previous.Used = row.ObservedAt, row.Used
				continue
			}
			if !row.ObservedAt.After(previous.ObservedAt) {
				continue
			}
			crossed := !row.ObservedAt.Before(previous.ResetAt) && previous.ObservedAt.Before(previous.ResetAt)
			dropped := row.Used < previous.Used
			if crossed || dropped {
				at := row.ObservedAt
				scheduled := crossed && (row.ResetAt.Sub(previous.ResetAt)-7*24*time.Hour).Abs() <= time.Minute
				if scheduled {
					at = previous.ResetAt
				}
				markers[key] = append(markers[key], AllowanceReset{Source: key.source, AccountID: key.account, DeviceID: key.device, Label: row.Label, At: at, After: previous.ObservedAt, ObservedAt: row.ObservedAt, Scheduled: scheduled, ObservedRemaining: 100 - row.Used, Usage: make([]AllowanceCycleUsage, 0)})
			}
			previous = row
		}
	}
	// Attribute each timestamp once per observation stream. Historical and live
	// scopes deliberately remain separate; reconciliation erases account identity.
	totals := make(map[allowanceStream][]map[string]*AllowanceCycleUsage)
	for key, list := range markers {
		totals[key] = make([]map[string]*AllowanceCycleUsage, len(list))
		for i := range list {
			totals[key][i] = make(map[string]*AllowanceCycleUsage)
			if i+1 < len(list) {
				end := list[i+1].At
				list[i].Until = &end
			}
		}
	}
	for _, u := range usage {
		if u.Provider != "openai" {
			continue
		}
		keys := []allowanceStream{{"history", "", u.DeviceID}}
		if u.Source == storage.UsageSourceRouted {
			keys = append(keys, allowanceStream{"live", u.AccountID, ""})
		}
		at, err := time.Parse(time.RFC3339Nano, u.RecordedAt)
		for _, key := range keys {
			list := markers[key]
			if len(list) == 0 {
				continue
			}
			if err != nil {
				// A daily legacy row can overlap multiple cycles. Flag those cycles rather
				// than inventing a response timestamp or prorating its tokens.
				day, e := time.Parse("2006-01-02", u.Day)
				if e != nil {
					continue
				}
				for i, m := range list {
					end := now
					if m.Until != nil {
						end = *m.Until
					}
					if day.Before(end) && day.Add(24*time.Hour).After(m.At) {
						if totals[key][i][u.DeviceID] == nil {
							totals[key][i][u.DeviceID] = &AllowanceCycleUsage{DeviceID: u.DeviceID}
						}
						totals[key][i][u.DeviceID].Untimed = true
					}
				}
				continue
			}
			if at.After(now) {
				continue
			}
			i := sort.Search(len(list), func(i int) bool { return list[i].At.After(at) }) - 1
			if i < 0 {
				continue
			}
			if totals[key][i][u.DeviceID] == nil {
				totals[key][i][u.DeviceID] = &AllowanceCycleUsage{DeviceID: u.DeviceID}
			}
			total := totals[key][i][u.DeviceID]
			total.Tokens += u.InputTokens + u.OutputTokens // cached/reasoning are subsets
			total.Requests += u.Requests
		}
	}
	result := make([]AllowanceReset, 0)
	for key, list := range markers {
		for i := range list {
			for _, total := range totals[key][i] {
				list[i].Usage = append(list[i].Usage, *total)
			}
			sort.Slice(list[i].Usage, func(a, b int) bool { return list[i].Usage[a].DeviceID < list[i].Usage[b].DeviceID })
			result = append(result, list[i])
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].At.Equal(result[j].At) {
			return result[i].At.Before(result[j].At)
		}
		return result[i].Source+result[i].AccountID+result[i].DeviceID < result[j].Source+result[j].AccountID+result[j].DeviceID
	})
	return result
}
