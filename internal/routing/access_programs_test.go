package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestAdvertisedSecondaryAccessRoutesOnlyToOwningAccount(t *testing.T) {
	var routedAccount string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		routedAccount = request.Header.Get("ChatGPT-Account-ID")
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: request}, nil
	})
	proxy, store, accounts := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{
		{stable: "standard", models: []string{"shared"}},
		{stable: "blue", models: []string{"shared", "exclusive"}},
	})
	ctx := context.Background()
	snapshots := []string{
		`{"models":[{"slug":"shared","available_access_programs":{"cyber":["standard"]}}]}`,
		`{"models":[{"slug":"shared","available_access_programs":{"cyber":["standard","daybreak_blue"]}},{"slug":"exclusive"}]}`,
	}
	for i, account := range accounts {
		if err := store.UpdateAccountCatalog(ctx, account.ID, []byte(snapshots[i]), account.EntitledModels, "1.0.0"); err != nil {
			t.Fatal(err)
		}
	}
	device, err := store.CreateEnrollment(ctx, "Catalog access test")
	if err != nil {
		t.Fatal(err)
	}
	for _, primary := range accounts {
		if err := store.SetPrimaryAccount(ctx, primary.ID); err != nil {
			t.Fatal(err)
		}
		result, err := proxy.catalog.BuildForDevice(ctx, device.DeviceID, "1.0.0")
		if err != nil || len(result.Conflicts) != 0 {
			t.Fatalf("catalog: %v, %v", result.Conflicts, err)
		}
		var picker struct {
			Models []struct {
				Slug      string              `json:"slug"`
				Available map[string][]string `json:"available_access_programs"`
			} `json:"models"`
		}
		if err := json.Unmarshal(result.Raw, &picker); err != nil {
			t.Fatal(err)
		}
		if len(picker.Models) != 2 {
			t.Fatalf("secondary-only model lost: %s", result.Raw)
		}
		for _, model := range picker.Models {
			body := map[string]any{"model": model.Slug}
			if model.Slug == "shared" {
				if !contains(model.Available["cyber"], "daybreak_blue") {
					t.Fatalf("secondary access hidden: %s", result.Raw)
				}
				body["access_programs"] = map[string]string{"cyber": "daybreak_blue"}
			}
			encoded, _ := json.Marshal(body)
			writer := httptest.NewRecorder()
			routedAccount = ""
			proxy.ServeDeviceHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(encoded)), DeviceContext{ID: device.DeviceID})
			if writer.Code != http.StatusOK || routedAccount != "blue" {
				t.Fatalf("primary=%s model=%s: status=%d account=%s", primary.ID, model.Slug, writer.Code, routedAccount)
			}
		}
		for i, account := range accounts {
			stored, err := store.Account(ctx, account.ID, false)
			if err != nil || !bytes.Equal(stored.RawCatalogSnapshot, []byte(snapshots[i])) {
				t.Fatalf("account's original access changed: %v", err)
			}
		}
	}
}

func TestNativeAccessProgramsStayAccountAndModelScoped(t *testing.T) {
	for _, model := range []string{"gpt-shared", "codex-auto-review"} {
		for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
			t.Run(model+path, func(t *testing.T) {
				var attempts []string
				var received []string
				exhaustBlue := false
				transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
					account := request.Header.Get("ChatGPT-Account-ID")
					attempts = append(attempts, account)
					body, _ := io.ReadAll(request.Body)
					received = append(received, string(body))
					status := http.StatusOK
					if exhaustBlue && account == "blue" {
						status = http.StatusTooManyRequests
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: request}, nil
				})
				proxy, store, accounts := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{
					{stable: "standard", models: []string{model}},
					{stable: "blue", models: []string{model}},
					{stable: "standard-fallback", models: []string{model}},
					{stable: "blue-fallback", models: []string{model}},
				})
				for i, account := range accounts {
					programs := []string{"standard"}
					if i == 1 || i == 3 {
						programs = append(programs, "daybreak_blue")
					}
					// Advertising Blue on another model must not qualify this one.
					raw, _ := json.Marshal(map[string]any{"models": []any{
						map[string]any{"slug": model, "available_access_programs": map[string]any{"cyber": programs}},
						map[string]any{"slug": "other-model", "available_access_programs": map[string]any{"cyber": []string{"daybreak_blue"}}},
					}})
					if err := store.UpdateAccountCatalog(context.Background(), account.ID, raw, []string{model, "other-model"}, "1.0.0"); err != nil {
						t.Fatal(err)
					}
				}
				call := func(body string, wantStatus int, wantAttempts []string) {
					t.Helper()
					attempts, received = nil, nil
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					request.Header.Set("thread-id", "same-thread")
					writer := httptest.NewRecorder()
					proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "device"})
					if writer.Code != wantStatus || !reflect.DeepEqual(attempts, wantAttempts) {
						t.Fatalf("status %d, attempts %v, response %s; want %d, %v", writer.Code, attempts, writer.Body.String(), wantStatus, wantAttempts)
					}
					for _, forwarded := range received {
						if forwarded != body {
							t.Fatalf("request access selection was rewritten: %s", forwarded)
						}
					}
				}
				plain := `{"model":"` + model + `"}`
				blue := `{"model":"` + model + `", "access_programs":{"cyber":"daybreak_blue"}}`
				// Establish affinity to the primary, then require access it lacks.
				call(plain, http.StatusOK, []string{"standard"})
				call(blue, http.StatusOK, []string{"blue"})
				// Quota fallback must skip the next account that lacks Blue.
				exhaustBlue = true
				call(blue, http.StatusOK, []string{"blue", "blue-fallback"})
				if err := store.MarkAccountExhausted(context.Background(), accounts[3].ID, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				call(blue, http.StatusServiceUnavailable, nil)
				call(plain, http.StatusOK, []string{"standard"})
			})
		}
	}
}

