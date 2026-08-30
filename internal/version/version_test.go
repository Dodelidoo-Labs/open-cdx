package version

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestIsNewerUsesNumericSemanticVersions(t *testing.T) {
	for _, test := range []struct {
		candidate, current string
		newer              bool
	}{
		{candidate: "v1.1.0", current: "1.0.9", newer: true},
		{candidate: "1.10.0", current: "1.9.9", newer: true},
		{candidate: "1.0.0", current: "1.0.0"},
		{candidate: "0.9.9", current: "1.0.0"},
		{candidate: "1.1.0-rc.1", current: "1.0.0"},
		{candidate: "latest", current: "1.0.0"},
	} {
		if got := IsNewer(test.candidate, test.current); got != test.newer {
			t.Fatalf("IsNewer(%q, %q) = %v, want %v", test.candidate, test.current, got, test.newer)
		}
	}
}

func TestUpdateCheckerFetchesAndNormalizesLatestRelease(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("User-Agent") == "" {
			t.Fatal("GitHub request headers were not set")
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.2.3"}`)), Request: request,
		}, nil
	})}
	checker := NewUpdateChecker(client)
	latest, err := checker.fetchLatest(context.Background())
	if err != nil || latest != "1.2.3" {
		t.Fatalf("latest release = %q, %v", latest, err)
	}
}
