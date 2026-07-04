package reachymini

import (
	"encoding/json"
	"time"
)

// Pose mirrors the daemon's `AnyPose = XYZRPYPose | Matrix4x4Pose` union.
// Exactly one of XYZRPY or Matrix should be set.
type Pose struct {
	XYZRPY *XYZRPYPose
	Matrix *Matrix4x4Pose
}

// XYZRPYPose is a Cartesian position plus roll/pitch/yaw orientation.
type XYZRPYPose struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Roll  float64 `json:"roll"`
	Pitch float64 `json:"pitch"`
	Yaw   float64 `json:"yaw"`
}

// Matrix4x4Pose is a raw row-major 4x4 homogeneous transform.
type Matrix4x4Pose struct {
	M [16]float64 `json:"m"`
}

// NewXYZRPYPose builds a Pose from Cartesian position and roll/pitch/yaw.
func NewXYZRPYPose(x, y, z, roll, pitch, yaw float64) *Pose {
	return &Pose{XYZRPY: &XYZRPYPose{X: x, Y: y, Z: z, Roll: roll, Pitch: pitch, Yaw: yaw}}
}

// NewMatrixPose builds a Pose from a raw 4x4 transform.
func NewMatrixPose(m [16]float64) *Pose {
	return &Pose{Matrix: &Matrix4x4Pose{M: m}}
}

// MarshalJSON emits whichever variant is set. The daemon's Pydantic model
// distinguishes the union by field shape rather than an explicit tag, so we
// match on the presence of the "m" field (verified against a live daemon's
// /openapi.json: XYZRPYPose has no "m" field, Matrix4x4Pose requires one).
func (p Pose) MarshalJSON() ([]byte, error) {
	switch {
	case p.Matrix != nil:
		return json.Marshal(p.Matrix)
	case p.XYZRPY != nil:
		return json.Marshal(p.XYZRPY)
	default:
		return []byte("null"), nil
	}
}

func (p *Pose) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var probe struct {
		M *[16]float64 `json:"m"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.M != nil {
		var m Matrix4x4Pose
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		p.Matrix = &m
		return nil
	}
	var xyz XYZRPYPose
	if err := json.Unmarshal(data, &xyz); err != nil {
		return err
	}
	p.XYZRPY = &xyz
	return nil
}

// InterpolationTechnique selects how `Goto` blends between the current and
// target pose. Verified against a live daemon's /openapi.json.
type InterpolationTechnique string

const (
	InterpolationLinear    InterpolationTechnique = "linear"
	InterpolationMinJerk   InterpolationTechnique = "minjerk"
	InterpolationEaseInOut InterpolationTechnique = "ease_in_out"
	InterpolationCartoon   InterpolationTechnique = "cartoon"
)

// MotorControlMode selects the robot's motor control mode. Verified against
// a live daemon's /openapi.json.
type MotorControlMode string

const (
	MotorModeEnabled             MotorControlMode = "enabled"
	MotorModeDisabled            MotorControlMode = "disabled"
	MotorModeGravityCompensation MotorControlMode = "gravity_compensation"
)

// MoveUUID identifies a move started via Goto, WakeUp, GotoSleep, or
// PlayRecordedMoveDataset, for use with Stop/ws/updates.
type MoveUUID struct {
	UUID string `json:"uuid"`
}

// GotoRequest is the body of POST /api/move/goto.
type GotoRequest struct {
	HeadPose      *Pose                  `json:"head_pose,omitempty"`
	Antennas      *[2]float64            `json:"antennas,omitempty"`
	BodyYaw       *float64               `json:"body_yaw,omitempty"`
	Duration      float64                `json:"duration"`
	Interpolation InterpolationTechnique `json:"interpolation"`
}

// FullBodyTarget is the body of POST /api/move/set_target and each frame of
// WS /api/move/ws/set_target.
type FullBodyTarget struct {
	TargetHeadPose *Pose       `json:"target_head_pose,omitempty"`
	TargetAntennas *[2]float64 `json:"target_antennas,omitempty"`
	TargetBodyYaw  *float64    `json:"target_body_yaw,omitempty"`
	Timestamp      *time.Time  `json:"timestamp,omitempty"`
}

// DoAInfo is the robot's direction-of-arrival (sound source) estimate.
type DoAInfo struct {
	Angle          float64 `json:"angle"`
	SpeechDetected bool    `json:"speech_detected"`
}

// FullState is the response of GET /api/state/full and each frame of
// WS /api/state/ws/full.
type FullState struct {
	ControlMode      *MotorControlMode `json:"control_mode,omitempty"`
	HeadPose         *Pose             `json:"head_pose,omitempty"`
	HeadJoints       []float64         `json:"head_joints,omitempty"`
	BodyYaw          *float64          `json:"body_yaw,omitempty"`
	AntennasPosition []float64         `json:"antennas_position,omitempty"`
	PassiveJoints    []float64         `json:"passive_joints,omitempty"`
	DoA              *DoAInfo          `json:"doa,omitempty"`
	Timestamp        *time.Time        `json:"timestamp,omitempty"`
}

// MotorStatus is the response of GET /api/motors/status.
type MotorStatus struct {
	Mode MotorControlMode `json:"mode"`
}
