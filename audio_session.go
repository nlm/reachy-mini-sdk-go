package reachymini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// ErrAudioSessionClosed is returned by AudioSession methods once the
// session has been closed.
var ErrAudioSessionClosed = errors.New("reachymini: audio session closed")

// AudioSessionOptions configures OpenAudioSession.
type AudioSessionOptions struct {
	// SignallingURL is the WebRTC signalling server's WebSocket URL, as
	// in AudioStreamOptions. Defaults to Client's BaseURL host on port
	// 8443.
	SignallingURL string

	// ProducerName optionally selects a producer by its advertised meta
	// name, as in AudioStreamOptions.
	ProducerName string

	// FFmpegPath overrides the ffmpeg binary used to encode speaker audio
	// to Opus. Defaults to "ffmpeg" (resolved via PATH).
	FFmpegPath string

	// SampleRate is the sample rate, in Hz, of the PCM passed to Write.
	// Defaults to AudioSampleRate. Any rate works: ffmpeg resamples.
	SampleRate int

	// Channels is the channel count of the (interleaved) PCM passed to
	// Write. Defaults to 1. The robot plays it downmixed to mono.
	Channels int

	// Microphone also decodes the robot's microphone, delivered on
	// AudioSession.Microphone in the same format as
	// StreamMicrophoneAudio. A microphone failure (e.g. its decoder
	// dying) ends the whole session. Prefer this to a separate
	// StreamMicrophoneAudio when you need both directions: each WebRTC
	// session costs the robot a camera encoder.
	Microphone bool
}

// AudioSession is a WebRTC session with the daemon that plays audio on the
// robot's speaker. Audio sent this way is what the daemon's head wobbling
// (EnableWobbling) reacts to, and what ClearIncomingAudio flushes.
//
// The session streams continuously in real time: Write queues PCM, and the
// session encodes the queue as it falls due, or silence when the queue is
// empty, so queued audio plays back to back and gaps play as silence. Its
// methods are safe for concurrent use.
//
// The daemon is built around a single client sending audio. With several
// (another AudioSession, the Python SDK, the phone app), ClearIncomingAudio
// and the echo canceller only follow the most recently connected one, and
// every sender's audio, silence included, feeds the same head wobbler.
type AudioSession struct {
	sampleRate     int
	bytesPerSample int // per sample frame, all channels

	ctx  context.Context
	stop func(err error)
	done chan struct{}
	err  error // set before done is closed

	mu    sync.Mutex
	queue []byte

	mic chan []byte // nil unless opts.Microphone
}

// pacerTick is how often the session hands queued PCM to the encoder.
const pacerTick = 10 * time.Millisecond

// maxCatchUp bounds how much audio the session sends at once after a stall
// (e.g. the process was suspended), so it resumes in real time instead of
// bursting what it missed.
const maxCatchUp = 200 * time.Millisecond

