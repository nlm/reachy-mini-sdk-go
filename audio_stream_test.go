package reachymini

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"testing"
	"time"
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
	defer cmd.Process.Kill()

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
