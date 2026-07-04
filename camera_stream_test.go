package reachymini

import (
	"bytes"
	"context"
	"encoding/binary"
	"os/exec"
	"testing"
	"time"
)

// TestFFmpegH264DecodePipeline exercises startFFmpegDecoder("h264", ...) and
// readRGBFrames against a synthetic H.264 stream, independent of the robot
// or the WebRTC/signalling code (which can't be tested without live
// hardware). It encodes a short test pattern, feeds it in through the same
// pipe the RTP depacketizer would write to, and checks the right number of
// correctly-sized RGB frames come out the other end.
func TestFFmpegH264DecodePipeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	const width, height, fps, duration = 320, 240, 10, 1
	encode := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-profile:v", "baseline",
		"-bsf:v", "h264_mp4toannexb",
		"-f", "h264", "-",
	)
	var h264 bytes.Buffer
	encode.Stdout = &h264
	if err := encode.Run(); err != nil {
		t.Fatalf("encode test stream: %v", err)
	}
	if h264.Len() == 0 {
		t.Fatal("encoded test stream is empty")
	}

	cmd, stdin, stdout, err := startFFmpegDecoder("ffmpeg", "h264", width, height)
	if err != nil {
		t.Fatalf("startFFmpegDecoder: %v", err)
	}
	defer cmd.Process.Kill()

	frames := make(chan Frame)
	errs := make(chan error, 1)
	go readRGBFrames(context.Background(), stdout, width, height, frames, errs)

	go func() {
		defer stdin.Close()
		_, _ = stdin.Write(h264.Bytes())
	}()

	got := drainFrames(t, frames, errs, width, height)
	if got != fps*duration {
		t.Fatalf("got %d frames, want %d", got, fps*duration)
	}
}

// TestFFmpegVP8DecodePipeline exercises writeIVFHeader/writeIVFFrame and
// startFFmpegDecoder("ivf", ...) against real VP8 frame payloads (extracted
// from an ffmpeg-encoded IVF file, stripping ffmpeg's own IVF framing so
// only our hand-written framing is under test), matching the shape
// depacketizeVP8 produces from RTP.
func TestFFmpegVP8DecodePipeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	const width, height, fps, duration = 320, 240, 10, 1
	encode := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1",
		"-pix_fmt", "yuv420p",
		"-c:v", "libvpx",
		"-f", "ivf", "-",
	)
	var ivf bytes.Buffer
	encode.Stdout = &ivf
	if err := encode.Run(); err != nil {
		t.Fatalf("encode test stream: %v", err)
	}

	vp8Frames, err := parseIVFFrames(ivf.Bytes())
	if err != nil {
		t.Fatalf("parse reference IVF file: %v", err)
	}
	if len(vp8Frames) == 0 {
		t.Fatal("reference IVF file had no frames")
	}

	cmd, stdin, stdout, err := startFFmpegDecoder("ffmpeg", "ivf", width, height)
	if err != nil {
		t.Fatalf("startFFmpegDecoder: %v", err)
	}
	defer cmd.Process.Kill()

	frames := make(chan Frame)
	errs := make(chan error, 1)
	go readRGBFrames(context.Background(), stdout, width, height, frames, errs)

	go func() {
		defer stdin.Close()
		if err := writeIVFHeader(stdin, width, height); err != nil {
			return
		}
		for i, f := range vp8Frames {
			if err := writeIVFFrame(stdin, f, uint64(i)); err != nil {
				return
			}
		}
	}()

	got := drainFrames(t, frames, errs, width, height)
	if got != len(vp8Frames) {
		t.Fatalf("got %d frames, want %d", got, len(vp8Frames))
	}
}

func drainFrames(t *testing.T, frames <-chan Frame, errs <-chan error, width, height int) int {
	t.Helper()
	got := 0
	timeout := time.After(10 * time.Second)
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return got
			}
			if len(frame.RGB) != width*height*3 {
				t.Fatalf("frame %d: got %d bytes, want %d", got, len(frame.RGB), width*height*3)
			}
			if frame.Width != width || frame.Height != height {
				t.Fatalf("frame %d: got %dx%d, want %dx%d", got, frame.Width, frame.Height, width, height)
			}
			got++
		case err := <-errs:
			t.Fatalf("decode error: %v", err)
		case <-timeout:
			t.Fatalf("timed out after %d frames", got)
		}
	}
}

func TestWriteIVFHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := writeIVFHeader(&buf, 320, 240); err != nil {
		t.Fatalf("writeIVFHeader: %v", err)
	}
	got := buf.Bytes()
	if len(got) != 32 {
		t.Fatalf("header length = %d, want 32", len(got))
	}
	if string(got[0:4]) != "DKIF" {
		t.Errorf("signature = %q, want DKIF", got[0:4])
	}
	if string(got[8:12]) != "VP80" {
		t.Errorf("fourcc = %q, want VP80", got[8:12])
	}
	if w := binary.LittleEndian.Uint16(got[12:14]); w != 320 {
		t.Errorf("width = %d, want 320", w)
	}
	if h := binary.LittleEndian.Uint16(got[14:16]); h != 240 {
		t.Errorf("height = %d, want 240", h)
	}
}

func TestWriteIVFFrame(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := writeIVFFrame(&buf, payload, 7); err != nil {
		t.Fatalf("writeIVFFrame: %v", err)
	}
	got := buf.Bytes()
	if len(got) != 12+len(payload) {
		t.Fatalf("frame record length = %d, want %d", len(got), 12+len(payload))
	}
	if size := binary.LittleEndian.Uint32(got[0:4]); size != uint32(len(payload)) {
		t.Errorf("size field = %d, want %d", size, len(payload))
	}
	if ts := binary.LittleEndian.Uint64(got[4:12]); ts != 7 {
		t.Errorf("timestamp field = %d, want 7", ts)
	}
	if !bytes.Equal(got[12:], payload) {
		t.Errorf("payload = %v, want %v", got[12:], payload)
	}
}

// parseIVFFrames extracts raw frame payloads from an IVF-container byte
// slice, skipping the 32-byte file header and each frame's 12-byte record
// header.
func parseIVFFrames(data []byte) ([][]byte, error) {
	if len(data) < 32 {
		return nil, nil
	}
	var frames [][]byte
	pos := 32
	for pos+12 <= len(data) {
		size := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 12
		if pos+int(size) > len(data) {
			break
		}
		frames = append(frames, data[pos:pos+int(size)])
		pos += int(size)
	}
	return frames, nil
}
