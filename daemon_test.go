package reachymini

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recordedRequest is what recordingServer saw.
type recordedRequest struct {
	method string
	uri    string
	body   string
}

// recordingServer returns a Client whose server records the last request
// and replies 200 with resp.
func recordingServer(t *testing.T, resp string) (*Client, *recordedRequest) {
	t.Helper()
	got := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = recordedRequest{method: r.Method, uri: r.URL.RequestURI(), body: string(b)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), got
}

func (got recordedRequest) check(t *testing.T, method, uri, body string) {
	t.Helper()
	if got.method != method || got.uri != uri || got.body != body {
		t.Errorf("request = %s %s %q, want %s %s %q", got.method, got.uri, got.body, method, uri, body)
	}
}

func TestStartStopDaemon(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) (string, error)
		uri  string
	}{
		{"start wake", func(c *Client) (string, error) { return c.StartDaemon(context.Background(), true) }, "/api/daemon/start?wake_up=true"},
		{"start", func(c *Client) (string, error) { return c.StartDaemon(context.Background(), false) }, "/api/daemon/start?wake_up=false"},
		{"stop sleep", func(c *Client) (string, error) { return c.StopDaemon(context.Background(), true) }, "/api/daemon/stop?goto_sleep=true"},
		{"stop", func(c *Client) (string, error) { return c.StopDaemon(context.Background(), false) }, "/api/daemon/stop?goto_sleep=false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, got := recordingServer(t, `{"job_id":"j1"}`)
			jobID, err := tt.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if jobID != "j1" {
				t.Errorf("jobID = %q, want j1", jobID)
			}
			got.check(t, http.MethodPost, tt.uri, "")
		})
	}
}

func TestStopDaemonBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"Daemon is busy."}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).StopDaemon(context.Background(), false); err == nil {
		t.Fatal("StopDaemon: want error on 409, got nil")
	}
}

func TestGetRobotName(t *testing.T) {
	for _, tt := range []struct{ resp, want string }{
		{`{"name":"reachy-kitchen"}`, "reachy-kitchen"},
		{`{"name":null}`, ""},
	} {
		c, got := recordingServer(t, tt.resp)
		name, err := c.GetRobotName(context.Background())
		if err != nil {
			t.Fatalf("GetRobotName: %v", err)
		}
		if name != tt.want {
			t.Errorf("GetRobotName(%s) = %q, want %q", tt.resp, name, tt.want)
		}
		got.check(t, http.MethodGet, "/api/daemon/robot-name", "")
	}
}

func TestSetRobotName(t *testing.T) {
	c, got := recordingServer(t, `{"name":"bob"}`)
	name, err := c.SetRobotName(context.Background(), "bob")
	if err != nil {
		t.Fatalf("SetRobotName: %v", err)
	}
	if name != "bob" {
		t.Errorf("name = %q, want bob", name)
	}
	got.check(t, http.MethodPost, "/api/daemon/robot-name", `{"name":"bob"}`)
}

func TestGetHardwareID(t *testing.T) {
	for _, tt := range []struct{ resp, want string }{
		{`{"hardware_id":"ABC123"}`, "ABC123"},
		{`{"hardware_id":null}`, ""},
	} {
		c, got := recordingServer(t, tt.resp)
		id, err := c.GetHardwareID(context.Background())
		if err != nil {
			t.Fatalf("GetHardwareID: %v", err)
		}
		if id != tt.want {
			t.Errorf("GetHardwareID(%s) = %q, want %q", tt.resp, id, tt.want)
		}
		got.check(t, http.MethodGet, "/api/daemon/hardware-id", "")
	}
}

func TestGetRobotAppLockStatus(t *testing.T) {
	c, got := recordingServer(t, `{"state":"local_app","holder_name":"radio"}`)
	s, err := c.GetRobotAppLockStatus(context.Background())
	if err != nil {
		t.Fatalf("GetRobotAppLockStatus: %v", err)
	}
	want := RobotAppLockStatus{State: RobotAppLockLocalApp, HolderName: "radio"}
	if s != want {
		t.Errorf("status = %+v, want %+v", s, want)
	}
	got.check(t, http.MethodGet, "/api/daemon/robot-app-lock-status", "")
}

func TestGetRobotAppLockStatusFree(t *testing.T) {
	c, _ := recordingServer(t, `{"state":"free","holder_name":null}`)
	s, err := c.GetRobotAppLockStatus(context.Background())
	if err != nil {
		t.Fatalf("GetRobotAppLockStatus: %v", err)
	}
	if s != (RobotAppLockStatus{State: RobotAppLockFree}) {
		t.Errorf("status = %+v, want free with no holder", s)
	}
}

func TestSetRobotNameInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"Invalid robot name"}`))
	}))
	defer srv.Close()

	name, err := New(srv.URL).SetRobotName(context.Background(), "   ")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("SetRobotName err = %v, want 422 *APIError", err)
	}
	if name != "" {
		t.Errorf("name = %q, want empty on error", name)
	}
}
