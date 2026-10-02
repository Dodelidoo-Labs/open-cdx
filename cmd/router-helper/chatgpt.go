package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/helper"
)

func openChatGPT(configPath string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("open-chatgpt", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "print launch paths without creating files or opening ChatGPT")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("open-chatgpt does not accept positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	launch, err := helper.PlanChatGPTLaunch(ctx, configPath)
	if err != nil {
		return err
	}
	if *dryRun {
		return json.NewEncoder(output).Encode(launch)
	}
	if err = launch.Open(ctx); err != nil {
		return err
	}
	fmt.Fprintf(output, "Opened ChatGPT without routing using %s. Sign in to ChatGPT on first use. CLI and IDE routing are unchanged. Use this command or OpenCDX Settings whenever starting the app.\n", launch.CodexHome)
	return nil
}
