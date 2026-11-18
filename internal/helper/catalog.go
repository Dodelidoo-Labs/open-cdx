package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CatalogResult struct {
	Changed         bool      `json:"changed"`
	ETag            string    `json:"etag"`
	RestartRequired bool      `json:"restart_required"`
	UpdatedAt       time.Time `json:"-"`
}

func SyncCatalog(ctx context.Context, client *RemoteClient, configPath string, config *Config, codexVersion string, force bool) (CatalogResult, error) {
	query := url.Values{"codex_version": {codexVersion}}
	path := "/api/v1/catalog?" + query.Encode()
	if force {
		path = "/api/v1/catalog/refresh?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, map[bool]string{true: http.MethodPost, false: http.MethodGet}[force], client.BaseURL+path, nil)
	if err != nil {
		return CatalogResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.DeviceToken)
	if config.CatalogETag != "" {
		request.Header.Set("If-None-Match", config.CatalogETag)
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return CatalogResult{}, errors.New("remote router is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return CatalogResult{Changed: false, ETag: config.CatalogETag}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CatalogResult{}, catalogResponseError(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil || len(raw) > 32<<20 {
		return CatalogResult{}, errors.New("router model catalog exceeded the helper limit")
	}
	var payload struct {
		Models []json.RawMessage `json:"models"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil || len(payload.Models) == 0 {
		return CatalogResult{}, errors.New("router model catalog was invalid or empty")
	}
	if err = AtomicWrite(config.CatalogPath, raw, 0o600); err != nil {
		return CatalogResult{}, err
	}
	etag := strings.TrimSpace(response.Header.Get("ETag"))
	previousETag := config.CatalogETag
	updatedAt := time.Now().UTC()
	config.CatalogETag = etag
	config.CatalogUpdatedAt = &updatedAt
	if err = SaveConfig(configPath, *config); err != nil {
		return CatalogResult{}, err
	}
	restart := response.Header.Get("X-OpenCDX-Codex-Restart-Required") == "true" ||
		(previousETag != "" && etag != "" && previousETag != etag)
	return CatalogResult{Changed: true, ETag: etag, RestartRequired: restart, UpdatedAt: updatedAt}, nil
}

// catalogResponseError keeps the router's own reason. The router already
// sanitizes it, and without it a failed build is indistinguishable from an
// outage.
func catalogResponseError(response *http.Response) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	message := ""
	if json.Unmarshal(raw, &payload) == nil {
		message = strings.Join(strings.Fields(payload.Error.Message), " ")
	}
	if runes := []rune(message); len(runes) > 300 {
		message = string(runes[:300]) + "..."
	}
	if message == "" {
		message = response.Status
	}
	return errors.New("router model catalog is unavailable: " + message)
}

func CatalogExists(config Config) bool {
	info, err := os.Stat(filepath.Clean(config.CatalogPath))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
