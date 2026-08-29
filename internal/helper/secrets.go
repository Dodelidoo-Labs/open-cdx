package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const keychainService = "com.opencdx.router-helper"

type SecretStore interface {
	Get(account string) (string, error)
	Set(account, value string) error
	Delete(account string) error
}

func NewSecretStore(configPath string) SecretStore {
	if runtime.GOOS == "darwin" {
		return macKeychain{}
	}
	return &fileSecrets{path: filepath.Join(filepath.Dir(configPath), "device-secrets.json")}
}

type macKeychain struct{}

func (macKeychain) Get(account string) (string, error) {
	command := exec.Command("/usr/bin/security", "find-generic-password", "-a", account, "-s", keychainService, "-w")
	output, err := command.Output()
	if err != nil {
		return "", errors.New("secret was not found in macOS Keychain")
	}
	return strings.TrimSpace(string(output)), nil
}

func (macKeychain) Set(account, value string) error {
	if value == "" {
		return errors.New("refusing to store an empty secret")
	}
	// Keeping -w last makes security read the password from stdin instead of
	// placing it in the process argument list. For a newly created item,
	// security asks for the value and its confirmation; provide both.
	command := exec.Command("/usr/bin/security", "add-generic-password", "-a", account, "-s", keychainService, "-U", "-w")
	command.Stdin = strings.NewReader(value + "\n" + value + "\n")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("store secret in macOS Keychain: %s", strings.TrimSpace(string(output)))
	}
	stored, err := (macKeychain{}).Get(account)
	if err != nil || stored != value {
		return errors.New("verify secret in macOS Keychain: stored value did not match")
	}
	return nil
}

func (macKeychain) Delete(account string) error {
	command := exec.Command("/usr/bin/security", "delete-generic-password", "-a", account, "-s", keychainService)
	if err := command.Run(); err != nil {
		return errors.New("secret was not found in macOS Keychain")
	}
	return nil
}

type fileSecrets struct{ path string }

func (store *fileSecrets) Get(account string) (string, error) {
	values, err := store.load()
	if err != nil {
		return "", err
	}
	value := values[account]
	if value == "" {
		return "", errors.New("secret was not found")
	}
	return value, nil
}

func (store *fileSecrets) Set(account, value string) error {
	values, err := store.loadAllowMissing()
	if err != nil {
		return err
	}
	values[account] = value
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return AtomicWrite(store.path, encoded, 0o600)
}

func (store *fileSecrets) Delete(account string) error {
	values, err := store.load()
	if err != nil {
		return err
	}
	delete(values, account)
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return AtomicWrite(store.path, encoded, 0o600)
}

func (store *fileSecrets) load() (map[string]string, error) {
	raw, err := os.ReadFile(store.path)
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err = json.Unmarshal(raw, &values); err != nil {
		return nil, errors.New("local device secret store is invalid")
	}
	return values, nil
}

func (store *fileSecrets) loadAllowMissing() (map[string]string, error) {
	values, err := store.load()
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]string), nil
	}
	return values, err
}
