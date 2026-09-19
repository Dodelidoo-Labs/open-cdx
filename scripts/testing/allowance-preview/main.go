// Creates a fresh, isolated preview database. Never reads local app state.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func main() {
	dir := flag.String("dir", "", "New preview data directory (must not exist)")
	flag.Parse()
	if *dir == "" {
		log.Fatal("--dir is required")
	}
	if err := os.Mkdir(*dir, 0700); err != nil {
		log.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.Fatal(err)
	}
	must(os.WriteFile(filepath.Join(*dir, "master_key"), []byte(hex.EncodeToString(key)), 0600))
	must(os.WriteFile(filepath.Join(*dir, "admin_token"), []byte("opencdx-allowance-preview-only"), 0600))
	box, err := secure.NewBox(key)
	must(err)
	store, err := storage.Open(filepath.Join(*dir, "router.db"), box)
	must(err)
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(5 * time.Minute)
	start := now.Add(-8 * 24 * time.Hour)
	devices := []string{"demo-laptop", "demo-workstation"}
	for i, id := range devices {
		_, err := store.Database().Exec(`INSERT INTO devices(id,name,status,enrollment_hash,created_at) VALUES(?,?,'approved',?,?)`, id, []string{"Demo laptop", "Demo workstation"}[i], []byte(id), start.Unix())
		must(err)
	}
	for accountIndex, label := range []string{"Demo · personal", "Demo · work"} {
		account, _, err := store.PutAccount(ctx, storage.AccountInput{Credential: storage.OpenAICredential{AccessToken: "synthetic-not-a-credential", RefreshToken: "synthetic-not-a-refresh-token", AccountID: fmt.Sprintf("preview-account-%d", accountIndex)}, MaskedEmail: label, Plan: "plus", Status: "ready"}, false)
		must(err)
		_, err = store.Database().Exec(`UPDATE accounts SET paused=1 WHERE id=?`, account.ID)
		must(err)
		// Two independent windows, regular polls, one outage, one burst, and resets.
		for at := start; !at.After(now); at = at.Add(5 * time.Minute) {
			elapsed := at.Sub(start).Hours()
			burst := math.Max(0, math.Min(1, (at.Sub(now.Add(-8*time.Hour)).Hours())))
			if accountIndex == 1 && elapsed > 70 && elapsed < 76 {
				continue
			}
			for _, hours := range []float64{168, 5} {
				phase := math.Mod(elapsed+float64(accountIndex)*hours/3, hours)
				reset := at.Add(time.Duration((hours - phase) * float64(time.Hour)))
				used := phase/hours*55 + float64(accountIndex)*5
				if accountIndex == 0 {
					used += burst * 28
				}
				must(store.RecordAllowanceObservation(ctx, storage.AllowanceObservation{AccountID: account.ID, ObservedAt: at, ResetAt: reset, Used: math.Min(99, used), WindowSeconds: int64(hours * 3600)}))
			}
			if at.Minute()%30 != 0 {
				continue
			}
			volume := int64(12000 + 9000*(1+math.Sin(elapsed*0.8)))
			if accountIndex == 0 && at.After(now.Add(-8*time.Hour)) && at.Before(now.Add(-7*time.Hour)) {
				volume *= 15
			}
			model := []string{"gpt-6-astra", "gpt-5.6-sol"}[accountIndex]
			_, err = store.Database().Exec(`INSERT INTO usage_aggregate(recorded_at,device_id,day,provider,model_id,account_id,source,routing,requests,input_tokens,output_tokens,cached_input_tokens) VALUES(?,?,?,'openai',?,?,'routed','routed',?,?,?,?)`, at.Format(time.RFC3339Nano), devices[accountIndex], at.Format("2006-01-02"), model, account.ID, volume/3000, volume, volume/5, volume/2)
			must(err)
		}
	}
	fmt.Println("Seeded synthetic accounts, machines, usage and allowance readings; all accounts paused.")
}
func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
