package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func (server *Server) adminInstructionHistory(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	q := request.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	page, err := server.store.InstructionHistory(request.Context(), storage.InstructionHistoryFilter{Before: before, Model: q.Get("model"), AccountID: q.Get("account"), Kind: q.Get("kind")})
	if err != nil {
		writeAPIError(writer, 500, "history_unavailable", "Instruction history could not be loaded")
		return
	}
	writeJSON(writer, 200, page)
}
func (server *Server) adminInstructionStatus(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	status, err := server.store.InstructionStatus(request.Context(), after)
	if err != nil {
		writeAPIError(writer, 500, "history_unavailable", "Instruction change notifications could not be loaded")
		return
	}
	writeJSON(writer, 200, status)
}
func (server *Server) adminInstructionRevision(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	id, _ := strconv.ParseInt(request.PathValue("id"), 10, 64)
	revision, err := server.store.InstructionRevision(request.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		writeAPIError(writer, 500, "history_unavailable", "Instruction revision could not be loaded")
		return
	}
	path := request.URL.Query().Get("path")
	if path == "" {
		writeJSON(writer, 200, revision)
		return
	}
	for _, change := range revision.Changes {
		if change.Path != path {
			continue
		}
		before, err := server.store.InstructionField(request.Context(), change.BeforeHash)
		if err != nil {
			writeAPIError(writer, 500, "history_unavailable", "Previous instruction content could not be loaded")
			return
		}
		after, err := server.store.InstructionField(request.Context(), change.AfterHash)
		if err != nil {
			writeAPIError(writer, 500, "history_unavailable", "Updated instruction content could not be loaded")
			return
		}
		writeJSON(writer, 200, map[string]any{"path": path, "before": before, "after": after})
		return
	}
	http.NotFound(writer, request)
}
