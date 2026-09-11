package main

import (
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
)

func consumeReset(configPath string, args []string) error {
	flags := flag.NewFlagSet("consume-reset", flag.ContinueOnError)
	accountID := flags.String("account", "", "OpenCDX account ID")
	key := flags.String("idempotency-key", "", "unique redemption key; reuse it when retrying")
	creditID := flags.String("credit", "", "optional earned reset ID")
	confirmed := flags.Bool("confirm", false, "confirm consuming ONE earned reset")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*confirmed || strings.TrimSpace(*accountID) == "" || flags.NArg() != 0 {
		return errors.New("consume-reset requires --account ID --idempotency-key KEY --confirm and optionally --credit ID")
	}
	input := openai.ConsumeResetRequest{IdempotencyKey: *key, CreditID: *creditID}
	if err := input.Validate(); err != nil {
		return err
	}
	body, _ := json.Marshal(input)
	return controlRequest(configPath, http.MethodPost, "/control/accounts/"+url.PathEscape(*accountID)+"/resets/consume", body, os.Stdout)
}
