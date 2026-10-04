package reachymini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnableHeadTrackingSendsWeight(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody trackingEnableRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","enabled":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	enabled, err := c.EnableHeadTracking(context.Background(), 0.01)
	if err != nil {
		t.Fatalf("EnableHeadTracking: %v", err)
	}
	if !enabled {
		t.Errorf("enabled = %v, want true", enabled)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want %s", gotMethod, http.MethodPost)
	}
	if gotPath != "/api/media/tracking/enable" {
		t.Errorf("path = %q, want /api/media/tracking/enable", gotPath)
	}
	if gotBody.Weight != 0.01 {
		t.Errorf("weight = %v, want 0.01", gotBody.Weight)
	}
}

func TestEnableHeadTrackingUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"unavailable","enabled":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	enabled, err := c.EnableHeadTracking(context.Background(), 0.5)
	if err != nil {
		t.Fatalf("EnableHeadTracking: %v", err)
	}
	if enabled {
		t.Errorf("enabled = %v, want false", enabled)
	}
}

func TestDisableHeadTracking(t *testing.T) {
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.DisableHeadTracking(context.Background())
	if err != nil {
		t.Fatalf("DisableHeadTracking: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want %s", gotMethod, http.MethodPost)
	}
	if gotPath != "/api/media/tracking/disable" {
		t.Errorf("path = %q, want /api/media/tracking/disable", gotPath)
	}
}

func TestGetTrackedFaceDecodesDetected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want %s", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/api/media/tracking/face" {
			t.Errorf("path = %q, want /api/media/tracking/face", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","face_target":{"detected":true,"x":-0.68,"y":-0.45,"roll":0.02,"ts":123.4}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	face, err := c.GetTrackedFace(context.Background())
	if err != nil {
		t.Fatalf("GetTrackedFace: %v", err)
	}
	if !face.Detected {
		t.Errorf("detected = %v, want true", face.Detected)
	}
	if face.X == nil || *face.X != -0.68 {
		t.Errorf("x = %v, want -0.68", face.X)
	}
	if face.Y == nil || *face.Y != -0.45 {
		t.Errorf("y = %v, want -0.45", face.Y)
	}
	if face.Roll == nil || *face.Roll != 0.02 {
		t.Errorf("roll = %v, want 0.02", face.Roll)
	}
	if face.TS == nil || *face.TS != 123.4 {
		t.Errorf("ts = %v, want 123.4", face.TS)
	}
}

func TestGetTrackedFaceDecodesNulls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","face_target":{"detected":false,"x":null,"y":null,"roll":null,"ts":null}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	face, err := c.GetTrackedFace(context.Background())
	if err != nil {
		t.Fatalf("GetTrackedFace: %v", err)
	}
	if face.Detected {
		t.Errorf("detected = %v, want false", face.Detected)
	}
	if face.X != nil {
		t.Errorf("x = %v, want nil", face.X)
	}
	if face.Y != nil {
		t.Errorf("y = %v, want nil", face.Y)
	}
	if face.Roll != nil {
		t.Errorf("roll = %v, want nil", face.Roll)
	}
	if face.TS != nil {
		t.Errorf("ts = %v, want nil", face.TS)
	}
}

func TestHeadTrackingHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.EnableHeadTracking(context.Background(), 0.5)
	if err == nil {
		t.Fatal("expected an error for 404 response, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got error of type %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}
