package reachymini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWobbling(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
		path string
	}{
		{"enable", func(c *Client) error { return c.EnableWobbling(context.Background()) }, "/api/media/wobbling/enable"},
		{"disable", func(c *Client) error { return c.DisableWobbling(context.Background()) }, "/api/media/wobbling/disable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer srv.Close()

			if err := tt.call(New(srv.URL)); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %s, want %s", gotMethod, http.MethodPost)
			}
			if gotPath != tt.path {
				t.Errorf("path = %q, want %q", gotPath, tt.path)
			}
		})
	}
}

func TestWobblingBackendNotRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"Backend not running"}`))
	}))
	defer srv.Close()

	if err := New(srv.URL).EnableWobbling(context.Background()); err == nil {
		t.Fatal("EnableWobbling: want error on 503, got nil")
	}
}
