package reachymini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListRecordedMoveDatasetsAllowsUnescapedSlash pins the documented
// behavior that datasetName is appended raw (not url.PathEscape'd) because
// the daemon route uses FastAPI's {dataset_name:path} converter, which
// itself expects to receive literal "/" characters.
func TestListRecordedMoveDatasetsAllowsUnescapedSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ListRecordedMoveDatasets(context.Background(), "vendor/pack1"); err != nil {
		t.Fatalf("ListRecordedMoveDatasets: %v", err)
	}
	want := "/api/move/recorded-move-datasets/list/vendor/pack1"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestPlayRecordedMoveDatasetBuildsPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"uuid":"uuid-1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	uuid, err := c.PlayRecordedMoveDataset(context.Background(), "vendor/pack1", "wave")
	if err != nil {
		t.Fatalf("PlayRecordedMoveDataset: %v", err)
	}
	if want := "/api/move/play/recorded-move-dataset/vendor/pack1/wave"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if uuid.UUID != "uuid-1" {
		t.Errorf("uuid = %q, want %q", uuid.UUID, "uuid-1")
	}
}

func TestStopSendsUUIDAsRequestBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Stop(context.Background(), MoveUUID{UUID: "uuid-1"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if want := `{"uuid":"uuid-1"}`; gotBody != want {
		t.Errorf("body = %q, want %q", gotBody, want)
	}
}
