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
	// X, Y, Z are the position, in the daemon's native units (meters,
	// matching the robot's URDF -- see GetURDF).
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
	// Roll, Pitch, Yaw are the orientation, in radians.
	Roll  float64 `json:"roll"`
	Pitch float64 `json:"pitch"`
	Yaw   float64 `json:"yaw"`
}

// Matrix4x4Pose is a raw row-major 4x4 homogeneous transform.
type Matrix4x4Pose struct {
	// M holds the matrix in row-major order: M[4*row+col]. The last row is
	// implicitly [0 0 0 1] for a rigid transform, but the daemon still sends
	// and expects all 16 values.
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
	// UUID is the move's unique identifier, as assigned by the daemon.
	UUID string `json:"uuid"`
}

// GotoRequest is the body of POST /api/move/goto. Only the non-nil target
// fields are commanded; the others are left at their current value.
type GotoRequest struct {
	// HeadPose is the target head pose, if the head should move.
	HeadPose *Pose `json:"head_pose,omitempty"`
	// Antennas is the target [left, right] antenna joint positions, if the
	// antennas should move.
	Antennas *[2]float64 `json:"antennas,omitempty"`
	// BodyYaw is the target body yaw, if the body should rotate.
	BodyYaw *float64 `json:"body_yaw,omitempty"`
	// Duration is how long the interpolation should take, in seconds.
	Duration float64 `json:"duration"`
	// Interpolation selects the blending curve between the current and
	// target pose (see InterpolationTechnique).
	Interpolation InterpolationTechnique `json:"interpolation"`
}

// FullBodyTarget is the body of POST /api/move/set_target and each frame of
// WS /api/move/ws/set_target. Unlike GotoRequest, there's no interpolation:
// whichever fields are set are commanded immediately as-is.
type FullBodyTarget struct {
	// TargetHeadPose is the target head pose, if the head should move.
	TargetHeadPose *Pose `json:"target_head_pose,omitempty"`
	// TargetAntennas is the target [left, right] antenna joint positions,
	// if the antennas should move.
	TargetAntennas *[2]float64 `json:"target_antennas,omitempty"`
	// TargetBodyYaw is the target body yaw, if the body should rotate.
	TargetBodyYaw *float64 `json:"target_body_yaw,omitempty"`
	// Timestamp optionally tags when this target was computed
	// client-side, e.g. for latency diagnostics during teleop.
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// DoAInfo is the robot's direction-of-arrival (sound source) estimate.
type DoAInfo struct {
	// Angle is the estimated direction to the sound source, in degrees.
	Angle float64 `json:"angle"`
	// SpeechDetected reports whether the sound was classified as speech.
	SpeechDetected bool `json:"speech_detected"`
}

// FullState is the response of GET /api/state/full and each frame of
// WS /api/state/ws/full. Fields are omitted by the daemon (left nil/empty)
// when that subsystem isn't available (e.g. NoMedia mode for DoA).
type FullState struct {
	// ControlMode is the current motor control mode (see MotorControlMode).
	ControlMode *MotorControlMode `json:"control_mode,omitempty"`
	// HeadPose is the head's current pose, equivalent to GetPresentHeadPose.
	HeadPose *Pose `json:"head_pose,omitempty"`
	// HeadJoints are the head's raw actuator joint positions, in the
	// daemon/URDF's native joint order (not a semantic X/Y/Z/RPY breakdown
	// like HeadPose).
	HeadJoints []float64 `json:"head_joints,omitempty"`
	// BodyYaw is the body's current yaw, equivalent to GetPresentBodyYaw.
	BodyYaw *float64 `json:"body_yaw,omitempty"`
	// AntennasPosition is the current [left, right] antenna joint
	// positions, equivalent to GetPresentAntennaPositions.
	AntennasPosition []float64 `json:"antennas_position,omitempty"`
	// PassiveJoints are the positions of joints not under direct motor
	// control (e.g. any spring-loaded or unactuated linkages).
	PassiveJoints []float64 `json:"passive_joints,omitempty"`
	// DoA is the current direction-of-arrival estimate, equivalent to GetDoA.
	DoA *DoAInfo `json:"doa,omitempty"`
	// Timestamp is when the daemon captured this state snapshot.
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// MotorStatus is the response of GET /api/motors/status.
type MotorStatus struct {
	// Mode is the robot's current motor control mode.
	Mode MotorControlMode `json:"mode"`
}
