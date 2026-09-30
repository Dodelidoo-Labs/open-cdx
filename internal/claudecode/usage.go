package claudecode

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// UsageCommandArgs runs Claude Code's own /usage command without a model
// request. Claude Code signs in and asks Anthropic itself; OpenCDX reads only
// the printed allowance lines. No settings source is loaded, so the user's
// hooks, status line, MCP servers, and telemetry export do not run, and no
// session is saved.
var UsageCommandArgs = []string{"-p", "/usage", "--output-format", "json", "--no-session-persistence", "--setting-sources", "", "--strict-mcp-config"}

// ErrNoUsageWindows means /usage printed no plan allowance, as for API-key
// logins, or printed it in a form this parser does not recognise.
var ErrNoUsageWindows = errors.New("claude /usage reported no plan allowance")

var (
	usageWindows = map[string]int64{
		"Current session":           5 * 60 * 60,
		"Current week (all models)": 7 * 24 * 60 * 60,
	}
	usageLine  = regexp.MustCompile(`^(Current session|Current week \(all models\)): (\d+(?:\.\d+)?)% used(?: · resets (.+) \(([^()]+)\))?$`)
	usageDates = []string{"Jan 2, 3:04pm", "Jan 2, 3pm", "Jan 2, 2006, 3:04pm", "Jan 2, 2006, 3pm"}
)

// ParseUsageCommand reads the plan windows from `claude -p /usage
// --output-format json`. Only the session and all-models weekly lines are
// used; per-model weeks and the local usage breakdown are ignored.
func ParseUsageCommand(output []byte, now time.Time) ([]Window, error) {
	var envelope struct {
		IsError bool    `json:"is_error"`
		Result  *string `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil || envelope.Result == nil {
		return nil, errors.New("claude /usage output is not the expected JSON")
	}
	if envelope.IsError {
		return nil, errors.New("claude /usage reported an error")
	}
	var windows []Window
	for _, line := range strings.Split(*envelope.Result, "\n") {
		match := usageLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil || match[3] == "" {
			continue
		}
		seconds := usageWindows[match[1]]
		used, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		reset, ok := parseUsageReset(match[3], match[4], now)
		// Same guards as the status line: drop stale or impossible windows.
		if !ok || !reset.After(now) || reset.Sub(now) > time.Duration(seconds)*time.Second+5*time.Minute || used < 0 || used > 100 {
			continue
		}
		windows = append(windows, Window{Seconds: seconds, UsedPercent: used, ResetAt: reset.Format(time.RFC3339)})
	}
	if len(windows) == 0 {
		return nil, ErrNoUsageWindows
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Seconds > windows[j].Seconds })
	return windows, nil
}

// parseUsageReset reads Claude Code's reset time, such as "Oct 3 at 1pm" in
// the named time zone. It is shown to the minute; the year appears only when
// it differs from the current one.
func parseUsageReset(value, zone string, now time.Time) (time.Time, bool) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.Local
	}
	value = strings.NewReplacer(" ", " ", " ", " ", " at ", ", ").Replace(value)
	value = strings.Replace(strings.Replace(value, " AM", "am", 1), " PM", "pm", 1)
	for _, layout := range usageDates {
		parsed, err := time.ParseInLocation(layout, value, location)
		if err != nil {
			continue
		}
		year := parsed.Year()
		if !strings.Contains(layout, "2006") {
			year = now.In(location).Year()
		}
		return time.Date(year, parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location).UTC(), true
	}
	return time.Time{}, false
}