// OpenAudioSession connects to the daemon's WebRTC feed and returns once
// the connection is up and audio written to the session will play on the
// robot's speaker. It runs until ctx is cancelled, Close is called, or the
// connection fails (see Done and Err). Requires an `ffmpeg` binary with
// libopus on PATH (or set opts.FFmpegPath).
//
// Each session is a WebRTC consumer of the daemon, which runs its own
// camera encoder per consumer, so prefer one long-lived session over one
// per utterance.
func (c *Client) OpenAudioSession(ctx context.Context, opts AudioSessionOptions) (*AudioSession, error) {
	if opts.SampleRate == 0 {
		opts.SampleRate = AudioSampleRate
	}
	if opts.Channels == 0 {
		opts.Channels = 1
	}
	if opts.SampleRate < 0 || opts.Channels < 0 {
		return nil, fmt.Errorf("invalid audio format: %d Hz, %d channels", opts.SampleRate, opts.Channels)
	}
	if opts.FFmpegPath == "" {
		opts.FFmpegPath = "ffmpeg"
	}
	if opts.SignallingURL == "" {
		opts.SignallingURL = defaultSignallingURL(c.BaseURL)
	}

	// Start the encoder first: a missing ffmpeg should fail before any
	// network round trips.
	cmd, encIn, encOut, err := startFFmpegOpusEncoder(opts.FFmpegPath, opts.SampleRate, opts.Channels)
	if err != nil {
		return nil, err
	}
	killEncoder := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	ws, _, err := websocket.DefaultDialer.DialContext(ctx, opts.SignallingURL, nil)
	if err != nil {
		killEncoder()
		return nil, fmt.Errorf("dial signalling server %s: %w", opts.SignallingURL, err)
	}
	conn := &sigConn{ws: ws}
	sessionID, offerSDP, err := negotiateSession(ctx, conn, opts.ProducerName)
	if err != nil {
		conn.Close()
		killEncoder()
		return nil, fmt.Errorf("negotiate session: %w", err)
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		conn.Close()
		killEncoder()
		return nil, fmt.Errorf("create peer connection: %w", err)
	}

	sessCtx, cancel := context.WithCancel(ctx)
	s := &AudioSession{
		sampleRate:     opts.SampleRate,
		bytesPerSample: 2 * opts.Channels,
		ctx:            sessCtx,
		done:           make(chan struct{}),
	}
	if opts.Microphone {
		s.mic = make(chan []byte)
	}
	// The microphone decoder starts from pion's OnTrack callback, which
	// can fire at any time, so it's tracked apart from wg: under micMu,
	// either shutdown hasn't begun and the decoder registers itself, or
	// it has and the decoder never starts.
	var (
		micMu       sync.Mutex
		micStopping bool
		micCmd      *exec.Cmd
		micDone     <-chan struct{}
	)
	var stopOnce sync.Once
	s.stop = func(err error) {
		stopOnce.Do(func() {
			s.err = err
			cancel()
		})
	}
	// Record a parent cancellation as soon as it happens, so it can't be
	// reported as whatever failure its teardown causes.
	stopAfter := context.AfterFunc(ctx, func() { s.stop(ctx.Err()) })

	// Everything below runs until sessCtx ends; shutdown tears it down in
	// dependency order and then closes done. wg starts with one count held
	// by the opening goroutine, so shutdown can't pass wg.Wait while
	// OpenAudioSession is still starting goroutines; opened releases it.
	var wg sync.WaitGroup
	wg.Add(1)
	var openedOnce sync.Once
	opened := func() { openedOnce.Do(wg.Done) }
	go func() {
		<-sessCtx.Done()
		stopAfter()
		if ctx.Err() != nil {
			// The AfterFunc may still be running in its own goroutine;
			// stopOnce makes this wait for it, so s.err is set before
			// done closes.
			s.stop(ctx.Err())
		}
		_ = conn.Close() // first: unblocks ICE callbacks stuck writing
		_ = pc.Close()
		_ = cmd.Process.Kill()
		// Unblock the pacer and sender even if a forking ffmpeg wrapper
		// left a child holding the pipes open.
		_ = encIn.Close()
		_ = encOut.Close()
		micMu.Lock()
		micStopping = true
		micMu.Unlock()
		if micCmd != nil {
			_ = micCmd.Process.Kill()
			<-micDone // closes s.mic
			_ = micCmd.Wait()
		} else if s.mic != nil {
			close(s.mic)
		}
		wg.Wait()
		_ = cmd.Wait() // only after the sender has stopped reading encOut
		close(s.done)
	}()
	fail := func(err error) (*AudioSession, error) {
		s.stop(err)
		opened()
		<-s.done
		return nil, err
	}

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "reachy-mini-sdk-go")
	if err != nil {
		return fail(fmt.Errorf("create audio track: %w", err))
	}

	errs := make(chan error, 8)
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		if err := sendICECandidate(conn, sessionID, candidate.ToJSON()); err != nil {
			reportErr(sessCtx, errs, fmt.Errorf("send local ICE candidate: %w", err))
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if s.mic != nil && remote.Kind() == webrtc.RTPCodecTypeAudio {
			micMu.Lock()
			started := false
			if !micStopping && micCmd == nil {
				dec, _, done, err := startMicDecoder(sessCtx, remote, opts.FFmpegPath, s.mic, errs)
				if err != nil {
					s.stop(fmt.Errorf("microphone: %w", err))
				} else {
					micCmd, micDone, started = dec, done, true
				}
			}
			micMu.Unlock()
			if started {
				return
			}
		}
		// The daemon sends its camera (and microphone) to every consumer;
		// drain what we don't use so pion's buffers don't fill. These end
		// with pc.
		go func() {
			for {
				if _, _, err := remote.ReadRTP(); err != nil {
					return
				}
			}
		}()
	})
	connected := make(chan struct{})
	var connectedOnce sync.Once
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateConnected:
			connectedOnce.Do(func() { close(connected) })
		case webrtc.PeerConnectionStateFailed:
			s.stop(errors.New("WebRTC connection failed"))
		case webrtc.PeerConnectionStateClosed:
			s.stop(errors.New("WebRTC connection closed"))
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		return fail(fmt.Errorf("set remote description: %w", err))
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		return fail(fmt.Errorf("add audio track: %w", err))
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(fmt.Errorf("create answer: %w", err))
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return fail(fmt.Errorf("set local description: %w", err))
	}
	// pion only reuses the offered audio transceiver if the daemon offered
	// to receive on it; otherwise AddTrack made a new, unnegotiated one.
	if !negotiatedSender(pc, sender) {
		return fail(errors.New("daemon does not accept audio from clients (offered no receiving audio transceiver)"))
	}
	if err := sendAnswer(conn, sessionID, answer.SDP); err != nil {
		return fail(fmt.Errorf("send answer: %w", err))
	}

	wg.Add(3)
	go func() {
		defer wg.Done()
		pumpSignalling(sessCtx, conn, pc, errs)
	}()
	go func() {
		defer wg.Done()
		// Read RTCP so pion's interceptors (NACK, reports) keep working.
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case err := <-errs:
				if !errors.Is(err, errAddICECandidate) {
					s.stop(err)
				}
			case <-sessCtx.Done():
				return
			}
		}
	}()

	select {
	case <-connected:
	case <-sessCtx.Done():
	}
	if sessCtx.Err() != nil { // also when both were ready at once
		opened()
		<-s.done
		return nil, fmt.Errorf("connect: %w", s.err)
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		s.pace(encIn)
	}()
	go func() {
		defer wg.Done()
		s.send(encOut, track)
	}()
	opened()
	return s, nil
}

