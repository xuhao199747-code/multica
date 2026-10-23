package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/multica-ai/multica/server/internal/selfupdate"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func run(ctx context.Context, args []string, runner selfupdate.CommandRunner) error {
	if len(args) == 0 || args[0] != "serve" {
		return fmt.Errorf("usage: selfhost-updater serve --config <absolute-path> [--dry-run]")
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "protected host configuration file")
	dry := flags.Bool("dry-run", false, "prepare locally without pushing or deploying")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("invalid serve arguments")
	}
	if flags.NArg() != 0 || *path == "" {
		return fmt.Errorf("serve requires --config <absolute-path>")
	}
	cfg, err := selfupdate.LoadWorkerConfig(*path)
	if err != nil {
		return err
	}
	cfg.DryRun = *dry
	return (&selfupdate.Worker{Config: cfg, Runner: runner}).Serve(ctx)
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], selfupdate.ExecRunner{}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
