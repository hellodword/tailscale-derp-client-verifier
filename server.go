package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"tailscale.com/tailcfg"
)

const (
	maxRequestBodyBytes = 8 << 10
	readHeaderTimeout   = 5 * time.Second
	readTimeout         = 10 * time.Second
	writeTimeout        = 10 * time.Second
	idleTimeout         = time.Minute
	shutdownTimeout     = 5 * time.Second
)

func serve(ctx context.Context, cfg config, logger *slog.Logger) error {
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	nodes, err := newNodeStore(cfg.nodesPath, cfg.allowEmpty)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.addr, err)
	}

	reloadCtx, stopReload := context.WithCancel(ctx)
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		pollNodes(reloadCtx, cfg.reloadInterval, nodes.reload, logger)
	}()
	defer func() {
		stopReload()
		<-reloadDone
	}()

	logger.Info(
		"serving",
		"addr", listener.Addr().String(),
		"nodes", nodes.count(),
		"reload_interval", cfg.reloadInterval,
	)

	server := newHTTPServer(cfg.addr, newHandler(nodes, logger))
	return serveHTTP(ctx, server, listener)
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func newHandler(nodes *nodeStore, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req tailcfg.DERPAdmitClientRequest
		if err := decodeAdmitRequest(w, r, &req); err != nil {
			logger.Debug("invalid admission request", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		body, err := json.Marshal(tailcfg.DERPAdmitClientResponse{
			Allow: nodes.contains(req.NodePublic),
		})
		if err != nil {
			logger.Error("encode admission response", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(body); err != nil {
			logger.Debug("write admission response", "error", err)
		}
	})
}

func decodeAdmitRequest(w http.ResponseWriter, r *http.Request, req *tailcfg.DERPAdmitClientRequest) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err := decoder.Decode(req); err != nil {
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", listener.Addr(), err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			closeErr := server.Close()
			<-serveResult
			return errors.Join(fmt.Errorf("shut down HTTP server: %w", err), closeErr)
		}

		if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve %s: %w", listener.Addr(), err)
		}
		return nil
	}
}
