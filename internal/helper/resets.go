package helper

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/Dodelidoo-Labs/open-cdx/internal/accounts"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
)

func (daemon *Daemon) controlConsumeReset(writer http.ResponseWriter, request *http.Request) {
	var input openai.ConsumeResetRequest
	if json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4096)).Decode(&input) != nil || input.Validate() != nil {
		writeHelperJSON(writer, http.StatusBadRequest, map[string]string{"error": "Invalid reset request"})
		return
	}
	var result accounts.ResetResult
	_, err := daemon.remote.JSON(request.Context(), http.MethodPost, "/api/v1/accounts/"+url.PathEscape(request.PathValue("id"))+"/resets/consume", input, &result, true)
	if err != nil {
		writeHelperJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// An older in-flight status read must finish before this refresh so it
	// cannot restore a redeemed ticket in the HUD.
	if err := daemon.refreshStatus(request.Context()); err != nil && result.Outcome != "nothingToReset" {
		daemon.updateStatus(func(status *LocalStatus) {
			status.Accounts = append([]AccountAllowance(nil), status.Accounts...)
			for i := range status.Accounts {
				if status.Accounts[i].ID == request.PathValue("id") {
					status.Accounts[i].ResetCredits = 0
					status.Accounts[i].ResetTickets = nil
				}
			}
		})
	}
	writeHelperJSON(writer, http.StatusOK, struct {
		accounts.ResetResult
		Status LocalStatus `json:"status"`
	}{result, daemon.currentStatus()})
}
