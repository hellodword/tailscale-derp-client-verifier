package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := parseConfig([]string{"-path", "/srv/nodes.json"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.addr != defaultAddr {
			t.Fatalf("addr = %q, want %q", cfg.addr, defaultAddr)
		}
		if cfg.nodesPath != "/srv/nodes.json" {
			t.Fatalf("nodesPath = %q, want %q", cfg.nodesPath, "/srv/nodes.json")
		}
		if cfg.reloadInterval != defaultReloadInterval {
			t.Fatalf("reloadInterval = %s, want %s", cfg.reloadInterval, defaultReloadInterval)
		}
		if cfg.allowEmpty {
			t.Fatal("allowEmpty = true, want false")
		}
	})

	t.Run("custom values", func(t *testing.T) {
		cfg, err := parseConfig([]string{
			"-addr", "127.0.0.1:4000",
			"-path", "/srv/nodes.json",
			"-reload-interval", "45s",
			"-allow-empty",
		}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.addr != "127.0.0.1:4000" {
			t.Fatalf("addr = %q, want %q", cfg.addr, "127.0.0.1:4000")
		}
		if cfg.reloadInterval != 45*time.Second {
			t.Fatalf("reloadInterval = %s, want 45s", cfg.reloadInterval)
		}
		if !cfg.allowEmpty {
			t.Fatal("allowEmpty = false, want true")
		}
	})

	for _, interval := range []string{"0", "-1s", "999ms"} {
		t.Run("reject interval "+interval, func(t *testing.T) {
			_, err := parseConfig([]string{
				"-path", "/srv/nodes.json",
				"-reload-interval", interval,
			}, io.Discard)
			if err == nil {
				t.Fatal("parseConfig() succeeded, want error")
			}
		})
	}
}

func TestRunCLIExitCodes(t *testing.T) {
	ctx := context.Background()
	logger := newTestLogger()

	t.Run("missing path is usage error", func(t *testing.T) {
		var stderr bytes.Buffer
		serveCalled := false
		exitCode := runCLI(ctx, nil, &stderr, logger, func(context.Context, config, *slog.Logger) error {
			serveCalled = true
			return nil
		})
		if exitCode != 2 {
			t.Fatalf("exit code = %d, want 2", exitCode)
		}
		if serveCalled {
			t.Fatal("serve called for invalid arguments")
		}
		if !strings.Contains(stderr.String(), errPathRequired.Error()) {
			t.Fatalf("stderr = %q, want required-path error", stderr.String())
		}
	})

	t.Run("invalid reload interval is usage error", func(t *testing.T) {
		var stderr bytes.Buffer
		exitCode := runCLI(ctx, []string{
			"-path", "/srv/nodes.json",
			"-reload-interval", "500ms",
		}, &stderr, logger, func(context.Context, config, *slog.Logger) error {
			t.Fatal("serve called for invalid arguments")
			return nil
		})
		if exitCode != 2 {
			t.Fatalf("exit code = %d, want 2", exitCode)
		}
	})

	t.Run("help succeeds", func(t *testing.T) {
		var stderr bytes.Buffer
		exitCode := runCLI(ctx, []string{"-h"}, &stderr, logger, func(context.Context, config, *slog.Logger) error {
			t.Fatal("serve called for help")
			return nil
		})
		if exitCode != 0 {
			t.Fatalf("exit code = %d, want 0", exitCode)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("stderr = %q, want usage", stderr.String())
		}
	})

	t.Run("runtime error fails", func(t *testing.T) {
		runtimeErr := errors.New("runtime failure")
		var stderr bytes.Buffer
		exitCode := runCLI(ctx, []string{"-path", "/srv/nodes.json"}, &stderr, logger, func(context.Context, config, *slog.Logger) error {
			return runtimeErr
		})
		if exitCode != 1 {
			t.Fatalf("exit code = %d, want 1", exitCode)
		}
		if !strings.Contains(stderr.String(), runtimeErr.Error()) {
			t.Fatalf("stderr = %q, want runtime error", stderr.String())
		}
	})
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
