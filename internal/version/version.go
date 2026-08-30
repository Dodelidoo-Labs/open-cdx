package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	RepositoryURL       = "https://github.com/Dodelidoo-Labs/open-cdx"
	LatestReleaseAPIURL = "https://api.github.com/repos/Dodelidoo-Labs/open-cdx/releases/latest"
)

var (
	Version = "1.1.0"
	Commit  = "unknown"
)

type UpdateStatus struct {
	Current   string
	Latest    string
	Available bool
}

// UpdateChecker keeps GitHub release discovery off the dashboard request path.
// A snapshot starts a bounded background refresh only when the cache is stale.
type UpdateChecker struct {
	client   *http.Client
	endpoint string
	now      func() time.Time

	mu        sync.Mutex
	latest    string
	nextCheck time.Time
	checking  bool
}

func NewUpdateChecker(client *http.Client) *UpdateChecker {
	if client == nil {
		client = http.DefaultClient
	}
	return &UpdateChecker{client: client, endpoint: LatestReleaseAPIURL, now: time.Now}
}

func (checker *UpdateChecker) Snapshot() UpdateStatus {
	current := Display()
	checker.mu.Lock()
	latest := checker.latest
	if !checker.checking && !checker.now().Before(checker.nextCheck) {
		checker.checking = true
		go checker.refresh()
	}
	checker.mu.Unlock()
	return UpdateStatus{Current: current, Latest: latest, Available: IsNewer(latest, current)}
}

func (checker *UpdateChecker) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	latest, err := checker.fetchLatest(ctx)
	now := checker.now()

	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.checking = false
	if err != nil {
		// GitHub being unavailable must not affect the application. Avoid retrying
		// on every dashboard request, while recovering much sooner than the normal
		// successful cache interval.
		checker.nextCheck = now.Add(15 * time.Minute)
		return
	}
	checker.latest = latest
	checker.nextCheck = now.Add(6 * time.Hour)
}

func (checker *UpdateChecker) fetchLatest(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checker.endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "OpenCDX-Router/"+Display())
	response, err := checker.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return "", fmt.Errorf("GitHub latest release returned %s", response.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err = decoder.Decode(&payload); err != nil {
		return "", err
	}
	latest, ok := canonical(payload.TagName)
	if !ok {
		return "", errors.New("GitHub latest release tag is not semantic version X.Y.Z")
	}
	return latest, nil
}

func Display() string {
	if value, ok := canonical(Version); ok {
		return value
	}
	value := strings.TrimSpace(strings.TrimPrefix(Version, "v"))
	if value == "" {
		return "dev"
	}
	return value
}

func IsNewer(candidate, current string) bool {
	left, leftOK := components(candidate)
	right, rightOK := components(current)
	if !leftOK || !rightOK {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return left[index] > right[index]
		}
	}
	return false
}

func canonical(value string) (string, bool) {
	parts, ok := components(value)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2]), true
}

func components(value string) ([3]uint64, bool) {
	var result [3]uint64
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	parts := strings.Split(value, ".")
	if len(parts) != len(result) {
		return result, false
	}
	for index, part := range parts {
		if part == "" || strings.ContainsAny(part, "+-") {
			return result, false
		}
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return result, false
		}
		result[index] = number
	}
	return result, true
}
