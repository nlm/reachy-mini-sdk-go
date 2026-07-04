package reachymini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureMotorModeNoChangeWhenAlreadyInMode(t *testing.T) {
	var setModeCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/motors/status" {
			_ = json.NewEncoder(w).Encode(MotorStatus{Mode: MotorModeEnabled})
			return
		}
		setModeCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	changed, err := c.EnsureMotorMode(context.Background(), MotorModeEnabled)
	if err != nil {
		t.Fatalf("EnsureMotorMode: %v", err)
	}
	if changed {
		t.Error("changed = true, want false (mode already matched)")
	}
	if setModeCalls != 0 {
		t.Errorf("set_mode called %d times, want 0", setModeCalls)
	}
}

func TestEnsureMotorModeChangesWhenModeDiffers(t *testing.T) {
	var setModePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/motors/status" {
			_ = json.NewEncoder(w).Encode(MotorStatus{Mode: MotorModeDisabled})
			return
		}
		setModePath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	changed, err := c.EnsureMotorMode(context.Background(), MotorModeEnabled)
	if err != nil {
		t.Fatalf("EnsureMotorMode: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true (mode differed)")
	}
	want := "/api/motors/set_mode/" + string(MotorModeEnabled)
	if setModePath != want {
		t.Errorf("set_mode called with path %q, want %q", setModePath, want)
	}
}

func TestEnsureMotorModePropagatesStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.EnsureMotorMode(context.Background(), MotorModeEnabled)
	if err == nil {
		t.Fatal("expected an error when GetMotorStatus fails, got nil")
	}
}

func TestEnsureMotorModePropagatesSetModeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/motors/status" {
			_ = json.NewEncoder(w).Encode(MotorStatus{Mode: MotorModeDisabled})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.EnsureMotorMode(context.Background(), MotorModeEnabled)
	if err == nil {
		t.Fatal("expected an error when SetMotorMode fails, got nil")
	}
}

func TestSetMotorModeEscapesPath(t *testing.T) {
	// MotorControlMode values never actually need escaping today, but the
	// handler builds the path by string concatenation with url.PathEscape,
	// so pin that behavior against a value containing a reserved character.
	const weird MotorControlMode = "a/b"
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.SetMotorMode(context.Background(), weird); err != nil {
		t.Fatalf("SetMotorMode: %v", err)
	}
	if want := "/api/motors/set_mode/a%2Fb"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}
