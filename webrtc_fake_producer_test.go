package reachymini

import (
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

// fakeProducer stands in for the daemon's webrtcsink: a signalling server
// plus a pion peer that offers a sendrecv Opus audio track and a VP8 video
// track, like the daemon after _enable_audio_receive. It serves one
// consumer session.
type fakeProducer struct {
	// SignallingURL is the ws:// URL to pass as SignallingURL.
	SignallingURL string
	// AudioOut is the producer's outgoing audio (the robot's microphone).
	AudioOut *webrtc.TrackLocalStaticSample
	// AudioIn receives the consumer's audio track (bound for the robot's
	// speaker), once the consumer sends one.
	AudioIn chan *webrtc.TrackRemote
	// Connected is closed once the peer connection is established.
	Connected chan struct{}

	connectedOnce sync.Once
	// errs collects protocol failures from the serving goroutine, which
	// must not call t after the test returns; checked in t.Cleanup.
	errs chan error
	done chan struct{}
}

func newFakeProducer(t *testing.T) *fakeProducer {
	t.Helper()
	return newFakeProducerWithAudio(t, webrtc.RTPTransceiverDirectionSendrecv)
}

// newFakeProducerWithAudio is newFakeProducer with the audio transceiver
// offered in direction audioDir (sendonly mimics a daemon that doesn't
// play client audio).
func newFakeProducerWithAudio(t *testing.T, audioDir webrtc.RTPTransceiverDirection) *fakeProducer {
	t.Helper()
	audioOut, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "fake")
	if err != nil {
		t.Fatalf("audio track: %v", err)
	}
	videoOut, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "video", "fake")
	if err != nil {
		t.Fatalf("video track: %v", err)
	}
	fp := &fakeProducer{
		AudioOut:  audioOut,
		AudioIn:   make(chan *webrtc.TrackRemote, 1),
		Connected: make(chan struct{}),
		errs:      make(chan error, 16),
		done:      make(chan struct{}),
	}

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(fp.done)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fp.fail("upgrade: %v", err)
			return
		}
		fp.serve(conn, audioDir, videoOut)
	}))
	t.Cleanup(func() {
		// Hijacked WebSocket handlers outlive srv.Close, so close the
		// client connections and wait for serve to return before
		// reporting what it saw.
		srv.CloseClientConnections()
		srv.Close()
		select {
		case <-fp.done:
		case <-time.After(5 * time.Second):
			t.Error("fake producer: serve did not return")
		}
		close(fp.errs)
		for err := range fp.errs {
			t.Error(err)
		}
	})
	fp.SignallingURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	return fp
}

// fail records a protocol failure, reported when the test ends.
func (fp *fakeProducer) fail(format string, args ...any) {
	select {
	case fp.errs <- fmt.Errorf("fake producer: "+format, args...):
	default:
	}
}

// serve runs the signalling protocol for one consumer. Only this goroutine
// writes to conn: the producer gathers all ICE candidates before sending
// its offer instead of trickling them.
func (fp *fakeProducer) serve(conn *websocket.Conn, audioDir webrtc.RTPTransceiverDirection, videoOut webrtc.TrackLocal) {
	defer conn.Close()
	expect := func(typ string) bool {
		msg, err := recvSig(conn)
		if err != nil || msg.Type != typ {
			fp.fail("want %q, got %+v (%v)", typ, msg, err)
			return false
		}
		return true
	}
	if sendSig(conn, sigMessage{Type: "welcome", PeerID: "consumer-1"}) != nil ||
		!expect("setPeerStatus") || !expect("list") ||
		sendSig(conn, sigMessage{Type: "list", Producers: []sigPeer{{ID: "producer-1", Meta: map[string]any{"name": "reachymini"}}}}) != nil ||
		!expect("startSession") {
		return
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		fp.fail("peer connection: %v", err)
		return
	}
	defer pc.Close()
	for _, tr := range []struct {
		track webrtc.TrackLocal
		dir   webrtc.RTPTransceiverDirection
	}{{fp.AudioOut, audioDir}, {videoOut, webrtc.RTPTransceiverDirectionSendrecv}} {
		if _, err := pc.AddTransceiverFromTrack(tr.track, webrtc.RTPTransceiverInit{Direction: tr.dir}); err != nil {
			fp.fail("add transceiver: %v", err)
			return
		}
	}
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		select {
		case fp.AudioIn <- track:
		default:
			fp.fail("unexpected extra audio track")
		}
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			fp.connectedOnce.Do(func() { close(fp.Connected) })
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		fp.fail("create offer: %v", err)
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		fp.fail("set local description: %v", err)
		return
	}
	<-gathered
	const sessionID = "session-1"
	if err := sendSig(conn, sigMessage{Type: "peer", SessionID: sessionID, Sdp: &sigSdp{Type: "offer", Sdp: pc.LocalDescription().SDP}}); err != nil {
		return
	}

	// The consumer trickles candidates from pion's goroutines, so some
	// can arrive before its answer. pion rejects candidates without a
	// remote description (webrtcbin queues them), so hold them until then.
	var pending []webrtc.ICECandidateInit
	answered := false
	addCandidate := func(c webrtc.ICECandidateInit) {
		if err := pc.AddICECandidate(c); err != nil {
			fp.fail("add ICE candidate: %v", err)
		}
	}
	for {
		msg, err := recvSig(conn)
		if err != nil {
			return // consumer went away
		}
		switch {
		case msg.Sdp != nil && msg.Sdp.Type == "answer":
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: msg.Sdp.Sdp}); err != nil {
				fp.fail("set remote description: %v", err)
				return
			}
			answered = true
			for _, c := range pending {
				addCandidate(c)
			}
			pending = nil
		case msg.Ice != nil:
			index := uint16(msg.Ice.SdpMLineIndex)
			c := webrtc.ICECandidateInit{Candidate: msg.Ice.Candidate, SDPMLineIndex: &index}
			if answered {
				addCandidate(c)
			} else {
				pending = append(pending, c)
			}
		}
	}
}
