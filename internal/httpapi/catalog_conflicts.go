package httpapi

import (
	"net/http"

	"github.com/Dodelidoo-Labs/open-cdx/internal/catalog"
)

func (server *Server) adminCatalogConflict(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	model := request.URL.Query().Get("model")
	if model == "" {
		writeAPIError(writer, 400, "missing_model", "Choose a model to inspect its catalog discrepancies")
		return
	}
	accounts, err := server.store.Accounts(request.Context(), false)
	if err != nil {
		writeAPIError(writer, 500, "catalog_unavailable", "Stored account catalogs could not be loaded")
		return
	}
	conflicts, err := catalog.NativeConflicts(accounts)
	if err != nil {
		writeAPIError(writer, 500, "catalog_unavailable", "Stored account catalogs could not be compared")
		return
	}
	for _, conflict := range conflicts {
		if conflict.Model == model {
			writeJSON(writer, http.StatusOK, conflict)
			return
		}
	}
	writeAPIError(writer, 404, "conflict_resolved", "This model no longer has a conflict in the stored account catalogs. Reload the page to update the notice.")
}
