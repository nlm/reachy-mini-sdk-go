//go:build live

// Live checks against a real daemon (robot or desktop simulator). They
// play tones on its speaker and wobble its head, so they only build with
// the "live" tag:
//
//	REACHY_MINI_URL=http://reachy-mini.local:8000 go test -tags live -run Live -v
//
// REACHY_MINI_URL defaults to http://localhost:8000; signalling uses the
// same host on port 8443.

package reachymini

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	url := os.Getenv("REACHY_MINI_URL")
	if url == "" {
		url = "http://localhost:8000"
	}
	c := New(url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := c.GetDaemonStatus(ctx)
	if err != nil {
		t.Fatalf("daemon at %s unreachable: %v", url, err)
	}
	t.Logf("daemon %s, state %s, wireless=%v", st.Version, st.State, st.WirelessVersion)
	return c
}

// liveTone is seconds of a 220 Hz tone at rate Hz with a 4 Hz,
// speech-like envelope for the wobbler to follow.
func liveTone(rate int, seconds float64) []byte {
	n := int(float64(rate) * seconds)
	pcm := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		ts := float64(i) / float64(rate)
		v := 9000 * math.Sin(2*math.Pi*220*ts) * (0.5 + 0.5*math.Sin(2*math.Pi*4*ts))
		pcm[2*i] = byte(int16(v))
		pcm[2*i+1] = byte(int16(v) >> 8)
	}
	return pcm
}

// liveHeadMotion samples the head pose for d and returns its largest
// rotation away from the first sample, in degrees.
func liveHeadMotion(ctx context.Context, t *testing.T, c *Client, d time.Duration) float64 {
	t.Helper()
	var first *XYZRPYPose
	maxDeg := 0.0
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		p, err := c.GetPresentHeadPose(ctx)
		if err != nil || p.XYZRPY == nil {
			continue
		}
		q := p.XYZRPY
		if first == nil {
			first = q
			continue
		}
		for _, delta := range []float64{q.Roll - first.Roll, q.Pitch - first.Pitch, q.Yaw - first.Yaw} {
			maxDeg = math.Max(maxDeg, math.Abs(delta)*180/math.Pi)
		}
	}
	return maxDeg
}

// liveWaitStill waits (up to 15 s) until the head stops moving, e.g. while
// the daemon finishes a wake-up, so motion measurements start from rest.
func liveWaitStill(ctx context.Context, t *testing.T, c *Client) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if liveHeadMotion(ctx, t, c, 500*time.Millisecond) < 0.2 {
			return
		}
	}
	t.Fatal("head never came to rest")
}

