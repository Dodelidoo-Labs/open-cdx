package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/helper"
	"github.com/Dodelidoo-Labs/open-cdx/internal/usagehistory"
	"github.com/Dodelidoo-Labs/open-cdx/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "router-helper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	configPath, remaining, err := globalArgs(args)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return usageError()
	}
	command := remaining[0]
	commandArgs := remaining[1:]
	switch command {
	case "enroll":
		return enroll(configPath, commandArgs)
	case "pair":
		return finishPairing(configPath, commandArgs)
	case "daemon":
		return daemon(configPath, commandArgs)
	case "token":
		return token(configPath, commandArgs)
	case "status":
		return control(configPath, http.MethodGet, "/control/status", commandArgs, os.Stdout)
	case "reconnect":
		return control(configPath, http.MethodPost, "/control/reconnect", commandArgs, os.Stdout)
	case "refresh-catalog":
		return control(configPath, http.MethodPost, "/control/catalog/refresh", commandArgs, os.Stdout)
	case "acknowledge-restart":
		return control(configPath, http.MethodPost, "/control/catalog/restart-ack", commandArgs, os.Stdout)
	case "refresh-quotas":
		return control(configPath, http.MethodPost, "/control/quotas/refresh", commandArgs, os.Stdout)
	case "reconcile-usage":
		return reconcileUsage(configPath, commandArgs)
	case "reset-telemetry":
		return resetTelemetry(configPath, commandArgs)
	case "quit":
		return control(configPath, http.MethodPost, "/control/quit", commandArgs, io.Discard)
	case "login-openai":
		return loginOpenAI(configPath, commandArgs)
	case "sync-catalog":
		return syncCatalog(configPath, commandArgs)
	case "config":
		return printConfig(configPath, commandArgs)
	case "open-dashboard":
		return openDashboard(configPath, commandArgs)
	case "version", "--version", "-version":
		fmt.Printf("router-helper %s (%s)\n", version.Version, version.Commit)
		return nil
	default:
		return usageError()
	}
}

func globalArgs(args []string) (string, []string, error) {
	path := strings.TrimSpace(os.Getenv("OPENCODEX_HELPER_CONFIG"))
	if path == "" {
		var err error
		path, err = helper.DefaultConfigPath()
		if err != nil {
			return "", nil, err
		}
	}
	if len(args) >= 2 && args[0] == "--config" {
		if strings.TrimSpace(args[1]) == "" {
			return "", nil, errors.New("--config requires a path")
		}
		path, args = args[1], args[2:]
	}
	return path, args, nil
}

