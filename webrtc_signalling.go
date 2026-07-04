package reachymini

import (
	"context"
	"fmt"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// This file implements a client for the GStreamer webrtcsink signalling
// protocol (github.com/GStreamer/gst-plugins-rs, net/webrtc/protocol) that
// the daemon's camera pipeline runs embedded (webrtcsink's
// "run-signalling-server" property). It is a *separate* connection from
// Client's REST/state WebSocket API -- the daemon's GStreamer signalling
// server conventionally listens on ws://<host>:8443, not the main daemon
// port. Verified live against Pollen's desktop simulator: the welcome /
// setPeerStatus / list / startSession / sessionStarted / peer(sdp) /
// peer(ice) message shapes below all matched a real connection exactly.

// sigMessage is a superset of every signalling message shape (peer- and
// server-originated), decoded/encoded loosely via omitempty since each
// message type only populates a subset of fields.
type sigMessage struct {
	Type string `json:"type"`

	// welcome
	PeerID string `json:"peerId,omitempty"`

	// setPeerStatus (outgoing)
	Roles []string `json:"roles,omitempty"`
	Meta  any      `json:"meta,omitempty"`

	// list (incoming response to an outgoing {"type":"list"})
	Producers []sigPeer `json:"producers,omitempty"`

	// startSession (outgoing)
	Offer *string `json:"offer,omitempty"`

	// peer, endSession, sessionStarted
	SessionID string `json:"sessionId,omitempty"`

	// peer: sdp offer/answer
	Sdp *sigSdp `json:"sdp,omitempty"`

	// peer: ice candidate
	Ice *sigIce `json:"ice,omitempty"`

	// error
	Details string `json:"details,omitempty"`
}

type sigPeer struct {
	ID   string `json:"id"`
	Meta any    `json:"meta,omitempty"`
}

type sigSdp struct {
	Type string `json:"type"` // "offer" or "answer"
	Sdp  string `json:"sdp"`
}

type sigIce struct {
	Candidate     string `json:"candidate"`
	SdpMLineIndex uint32 `json:"sdpMLineIndex"`
}

// defaultSignallingURL derives ws://<host>:8443 from a Client.BaseURL like
// "http://localhost:9000" -- the signalling server is a distinct port from
// the REST API, conventionally 8443 for an embedded webrtcsink signaller.
func defaultSignallingURL(baseURL string) string {
	host := baseURL
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	} else if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return "ws://" + host + ":8443"
}

// reportErr sends err on errs unless ctx is already done. A `select` with
// both a ctx.Done() case and an errs<- case alongside a `default` is racy --
// if both channels happen to be ready at once (errs is buffered), Go picks
// among them at random, so relying on that pattern to suppress teardown
// noise after an intentional cancellation doesn't reliably work. Checking
// ctx.Err() first makes the suppression deterministic: once the caller has
// cancelled ctx, "connection closed"/"read RTP packet: EOF"-type errors from
// the resulting cleanup (closing the peer connection, killing ffmpeg, etc.)
// are expected and not worth surfacing.
func reportErr(ctx context.Context, errs chan<- error, err error) {
	if ctx.Err() != nil {
		return
	}
	select {
	case errs <- err:
	default:
	}
}

func sendSig(conn *websocket.Conn, msg sigMessage) error {
	return conn.WriteJSON(msg)
}

func recvSig(conn *websocket.Conn) (sigMessage, error) {
	var msg sigMessage
	err := conn.ReadJSON(&msg)
	return msg, err
}

