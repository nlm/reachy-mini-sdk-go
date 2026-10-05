package reachymini

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
}

// openTestSession opens an AudioSession against fp.
func openTestSession(t *testing.T, ctx context.Context, fp *fakeProducer, opts AudioSessionOptions) *AudioSession {
	t.Helper()
	opts.SignallingURL = fp.SignallingURL
	s, err := New("http://127.0.0.1:1").OpenAudioSession(ctx, opts)
	if err != nil {
		t.Fatalf("OpenAudioSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// receivedAudio decodes the producer's incoming audio track to PCM and
// counts its RTP packets, until ctx ends.
type receivedAudio struct {
	mu      sync.Mutex
	pcm     []byte
	packets int
}

func (r *receivedAudio) snapshot() ([]byte, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.pcm...), r.packets
}

func receiveAudio(t *testing.T, ctx context.Context, fp *fakeProducer) *receivedAudio {
	t.Helper()
	var track *webrtc.TrackRemote
	select {
	case track = <-fp.AudioIn:
	case <-ctx.Done():
		t.Fatal("producer never received an audio track")
	}
	cmd, decIn, decOut, err := startFFmpegAudioDecoder("ffmpeg")
	if err != nil {
		t.Fatalf("start decoder: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	w, err := oggwriter.NewWith(decIn, 48000, 2)
	if err != nil {
		t.Fatalf("ogg writer: %v", err)
	}
	r := &receivedAudio{}
	go func() {
		defer decIn.Close()
		for {
			p, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			r.mu.Lock()
			r.packets++
			r.mu.Unlock()
			if w.WriteRTP(p) != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := decOut.Read(buf)
			r.mu.Lock()
			r.pcm = append(r.pcm, buf[:n]...)
			r.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return r
}

// loudestWindowRMS returns the highest RMS over consecutive 100 ms windows
// of s16le mono PCM at AudioSampleRate.
func loudestWindowRMS(pcm []byte) float64 {
	const window = AudioSampleRate / 10 * 2
	best := 0.0
	for i := 0; i+window <= len(pcm); i += window {
		best = max(best, rms(pcm[i:i+window]))
	}
	return best
}

func TestAudioSessionPlaysWrittenAudio(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fp := newFakeProducer(t)
	const rate = 22050 // piper's rate: exercises resampling
	s := openTestSession(t, ctx, fp, AudioSessionOptions{SampleRate: rate})
	rx := receiveAudio(t, ctx, fp)

	// Half a second of silence, then one second of tone.
	time.Sleep(500 * time.Millisecond)
	if _, err := s.Write(squareWave(rate, 1)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if b := s.Buffered(); b < 900*time.Millisecond || b > time.Second {
		t.Errorf("Buffered = %v right after writing 1s, want ~1s", b)
	}

	start := time.Now()
	// The tone should be audible at the producer well within a second of
	// being written (encoder probing once delayed it ~5 s).
	for {
		pcm, _ := rx.snapshot()
		if loudestWindowRMS(pcm) >= 3000 {
			break
		}
		if time.Since(start) > time.Second {
			t.Fatalf("tone not received %v after Write", time.Since(start))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("write-to-receive latency: %v", time.Since(start))

	if err := s.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	// Paced in real time: 1 s of audio can't drain much faster.
	if d := time.Since(start); d < 900*time.Millisecond || d > 2*time.Second {
		t.Errorf("Drain took %v, want ~1s", d)
	}
	time.Sleep(300 * time.Millisecond) // let the tail through the pipeline

	pcm, packets := rx.snapshot()
	elapsed := time.Since(start) + 500*time.Millisecond
	// The session streams continuously, silence included: ~50 packets/s.
	if want := int(elapsed / (20 * time.Millisecond)); packets < want*7/10 || packets > want*13/10 {
		t.Errorf("producer got %d packets in %v, want ~%d", packets, elapsed, want)
	}
	if level := rms(pcm[:min(len(pcm), AudioSampleRate*2/5)]); level > 200 {
		t.Errorf("leading silence RMS = %.0f, want near zero", level)
	}
	if level := loudestWindowRMS(pcm); level < 3000 {
		t.Errorf("loudest window RMS = %.0f, want the written tone", level)
	}
}

func TestAudioSessionClear(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := openTestSession(t, ctx, newFakeProducer(t), AudioSessionOptions{})
	if _, err := s.Write(squareWave(AudioSampleRate, 5)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	s.Clear()
	if b := s.Buffered(); b != 0 {
		t.Errorf("Buffered after Clear = %v, want 0", b)
	}
	if err := s.Drain(ctx); err != nil {
		t.Errorf("Drain after Clear: %v", err)
	}
}

func TestAudioSessionCloseAndWrite(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := openTestSession(t, ctx, newFakeProducer(t), AudioSessionOptions{})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
	if err := s.Err(); err != nil {
		t.Errorf("Err after Close = %v, want nil", err)
	}
	if _, err := s.Write([]byte{0, 0}); !errors.Is(err, ErrAudioSessionClosed) {
		t.Errorf("Write after Close = %v, want ErrAudioSessionClosed", err)
	}
	if err := s.Drain(ctx); !errors.Is(err, ErrAudioSessionClosed) && err != nil {
		t.Errorf("Drain after Close = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestAudioSessionContextCancel(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sessCtx, sessCancel := context.WithCancel(ctx)
	s := openTestSession(t, sessCtx, newFakeProducer(t), AudioSessionOptions{})
	sessCancel()
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal("session didn't end after its context was cancelled")
	}
	if err := s.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("Err = %v, want context.Canceled", err)
	}
	if _, err := s.Write([]byte{0, 0}); !errors.Is(err, context.Canceled) {
		t.Errorf("Write = %v, want context.Canceled", err)
	}
}

func TestAudioSessionDaemonWithoutAudioReceive(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fp := newFakeProducerWithAudio(t, webrtc.RTPTransceiverDirectionSendonly)
	_, err := New("http://127.0.0.1:1").OpenAudioSession(ctx, AudioSessionOptions{SignallingURL: fp.SignallingURL})
	if err == nil {
		t.Fatal("OpenAudioSession: want error when the daemon offers no receiving audio transceiver")
	}
}

func TestAudioSessionMissingFFmpeg(t *testing.T) {
	_, err := New("http://127.0.0.1:1").OpenAudioSession(context.Background(), AudioSessionOptions{FFmpegPath: "/nonexistent/ffmpeg"})
	if err == nil {
		t.Fatal("want error for missing ffmpeg")
	}
}

func TestAudioSessionContextCancelledDuringNegotiation(t *testing.T) {
	requireFFmpeg(t)
	// A signalling server that accepts the connection but never speaks.
	upgrader := websocket.Upgrader{}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := New("http://127.0.0.1:1").OpenAudioSession(ctx, AudioSessionOptions{
		SignallingURL: "ws" + strings.TrimPrefix(srv.URL, "http"),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("OpenAudioSession returned after %v, want soon after the 300ms deadline", d)
	}
}

// sendTone writes Opus-encoded tone packets to the producer's outgoing
// audio track in real time until ctx ends or they run out.
func sendTone(t *testing.T, ctx context.Context, fp *fakeProducer, seconds float64) {
	t.Helper()
	packets := encodeOpusPackets(t, squareWave(48000, seconds), 48000)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for _, p := range packets {
			if fp.AudioOut.WriteSample(media.Sample{Data: p, Duration: 20 * time.Millisecond}) != nil {
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}

func TestAudioSessionDuplex(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fp := newFakeProducer(t)
	s := openTestSession(t, ctx, fp, AudioSessionOptions{Microphone: true})
	rx := receiveAudio(t, ctx, fp)
	sendTone(t, ctx, fp, 2)
	if _, err := s.Write(squareWave(AudioSampleRate, 1)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var mic []byte
	for len(mic) < AudioSampleRate { // half a second of 16-bit mono
		select {
		case chunk, ok := <-s.Microphone():
			if !ok {
				t.Fatalf("Microphone closed after %d bytes: %v", len(mic), s.Err())
			}
			mic = append(mic, chunk...)
		case <-ctx.Done():
			t.Fatalf("timed out after %d microphone bytes", len(mic))
		}
	}
	if level := rms(mic); level < 1000 {
		t.Errorf("microphone RMS = %.0f, want the producer's tone", level)
	}

	if err := s.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if pcm, _ := rx.snapshot(); loudestWindowRMS(pcm) < 3000 {
		t.Errorf("speaker audio not received while the microphone was streaming")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for range s.Microphone() { // must be closed, possibly after buffered chunks
	}
}

func TestAudioSessionMicrophoneClosedOnCloseWithoutReader(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fp := newFakeProducer(t)
	s := openTestSession(t, ctx, fp, AudioSessionOptions{Microphone: true})
	sendTone(t, ctx, fp, 2)
	time.Sleep(500 * time.Millisecond) // decoder running, nobody reading

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung with an unread microphone channel")
	}
	if _, ok := <-s.Microphone(); ok {
		// One chunk may have been in flight; the channel must still close.
		for range s.Microphone() {
		}
	}
}

func TestAudioSessionNoMicrophoneByDefault(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := openTestSession(t, ctx, newFakeProducer(t), AudioSessionOptions{})
	if s.Microphone() != nil {
		t.Error("Microphone() non-nil without opts.Microphone")
	}
}

func TestAudioSessionMicrophoneClosedWhenNoTrackArrived(t *testing.T) {
	requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// The producer never sends mic audio, so no decoder ever starts.
	s := openTestSession(t, ctx, newFakeProducer(t), AudioSessionOptions{Microphone: true})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := <-s.Microphone(); ok {
		t.Error("Microphone channel still open after Close")
	}
}
