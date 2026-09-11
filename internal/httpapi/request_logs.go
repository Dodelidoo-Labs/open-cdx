package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

type requestLogBackup struct {
	Version int                `json:"version"`
	Log     storage.RequestLog `json:"log"`
}

func logFilter(request *http.Request) storage.RequestLogFilter {
	q := request.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	return storage.RequestLogFilter{Before: before, Limit: 50, Provider: q.Get("provider"), Model: q.Get("model"), DeviceID: q.Get("device"), Outcome: q.Get("outcome")}
}
func (server *Server) adminRequestLogs(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	page, err := server.store.RequestLogs(request.Context(), logFilter(request))
	if err != nil {
		writeAPIError(writer, 500, "logs_unavailable", "Request logs could not be loaded")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(page)
}
func (server *Server) adminRequestLog(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	entry, err := server.store.RequestLog(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		writeAPIError(writer, 500, "logs_unavailable", "Request details could not be loaded")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(entry)
}
func (server *Server) adminExportRequestLogs(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	filter := storage.RequestLogFilter{Limit: 500}
	// Fetch before sending download headers, then release the database between pages.
	page, err := server.store.RequestLogs(request.Context(), filter)
	if err != nil {
		writeAPIError(writer, 500, "logs_unavailable", "Request logs could not be exported")
		return
	}
	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.Header().Set("Content-Disposition", `attachment; filename="opencdx-request-logs.ndjson"`)
	encoder := json.NewEncoder(writer)
	for {
		for _, entry := range page.Logs {
			if err = encoder.Encode(requestLogBackup{Version: 1, Log: entry}); err != nil {
				return
			}
		}
		if page.NextBefore == 0 {
			return
		}
		filter.Before = page.NextBefore
		page, err = server.store.RequestLogs(request.Context(), filter)
		if err != nil {
			panic(http.ErrAbortHandler)
		} // Never present a truncated backup as a successful download.
	}
}
func (server *Server) adminImportRequestLogs(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 8<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var backup []requestLogBackup
	if err := decoder.Decode(&backup); err != nil {
		writeAPIError(writer, 400, "invalid_backup", "Expected a JSON batch of request logs (maximum 8 MiB)")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || len(backup) > 250 {
		writeAPIError(writer, 400, "invalid_backup", "Import at most 250 logs per batch")
		return
	}
	entries := make([]storage.RequestLog, 0, len(backup))
	for _, record := range backup {
		if record.Version != 1 {
			writeAPIError(writer, 400, "invalid_backup", "Unsupported request log backup version")
			return
		}
		entries = append(entries, record.Log)
	}
	count, err := server.store.ImportRequestLogs(request.Context(), entries)
	if err != nil {
		writeAPIError(writer, 400, "import_failed", "Request logs could not be imported; check the backup format")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]int64{"imported": count, "duplicates": int64(len(entries)) - count})
}
