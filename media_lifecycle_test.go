package reachymini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4/pkg/media"
)

func TestTrackDecoderStopWithoutStartClosesOutput(t *testing.T) {
	out := make(chan int)
	d := &trackDecoder{closeOut: func() { close(out) }}
	d.stop()
	if _, ok := <-out; ok {
		t.Fatal("output not closed")
	}
	d.stop() // idempotent: must not close twice
	ran, err := d.start(func() (decoderProc, error) {
		t.Error("start ran after stop")
		return decoderProc{}, nil
	})
	if ran || err != nil {
		t.Errorf("start after stop = %v, %v; want false, nil", ran, err)
	}
}

func TestTrackDecoderStartsOnce(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not on PATH")
	}
	d := &trackDecoder{closeOut: func() { t.Error("closeOut called although a decoder started") }}
	starts := 0
	startFn := func() (decoderProc, error) {
		starts++
		cmd := exec.Command("sleep", "60")
		stdin, _ := cmd.StdinPipe()
		if err := cmd.Start(); err != nil {
			return decoderProc{}, err
		}
		done := make(chan struct{})
		close(done)
		return decoderProc{cmd: cmd, pipes: []io.Closer{stdin}, done: done}, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := d.start(startFn); err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	if starts != 1 {
		t.Errorf("startFn ran %d times, want 1", starts)
	}
	d.stop()
	if d.proc.cmd.ProcessState == nil {
		t.Error("decoder process not reaped by stop")
	}
}

// ffmpegChildren lists this process's ffmpeg children, zombies included,
// as "pid:state".
func ffmpegChildren(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("ps", "--ppid", fmt.Sprint(os.Getpid()), "-o", "pid=,stat=,comm=").Output()
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	var kids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && strings.HasPrefix(f[2], "ffmpeg") {
			kids = append(kids, f[0]+":"+f[1])
		}
	}
	return kids
}

// waitNoNewFFmpegChildren fails unless every ffmpeg child not in before
// (a snapshot from ffmpegChildren) has been reaped within a few seconds.
// The snapshot keeps other tests' processes out of it.
func waitNoNewFFmpegChildren(t *testing.T, before []string) {
	t.Helper()
	old := make(map[string]bool)
	for _, k := range before {
		old[strings.SplitN(k, ":", 2)[0]] = true
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var kids []string
		for _, k := range ffmpegChildren(t) {
			if !old[strings.SplitN(k, ":", 2)[0]] {
				kids = append(kids, k)
			}
		}
		if len(kids) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ffmpeg children left behind (pid:state): %v", kids)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitClosed fails unless ch is closed (after draining) within 5 s.
func waitClosed[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatalf("%s not closed after cancel", what)
		}
	}
}

// requireClosedWithoutReading fails unless ch is already closed after
// waiting without reading from it: a reader blocked sending into ch (the
// old leak) would instead hand over a stale value here.
func requireClosedWithoutReading[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	time.Sleep(time.Second)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("%s still had a reader blocked sending after cancel", what)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s not closed after cancel", what)
	}
}

