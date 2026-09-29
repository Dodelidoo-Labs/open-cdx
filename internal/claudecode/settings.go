package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	StatusLineSubcommand = "claude-statusline"
	HeadersSubcommand    = "claude-otel-headers"
	OTLPLogsPath         = "/claude/otlp/v1/logs"
)

// Integration describes the OpenCDX entries in Claude Code's user settings.
// HelperCommand is a shell-quoted router-helper invocation.
type Integration struct {
	HelperCommand string
	Port          int
}

func (integration Integration) StatusLineCommand() string {
	return integration.HelperCommand + " " + StatusLineSubcommand
}

func (integration Integration) HeadersCommand() string {
	return integration.HelperCommand + " " + HeadersSubcommand
}

func (integration Integration) Environment() [][2]string {
	return [][2]string{
		{"CLAUDE_CODE_ENABLE_TELEMETRY", "1"},
		{"OTEL_LOGS_EXPORTER", "otlp"},
		{"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json"},
		{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", fmt.Sprintf("http://127.0.0.1:%d%s", integration.Port, OTLPLogsPath)},
	}
}

// ConflictError lists user settings that OpenCDX will not overwrite.
type ConflictError struct{ Conflicts []string }

func (err *ConflictError) Error() string {
	return "Claude Code settings already configure telemetry: " + strings.Join(err.Conflicts, "; ")
}

// Plan is a proposed settings edit. Original holds the status line object
// that the edit replaces, which Remove later restores.
type Plan struct {
	Settings         []byte          `json:"-"`
	Original         json.RawMessage `json:"-"`
	Changes          []string        `json:"changes"`
	AlreadyInstalled bool            `json:"already_installed"`
	WrapsStatusLine  string          `json:"wraps_status_line,omitempty"`
}

// ShellQuote quotes one argument for the POSIX shell that runs Claude Code's
// status line and headers helper commands.
func ShellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+=:,@", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// Installed reports whether settings contain any OpenCDX telemetry entry.
func Installed(current []byte) (bool, error) {
	document, err := parseObject(current)
	if err != nil {
		return false, err
	}
	statusLine, _ := document.object("statusLine")
	env, _ := document.object("env")
	return statusLine != nil && isOurCommand(statusLine.text("command"), StatusLineSubcommand) ||
		isOurCommand(document.text("otelHeadersHelper"), HeadersSubcommand) ||
		env != nil && strings.HasSuffix(env.text("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"), OTLPLogsPath), nil
}

// Install adds the status line wrapper and OpenTelemetry log export. It
// refuses to replace telemetry settings that belong to the user.
func Install(current []byte, integration Integration) (Plan, error) {
	document, err := parseObject(current)
	if err != nil {
		return Plan{}, err
	}
	env, err := document.object("env")
	if err != nil {
		return Plan{}, err
	}
	statusLine, err := document.object("statusLine")
	if err != nil {
		return Plan{}, err
	}
	var conflicts []string
	if helper := document.text("otelHeadersHelper"); document.has("otelHeadersHelper") && !isOurCommand(helper, HeadersSubcommand) {
		conflicts = append(conflicts, "otelHeadersHelper is already set")
	}
	desired := integration.Environment()
	if env != nil {
		for _, member := range env.members {
			if !telemetryVariable(member.key) {
				continue
			}
			ours := false
			for _, pair := range desired {
				ours = ours || pair[0] == member.key && (env.text(member.key) == pair[1] || member.key == "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT" && strings.HasSuffix(env.text(member.key), OTLPLogsPath))
			}
			if !ours {
				conflicts = append(conflicts, "env."+member.key+" is already set")
			}
		}
	}
	if len(conflicts) > 0 {
		return Plan{}, &ConflictError{Conflicts: conflicts}
	}
	before := document.compact()
	plan := Plan{}
	if statusLine == nil || !isOurCommand(statusLine.text("command"), StatusLineSubcommand) {
		if statusLine != nil {
			plan.Original = append(json.RawMessage(nil), document.raw("statusLine")...)
			if statusLine.text("type") == "command" {
				plan.WrapsStatusLine = statusLine.text("command")
			}
			plan.Changes = append(plan.Changes, "Wrap the existing status line; its output is unchanged")
		} else {
			statusLine = &object{}
			plan.Changes = append(plan.Changes, "Add a status line command that reports plan allowance and prints nothing")
		}
	} else if statusLine.text("command") != integration.StatusLineCommand() {
		plan.Changes = append(plan.Changes, "Update the OpenCDX status line command")
	}
	statusLine.set("type", "command")
	statusLine.set("command", integration.StatusLineCommand())
	document.setObject("statusLine", statusLine)
	if document.text("otelHeadersHelper") != integration.HeadersCommand() {
		plan.Changes = append(plan.Changes, "Set otelHeadersHelper to issue short-lived local credentials")
		document.set("otelHeadersHelper", integration.HeadersCommand())
	}
	if env == nil {
		env = &object{}
	}
	envChanged := false
	for _, pair := range desired {
		if env.text(pair[0]) != pair[1] {
			env.set(pair[0], pair[1])
			envChanged = true
		}
	}
	if envChanged {
		plan.Changes = append(plan.Changes, "Export Claude Code API request events (no prompts or responses) to the local OpenCDX helper")
	}
	document.setObject("env", env)
	plan.AlreadyInstalled = bytes.Equal(before, document.compact())
	plan.Settings = document.encode()
	return plan, nil
}

// Remove deletes only OpenCDX entries and restores the wrapped status line.
func Remove(current []byte, original json.RawMessage) (Plan, error) {
	document, err := parseObject(current)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{}
	if statusLine, err := document.object("statusLine"); err == nil && statusLine != nil && isOurCommand(statusLine.text("command"), StatusLineSubcommand) {
		if len(bytes.TrimSpace(original)) > 0 && string(bytes.TrimSpace(original)) != "null" {
			if _, err := parseObject(original); err != nil {
				return Plan{}, errors.New("saved status line is invalid")
			}
			document.set("statusLine", json.RawMessage(original))
			plan.Changes = append(plan.Changes, "Restore the original status line")
		} else {
			document.remove("statusLine")
			plan.Changes = append(plan.Changes, "Remove the OpenCDX status line")
		}
	}
	if isOurCommand(document.text("otelHeadersHelper"), HeadersSubcommand) {
		document.remove("otelHeadersHelper")
		plan.Changes = append(plan.Changes, "Remove the OpenCDX otelHeadersHelper")
	}
	if env, err := document.object("env"); err == nil && env != nil && strings.HasSuffix(env.text("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"), OTLPLogsPath) {
		for _, pair := range (Integration{}).Environment() {
			env.remove(pair[0])
		}
		if len(env.members) == 0 {
			document.remove("env")
		} else {
			document.setObject("env", env)
		}
		plan.Changes = append(plan.Changes, "Stop exporting Claude Code telemetry to OpenCDX")
	}
	plan.AlreadyInstalled = len(plan.Changes) == 0
	plan.Settings = document.encode()
	return plan, nil
}

func telemetryVariable(name string) bool {
	return name == "CLAUDE_CODE_ENABLE_TELEMETRY" || strings.HasPrefix(name, "OTEL_")
}

func isOurCommand(command, subcommand string) bool {
	return strings.Contains(command, "router-helper") && strings.HasSuffix(strings.TrimSpace(command), " "+subcommand)
}

// object is a JSON object that keeps its member order, so edits leave the
// rest of the user's settings file recognizable.
type object struct{ members []member }

type member struct {
	key   string
	value json.RawMessage
}

func parseObject(data []byte) (*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &object{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if delimiter, ok := token.(json.Delim); err != nil || !ok || delimiter != '{' {
		return nil, errors.New("settings must contain a JSON object")
	}
	result := &object{}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, errors.New("settings contain invalid JSON")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, errors.New("settings contain invalid JSON")
		}
		result.set(key, value)
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("settings contain invalid JSON")
	}
	if _, err = decoder.Token(); err == nil {
		return nil, errors.New("settings contain data after the JSON object")
	}
	return result, nil
}

