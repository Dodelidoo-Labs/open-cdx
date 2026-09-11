package httpapi

import (
	"net/http"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
)

func (server *Server) consumeAccountReset(writer http.ResponseWriter, request *http.Request) {
	var input openai.ConsumeResetRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := input.Validate(); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_reset_request", err.Error())
		return
	}
	if _, err := server.store.Account(request.Context(), request.PathValue("id"), false); err != nil {
		writeAPIError(writer, http.StatusNotFound, "account_not_found", "Account no longer exists.")
		return
	}
	result, err := server.accounts.ConsumeReset(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeAPIError(writer, http.StatusBadGateway, "reset_failed", "Could not confirm the reset. Retry with the same idempotency key.")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
