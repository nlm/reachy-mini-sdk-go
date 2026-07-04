package reachymini

import (
	"context"
	"net/http"
	"net/url"
)

// GetMotorStatus returns the robot's current motor control mode.
func (c *Client) GetMotorStatus(ctx context.Context) (MotorStatus, error) {
	var s MotorStatus
	err := c.doJSON(ctx, http.MethodGet, "/api/motors/status", nil, &s)
	return s, err
}

// SetMotorMode changes the robot's motor control mode (e.g. enabling/disabling
// torque).
func (c *Client) SetMotorMode(ctx context.Context, mode MotorControlMode) error {
	return c.doJSON(ctx, http.MethodPost, "/api/motors/set_mode/"+url.PathEscape(string(mode)), nil, nil)
}

// EnsureMotorMode sets the robot's motor control mode to mode if it isn't
// already there, and reports whether a change was made. Movement commands
// (Goto, SetTarget, WakeUp, GotoSleep, ...) are accepted by the daemon and
// silently no-op if the robot isn't actually in MotorModeEnabled, so this is
// meant to be called at the top of anything that's about to move the robot.
func (c *Client) EnsureMotorMode(ctx context.Context, mode MotorControlMode) (changed bool, err error) {
	status, err := c.GetMotorStatus(ctx)
	if err != nil {
		return false, err
	}
	if status.Mode == mode {
		return false, nil
	}
	if err := c.SetMotorMode(ctx, mode); err != nil {
		return false, err
	}
	return true, nil
}