// negotiatedSender reports whether sender's transceiver made it into the
// local description, with a direction that sends.
func negotiatedSender(pc *webrtc.PeerConnection, sender *webrtc.RTPSender) bool {
	for _, t := range pc.GetTransceivers() {
		if t.Sender() != sender {
			continue
		}
		d := t.Direction()
		return t.Mid() != "" && (d == webrtc.RTPTransceiverDirectionSendrecv || d == webrtc.RTPTransceiverDirectionSendonly)
	}
	return false
}

// pace feeds the encoder in real time: on each tick it writes the PCM due
// since the last one, from the queue, padded with silence.
func (s *AudioSession) pace(encIn io.WriteCloser) {
	defer encIn.Close()
	ticker := time.NewTicker(pacerTick)
	defer ticker.Stop()
	start := time.Now()
	var sent int64 // sample frames written
	maxFrames := int64(maxCatchUp.Seconds() * float64(s.sampleRate))
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		due := int64(time.Since(start).Seconds() * float64(s.sampleRate))
		n := due - sent
		if n <= 0 {
			continue
		}
		if n > maxFrames {
			sent += n - maxFrames
			n = maxFrames
		}
		buf := make([]byte, int(n)*s.bytesPerSample)
		s.mu.Lock()
		taken := copy(buf, s.queue)
		taken -= taken % s.bytesPerSample
		s.queue = s.queue[taken:]
		s.mu.Unlock()
		clear(buf[taken:]) // a partial trailing sample frame stays queued
		if _, err := encIn.Write(buf); err != nil {
			if s.ctx.Err() == nil {
				s.stop(fmt.Errorf("write to speaker encoder: %w", err))
			}
			return
		}
		sent += n
	}
}

