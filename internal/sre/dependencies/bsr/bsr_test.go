package bsr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != latestPluginPath {
			http.Error(w, "unexpected request", http.StatusBadRequest)

			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req["owner"] + "/" + req["name"] {
		case "apple/swift":
			_, _ = w.Write([]byte(`{"plugin":{"owner":"apple","name":"swift","version":"v1.38.1"}}`))
		case "acme/broken":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"internal","message":"boom"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"plugin not found"}`))
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestLatestPlugin(t *testing.T) {
	c := Client{BaseURL: server(t).URL}
	ctx := context.Background()

	if v, err := c.LatestPlugin(ctx, "buf.build/apple/swift"); err != nil || v != "v1.38.1" {
		t.Errorf("apple/swift = %q, %v, want v1.38.1", v, err)
	}
	if _, err := c.LatestPlugin(ctx, "buf.build/acme/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("acme/missing: err = %v, want ErrNotFound", err)
	}
	if _, err := c.LatestPlugin(ctx, "buf.build/acme/broken"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("acme/broken: err = %v, want a server error", err)
	}
	if _, err := c.LatestPlugin(ctx, "buf.build/swift"); err == nil {
		t.Error("a reference without an owner was accepted")
	}
}
