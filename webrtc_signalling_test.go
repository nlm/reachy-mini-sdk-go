package reachymini

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func TestDefaultSignallingURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
	}{
		{"http://localhost:8000", "ws://localhost:8443"},
		{"https://robot.local:8000", "ws://robot.local:8443"},
		{"http://192.168.1.5:9000/", "ws://192.168.1.5:8443"},
		{"http://robot.local", "ws://robot.local:8443"},
	}
	for _, tt := range tests {
		if got := defaultSignallingURL(tt.baseURL); got != tt.want {
			t.Errorf("defaultSignallingURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
		}
	}
}

// dialSignalling upgrades an httptest server to a websocket connection and
// returns both ends, so negotiateSession/sendAnswer/etc. can be exercised
// against a real (in-process) connection without a live daemon.
func dialSignalling(t *testing.T, handler func(server *websocket.Conn)) *sigConn {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		handler(conn)
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return &sigConn{ws: client}
}

func TestNegotiateSessionHappyPath(t *testing.T) {
	const offerSDP = "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\n"
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteJSON(sigMessage{Type: "welcome", PeerID: "srv"})

		var setStatus sigMessage
		if err := server.ReadJSON(&setStatus); err != nil || setStatus.Type != "setPeerStatus" {
			t.Errorf("expected setPeerStatus, got %+v (err=%v)", setStatus, err)
			return
		}

		var list sigMessage
		if err := server.ReadJSON(&list); err != nil || list.Type != "list" {
			t.Errorf("expected list, got %+v (err=%v)", list, err)
			return
		}
		_ = server.WriteJSON(sigMessage{
			Type: "list",
			Producers: []sigPeer{
				{ID: "producer-a", Meta: map[string]any{"name": "front-cam"}},
				{ID: "producer-b", Meta: map[string]any{"name": "back-cam"}},
			},
		})

		var start sigMessage
		if err := server.ReadJSON(&start); err != nil || start.Type != "startSession" {
			t.Errorf("expected startSession, got %+v (err=%v)", start, err)
			return
		}
		if start.PeerID != "producer-b" {
			t.Errorf("startSession targeted %q, want %q", start.PeerID, "producer-b")
			return
		}
		_ = server.WriteJSON(sigMessage{
			Type:      "peer",
			SessionID: "session-1",
			Sdp:       &sigSdp{Type: "offer", Sdp: offerSDP},
		})
	})

	sessionID, offer, err := negotiateSession(context.Background(), client, "back-cam")
	if err != nil {
		t.Fatalf("negotiateSession: %v", err)
	}
	if sessionID != "session-1" {
		t.Errorf("sessionID = %q, want %q", sessionID, "session-1")
	}
	if offer != offerSDP {
		t.Errorf("offer = %q, want %q", offer, offerSDP)
	}
}

func TestNegotiateSessionUnknownProducerName(t *testing.T) {
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteJSON(sigMessage{Type: "welcome"})
		var msg sigMessage
		_ = server.ReadJSON(&msg) // setPeerStatus
		_ = server.ReadJSON(&msg) // list
		_ = server.WriteJSON(sigMessage{
			Type:      "list",
			Producers: []sigPeer{{ID: "producer-a", Meta: map[string]any{"name": "front-cam"}}},
		})
	})

	_, _, err := negotiateSession(context.Background(), client, "no-such-camera")
	if err == nil {
		t.Fatal("expected an error for an unknown producer name, got nil")
	}
}

func TestNegotiateSessionNoProducers(t *testing.T) {
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteJSON(sigMessage{Type: "welcome"})
		var msg sigMessage
		_ = server.ReadJSON(&msg) // setPeerStatus
		_ = server.ReadJSON(&msg) // list
		_ = server.WriteJSON(sigMessage{Type: "list", Producers: nil})
	})

	_, _, err := negotiateSession(context.Background(), client, "")
	if err == nil {
		t.Fatal("expected an error when no producers are available, got nil")
	}
}

func TestNegotiateSessionServerError(t *testing.T) {
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteJSON(sigMessage{Type: "welcome"})
		var msg sigMessage
		_ = server.ReadJSON(&msg) // setPeerStatus
		_ = server.ReadJSON(&msg) // list
		_ = server.WriteJSON(sigMessage{Type: "error", Details: "no soup for you"})
	})

	_, _, err := negotiateSession(context.Background(), client, "")
	if err == nil || !strings.Contains(err.Error(), "no soup for you") {
		t.Fatalf("got err %v, want it to mention the server's error details", err)
	}
}

