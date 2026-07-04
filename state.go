package reachymini

import (
	"context"
	"net/http"
)

// GetFullState returns the robot's complete current state.
func (c *Client) GetFullState(ctx context.Context) (FullState, error) {
	var s FullState
	err := c.doJSON(ctx, http.MethodGet, "/api/state/full", nil, &s)
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
