package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"sync"
	"time"

	"tailscale.com/types/key"
)

const maxNodesFileBytes int64 = 16 << 20

type nodeSet map[key.NodePublic]struct{}

type nodeStore struct {
	path       string
	allowEmpty bool

	mu    sync.RWMutex
	nodes nodeSet
}

type reloadFunc func() (changed bool, count int, err error)

func newNodeStore(path string, allowEmpty bool) (*nodeStore, error) {
	nodes, err := readNodesFile(path, allowEmpty)
	if err != nil {
		return nil, err
	}
	return &nodeStore{
		path:       path,
		allowEmpty: allowEmpty,
		nodes:      nodes,
	}, nil
}

func (s *nodeStore) reload() (bool, int, error) {
	nodes, err := readNodesFile(s.path, s.allowEmpty)
	if err != nil {
		return false, 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if maps.Equal(s.nodes, nodes) {
		return false, len(s.nodes), nil
	}
	s.nodes = nodes
	return true, len(s.nodes), nil
}

func (s *nodeStore) contains(node key.NodePublic) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.nodes[node]
	return ok
}

func (s *nodeStore) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}

func readNodesFile(path string, allowEmpty bool) (nodeSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open nodes file %q: %w", path, err)
	}

	data, readErr := io.ReadAll(io.LimitReader(f, maxNodesFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read nodes file %q: %w", path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close nodes file %q: %w", path, closeErr)
	}
	if int64(len(data)) > maxNodesFileBytes {
		return nil, fmt.Errorf("read nodes file %q: file exceeds %d bytes", path, maxNodesFileBytes)
	}

	var decoded []string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode nodes file %q: %w", path, err)
	}
	if decoded == nil {
		return nil, fmt.Errorf("decode nodes file %q: expected a JSON array", path)
	}
	if len(decoded) == 0 && !allowEmpty {
		return nil, fmt.Errorf("decode nodes file %q: empty array requires -allow-empty", path)
	}

	nodes := make(nodeSet, len(decoded))
	for i, encoded := range decoded {
		var node key.NodePublic
		if err := node.UnmarshalText([]byte(encoded)); err != nil {
			return nil, fmt.Errorf("decode nodes file %q: invalid node at index %d: %w", path, i, err)
		}
		if node.IsZero() {
			return nil, fmt.Errorf("decode nodes file %q: zero node key at index %d", path, i)
		}
		nodes[node] = struct{}{}
	}
	return nodes, nil
}

func pollNodes(ctx context.Context, interval time.Duration, reload reloadFunc, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	runNodeReloadLoop(ctx, ticker.C, reload, logger)
}

func runNodeReloadLoop(ctx context.Context, ticks <-chan time.Time, reload reloadFunc, logger *slog.Logger) {
	reloadFailed := false
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}

			changed, count, err := reload()
			if err != nil {
				if reloadFailed {
					logger.Debug("nodes reload still failing", "error", err)
				} else {
					logger.Error("nodes reload failed", "error", err)
				}
				reloadFailed = true
				continue
			}

			if reloadFailed {
				logger.Info("nodes reload recovered", "count", count)
			} else if changed {
				logger.Info("nodes reloaded", "count", count)
			}
			reloadFailed = false
		}
	}
}
