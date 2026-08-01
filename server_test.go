package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func TestAdmissionHandler(t *testing.T) {
	store := &nodeStore{nodes: nodeSet{mustNodePublic(t, testNodeOne): {}}}
	handler := newHandler(store, newTestLogger())

	t.Run("allowed", func(t *testing.T) {
		recorder := postAdmissionRequest(t, handler, "/", mustNodePublic(t, testNodeOne))
		assertAdmissionResponse(t, recorder, http.StatusOK, true)
	})

	t.Run("denied", func(t *testing.T) {
		recorder := postAdmissionRequest(t, handler, "/", mustNodePublic(t, testNodeTwo))
		assertAdmissionResponse(t, recorder, http.StatusOK, false)
	})

	t.Run("existing catch-all path remains supported", func(t *testing.T) {
		recorder := postAdmissionRequest(t, handler, "/verify", mustNodePublic(t, testNodeOne))
		assertAdmissionResponse(t, recorder, http.StatusOK, true)
	})

	t.Run("unknown fields remain forward compatible", func(t *testing.T) {
		body := `{"NodePublic":"` + testNodeOne + `","FutureField":true}`
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		assertAdmissionResponse(t, recorder, http.StatusOK, true)
	})

	t.Run("wrong method", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
		}
		if allow := recorder.Header().Get("Allow"); allow != http.MethodPost {
			t.Fatalf("Allow = %q, want %q", allow, http.MethodPost)
		}
	})

	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `[`},
		{name: "trailing JSON", body: `{"NodePublic":"` + testNodeOne + `"} {}`},
		{
			name: "oversized",
			body: `{"NodePublic":"` + testNodeOne + `"}` + strings.Repeat(" ", maxRequestBodyBytes),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAdmissionHandlerConcurrentReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	writeTestFile(t, path, `["`+testNodeOne+`"]`)
	store, err := newNodeStore(path, false)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(store, newTestLogger())
	body := marshalAdmissionRequest(t, mustNodePublic(t, testNodeOne))

	start := make(chan struct{})
	var requests sync.WaitGroup
	for range 4 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			for range 50 {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
				if recorder.Code != http.StatusOK {
					t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
				}
			}
		}()
	}
	close(start)

	for i := range 20 {
		node := testNodeOne
		if i%2 == 0 {
			node = testNodeTwo
		}
		writeTestFile(t, path, `["`+node+`"]`)
		if _, _, err := store.reload(); err != nil {
			t.Fatal(err)
		}
	}
	requests.Wait()
}

func TestHTTPServerConfiguration(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if server.ReadHeaderTimeout != readHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, readHeaderTimeout)
	}
	if server.ReadTimeout != readTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", server.ReadTimeout, readTimeout)
	}
	if server.WriteTimeout != writeTimeout {
		t.Fatalf("WriteTimeout = %s, want %s", server.WriteTimeout, writeTimeout)
	}
	if server.IdleTimeout != idleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.IdleTimeout, idleTimeout)
	}
}

func TestServeHTTPShutsDownOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newHTTPServer(listener.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveHTTP(ctx, server, listener)
	}()

	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   time.Second,
	}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		cancel()
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not shut down after context cancellation")
	}
}

func postAdmissionRequest(t *testing.T, handler http.Handler, path string, nodeKey key.NodePublic) *httptest.ResponseRecorder {
	t.Helper()
	body := marshalAdmissionRequest(t, nodeKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return recorder
}

func marshalAdmissionRequest(t *testing.T, nodeKey key.NodePublic) string {
	t.Helper()
	text, err := nodeKey.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(struct {
		NodePublic string
	}{
		NodePublic: string(text),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func assertAdmissionResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantAllow bool) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var response tailcfg.DERPAdmitClientResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Allow != wantAllow {
		t.Fatalf("Allow = %v, want %v", response.Allow, wantAllow)
	}
}
