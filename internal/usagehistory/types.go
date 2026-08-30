package usagehistory

const SnapshotVersion = 2

const (
	RoutingRouted = "routed"
	RoutingNative = "native"
)

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
	Routing               string `json:"routing"`
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

// ReconciliationPreview is a compact, local-only summary used to confirm a
// destructive telemetry replacement. It deliberately omits rollout paths and
// individual aggregate rows.
type ReconciliationPreview struct {
	FilesScanned    int   `json:"files_scanned"`
	EventsImported  int   `json:"events_imported"`
	RowsFound       int   `json:"rows_found"`
	RoutedRequests  int64 `json:"routed_requests"`
	NativeRequests  int64 `json:"native_requests"`
	DuplicateEvents int   `json:"duplicate_events_skipped"`
	MalformedLines  int   `json:"malformed_lines_skipped"`
}

func Preview(snapshot Snapshot) ReconciliationPreview {
	preview := ReconciliationPreview{
		FilesScanned: snapshot.FilesScanned, EventsImported: snapshot.EventsImported,
		RowsFound: len(snapshot.Rows), DuplicateEvents: snapshot.DuplicateEvents,
		MalformedLines: snapshot.MalformedLines,
	}
	for _, row := range snapshot.Rows {
		if row.Routing == RoutingRouted {
			preview.RoutedRequests += row.Requests
		} else {
			preview.NativeRequests += row.Requests
		}
	}
	return preview
}
