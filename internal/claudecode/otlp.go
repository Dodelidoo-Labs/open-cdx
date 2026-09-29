package claudecode

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
)

// LogBatch is what one OTLP/HTTP JSON logs export contributes: completed API
// requests plus the session-to-account mapping seen on any event. Every other
// event, including prompt and response events, is discarded unread.
type LogBatch struct {
	Requests []Request
	Accounts []Account
	// Sessions maps Claude Code session IDs to account identities so status
	// line readings, which carry no account, can be attributed.
	Sessions map[string]string
}

type otlpLogs struct {
	ResourceLogs []struct {
		ScopeLogs []struct {
			LogRecords []struct {
				TimeUnixNano         json.RawMessage `json:"timeUnixNano"`
				ObservedTimeUnixNano json.RawMessage `json:"observedTimeUnixNano"`
				Body                 otlpValue       `json:"body"`
				Attributes           []struct {
					Key   string    `json:"key"`
					Value otlpValue `json:"value"`
				} `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

// otlpValue decodes the scalar AnyValue variants. OTLP/JSON encodes 64-bit
// integers as strings, but some exporters emit JSON numbers.
type otlpValue struct {
	StringValue *string         `json:"stringValue"`
	IntValue    json.RawMessage `json:"intValue"`
}

func (value otlpValue) text() string {
	if value.StringValue == nil {
		return ""
	}
	return *value.StringValue
}

func (value otlpValue) integer() (int64, bool) {
	raw := strings.Trim(string(value.IntValue), `"`)
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	return parsed, err == nil && parsed >= 0
}

// ParseOTLPLogs decodes one export. Records without any timestamp are dated
// at receipt, which is when the exporter flushed them.
func ParseOTLPLogs(body []byte, received time.Time) (LogBatch, error) {
	var payload otlpLogs
	if err := json.Unmarshal(body, &payload); err != nil {
		return LogBatch{}, errors.New("OTLP logs payload is not valid JSON")
	}
	batch := LogBatch{Sessions: make(map[string]string)}
	accounts := make(map[string]string)
	for _, resource := range payload.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			for _, record := range scope.LogRecords {
				attributes := make(map[string]otlpValue, len(record.Attributes))
				for _, attribute := range record.Attributes {
					attributes[attribute.Key] = attribute.Value
				}
				identity := AccountIdentity(attributes["user.account_uuid"].text())
				if identity != "" {
					if _, known := accounts[identity]; !known || accounts[identity] == "" {
						accounts[identity] = maskedEmail(attributes["user.email"].text())
					}
					if session := attributes["session.id"].text(); session != "" {
						batch.Sessions[session] = identity
					}
				}
				name := attributes["event.name"].text()
				if name != "api_request" && record.Body.text() != "claude_code.api_request" {
					continue
				}
				if request, ok := requestFromEvent(attributes, identity, received, record.TimeUnixNano, record.ObservedTimeUnixNano); ok {
					batch.Requests = append(batch.Requests, request)
				}
			}
		}
	}
	for identity, email := range accounts {
		batch.Accounts = append(batch.Accounts, Account{Identity: identity, MaskedEmail: email})
	}
	return batch, nil
}

func requestFromEvent(attributes map[string]otlpValue, identity string, received time.Time, times ...json.RawMessage) (Request, bool) {
	id := attributes["request_id"].text()
	if id == "" {
		if client := attributes["client_request_id"].text(); client != "" {
			id = "client:" + client
		}
	}
	model := strings.TrimSpace(attributes["model"].text())
	if id == "" || model == "" {
		return Request{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, attributes["event.timestamp"].text())
	for _, raw := range times {
		if err == nil {
			break
		}
		if nanos, parseErr := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64); parseErr == nil && nanos > 0 {
			at, err = time.Unix(0, nanos), nil
		}
	}
	if err != nil {
		at = received
	}
	request := Request{ID: id, At: at.UTC().Format(time.RFC3339Nano), Model: model, Account: identity}
	for key, target := range map[string]*int64{
		"input_tokens": &request.InputTokens, "output_tokens": &request.OutputTokens,
		"cache_read_tokens": &request.CacheReadTokens, "cache_creation_tokens": &request.CacheCreationTokens,
	} {
		if value, ok := attributes[key].integer(); ok {
			*target = value
		}
	}
	return request, true
}

func maskedEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	return openai.MaskEmail(email)
}
