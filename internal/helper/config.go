package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DefaultPort           = 17464
	ApplicationIdentifier = "com.dodelidoo.opencdx"
	HelperIdentifier      = ApplicationIdentifier + ".helper"
	URLScheme             = ApplicationIdentifier
)

type Config struct {
	RouterURL           string `json:"router_url"`
	DeviceID            string `json:"device_id,omitempty"`
	DeviceName          string `json:"device_name,omitempty"`
	ListenPort          int    `json:"listen_port"`
	CatalogPath         string `json:"catalog_path"`
	CatalogETag         string `json:"catalog_etag,omitempty"`
	InsecureDevelopment bool   `json:"insecure_development,omitempty"`
}

func DefaultConfigPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, ApplicationIdentifier, "helper.json"), nil
}

func DefaultCatalogPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, ApplicationIdentifier, "catalog.json"), nil
}

func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err = json.Unmarshal(raw, &config); err != nil {
		return Config{}, errors.New("helper configuration is invalid")
	}
	if config.ListenPort == 0 {
		config.ListenPort = DefaultPort
	}
	if err = config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func SaveConfig(path string, config Config) error {
	if config.ListenPort == 0 {
		config.ListenPort = DefaultPort
	}
	if config.CatalogPath == "" {
		catalogPath, err := DefaultCatalogPath()
		if err != nil {
			return err
		}
		config.CatalogPath = catalogPath
	}
	if err := config.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, append(encoded, '\n'), 0o600)
}

func (config Config) Validate() error {
	parsed, err := url.Parse(config.RouterURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("router URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("router URL must not contain credentials, query parameters, or a fragment")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return errors.New("router URL must not contain a path")
	}
	if parsed.Scheme == "http" && !isLoopback(parsed.Hostname()) && !config.InsecureDevelopment {
		return errors.New("a non-loopback router URL must use HTTPS; --insecure-dev is for development only")
	}
	if config.ListenPort < 1024 || config.ListenPort > 65535 {
		return errors.New("helper listen port must be between 1024 and 65535")
	}
	if config.CatalogPath == "" {
		return errors.New("catalog path is required")
	}
	return nil
}

func (config Config) LocalBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", config.ListenPort)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func RuntimePlatform() string { return runtime.GOOS }
