package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RemoteClient struct {
	BaseURL     string
	DeviceToken string
	HTTP        *http.Client
}

type EnrollmentResponse struct {
	DeviceID         string `json:"device_id"`
	EnrollmentSecret string `json:"enrollment_secret"`
	Status           string `json:"status"`
	DeviceToken      string `json:"device_token,omitempty"`
}

type OAuthStartResponse struct {
	TransactionID    string    `json:"transaction_id"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func NewRemoteClient(config Config, deviceToken string) (*RemoteClient, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &RemoteClient{
		BaseURL: strings.TrimRight(config.RouterURL, "/"), DeviceToken: deviceToken,
		HTTP: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func (client *RemoteClient) JSON(ctx context.Context, method, path string, input, output any, authenticated bool) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		if client.DeviceToken == "" {
			return nil, errors.New("this helper is not paired with the router")
		}
		request.Header.Set("Authorization", "Bearer "+client.DeviceToken)
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, errors.New("remote router is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload)
		message := payload.Error.Message
		if message == "" {
			message = response.Status
		}
		return response, fmt.Errorf("router request failed: %s", message)
	}
	if output != nil {
		if err = json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(output); err != nil {
			return response, errors.New("router returned an invalid response")
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	}
	return response, nil
}

func (client *RemoteClient) URL(path string) string {
	base, _ := url.Parse(client.BaseURL)
	reference, _ := url.Parse(path)
	return base.ResolveReference(reference).String()
}