func enroll(configPath string, args []string) error {
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	routerURL := flags.String("router", "", "remote router URL")
	name := flags.String("name", defaultDeviceName(), "device name")
	port := flags.Int("port", helper.DefaultPort, "loopback Responses port")
	insecure := flags.Bool("insecure-dev", false, "allow plaintext LAN router URL for development")
	noWait := flags.Bool("no-wait", false, "return after requesting administrator approval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	catalogPath, err := helper.DefaultCatalogPath()
	if err != nil {
		return err
	}
	config := helper.Config{RouterURL: strings.TrimRight(*routerURL, "/"), DeviceName: *name, ListenPort: *port, CatalogPath: catalogPath, InsecureDevelopment: *insecure}
	if err = config.Validate(); err != nil {
		return err
	}
	secrets := helper.NewSecretStore(configPath)
	if _, getErr := secrets.Get("local-token-secret"); getErr != nil {
		localSecret, randomErr := secure.RandomURLSafe(36)
		if randomErr != nil {
			return randomErr
		}
		if err = secrets.Set("local-token-secret", localSecret); err != nil {
			return err
		}
	}
	client, err := helper.NewRemoteClient(config, "")
	if err != nil {
		return err
	}
	var enrollment helper.EnrollmentResponse
	for attempt := 0; attempt < 6; attempt++ {
		_, err = client.JSON(context.Background(), http.MethodPost, "/api/v1/enroll", map[string]string{"name": config.DeviceName}, &enrollment, false)
		if err == nil || runtime.GOOS != "darwin" || !strings.Contains(err.Error(), "remote router is unreachable") {
			break
		}
		// macOS local-network privacy attributes this child process to the menu
		// app. Keep it alive long enough for the first-use permission alert.
		time.Sleep(time.Second)
	}
	if err != nil {
		return err
	}
	config.DeviceID = enrollment.DeviceID
	if err = helper.SaveConfig(configPath, config); err != nil {
		return err
	}
	if err = secrets.Set("enrollment-secret", enrollment.EnrollmentSecret); err != nil {
		return err
	}
	fmt.Println("Enrollment requested. Approve this device in the router dashboard.")
	if *noWait {
		return nil
	}
	return waitForApproval(context.Background(), configPath, config, secrets, enrollment.EnrollmentSecret)
}

func finishPairing(configPath string, args []string) error {
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	timeout := flags.Duration("timeout", 10*time.Minute, "approval wait timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return err
	}
	secrets := helper.NewSecretStore(configPath)
	enrollmentSecret, err := secrets.Get("enrollment-secret")
	if err != nil {
		return errors.New("no pending enrollment was found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return waitForApproval(ctx, configPath, config, secrets, enrollmentSecret)
}

func waitForApproval(ctx context.Context, configPath string, config helper.Config, secrets helper.SecretStore, enrollmentSecret string) error {
	client, err := helper.NewRemoteClient(config, "")
	if err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var enrollment helper.EnrollmentResponse
		_, requestErr := client.JSON(ctx, http.MethodPost, "/api/v1/enroll/status", map[string]string{"device_id": config.DeviceID, "enrollment_secret": enrollmentSecret}, &enrollment, false)
		if requestErr == nil && enrollment.DeviceToken != "" {
			if err = secrets.Set("device-token", enrollment.DeviceToken); err != nil {
				return err
			}
			_, ackErr := client.JSON(ctx, http.MethodPost, "/api/v1/enroll/ack", map[string]string{"device_id": config.DeviceID, "enrollment_secret": enrollmentSecret}, nil, false)
			if ackErr != nil {
				return ackErr
			}
			_ = secrets.Delete("enrollment-secret")
			fmt.Println("Device approved and credential stored locally.")
			return nil
		}
		if requestErr != nil && !strings.Contains(requestErr.Error(), "pending") {
			return requestErr
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out waiting for administrator approval; run pair to resume")
		case <-ticker.C:
		}
	}
}

func daemon(configPath string, args []string) error {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return err
	}
	instance, err := helper.NewDaemon(configPath, config, helper.NewSecretStore(configPath))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return instance.Run(ctx)
}

func token(configPath string, args []string) error {
	flags := flag.NewFlagSet("token", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	secret, err := helper.NewSecretStore(configPath).Get("local-token-secret")
	if err != nil {
		return errors.New("local helper token is unavailable")
	}
	value, err := helper.IssueLocalToken(secret, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Println(value)
	notifyCodexStarted(configPath, secret, os.Getppid())
	return nil
}

func notifyCodexStarted(configPath, secret string, parentPID int) {
	config, err := helper.LoadConfig(configPath)
	if err != nil || parentPID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		config.LocalBaseURL()+"/control/catalog/codex-started?pid="+strconv.Itoa(parentPID), nil)
	if err != nil {
		return
	}
	request.Header.Set("X-OpenCDX-Control", secret)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err == nil {
		response.Body.Close()
	}
}

func loginOpenAI(configPath string, args []string) error {
	flags := flag.NewFlagSet("login-openai", flag.ContinueOnError)
	replace := flags.Bool("replace", false, "replace credentials if this account is already registered")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, client, err := pairedClient(configPath)
	if err != nil {
		return err
	}
	result, err := helper.RunOpenAILogin(context.Background(), client, helper.DetectCodexVersion(context.Background()), *replace)
	if err != nil {
		return err
	}
	fmt.Printf("Connected %s (%s).\n", result.MaskedEmail, result.Plan)
	_, syncErr := helper.SyncCatalog(context.Background(), client, configPath, &config, helper.DetectCodexVersion(context.Background()), false)
	if syncErr != nil {
		fmt.Fprintln(os.Stderr, "Account connected, but catalog synchronization is pending:", syncErr)
	}
	return nil
}

func syncCatalog(configPath string, args []string) error {
	flags := flag.NewFlagSet("sync-catalog", flag.ContinueOnError)
	force := flags.Bool("force", false, "refresh upstream catalogs before download")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, client, err := pairedClient(configPath)
	if err != nil {
		return err
	}
	result, err := helper.SyncCatalog(context.Background(), client, configPath, &config, helper.DetectCodexVersion(context.Background()), *force)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
	return nil
}

func reconcileUsage(configPath string, args []string) error {
	return reconcileUsageTo(configPath, args, os.Stdout)
}

func reconcileUsageTo(configPath string, args []string, output io.Writer) error {
	return reconcileUsageToWithSecrets(configPath, args, output, helper.NewSecretStore(configPath))
}

func reconcileUsageToWithSecrets(configPath string, args []string, output io.Writer, secrets helper.SecretStore) error {
	flags := flag.NewFlagSet("reconcile-usage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	codexHome := flags.String("codex-home", "", "Codex data directory (defaults to CODEX_HOME or ~/.codex)")
	jsonOutput := flags.Bool("json", false, "print the reconciliation result as JSON")
	dryRun := flags.Bool("dry-run", false, "scan and summarize without replacing router telemetry")
	previewJSON := flags.Bool("preview-json", false, "print a compact JSON preview without replacing router telemetry")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*codexHome) == "" {
		var err error
		*codexHome, err = usagehistory.DefaultCodexHome()
		if err != nil {
			return err
		}
	}
	snapshot, err := usagehistory.Scan(context.Background(), *codexHome, time.Now().UTC())
	if err != nil {
		return err
	}
	if snapshot.EventsImported == 0 || len(snapshot.Rows) == 0 {
		return errors.New("no importable Codex usage history was found; existing telemetry was left unchanged")
	}
	if *previewJSON {
		encoded, _ := json.Marshal(usagehistory.Preview(snapshot))
		fmt.Fprintln(output, string(encoded))
		return nil
	}
	if *dryRun {
		if *jsonOutput {
			encoded, _ := json.Marshal(snapshot)
			fmt.Fprintln(output, string(encoded))
		} else {
			preview := usagehistory.Preview(snapshot)
			fmt.Fprintf(output, "Found %s: %d routed and %d native requests. Prompts and responses were not read or exported.\n",
				snapshot.Summary(), preview.RoutedRequests, preview.NativeRequests)
		}
		return nil
	}
	_, client, err := pairedClientWithSecrets(configPath, secrets)
	if err != nil {
		return err
	}
	var result usagehistory.Result
	if _, err = client.JSON(context.Background(), http.MethodPost, "/api/v1/telemetry/reconcile", snapshot, &result, true); err != nil {
		return err
	}
	if *jsonOutput {
		encoded, _ := json.Marshal(result)
		fmt.Fprintln(output, string(encoded))
		return nil
	}
	fmt.Fprintf(output, "Usage history reconciled: %s. Prompts and responses were not imported.", snapshot.Summary())
	if snapshot.DuplicateEvents > 0 || snapshot.MalformedLines > 0 {
		fmt.Fprintf(output, " Skipped %d copied events and %d malformed records.", snapshot.DuplicateEvents, snapshot.MalformedLines)
	}
	fmt.Fprintln(output)
	return nil
}

func resetTelemetry(configPath string, args []string) error {
	return resetTelemetryToWithSecrets(configPath, args, os.Stdout, helper.NewSecretStore(configPath))
}

func resetTelemetryToWithSecrets(configPath string, args []string, output io.Writer, secrets helper.SecretStore) error {
	flags := flag.NewFlagSet("reset-telemetry", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	_, client, err := pairedClientWithSecrets(configPath, secrets)
	if err != nil {
		return err
	}
	if _, err = client.JSON(context.Background(), http.MethodPost, "/api/v1/telemetry/reset", nil, nil, true); err != nil {
		return err
	}
	fmt.Fprintln(output, "Telemetry reset. Providers, devices, accounts, and local Codex history were not changed.")
	return nil
}

func printConfig(configPath string, args []string) error {
	flags := flag.NewFlagSet("config", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return err
	}
	executable, err := helper.ExecutablePath()
	if err != nil {
		return err
	}
	snippet, err := helper.ConfigSnippet(config, executable)
	if err != nil {
		return err
	}
	fmt.Print(snippet)
	return nil
}

func openDashboard(configPath string, args []string) error {
	flags := flag.NewFlagSet("open-dashboard", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return err
	}
	return helper.OpenRouterURL(strings.TrimRight(config.RouterURL, "/")+"/admin", config.InsecureDevelopment)
}

func control(configPath, method, path string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet(strings.TrimPrefix(path, "/control/"), flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return err
	}
	secret, err := helper.NewSecretStore(configPath).Get("local-token-secret")
	if err != nil {
		return errors.New("local control credential is unavailable")
	}
	request, err := http.NewRequest(method, config.LocalBaseURL()+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-OpenCDX-Control", secret)
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Do(request)
	if err != nil {
		return errors.New("helper daemon is not running")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("helper control request returned %s", response.Status)
	}
	_, err = io.Copy(output, io.LimitReader(response.Body, 2<<20))
	return err
}

func pairedClient(configPath string) (helper.Config, *helper.RemoteClient, error) {
	return pairedClientWithSecrets(configPath, helper.NewSecretStore(configPath))
}

func pairedClientWithSecrets(configPath string, secrets helper.SecretStore) (helper.Config, *helper.RemoteClient, error) {
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return helper.Config{}, nil, err
	}
	token, err := secrets.Get("device-token")
	if err != nil {
		return helper.Config{}, nil, errors.New("device is not paired with the router")
	}
	client, err := helper.NewRemoteClient(config, token)
	return config, client, err
}

func defaultDeviceName() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "Codex Mac"
	}
	return name
}

func usageError() error {
	return errors.New("usage: router-helper [--config PATH] <enroll|pair|daemon|token|status|login-openai|sync-catalog|refresh-catalog|acknowledge-restart|refresh-quotas|reconcile-usage|reset-telemetry|reconnect|config|open-dashboard|quit|version>")
}
