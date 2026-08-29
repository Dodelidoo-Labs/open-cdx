package usagehistory

const SnapshotVersion = 1

// Snapshot is the complete, privacy-minimal usage history sent by the local
// helper to the router. It intentionally has no field capable of carrying
// prompts, responses, file paths, credentials, or account identifiers.
type Snapshot struct {
	Version         int    `json:"version"`
	GeneratedAt     string `json:"generated_at"`
	FilesScanned    int    `json:"files_scanned"`
	EventsImported  int    `json:"events_imported"`
	DuplicateEvents int    `json:"duplicate_events_skipped"`
	MalformedLines  int    `json:"malformed_lines_skipped"`
	Rows            []Row  `json:"rows"`
}

type Row struct {
	Day                   string `json:"day"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	Requests              int64  `json:"requests"`
	InputTokens           int64  `json:"input_tokens"`
	CachedInputTokens     int64  `json:"cached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningOutputTokens int64  `json:"reasoning_output_tokens"`
}

type Result struct {
	ReconciledAt   string `json:"reconciled_at"`
	FilesScanned   int    `json:"files_scanned"`
	EventsImported int    `json:"events_imported"`
	RowsImported   int    `json:"rows_imported"`
}
