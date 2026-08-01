package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	defaultAddr           = "localhost:3000"
	defaultReloadInterval = 30 * time.Second
	minReloadInterval     = time.Second
)

var errPathRequired = errors.New("-path is required")

type config struct {
	addr           string
	nodesPath      string
	reloadInterval time.Duration
	allowEmpty     bool
}

type serveFunc func(context.Context, config, *slog.Logger) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	exitCode := runCLI(ctx, os.Args[1:], os.Stderr, logger, serve)
	stop()
	os.Exit(exitCode)
}

func runCLI(ctx context.Context, args []string, stderr io.Writer, logger *slog.Logger, serveFn serveFunc) int {
	cfg, err := parseConfig(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		writeDiagnostic(stderr, "error: %v\n", err)
		return 2
	}

	if err := serveFn(ctx, cfg, logger); err != nil {
		writeDiagnostic(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("tailscale-derp-client-verifier", flag.ContinueOnError)
	fs.SetOutput(stderr)

	cfg := config{reloadInterval: defaultReloadInterval}
	fs.StringVar(&cfg.addr, "addr", defaultAddr, "address to listen on")
	fs.StringVar(&cfg.nodesPath, "path", "", "path to nodes.json (required)")
	fs.DurationVar(&cfg.reloadInterval, "reload-interval", defaultReloadInterval, "interval between nodes.json reloads (minimum 1s)")
	fs.BoolVar(&cfg.allowEmpty, "allow-empty", false, "allow an empty nodes list that denies every client")
	fs.Usage = func() {
		writeDiagnostic(stderr, "Usage: %s -path /path/to/nodes.json [options]\n", fs.Name())
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %q", fs.Args())
	}
	if err := cfg.validate(); err != nil {
		if errors.Is(err, errPathRequired) {
			fs.Usage()
		}
		return config{}, err
	}
	return cfg, nil
}

func (cfg config) validate() error {
	if cfg.nodesPath == "" {
		return errPathRequired
	}
	if cfg.reloadInterval < minReloadInterval {
		return fmt.Errorf("-reload-interval must be at least %s", minReloadInterval)
	}
	return nil
}

func writeDiagnostic(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