// liveRequire111 skips t on daemons older than 1.11.
func liveRequire111(t *testing.T, c *Client) {
	t.Helper()
	st, err := c.GetDaemonStatus(context.Background())
	if err != nil {
		t.Fatalf("GetDaemonStatus: %v", err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(st.Version, "%d.%d", &major, &minor); err != nil || major < 1 || major == 1 && minor < 11 {
		t.Skipf("needs daemon 1.11+, have %q", st.Version)
	}
}

func TestLiveDaemon111Endpoints(t *testing.T) {
	c := liveClient(t)
	liveRequire111(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name, err := c.GetRobotName(ctx)
	if err != nil {
		t.Fatalf("GetRobotName: %v", err)
	}
	t.Logf("robot name %q", name)
	if name != "" {
		// Rename to the same name: exercises the call without changing anything.
		got, err := c.SetRobotName(ctx, name)
		if err != nil || got != name {
			t.Errorf("SetRobotName(%q) = %q, %v", name, got, err)
		}
	}

	app, err := c.GetStartupApp(ctx)
	if err != nil {
		t.Fatalf("GetStartupApp: %v", err)
	}
	t.Logf("startup app %q", app)
	if err := c.SetStartupApp(ctx, app); err != nil { // rewrite the current value
		t.Errorf("SetStartupApp(%q): %v", app, err)
	}

	imu, err := c.GetIMU(ctx)
	if err != nil {
		t.Errorf("GetIMU: %v", err)
	}
	t.Logf("IMU %+v", imu)

	enabled, err := c.EnableHeadTracking(ctx, 0.5)
	if err != nil {
		t.Fatalf("EnableHeadTracking: %v", err)
	}
	defer c.DisableHeadTracking(context.Background())
	t.Logf("head tracking enabled: %v", enabled)
	time.Sleep(time.Second)
	face, err := c.GetTrackedFace(ctx)
	if err != nil {
		t.Errorf("GetTrackedFace: %v", err)
	}
	st, err := c.GetDaemonStatus(ctx)
	if err != nil {
		t.Fatalf("GetDaemonStatus: %v", err)
	}
	t.Logf("tracked face %+v, in status %+v", face, st.FaceTarget)
	if err := c.DisableHeadTracking(ctx); err != nil {
		t.Errorf("DisableHeadTracking: %v", err)
	}
}

func TestLiveReadOnlyEndpoints(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if id, err := c.GetHardwareID(ctx); err != nil {
		t.Errorf("GetHardwareID: %v", err)
	} else {
		t.Logf("hardware ID %q", id)
	}
	if lock, err := c.GetRobotAppLockStatus(ctx); err != nil {
		t.Errorf("GetRobotAppLockStatus: %v", err)
	} else {
		t.Logf("app lock %+v", lock)
	}
	s, err := c.GetFullStateWithOptions(ctx, FullStateOptions{HeadJoints: true, IMU: true, PoseMatrix: true, OmitAntennas: true})
	if err != nil {
		t.Fatalf("GetFullStateWithOptions: %v", err)
	}
	if s.HeadPose == nil || s.HeadPose.Matrix == nil {
		t.Errorf("HeadPose = %+v, want a matrix pose", s.HeadPose)
	}
	if len(s.AntennasPosition) != 0 {
		t.Errorf("AntennasPosition = %v, want omitted", s.AntennasPosition)
	}
	t.Logf("head joints %v, IMU %+v", s.HeadJoints, s.IMU)
}

func TestLiveAudioSession(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	s, err := c.OpenAudioSession(ctx, AudioSessionOptions{Microphone: true, SampleRate: 22050})
	if err != nil {
		t.Fatalf("OpenAudioSession: %v", err)
	}
	defer s.Close()
	t.Logf("connected in %v", time.Since(start))

	var micMu sync.Mutex
	micBytes := 0
	go func() {
		for chunk := range s.Microphone() {
			micMu.Lock()
			micBytes += len(chunk)
			micMu.Unlock()
		}
	}()

	if err := c.EnableWobbling(ctx); err != nil {
		t.Fatalf("EnableWobbling: %v", err)
	}
	defer c.DisableWobbling(context.Background())

	liveWaitStill(ctx, t, c)
	quiet := liveHeadMotion(ctx, t, c, time.Second)
	if _, err := s.Write(liveTone(22050, 2)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	playing := liveHeadMotion(ctx, t, c, 2*time.Second)
	t.Logf("head motion: %.2f° silent, %.2f° while playing", quiet, playing)
	if playing <= quiet {
		t.Errorf("head didn't wobble while playing (%.2f° vs %.2f° silent)", playing, quiet)
	}
	if err := s.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	time.Sleep(time.Second)

	// Barge-in: cut a long utterance after a second.
	if _, err := s.Write(liveTone(22050, 6)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(time.Second)
	s.Clear()
	if _, err := c.ClearIncomingAudio(ctx); err != nil {
		t.Errorf("ClearIncomingAudio: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // let the wobbler settle
	after := liveHeadMotion(ctx, t, c, 1500*time.Millisecond)
	t.Logf("head motion after barge-in: %.2f°", after)
	if after >= playing/2 {
		t.Errorf("head still wobbling after Clear+ClearIncomingAudio (%.2f° vs %.2f° playing)", after, playing)
	}

	micMu.Lock()
	t.Logf("microphone: %.1f s received", float64(micBytes)/2/AudioSampleRate)
	if micBytes == 0 {
		t.Error("no microphone audio received")
	}
	micMu.Unlock()
	if err := s.Err(); err != nil {
		t.Errorf("session ended early: %v", err)
	}
}
