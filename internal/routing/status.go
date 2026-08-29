package routing

import (
	"sync"
	"time"
)

type RouteStatus struct {
	Connected       bool      `json:"connected"`
	State           string    `json:"state"`
	Provider        string    `json:"provider,omitempty"`
	Model           string    `json:"model,omitempty"`
	Account         string    `json:"account,omitempty"`
	QuotaRemaining  float64   `json:"quota_remaining,omitempty"`
	QuotaResetAt    time.Time `json:"quota_reset_at,omitempty"`
	CatalogHash     string    `json:"catalog_hash,omitempty"`
	CatalogUpdated  time.Time `json:"catalog_updated_at,omitempty"`
	RestartRequired bool      `json:"codex_restart_required,omitempty"`
	LastRequestAt   time.Time `json:"last_request_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

type StatusRegistry struct {
	mutex  sync.RWMutex
	status map[string]RouteStatus
}

func NewStatusRegistry() *StatusRegistry {
	return &StatusRegistry{status: make(map[string]RouteStatus)}
}

func (registry *StatusRegistry) Get(deviceID string) RouteStatus {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	status := registry.status[deviceID]
	if status.State == "" {
		status.State = "connected"
		status.Connected = true
	}
	return status
}

func (registry *StatusRegistry) Update(deviceID string, update func(*RouteStatus)) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	status := registry.status[deviceID]
	update(&status)
	registry.status[deviceID] = status
}
