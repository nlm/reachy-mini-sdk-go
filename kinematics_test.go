package reachymini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetKinematicsSTLAllowsUnescapedSlash pins the documented behavior that
// filename is appended raw (not url.PathEscape'd) because the daemon route
// uses FastAPI's {filename:path} converter, which itself expects to receive
// literal "/" characters (e.g. filenames returned by GetURDF).
func TestGetKinematicsSTLAllowsUnescapedSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte{0xde, 0xad})
	}))
	defer srv.Close()

	c := New(srv.URL)
	data, err := c.GetKinematicsSTL(context.Background(), "meshes/head.stl")
	if err != nil {
		t.Fatalf("GetKinematicsSTL: %v", err)
	}
	if want := "/api/kinematics/stl/meshes/head.stl"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if len(data) != 2 {
		t.Errorf("got %d bytes, want 2", len(data))
	}
}
