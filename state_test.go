package reachymini

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// serveJSON returns a Client whose server records the request URL and
// replies with body.
func serveJSON(t *testing.T, body string, gotURL **url.URL) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotURL = r.URL
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestGetFullStateSendsNoQueryByDefault(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `{}`, &got)
	if _, err := c.GetFullState(context.Background()); err != nil {
		t.Fatalf("GetFullState: %v", err)
	}
	if got.Path != "/api/state/full" || got.RawQuery != "" {
		t.Errorf("url = %s, want /api/state/full with no query", got)
	}
}

func TestGetFullStateWithOptionsQuery(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `{}`, &got)
	_, err := c.GetFullStateWithOptions(context.Background(), FullStateOptions{
		OmitControlMode: true,
		OmitHeadPose:    true,
		OmitBodyYaw:     true,
		OmitAntennas:    true,
		HeadJoints:      true,
		PassiveJoints:   true,
		DoA:             true,
		IMU:             true,
		PoseMatrix:      true,
	})
	if err != nil {
		t.Fatalf("GetFullStateWithOptions: %v", err)
	}
	want := url.Values{
		"with_control_mode":      {"false"},
		"with_head_pose":         {"false"},
		"with_body_yaw":          {"false"},
		"with_antenna_positions": {"false"},
		"with_head_joints":       {"true"},
		"with_passive_joints":    {"true"},
		"with_doa":               {"true"},
		"with_imu":               {"true"},
		"use_pose_matrix":        {"true"},
	}
	if got.Path != "/api/state/full" {
		t.Errorf("path = %q, want /api/state/full", got.Path)
	}
	if got.Query().Encode() != want.Encode() {
		t.Errorf("query = %s, want %s", got.Query().Encode(), want.Encode())
	}
}

func TestGetFullStateDecodesIMU(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `{"imu":{"accelerometer":[0.1,0.2,9.8],"gyroscope":[0,0,0.5],"quaternion":[1,0,0,0],"temperature":31.5}}`, &got)
	s, err := c.GetFullStateWithOptions(context.Background(), FullStateOptions{IMU: true})
	if err != nil {
		t.Fatalf("GetFullStateWithOptions: %v", err)
	}
	if s.IMU == nil {
		t.Fatal("IMU = nil, want reading")
	}
	if s.IMU.Accelerometer[2] != 9.8 || s.IMU.Quaternion[0] != 1 || s.IMU.Temperature != 31.5 {
		t.Errorf("IMU = %+v", *s.IMU)
	}
}

func TestGetIMU(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `{"accelerometer":[1,2,3],"gyroscope":[4,5,6],"quaternion":[0.5,0.5,0.5,0.5],"temperature":30}`, &got)
	d, err := c.GetIMU(context.Background())
	if err != nil {
		t.Fatalf("GetIMU: %v", err)
	}
	if got.Path != "/api/state/imu" {
		t.Errorf("path = %q, want /api/state/imu", got.Path)
	}
	want := ImuData{
		Accelerometer: [3]float64{1, 2, 3},
		Gyroscope:     [3]float64{4, 5, 6},
		Quaternion:    [4]float64{0.5, 0.5, 0.5, 0.5},
		Temperature:   30,
	}
	if d == nil || *d != want {
		t.Errorf("GetIMU = %+v, want %+v", d, want)
	}
}

func TestGetIMUUnavailable(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `null`, &got)
	d, err := c.GetIMU(context.Background())
	if err != nil {
		t.Fatalf("GetIMU: %v", err)
	}
	if d != nil {
		t.Errorf("GetIMU = %+v, want nil", d)
	}
}