// negotiateSession registers as a consumer, lists available producers,
// starts a session with the chosen one (by producerName, or the first
// available if empty), and waits for the producer's SDP offer. It returns
// the session ID (needed for the answer and ICE exchange) and the offer.
func negotiateSession(conn *websocket.Conn, producerName string) (sessionID, offerSDP string, err error) {
	// Consume the initial "welcome" message before proceeding, per protocol.
	welcome, err := recvSig(conn)
	if err != nil {
		return "", "", fmt.Errorf("read welcome: %w", err)
	}
	if welcome.Type != "welcome" {
		return "", "", fmt.Errorf("expected welcome message, got %q", welcome.Type)
	}

	if err := sendSig(conn, sigMessage{Type: "setPeerStatus", Roles: []string{"consumer"}}); err != nil {
		return "", "", fmt.Errorf("send setPeerStatus: %w", err)
	}

	if err := sendSig(conn, sigMessage{Type: "list"}); err != nil {
		return "", "", fmt.Errorf("send list: %w", err)
	}
	var producers []sigPeer
	for {
		msg, err := recvSig(conn)
		if err != nil {
			return "", "", fmt.Errorf("read list response: %w", err)
		}
		if msg.Type == "list" {
			producers = msg.Producers
			break
		}
		if msg.Type == "error" {
			return "", "", fmt.Errorf("signalling server error: %s", msg.Details)
		}
		// Ignore anything else (e.g. an unrelated peerStatusChanged) while
		// waiting for our list response.
	}
	if len(producers) == 0 {
		return "", "", fmt.Errorf("no producers available on signalling server")
	}

	producerID := producers[0].ID
	if producerName != "" {
		found := false
		for _, p := range producers {
			meta, _ := p.Meta.(map[string]any)
			name, _ := meta["name"].(string)
			if name == producerName {
				producerID, found = p.ID, true
				break
			}
		}
		if !found {
			return "", "", fmt.Errorf("no producer named %q (have %d producer(s))", producerName, len(producers))
		}
	}

	if err := sendSig(conn, sigMessage{Type: "startSession", PeerID: producerID}); err != nil {
		return "", "", fmt.Errorf("send startSession: %w", err)
	}

	// The producer (webrtcsink) creates the SDP offer once the session
	// starts; wait for it, picking up the session ID from whichever message
	// carries it first rather than depending on sessionStarted's exact
	// shape.
	for {
		msg, err := recvSig(conn)
		if err != nil {
			return "", "", fmt.Errorf("read offer: %w", err)
		}
		switch {
		case msg.Type == "error":
			return "", "", fmt.Errorf("signalling server error: %s", msg.Details)
		case msg.Type == "peer" && msg.Sdp != nil && msg.Sdp.Type == "offer":
			return msg.SessionID, msg.Sdp.Sdp, nil
		}
	}
}

// sendAnswer sends our SDP answer for sessionID.
func sendAnswer(conn *websocket.Conn, sessionID, answerSDP string) error {
	return sendSig(conn, sigMessage{
		Type:      "peer",
		SessionID: sessionID,
		Sdp:       &sigSdp{Type: "answer", Sdp: answerSDP},
	})
}

// sendICECandidate forwards a local ICE candidate to the producer.
func sendICECandidate(conn *websocket.Conn, sessionID string, candidate webrtc.ICECandidateInit) error {
	index := uint32(0)
	if candidate.SDPMLineIndex != nil {
		index = uint32(*candidate.SDPMLineIndex)
	}
	return sendSig(conn, sigMessage{
		Type:      "peer",
		SessionID: sessionID,
		Ice:       &sigIce{Candidate: candidate.Candidate, SdpMLineIndex: index},
	})
}

// pumpSignalling continues reading signalling messages after negotiation,
// forwarding remote ICE candidates into pc and surfacing errors, until ctx
// is cancelled or the connection closes.
func pumpSignalling(ctx context.Context, conn *websocket.Conn, pc *webrtc.PeerConnection, errs chan<- error) {
	for {
		msg, err := recvSig(conn)
		if err != nil {
			reportErr(ctx, errs, fmt.Errorf("signalling connection closed: %w", err))
			return
		}
		switch {
		case msg.Type == "peer" && msg.Ice != nil:
			index := uint16(msg.Ice.SdpMLineIndex)
			candErr := pc.AddICECandidate(webrtc.ICECandidateInit{
				Candidate:     msg.Ice.Candidate,
				SDPMLineIndex: &index,
			})
			if candErr != nil {
				reportErr(ctx, errs, fmt.Errorf("add remote ICE candidate: %w", candErr))
			}
		case msg.Type == "error":
			reportErr(ctx, errs, fmt.Errorf("signalling server error: %s", msg.Details))
		case msg.Type == "endSession":
			reportErr(ctx, errs, fmt.Errorf("producer ended the session"))
			return
		}
	}
}
