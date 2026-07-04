package reachymini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialWS upgrades an httptest server to a websocket connection at path and
// runs handler against the server side, mirroring dialSignalling in
// webrtc_signalling_test.go but returning a Client pointed at the server
// instead of a raw client conn.
func dialWS(t *testing.T, path string, handler func(server *websocket.Conn)) *Client {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}
}

func TestStreamFullStateDeliversFrames(t *testing.T) {
	c := dialWS(t, "/api/state/ws/full", func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteJSON(FullState{})
		_ = server.WriteJSON(FullState{})
	})

	states, errs, err := c.StreamFullState(context.Background())
	if err != nil {
		t.Fatalf("StreamFullState: %v", err)
	}

	got := 0
	timeout := time.After(5 * time.Second)
	for got < 2 {
		select {
		case _, ok := <-states:
			if !ok {
				t.Fatalf("states closed early after %d frames", got)
			}
			got++
		case err := <-errs:
			t.Fatalf("unexpected error: %v", err)
		case <-timeout:
			t.Fatalf("timed out after %d frames", got)
		}
	}
}

func TestStreamFullStateSurfacesReadError(t *testing.T) {
	c := dialWS(t, "/api/state/ws/full", func(server *websocket.Conn) {
		server.Close() // close immediately so the client's ReadJSON fails
	})

	states, errs, err := c.StreamFullState(context.Background())
	if err != nil {
		t.Fatalf("StreamFullState: %v", err)
	}

	select {
	case _, ok := <-states:
		if ok {
			t.Fatal("expected states to be closed, got a frame")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for states to close")
	}

	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected a non-nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an error")
	}
}

func TestStreamFullStateContextCancellationClosesConn(t *testing.T) {
	closed := make(chan struct{})
	c := dialWS(t, "/api/state/ws/full", func(server *websocket.Conn) {
		defer server.Close()
		var frame FullState
		if err := server.ReadJSON(&frame); err != nil {
			close(closed)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	_, _, err := c.StreamFullState(ctx)
	if err != nil {
		t.Fatalf("StreamFullState: %v", err)
	}
	cancel()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the connection to close after cancellation")
	}
}

func TestStreamSetTargetSendsTargets(t *testing.T) {
	received := make(chan FullBodyTarget, 1)
	c := dialWS(t, "/api/move/ws/set_target", func(server *websocket.Conn) {
		defer server.Close()
		var target FullBodyTarget
		if err := server.ReadJSON(&target); err != nil {
			t.Errorf("server ReadJSON: %v", err)
			return
		}
		received <- target
	})

	targets, errs, err := c.StreamSetTarget(context.Background())
	if err != nil {
		t.Fatalf("StreamSetTarget: %v", err)
	}

	want := FullBodyTarget{TargetAntennas: &[2]float64{0.1, 0.2}}
	select {
	case targets <- want:
	case err := <-errs:
		t.Fatalf("unexpected error sending target: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out sending target")
	}

	select {
	case got := <-received:
		if got.TargetAntennas == nil || *got.TargetAntennas != *want.TargetAntennas {
			t.Errorf("got %+v, want %+v", got.TargetAntennas, want.TargetAntennas)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the server to receive the target")
	}
}

func TestStreamSetTargetClosingChannelStopsLoop(t *testing.T) {
	serverDone := make(chan struct{})
	c := dialWS(t, "/api/move/ws/set_target", func(server *websocket.Conn) {
		defer server.Close()
		defer close(serverDone)
		var target FullBodyTarget
		_ = server.ReadJSON(&target) // returns once the client closes the conn
	})

	targets, errs, err := c.StreamSetTarget(context.Background())
	if err != nil {
		t.Fatalf("StreamSetTarget: %v", err)
	}
	close(targets)

	select {
	case _, ok := <-errs:
		if ok {
			t.Fatal("expected errs to close without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for errs to close")
	}
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the server side to observe the close")
	}
}

func TestStreamFullStateDialError(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:0"}
	_, _, err := c.StreamFullState(context.Background())
	if err == nil {
		t.Fatal("expected a dial error against an unreachable server, got nil")
	}
	if !strings.Contains(err.Error(), "connect") && !strings.Contains(err.Error(), "refused") && !strings.Contains(err.Error(), "dial") {
		t.Logf("dial error (informational): %v", err)
	}
}