// cameraClient returns a Client whose REST API serves camera specs for a
// width x height default resolution; signalling goes to fp.
func cameraClient(t *testing.T, width, height int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/camera/specs" {
			http.NotFound(w, r)
			return
		}
		res := ResolutionInfo{Name: "test", Width: width, Height: height, FPS: 30}
		_ = json.NewEncoder(w).Encode(CameraSpecs{Name: "test", DefaultResolution: res, AvailableResolutions: []ResolutionInfo{res}})
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// sendVideo encodes a solid-colour VP8 clip and writes it to the
// producer's video track in real time until ctx ends.
func sendVideo(t *testing.T, ctx context.Context, fp *fakeProducer, width, height int) {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=red:s=%dx%d:r=30:d=4", width, height),
		"-c:v", "libvpx", "-deadline", "realtime", "-g", "10", "-f", "ivf", "-").Output()
	if err != nil {
		t.Fatalf("encode test video: %v", err)
	}
	frames, _ := parseIVFFrames(out)
	if len(frames) == 0 {
		t.Fatal("no test video frames")
	}
	go func() {
		ticker := time.NewTicker(time.Second / 30)
		defer ticker.Stop()
		for i := 0; ; i++ {
			if fp.VideoOut.WriteSample(media.Sample{Data: frames[i%len(frames)], Duration: time.Second / 30}) != nil {
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

func TestStreamCameraFramesLoopbackAndReap(t *testing.T) {
	requireFFmpeg(t)
	const width, height = 160, 120
	fp := newFakeProducer(t)
	c := cameraClient(t, width, height)
	before := ffmpegChildren(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	streamCtx, stopStream := context.WithCancel(ctx)
	frames, errs, err := c.StreamCameraFrames(streamCtx, CameraStreamOptions{SignallingURL: fp.SignallingURL})
	if err != nil {
		t.Fatalf("StreamCameraFrames: %v", err)
	}
	select {
	case <-fp.Connected:
	case <-ctx.Done():
		t.Fatal("never connected")
	}
	sendVideo(t, ctx, fp, width, height)

	select {
	case f := <-frames:
		if f.Width != width || f.Height != height || len(f.RGB) != width*height*3 {
			t.Fatalf("frame %dx%d with %d bytes", f.Width, f.Height, len(f.RGB))
		}
		// Centre pixel of a red frame (allowing for codec colour drift).
		px := f.RGB[(height/2*width+width/2)*3:]
		if px[0] < 180 || px[1] > 80 || px[2] > 80 {
			t.Errorf("centre pixel = %v, want red", px[:3])
		}
	case err := <-errs:
		t.Fatalf("stream error: %v", err)
	case <-ctx.Done():
		t.Fatal("no frame")
	}

	// Like a single-frame capture: stop reading, then cancel. The reader
	// must not stay blocked on the next frame, and ffmpeg must be reaped.
	time.Sleep(200 * time.Millisecond)
	stopStream()
	requireClosedWithoutReading(t, frames, "frames")
	waitNoNewFFmpegChildren(t, before)
}

func TestStreamCameraFramesClosedWithoutTrack(t *testing.T) {
	requireFFmpeg(t)
	fp := newFakeProducer(t) // never sends video
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	streamCtx, stopStream := context.WithCancel(ctx)
	frames, _, err := cameraClient(t, 160, 120).StreamCameraFrames(streamCtx, CameraStreamOptions{SignallingURL: fp.SignallingURL})
	if err != nil {
		t.Fatalf("StreamCameraFrames: %v", err)
	}
	<-fp.Connected
	stopStream()
	waitClosed(t, frames, "frames")
}

func TestStreamMicrophoneAudioReapsDecoder(t *testing.T) {
	requireFFmpeg(t)
	fp := newFakeProducer(t)
	before := ffmpegChildren(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	streamCtx, stopStream := context.WithCancel(ctx)
	pcm, _, err := New("http://127.0.0.1:1").StreamMicrophoneAudio(streamCtx, AudioStreamOptions{SignallingURL: fp.SignallingURL})
	if err != nil {
		t.Fatalf("StreamMicrophoneAudio: %v", err)
	}
	<-fp.Connected
	sendTone(t, ctx, fp, 3)
	select {
	case <-pcm:
	case <-ctx.Done():
		t.Fatal("no audio")
	}
	time.Sleep(200 * time.Millisecond) // stop reading
	stopStream()
	requireClosedWithoutReading(t, pcm, "pcm")
	waitNoNewFFmpegChildren(t, before)
}

func TestStreamMicrophoneAudioClosedWithoutTrack(t *testing.T) {
	requireFFmpeg(t)
	fp := newFakeProducer(t) // never sends audio
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	streamCtx, stopStream := context.WithCancel(ctx)
	pcm, _, err := New("http://127.0.0.1:1").StreamMicrophoneAudio(streamCtx, AudioStreamOptions{SignallingURL: fp.SignallingURL})
	if err != nil {
		t.Fatalf("StreamMicrophoneAudio: %v", err)
	}
	<-fp.Connected
	stopStream()
	waitClosed(t, pcm, "pcm")
}
