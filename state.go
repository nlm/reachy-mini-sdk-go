package reachymini

import (
	"context"
	"net/http"
	"net/url"
)

// FullStateOptions selects which fields GetFullStateWithOptions and
// StreamFullStateWithOptions ask the daemon for. The zero value matches the
// daemon's defaults: control mode, head pose, body yaw and antenna positions.
type FullStateOptions struct {
	// OmitControlMode drops ControlMode. Ignored when streaming: the
	// WebSocket always includes it.
	OmitControlMode bool
	// OmitHeadPose drops HeadPose.
	OmitHeadPose bool
	// OmitBodyYaw drops BodyYaw.
	OmitBodyYaw bool
	// OmitAntennas drops AntennasPosition.
	OmitAntennas bool
	// HeadJoints adds HeadJoints.
	HeadJoints bool
	// PassiveJoints adds PassiveJoints.
	PassiveJoints bool
	// DoA adds DoA.
	DoA bool
	// IMU adds IMU. Daemons before 1.11 ignore it and never report one.
	IMU bool
	// PoseMatrix returns HeadPose as a 4x4 matrix (Pose.Matrix) instead of
	// XYZ/RPY (Pose.XYZRPY).
	PoseMatrix bool
}

// query encodes opts as daemon query parameters, sending only those that
// differ from the daemon's defaults.
func (opts FullStateOptions) query() url.Values {
	q := url.Values{}
	for _, p := range []struct {
		name string
		set  bool
		val  string
	}{
		{"with_control_mode", opts.OmitControlMode, "false"},
		{"with_head_pose", opts.OmitHeadPose, "false"},
		{"with_body_yaw", opts.OmitBodyYaw, "false"},
		{"with_antenna_positions", opts.OmitAntennas, "false"},
		{"with_head_joints", opts.HeadJoints, "true"},
		{"with_passive_joints", opts.PassiveJoints, "true"},
		{"with_doa", opts.DoA, "true"},
		{"with_imu", opts.IMU, "true"},
		{"use_pose_matrix", opts.PoseMatrix, "true"},
	} {
		if p.set {
			q.Set(p.name, p.val)
		}
	}
	return q
}

// GetFullState returns the robot's complete current state, with the
// daemon's default fields (see FullStateOptions).
func (c *Client) GetFullState(ctx context.Context) (FullState, error) {
	return c.GetFullStateWithOptions(ctx, FullStateOptions{})
}

// GetFullStateWithOptions returns the robot's current state with the fields
// selected by opts.
func (c *Client) GetFullStateWithOptions(ctx context.Context, opts FullStateOptions) (FullState, error) {
	var s FullState
	err := c.doJSON(ctx, http.MethodGet, withQuery("/api/state/full", opts.query()), nil, &s)
	return s, err
}

// GetPresentHeadPose returns the head's current pose.
func (c *Client) GetPresentHeadPose(ctx context.Context) (Pose, error) {
	var p Pose
	err := c.doJSON(ctx, http.MethodGet, "/api/state/present_head_pose", nil, &p)
	return p, err
}

// GetPresentBodyYaw returns the body's current yaw.
func (c *Client) GetPresentBodyYaw(ctx context.Context) (float64, error) {
	var yaw float64
	err := c.doJSON(ctx, http.MethodGet, "/api/state/present_body_yaw", nil, &yaw)
	return yaw, err
}

// GetPresentAntennaPositions returns the [left, right] antenna joint positions.
func (c *Client) GetPresentAntennaPositions(ctx context.Context) ([2]float64, error) {
	var pos [2]float64
	err := c.doJSON(ctx, http.MethodGet, "/api/state/present_antenna_joint_positions", nil, &pos)
	return pos, err
}

// GetDoA returns the current direction-of-arrival (sound source) estimate.
func (c *Client) GetDoA(ctx context.Context) (DoAInfo, error) {
	var d DoAInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/state/doa", nil, &d)
	return d, err
}

// GetIMU returns the latest IMU reading, or nil when there is none: the Lite
// and simulation have no IMU, and the daemon drops stale readings.
//
// Requires daemon 1.11+ (older daemons answer 404).
func (c *Client) GetIMU(ctx context.Context) (*ImuData, error) {
	var d *ImuData
	err := c.doJSON(ctx, http.MethodGet, "/api/state/imu", nil, &d)
	return d, err
}