func TestGetDaemonStatusDecodesFaceTarget(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `{"state":"running","face_target":{"detected":true,"x":-0.25,"y":0.1,"roll":0,"ts":12.5}}`, &got)
	s, err := c.GetDaemonStatus(context.Background())
	if err != nil {
		t.Fatalf("GetDaemonStatus: %v", err)
	}
	ft := s.FaceTarget
	if !ft.Detected || ft.X == nil || *ft.X != -0.25 || ft.TS == nil || *ft.TS != 12.5 {
		t.Errorf("FaceTarget = %+v", ft)
	}
}

func TestStreamFullStateWithOptionsQuery(t *testing.T) {
	gotQuery := make(chan url.Values, 1)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery <- r.URL.Query()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, _, err := c.StreamFullStateWithOptions(context.Background(), StreamFullStateOptions{
		FullStateOptions: FullStateOptions{OmitControlMode: true, IMU: true},
		Frequency:        50,
	})
	if err != nil {
		t.Fatalf("StreamFullStateWithOptions: %v", err)
	}
	select {
	case q := <-gotQuery:
		want := url.Values{"with_imu": {"true"}, "frequency": {"50"}}
		if q.Encode() != want.Encode() {
			t.Errorf("query = %s, want %s", q.Encode(), want.Encode())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never saw a request")
	}
}

func TestStreamFullStateSendsNoQueryByDefault(t *testing.T) {
	gotQuery := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery <- r.URL.RawQuery
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	if _, _, err := New(srv.URL).StreamFullStateWithOptions(context.Background(), StreamFullStateOptions{Frequency: math.Inf(1)}); err != nil {
		t.Fatalf("StreamFullStateWithOptions: %v", err)
	}
	if q := <-gotQuery; q != "" {
		t.Errorf("query = %q, want empty", q)
	}
}

func TestGetFullStateExplicitNulls(t *testing.T) {
	var got *url.URL
	c := serveJSON(t, `{"control_mode":"enabled","imu":null,"doa":null,"passive_joints":null}`, &got)
	s, err := c.GetFullStateWithOptions(context.Background(), FullStateOptions{IMU: true, DoA: true, PassiveJoints: true})
	if err != nil {
		t.Fatalf("GetFullStateWithOptions: %v", err)
	}
	if s.IMU != nil || s.DoA != nil || s.PassiveJoints != nil {
		t.Errorf("want nil IMU/DoA/PassiveJoints, got %+v", s)
	}
	if s.ControlMode == nil || *s.ControlMode != MotorModeEnabled {
		t.Errorf("ControlMode = %v, want enabled", s.ControlMode)
	}
}

func TestStreamFullStateWithOptionsDecodesIMU(t *testing.T) {
	c := dialWS(t, "/api/state/ws/full", func(server *websocket.Conn) {
		defer server.Close()
		_ = server.WriteMessage(websocket.TextMessage, []byte(`{"imu":null}`))
		_ = server.WriteMessage(websocket.TextMessage, []byte(`{"imu":{"accelerometer":[0,0,9.81],"gyroscope":[0,0,0],"quaternion":[1,0,0,0],"temperature":28}}`))
	})
	states, errs, err := c.StreamFullStateWithOptions(context.Background(), StreamFullStateOptions{
		FullStateOptions: FullStateOptions{IMU: true},
	})
	if err != nil {
		t.Fatalf("StreamFullStateWithOptions: %v", err)
	}
	var frames []FullState
	timeout := time.After(5 * time.Second)
	for len(frames) < 2 {
		select {
		case s, ok := <-states:
			if !ok {
				t.Fatalf("states closed after %d frames: %v", len(frames), <-errs)
			}
			frames = append(frames, s)
		case <-timeout:
			t.Fatalf("timed out after %d frames", len(frames))
		}
	}
	if frames[0].IMU != nil {
		t.Errorf("frame 0 IMU = %+v, want nil", frames[0].IMU)
	}
	if frames[1].IMU == nil || frames[1].IMU.Accelerometer[2] != 9.81 || frames[1].IMU.Temperature != 28 {
		t.Errorf("frame 1 IMU = %+v", frames[1].IMU)
	}
}