// send forwards each encoded Opus frame to the WebRTC track.
func (s *AudioSession) send(encOut io.Reader, track *webrtc.TrackLocalStaticSample) {
	err := readOggPackets(encOut, func(packet []byte) error {
		if bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
			return nil
		}
		return track.WriteSample(media.Sample{Data: packet, Duration: opusFrameDuration * time.Millisecond})
	})
	if s.ctx.Err() != nil {
		return
	}
	if err == nil {
		err = errors.New("encoder exited")
	}
	s.stop(fmt.Errorf("speaker encoder: %w", err))
}

// Write queues pcm (signed 16-bit little-endian, in the session's
// SampleRate and Channels) to play after any audio already queued. It
// accepts all of pcm without waiting for playback; once the session has
// ended it returns why (waiting for shutdown to finish if it's underway).
func (s *AudioSession) Write(pcm []byte) (int, error) {
	if err := s.ended(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.queue = append(s.queue, pcm...)
	s.mu.Unlock()
	return len(pcm), nil
}

// Clear drops all queued audio that hasn't been sent yet, e.g. when the
// user interrupts the robot. Audio already sent is still in the daemon's
// playback buffer; call Client.ClearIncomingAudio to drop that too.
func (s *AudioSession) Clear() {
	s.mu.Lock()
	s.queue = nil
	s.mu.Unlock()
}

// Buffered returns how much queued audio hasn't been sent yet.
func (s *AudioSession) Buffered() time.Duration {
	s.mu.Lock()
	frames := len(s.queue) / s.bytesPerSample
	s.mu.Unlock()
	return time.Duration(frames) * time.Second / time.Duration(s.sampleRate)
}

// Drain waits until all queued audio has been handed to the encoder. The
// robot finishes playing it about half a second later: the encoder, the
// network and the daemon's 300 ms jitter buffer all sit in between. Close
// drops whatever is still in flight, so to finish an utterance, wait that
// long after Drain before closing.
func (s *AudioSession) Drain(ctx context.Context) error {
	ticker := time.NewTicker(pacerTick)
	defer ticker.Stop()
	for s.Buffered() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return s.ended()
		case <-ticker.C:
		}
	}
	return nil
}

// Microphone returns the robot microphone's audio as signed 16-bit
// little-endian mono PCM at AudioSampleRate, if opts.Microphone was set
// (nil otherwise). The channel is closed when the session ends, or just
// before if a microphone failure is what ends it. Keep reading it: if it
// isn't drained, incoming audio backs up and is eventually dropped.
func (s *AudioSession) Microphone() <-chan []byte {
	return s.mic
}

// Done is closed when the session has ended and released its resources.
func (s *AudioSession) Done() <-chan struct{} {
	return s.done
}

// Err returns why the session ended: nil while it's running or if Close
// ended it, ctx's error if its context ended it, or the failure that ended
// it.
func (s *AudioSession) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// Close ends the session, dropping any audio not yet played, and waits for
// it to release its resources. It returns Err, which is nil unless
// something else had already ended the session.
func (s *AudioSession) Close() error {
	s.stop(nil)
	<-s.done
	return s.err
}

// ended returns nil while the session is running, else why it ended
// (ErrAudioSessionClosed after Close).
func (s *AudioSession) ended() error {
	if s.ctx.Err() == nil {
		return nil
	}
	<-s.done
	if s.err != nil {
		return s.err
	}
	return ErrAudioSessionClosed
}