func (o *object) index(key string) int {
	for index, member := range o.members {
		if member.key == key {
			return index
		}
	}
	return -1
}

func (o *object) has(key string) bool { return o.index(key) >= 0 }

func (o *object) raw(key string) json.RawMessage {
	if index := o.index(key); index >= 0 {
		return o.members[index].value
	}
	return nil
}

func (o *object) text(key string) string {
	var value string
	if raw := o.raw(key); raw != nil && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func (o *object) object(key string) (*object, error) {
	raw := o.raw(key)
	if raw == nil || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("settings key %s is not an object", strconv.Quote(key))
	}
	return parseObject(raw)
}

func (o *object) set(key string, value any) {
	raw, ok := value.(json.RawMessage)
	if !ok {
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(value)
		raw = bytes.TrimSpace(buffer.Bytes())
	}
	if index := o.index(key); index >= 0 {
		o.members[index].value = raw
		return
	}
	o.members = append(o.members, member{key: key, value: raw})
}

func (o *object) setObject(key string, value *object) { o.set(key, json.RawMessage(value.compact())) }

func (o *object) remove(key string) {
	if index := o.index(key); index >= 0 {
		o.members = append(o.members[:index], o.members[index+1:]...)
	}
}

func (o *object) compact() []byte {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, member := range o.members {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, _ := json.Marshal(member.key)
		buffer.Write(key)
		buffer.WriteByte(':')
		_ = json.Compact(&buffer, member.value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes()
}

// encode matches JSON.stringify(value, null, 2), which Claude Code uses.
func (o *object) encode() []byte {
	var buffer bytes.Buffer
	_ = json.Indent(&buffer, o.compact(), "", "  ")
	buffer.WriteByte('\n')
	return buffer.Bytes()
}
