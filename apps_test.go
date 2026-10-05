package reachymini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCheckAppUpdatesForceQueryParam(t *testing.T) {
	tests := []struct {
		force bool
		want  string
	}{
		{force: false, want: ""},
		{force: true, want: "force=true"},
	}
	for _, tt := range tests {
		var gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))

		c := New(srv.URL)
		if _, err := c.CheckAppUpdates(context.Background(), tt.force); err != nil {
			t.Fatalf("CheckAppUpdates(force=%v): %v", tt.force, err)
		}
		if gotQuery != tt.want {
			t.Errorf("force=%v: query = %q, want %q", tt.force, gotQuery, tt.want)
		}
		srv.Close()
	}
}

func TestGetCurrentAppStatusNilWhenNoAppRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("null"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	status, err := c.GetCurrentAppStatus(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentAppStatus: %v", err)
	}
	if status != nil {
		t.Errorf("got %+v, want nil", status)
	}
}

func TestGetJobStatusEscapesPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.GetJobStatus(context.Background(), "job/with slash"); err != nil {
		t.Fatalf("GetJobStatus: %v", err)
	}
	want := "/api/apps/job-status/" + url.PathEscape("job/with slash")
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestListAvailableAppsBySourceEscapesSource(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ListAvailableAppsBySource(context.Background(), SourceKindHFSpace); err != nil {
		t.Fatalf("ListAvailableAppsBySource: %v", err)
	}
	if want := "/api/apps/list-available/hf_space"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestResetAppsCacheUsesNonAPIPath(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ResetAppsCache(context.Background()); err != nil {
		t.Fatalf("ResetAppsCache: %v", err)
	}
	if gotPath != "/cache/reset-apps" {
		t.Errorf("path = %q, want /cache/reset-apps", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
}

func TestStartAppNoEvictEscapesPath(t *testing.T) {
	c, got := recordingServer(t, `{"info":{"name":"a/b"},"state":"running"}`)
	if _, err := c.StartAppNoEvict(context.Background(), "a/b"); err != nil {
		t.Fatalf("StartAppNoEvict: %v", err)
	}
	got.check(t, http.MethodPost, "/api/apps/start-app/a%2Fb/no-evict", "")
}

func TestGetStartupApp(t *testing.T) {
	for _, tt := range []struct{ resp, want string }{
		{`{"startup_app":"radio"}`, "radio"},
		{`{"startup_app":null}`, ""},
	} {
		c, got := recordingServer(t, tt.resp)
		name, err := c.GetStartupApp(context.Background())
		if err != nil {
			t.Fatalf("GetStartupApp: %v", err)
		}
		if name != tt.want {
			t.Errorf("GetStartupApp(%s) = %q, want %q", tt.resp, name, tt.want)
		}
		got.check(t, http.MethodGet, "/api/apps/startup-app", "")
	}
}

func TestSetStartupApp(t *testing.T) {
	for _, tt := range []struct{ name, body string }{
		{"radio", `{"startup_app":"radio"}`},
		{"", `{"startup_app":null}`},
	} {
		c, got := recordingServer(t, `{"startup_app":null}`)
		if err := c.SetStartupApp(context.Background(), tt.name); err != nil {
			t.Fatalf("SetStartupApp(%q): %v", tt.name, err)
		}
		got.check(t, http.MethodPut, "/api/apps/startup-app", tt.body)
	}
}