func TestSendAnswerAndSendICECandidate(t *testing.T) {
	received := make(chan sigMessage, 2)
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		for i := 0; i < 2; i++ {
			var msg sigMessage
			if err := server.ReadJSON(&msg); err != nil {
				return
			}
			received <- msg
		}
	})

	if err := sendAnswer(client, "session-1", "v=0\r\n"); err != nil {
		t.Fatalf("sendAnswer: %v", err)
	}
	idx := uint16(3)
	candidate := webrtc.ICECandidateInit{Candidate: "candidate:1 1 UDP", SDPMLineIndex: &idx}
	if err := sendICECandidate(client, "session-1", candidate); err != nil {
		t.Fatalf("sendICECandidate: %v", err)
	}

	timeout := time.After(2 * time.Second)
	var msgs []sigMessage
	for len(msgs) < 2 {
		select {
		case m := <-received:
			msgs = append(msgs, m)
		case <-timeout:
			t.Fatalf("timed out waiting for messages, got %d of 2", len(msgs))
		}
	}

	answerMsg := msgs[0]
	if answerMsg.Type != "peer" || answerMsg.SessionID != "session-1" || answerMsg.Sdp == nil || answerMsg.Sdp.Type != "answer" || answerMsg.Sdp.Sdp != "v=0\r\n" {
		t.Errorf("got answer message %+v", answerMsg)
	}

	iceMsg := msgs[1]
	if iceMsg.Type != "peer" || iceMsg.SessionID != "session-1" || iceMsg.Ice == nil {
		t.Fatalf("got ICE message %+v", iceMsg)
	}
	if iceMsg.Ice.Candidate != candidate.Candidate || iceMsg.Ice.SdpMLineIndex != uint32(idx) {
		t.Errorf("ICE payload = %+v, want candidate %q index %d", iceMsg.Ice, candidate.Candidate, idx)
	}
}

func TestReportErrDeliversWhenNotCancelled(t *testing.T) {
	errs := make(chan error, 1)
	reportErr(context.Background(), errs, errBoom)

	select {
	case err := <-errs:
		if err != errBoom {
			t.Errorf("got %v, want %v", err, errBoom)
		}
	default:
		t.Fatal("expected an error on errs")
	}
}

func TestReportErrSuppressedAfterCancel(t *testing.T) {
	// Regression test for the race this helper fixes: a bare `select` with
	// both a ctx.Done() case and an errs<- case (plus a default) is racy
	// once errs is buffered and non-empty, since Go picks among ready cases
	// at random. Checking ctx.Err() first makes suppression deterministic.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	errs := make(chan error, 1)
	reportErr(ctx, errs, errBoom)

	select {
	case err := <-errs:
		t.Fatalf("expected no error after cancellation, got %v", err)
	default:
	}
}

func TestReportErrDropsWhenChannelFull(t *testing.T) {
	errs := make(chan error, 1)
	errs <- errBoom // fill the buffer

	reportErr(context.Background(), errs, fmt.Errorf("second error"))

	got := <-errs
	if got != errBoom {
		t.Errorf("got %v, want the first error to survive (send should have been dropped)", got)
	}
}

var errBoom = fmt.Errorf("boom")

func TestPumpSignallingSurfacesEndSession(t *testing.T) {
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteJSON(sigMessage{Type: "endSession", SessionID: "session-1"})
	})

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	defer pc.Close()

	errs := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pumpSignalling(ctx, client, pc, errs)

	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "ended the session") {
			t.Errorf("got err %v, want it to mention the session ending", err)
		}
	default:
		t.Fatal("expected an error on the errs channel after endSession")
	}
}

func TestSigConnConcurrentSends(t *testing.T) {
	const senders, perSender = 4, 50
	received := make(chan int, 1)
	client := dialSignalling(t, func(server *websocket.Conn) {
		defer server.Close()
		n := 0
		for n < senders*perSender {
			if _, err := recvSig(server); err != nil {
				break
			}
			n++
		}
		received <- n
	})

	var wg sync.WaitGroup
	for i := 0; i < senders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perSender; j++ {
				if err := sendICECandidate(client, "s", webrtc.ICECandidateInit{Candidate: "candidate:1"}); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case n := <-received:
		if n != senders*perSender {
			t.Errorf("server read %d intact messages, want %d", n, senders*perSender)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never finished reading")
	}
}
