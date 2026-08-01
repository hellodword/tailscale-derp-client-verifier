package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tailscale.com/types/key"
)

const (
	testNodeOne = "nodekey:50d20b455ecf12bc453f83c2cfdb2a24925d06cf2598dcaa54e91af82ce9f765"
	testNodeTwo = "nodekey:1111111111111111111111111111111111111111111111111111111111111111"
)

func TestReadNodesFile(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		allowEmpty bool
		wantLen    int
		wantErr    bool
	}{
		{name: "valid", content: `["` + testNodeOne + `"]`, wantLen: 1},
		{name: "duplicate", content: `["` + testNodeOne + `","` + testNodeOne + `"]`, wantLen: 1},
		{name: "empty rejected", content: `[]`, wantErr: true},
		{name: "empty allowed", content: `[]`, allowEmpty: true, wantLen: 0},
		{name: "null", content: `null`, wantErr: true},
		{name: "object", content: `{}`, wantErr: true},
		{name: "null node", content: `[null]`, wantErr: true},
		{name: "numeric node", content: `[1]`, wantErr: true},
		{name: "invalid key", content: `["not-a-node-key"]`, wantErr: true},
		{name: "zero key", content: `["nodekey:0000000000000000000000000000000000000000000000000000000000000000"]`, wantErr: true},
		{name: "multiple values", content: `[] []`, allowEmpty: true, wantErr: true},
		{name: "malformed", content: `[`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nodes.json")
			writeTestFile(t, path, tt.content)

			nodes, err := readNodesFile(path, tt.allowEmpty)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readNodesFile() succeeded, want error; nodes = %v", nodes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != tt.wantLen {
				t.Fatalf("len(nodes) = %d, want %d", len(nodes), tt.wantLen)
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		_, err := readNodesFile(filepath.Join(t.TempDir(), "missing.json"), false)
		if err == nil {
			t.Fatal("readNodesFile() succeeded for missing file, want error")
		}
	})

	t.Run("oversized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nodes.json")
		data := make([]byte, int(maxNodesFileBytes)+1)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readNodesFile(path, true); err == nil {
			t.Fatal("readNodesFile() succeeded for oversized file, want error")
		}
	})
}

func TestNodeStoreReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	writeTestFile(t, path, `["`+testNodeOne+`"]`)

	store, err := newNodeStore(path, false)
	if err != nil {
		t.Fatal(err)
	}
	first := mustNodePublic(t, testNodeOne)
	second := mustNodePublic(t, testNodeTwo)
	if !store.contains(first) {
		t.Fatal("initial node missing")
	}

	changed, count, err := store.reload()
	if err != nil {
		t.Fatal(err)
	}
	if changed || count != 1 {
		t.Fatalf("unchanged reload = (%v, %d), want (false, 1)", changed, count)
	}

	writeTestFile(t, path, `{invalid`)
	if _, _, err := store.reload(); err == nil {
		t.Fatal("reload succeeded for invalid JSON, want error")
	}
	if !store.contains(first) {
		t.Fatal("failed reload discarded the last valid node list")
	}

	writeTestFile(t, path, `[]`)
	if _, _, err := store.reload(); err == nil {
		t.Fatal("reload succeeded for an unapproved empty list, want error")
	}
	if !store.contains(first) {
		t.Fatal("empty reload discarded the last valid node list")
	}

	writeTestFile(t, path, `["`+testNodeTwo+`"]`)
	changed, count, err = store.reload()
	if err != nil {
		t.Fatal(err)
	}
	if !changed || count != 1 {
		t.Fatalf("updated reload = (%v, %d), want (true, 1)", changed, count)
	}
	if store.contains(first) {
		t.Fatal("old node remains after successful reload")
	}
	if !store.contains(second) {
		t.Fatal("updated node missing after successful reload")
	}
}

func TestNodeStoreAllowsExplicitEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	writeTestFile(t, path, `["`+testNodeOne+`"]`)

	store, err := newNodeStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, `[]`)

	changed, count, err := store.reload()
	if err != nil {
		t.Fatal(err)
	}
	if !changed || count != 0 {
		t.Fatalf("empty reload = (%v, %d), want (true, 0)", changed, count)
	}
	if store.contains(mustNodePublic(t, testNodeOne)) {
		t.Fatal("node remains after explicitly allowed empty reload")
	}
}

func TestRunNodeReloadLoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	writeTestFile(t, path, `["`+testNodeOne+`"]`)
	store, err := newNodeStore(path, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	reloadDone := make(chan struct{})
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		runNodeReloadLoop(ctx, ticks, func() (bool, int, error) {
			changed, count, err := store.reload()
			reloadDone <- struct{}{}
			return changed, count, err
		}, newTestLogger())
	}()

	writeTestFile(t, path, `["`+testNodeTwo+`"]`)
	ticks <- time.Now()
	<-reloadDone
	if !store.contains(mustNodePublic(t, testNodeTwo)) {
		t.Fatal("background reload did not install updated nodes")
	}

	writeTestFile(t, path, `{invalid`)
	ticks <- time.Now()
	<-reloadDone
	if !store.contains(mustNodePublic(t, testNodeTwo)) {
		t.Fatal("failed background reload discarded the last valid list")
	}

	writeTestFile(t, path, `["`+testNodeOne+`"]`)
	ticks <- time.Now()
	<-reloadDone
	if !store.contains(mustNodePublic(t, testNodeOne)) {
		t.Fatal("background reload did not recover after an invalid file")
	}

	cancel()
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("reload loop did not stop after context cancellation")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustNodePublic(t *testing.T, value string) key.NodePublic {
	t.Helper()
	var node key.NodePublic
	if err := node.UnmarshalText([]byte(value)); err != nil {
		t.Fatal(err)
	}
	return node
}
