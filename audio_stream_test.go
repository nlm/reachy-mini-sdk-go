package reachymini

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os/exec"
	"testing"
	"time"

	"github.com/pion/webrtc/v4/pkg/media"
)

// TestFFmpegOpusDecodePipeline exercises startFFmpegAudioDecoder against a
// real Ogg/Opus stream, independent of the robot or WebRTC/signalling code
// (which can't be tested without live hardware): it encodes a short sine
// wave to Opus, feeds the resulting Ogg container in through the same pipe
// depacketizeOpus's oggwriter would write to, and checks decoded PCM comes
// out the other end.
func TestFFmpegOpusDecodePipeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	const duration = 1 // second
	encode := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-ar", "48000", "-ac", "1",
		"-c:a", "libopus",
		"-f", "ogg", "-",
	)
	var ogg bytes.Buffer
	encode.Stdout = &ogg
	if err := encode.Run(); err != nil {
		t.Fatalf("encode test stream: %v", err)
	}
	if ogg.Len() == 0 {
		t.Fatal("encoded test stream is empty")
	}

	cmd, stdin, stdout, err := startFFmpegAudioDecoder("ffmpeg")
	if err != nil {
		t.Fatalf("startFFmpegAudioDecoder: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	pcmChunks := make(chan []byte)
	errs := make(chan error, 1)
	go readPCMChunks(context.Background(), stdout, pcmChunks, errs)

	go func() {
		defer stdin.Close()
		_, _ = stdin.Write(ogg.Bytes())
	}()

	total := 0
	timeout := time.After(10 * time.Second)
loop:
	for {
		select {
		case chunk, ok := <-pcmChunks:
			if !ok {
				break loop
			}
			total += len(chunk)
		case err := <-errs:
			t.Fatalf("decode error: %v", err)
		case <-timeout:
			t.Fatalf("timed out after %d PCM bytes", total)
		}
	}

	// duration seconds of s16le mono at AudioSampleRate Hz = duration *
	// AudioSampleRate * 2 bytes/sample; allow slack for encoder priming.
	wantApprox := duration * AudioSampleRate * 2
	if total < wantApprox/2 {
		t.Fatalf("got %d bytes of PCM, want roughly %d", total, wantApprox)
	}
}

func TestReadPCMChunksDeliversAllBytesThenCloses(t *testing.T) {
	const data = "some raw pcm bytes, longer than one might expect"
	r := io.NopCloser(bytes.NewReader([]byte(data)))

	pcmChunks := make(chan []byte)
	errs := make(chan error, 1)
	go readPCMChunks(context.Background(), r, pcmChunks, errs)

	var got bytes.Buffer
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case chunk, ok := <-pcmChunks:
			if !ok {
				break loop
			}
			got.Write(chunk)
		case err := <-errs:
			t.Fatalf("unexpected error: %v", err)
		case <-timeout:
			t.Fatal("timed out waiting for pcmChunks to close")
		}
	}

	if got.String() != data {
		t.Errorf("got %q, want %q", got.String(), data)
	}
}

// readErrCloser is an io.ReadCloser that returns a fixed error after
// yielding a fixed prefix, for exercising readPCMChunks' non-EOF error path.
type readErrCloser struct {
	data []byte
	err  error
	read bool
}

func (r *readErrCloser) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, r.err
}

func (r *readErrCloser) Close() error { return nil }

func TestReadPCMChunksSurfacesNonEOFError(t *testing.T) {
	wantErr := io.ErrClosedPipe
	r := &readErrCloser{data: []byte("partial"), err: wantErr}

	pcmChunks := make(chan []byte)
	errs := make(chan error, 1)
	go readPCMChunks(context.Background(), r, pcmChunks, errs)

	var got bytes.Buffer
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case chunk, ok := <-pcmChunks:
			if !ok {
				break loop
			}
			got.Write(chunk)
		case <-timeout:
			t.Fatal("timed out waiting for pcmChunks to close")
		}
	}

	if got.String() != "partial" {
		t.Errorf("got %q, want %q", got.String(), "partial")
	}

	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected a non-nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the error")
	}
}

// encodeOpusPackets encodes pcm (s16le mono at sampleRate) to 20 ms Opus
// packets, without the OpusHead/OpusTags header packets.
func encodeOpusPackets(t *testing.T, pcm []byte, sampleRate int) [][]byte {
	t.Helper()
	cmd, stdin, stdout, err := startFFmpegOpusEncoder("ffmpeg", sampleRate, 1)
	if err != nil {
		t.Fatalf("startFFmpegOpusEncoder: %v", err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	go func() {
		_, _ = stdin.Write(pcm)
		_ = stdin.Close()
	}()
	var packets [][]byte
	if err := readOggPackets(stdout, func(p []byte) error {
		packets = append(packets, append([]byte(nil), p...))
		return nil
	}); err != nil {
		t.Fatalf("readOggPackets: %v", err)
	}
	waited = true
	if err := cmd.Wait(); err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	if len(packets) < 3 {
		t.Fatalf("ffmpeg produced %d packets, want headers plus audio", len(packets))
	}
	return packets[2:]
}

// squareWave returns seconds of a loud s16le mono square wave.
func squareWave(sampleRate int, seconds float64) []byte {
	pcm := make([]byte, int(float64(sampleRate)*seconds)*2)
	for i := 0; i < len(pcm)/2; i++ {
		v := int16(8000)
		if (i/50)%2 == 0 {
			v = -v
		}
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
	}
	return pcm
}

// rms returns the root-mean-square level of s16le PCM.
func rms(pcm []byte) float64 {
	var sum float64
	n := len(pcm) / 2
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
		sum += v * v
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

func TestStreamMicrophoneAudioLoopback(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	fp := newFakeProducer(t)
	packets := encodeOpusPackets(t, squareWave(48000, 2), 48000)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pcm, errs, err := New("http://127.0.0.1:1").StreamMicrophoneAudio(ctx, AudioStreamOptions{SignallingURL: fp.SignallingURL})
	if err != nil {
		t.Fatalf("StreamMicrophoneAudio: %v", err)
	}

	select {
	case <-fp.Connected:
	case <-ctx.Done():
		t.Fatal("peer connection never established")
	}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for _, p := range packets {
			if err := fp.AudioOut.WriteSample(media.Sample{Data: p, Duration: 20 * time.Millisecond}); err != nil {
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Collect half a second of decoded audio.
	var got []byte
	for len(got) < AudioSampleRate*2/2 {
		select {
		case chunk, ok := <-pcm:
			if !ok {
				t.Fatalf("pcm closed after %d bytes", len(got))
			}
			got = append(got, chunk...)
		case err := <-errs:
			t.Fatalf("stream error: %v", err)
		case <-ctx.Done():
			t.Fatalf("timed out after %d PCM bytes", len(got))
		}
	}
	if level := rms(got); level < 1000 {
		t.Errorf("decoded audio RMS = %.0f, want a loud signal", level)
	}
}

func TestReadPCMChunksStopsOnCancelWithoutReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pcm := make(chan []byte) // never read
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		readPCMChunks(ctx, io.NopCloser(bytes.NewReader(make([]byte, 8192))), pcm, errs)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("readPCMChunks blocked on an unread channel after cancel")
	}
	if _, ok := <-pcm; ok {
		t.Error("pcm not closed")
	}
}
