package reachymini

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadSoundSendsMultipartForm(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "beep.wav")
	const content = "fake wav bytes"
	if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	var gotPath, gotFilename, gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("read form file: %v", err)
			return
		}
		defer file.Close()
		gotFilename = header.Filename
		data, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read file contents: %v", err)
			return
		}
		gotContent = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.UploadSound(context.Background(), localPath); err != nil {
		t.Fatalf("UploadSound: %v", err)
	}
	if gotPath != "/api/media/sounds/upload" {
		t.Errorf("path = %q, want /api/media/sounds/upload", gotPath)
	}
	if gotFilename != "beep.wav" {
		t.Errorf("filename = %q, want beep.wav", gotFilename)
	}
	if gotContent != content {
		t.Errorf("content = %q, want %q", gotContent, content)
	}
}

func TestUploadSoundMissingLocalFile(t *testing.T) {
	c := New("http://unused.invalid")
	err := c.UploadSound(context.Background(), filepath.Join(t.TempDir(), "missing.wav"))
	if err == nil {
		t.Fatal("expected an error for a missing local file, got nil")
	}
}

func TestUploadSoundErrorStatus(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "beep.wav")
	if err := os.WriteFile(localPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.UploadSound(context.Background(), localPath)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got error of type %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnsupportedMediaType)
	}
}

func TestDeleteSoundEscapesFilename(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteSound(context.Background(), "my sound/effect.wav"); err != nil {
		t.Fatalf("DeleteSound: %v", err)
	}
	want := "/api/media/sounds/" + url.PathEscape("my sound/effect.wav")
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestListSoundsUnwrapsFilesField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"files":["a.wav","b.wav"]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	files, err := c.ListSounds(context.Background())
	if err != nil {
		t.Fatalf("ListSounds: %v", err)
	}
	if len(files) != 2 || files[0] != "a.wav" || files[1] != "b.wav" {
		t.Errorf("got %v, want [a.wav b.wav]", files)
	}
}