func TestNativeAccessProgramFallbackStopsWithoutEligibleAccount(t *testing.T) {
	attempts := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if request.Header.Get("ChatGPT-Account-ID") != "blue" {
			t.Fatal("Blue request reached an account without Blue access")
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	proxy, store, accounts := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{
		{stable: "blue", models: []string{"model"}},
		{stable: "standard", models: []string{"model"}},
	})
	if err := store.UpdateAccountCatalog(context.Background(), accounts[0].ID, []byte(`{"models":[{"slug":"model","available_access_programs":{"cyber":["daybreak_blue"]}}]}`), []string{"model"}, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	writer := httptest.NewRecorder()
	proxy.ServeDeviceHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"model","access_programs":{"cyber":"daybreak_blue"}}`)), DeviceContext{ID: "device"})
	if writer.Code != http.StatusTooManyRequests || attempts != 1 {
		t.Fatalf("status %d, attempts %d: %s", writer.Code, attempts, writer.Body.String())
	}
}

func TestNativeAccessProgramsRequireExplicitCatalogEvidence(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"models":[]}`, `invalid`,
		`{"models":[{"slug":"model"}]}`,
		`{"models":[{"slug":"model","available_access_programs":null}]}`,
		`{"models":[{"slug":"model","available_access_programs":{"cyber":[]}}]}`,
		`{"models":[{"slug":"model","available_access_programs":{"cyber":["standard"]}}]}`,
		`{"models":[{"slug":"other","available_access_programs":{"cyber":["daybreak_blue"]}}]}`,
	} {
		if accountSupportsAccessPrograms(storage.Account{RawCatalogSnapshot: []byte(raw)}, "model", accessPrograms{"cyber": "daybreak_blue"}) {
			t.Fatalf("invented access from %s", raw)
		}
	}
	account := storage.Account{RawCatalogSnapshot: []byte(`{"models":[{"slug":"model","available_access_programs":{"cyber":["standard","daybreak_blue"],"future":["special"]}}]}`)}
	if !accountSupportsAccessPrograms(account, "model", accessPrograms{"cyber": "daybreak_blue", "future": "special"}) {
		t.Fatal("matching access programs rejected")
	}
	if accountSupportsAccessPrograms(account, "model", accessPrograms{"cyber": "daybreak_blue", "future": "missing"}) {
		t.Fatal("did not require every requested access program")
	}
}

func TestNativeProxyRejectsMalformedAccessPrograms(t *testing.T) {
	for _, value := range []string{`[]`, `"daybreak_blue"`, `{"cyber":null}`, `{"cyber":true}`, `{"cyber":["daybreak_blue"]}`, `{"cyber":""}`} {
		proxy, _, _ := proxyFixture(t, &http.Client{}, "https://unused.invalid", nil)
		writer := httptest.NewRecorder()
		body := `{"model":"model","access_programs":` + value + `}`
		proxy.ServeDeviceHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)), DeviceContext{ID: "device"})
		if writer.Code != http.StatusBadRequest || !strings.Contains(writer.Body.String(), "invalid_access_programs") {
			t.Fatalf("value %s: status %d, response %s", value, writer.Code, writer.Body.String())
		}
	}
}
