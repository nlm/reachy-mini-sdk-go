package reachymini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoJSONGetDecodesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/thing" {
			t.Errorf("path = %s, want /api/thing", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":42}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var resp struct {
		Value int `json:"value"`
	}
	if err := c.doJSON(context.Background(), http.MethodGet, "/api/thing", nil, &resp); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if resp.Value != 42 {
		t.Errorf("value = %d, want 42", resp.Value)
	}
}

func TestDoJSONPostEncodesRequestBody(t *testing.T) {
	type reqBody struct {
		Name string `json:"name"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var got reqBody
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got.Name != "reachy" {
			t.Errorf("name = %q, want %q", got.Name, "reachy")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.doJSON(context.Background(), http.MethodPost, "/api/thing", reqBody{Name: "reachy"}, nil)
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
}

func TestDoJSONNoRequestBodyOmitsContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "" {
			t.Errorf("Content-Type = %q, want empty", ct)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.doJSON(context.Background(), http.MethodPost, "/api/thing", nil, nil); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
}

func TestDoJSONErrorStatusReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no such thing"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.doJSON(context.Background(), http.MethodGet, "/api/thing", nil, nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got error of type %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
	if apiErr.Body != "no such thing" {
		t.Errorf("Body = %q, want %q", apiErr.Body, "no such thing")
	}
	if !strings.Contains(apiErr.Error(), "404") || !strings.Contains(apiErr.Error(), "no such thing") {
		t.Errorf("Error() = %q, want it to mention the status and body", apiErr.Error())
	}
}

func TestDoJSONEmptyResponseBodyLeavesRespBodyUntouched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	resp := struct{ Value int }{Value: -1}
	if err := c.doJSON(context.Background(), http.MethodGet, "/api/thing", nil, &resp); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if resp.Value != -1 {
		t.Errorf("value = %d, want untouched -1", resp.Value)
	}
}

func TestDoJSONInvalidResponseJSONReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var resp struct{ Value int }
	err := c.doJSON(context.Background(), http.MethodGet, "/api/thing", nil, &resp)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if _, ok := err.(*APIError); ok {
		t.Fatalf("got *APIError, want a decode error since status was 2xx")
	}
}

func TestDoRawReturnsBodyBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/kinematics/stl/robot/head.stl" {
			t.Errorf("path = %s, want /api/kinematics/stl/robot/head.stl", r.URL.Path)
		}
		_, _ = w.Write([]byte{0x01, 0x02, 0x03})
	}))
	defer srv.Close()

	c := New(srv.URL)
	data, err := c.doRaw(context.Background(), http.MethodGet, "/api/kinematics/stl/robot/head.stl")
	if err != nil {
		t.Fatalf("doRaw: %v", err)
	}
	if string(data) != "\x01\x02\x03" {
		t.Errorf("got %v, want [1 2 3]", data)
	}
}

func TestDoRawErrorStatusReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.doRaw(context.Background(), http.MethodGet, "/api/thing")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got error of type %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError || apiErr.Body != "boom" {
		t.Errorf("got %+v, want status 500 body %q", apiErr, "boom")
	}
}

func TestDoJSONContextCancellationPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New(srv.URL)
	err := c.doJSON(ctx, http.MethodGet, "/api/thing", nil, nil)
	if err == nil {
		t.Fatal("expected an error from a cancelled context, got nil")
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://localhost:8000/")
	if c.BaseURL != "http://localhost:8000" {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL, "http://localhost:8000")
	}
}

func TestWsURL(t *testing.T) {
	tests := []struct {
		baseURL string
		path    string
		want    string
	}{
		{"http://localhost:8000", "/api/state/ws/full", "ws://localhost:8000/api/state/ws/full"},
		{"https://robot.local:8000", "/api/state/ws/full", "wss://robot.local:8000/api/state/ws/full"},
		{"localhost:8000", "/api/state/ws/full", "ws://localhost:8000/api/state/ws/full"},
	}
	for _, tt := range tests {
		c := &Client{BaseURL: tt.baseURL}
		if got := c.wsURL(tt.path); got != tt.want {
			t.Errorf("wsURL(%q) with base %q = %q, want %q", tt.path, tt.baseURL, got, tt.want)
		}
	}
}
