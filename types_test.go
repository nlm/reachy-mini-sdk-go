package reachymini

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPoseMarshalUnmarshalXYZRPY(t *testing.T) {
	p := NewXYZRPYPose(1, 2, 3, 0.1, 0.2, 0.3)

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var want map[string]any
	if err := json.Unmarshal([]byte(`{"x":1,"y":2,"z":3,"roll":0.1,"pitch":0.2,"yaw":0.3}`), &want); err != nil {
		t.Fatalf("parse want: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse got: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("marshalled to %s, want fields %v", data, want)
	}

	var round Pose
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.Matrix != nil {
		t.Fatalf("round-tripped as Matrix, want XYZRPY")
	}
	if round.XYZRPY == nil || *round.XYZRPY != *p.XYZRPY {
		t.Fatalf("got %+v, want %+v", round.XYZRPY, p.XYZRPY)
	}
}

func TestPoseMarshalUnmarshalMatrix(t *testing.T) {
	var m [16]float64
	for i := range m {
		m[i] = float64(i)
	}
	p := NewMatrixPose(m)

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var round Pose
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.XYZRPY != nil {
		t.Fatalf("round-tripped as XYZRPY, want Matrix")
	}
	if round.Matrix == nil || round.Matrix.M != m {
		t.Fatalf("got %+v, want %+v", round.Matrix, m)
	}
}

func TestPoseMarshalEmptyIsNull(t *testing.T) {
	var p Pose
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("got %s, want null", data)
	}
}

func TestPoseUnmarshalNull(t *testing.T) {
	var p Pose
	if err := json.Unmarshal([]byte("null"), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.XYZRPY != nil || p.Matrix != nil {
		t.Fatalf("got %+v, want zero value", p)
	}
}

func TestGotoRequestOmitsUnsetFields(t *testing.T) {
	req := GotoRequest{Duration: 1.5, Interpolation: InterpolationMinJerk}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"head_pose", "antennas", "body_yaw"} {
		if _, ok := m[key]; ok {
			t.Errorf("expected %q to be omitted, got %s", key, data)
		}
	}
	if m["duration"] != 1.5 {
		t.Errorf("duration = %v, want 1.5", m["duration"])
	}
	if m["interpolation"] != string(InterpolationMinJerk) {
		t.Errorf("interpolation = %v, want %q", m["interpolation"], InterpolationMinJerk)
	}
}

func TestFullBodyTargetWithHeadPose(t *testing.T) {
	target := FullBodyTarget{
		TargetHeadPose: NewXYZRPYPose(1, 0, 0, 0, 0, 0),
		TargetAntennas: &[2]float64{0.1, 0.2},
	}
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var round FullBodyTarget
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.TargetHeadPose == nil || round.TargetHeadPose.XYZRPY == nil {
		t.Fatalf("got nil head pose after round-trip: %s", data)
	}
	if *round.TargetHeadPose.XYZRPY != *target.TargetHeadPose.XYZRPY {
		t.Errorf("got %+v, want %+v", round.TargetHeadPose.XYZRPY, target.TargetHeadPose.XYZRPY)
	}
	if round.TargetAntennas == nil || *round.TargetAntennas != *target.TargetAntennas {
		t.Errorf("got %+v, want %+v", round.TargetAntennas, target.TargetAntennas)
	}
	if round.Timestamp != nil {
		t.Errorf("got timestamp %v, want nil (was unset)", round.Timestamp)
	}
}
