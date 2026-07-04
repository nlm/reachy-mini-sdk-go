package reachymini

import (
	"context"
	"net/http"
)

// ControlLoopStats reports low-level motor control loop timing/health.
type ControlLoopStats struct {
	MeanControlLoopFrequency float64 `json:"mean_control_loop_frequency"`
	MaxControlLoopInterval   float64 `json:"max_control_loop_interval"`
	NbError                  int     `json:"nb_error"`
	MotorController          string  `json:"motor_controller"`
}

// BackendStatus reports the motor/hardware backend's health. Ready has been
// observed false even during normal operation, so don't gate on it; State on
// the enclosing DaemonStatus is the more reliable liveness signal.
type BackendStatus struct {
	Ready            bool              `json:"ready"`
	MotorControlMode MotorControlMode  `json:"motor_control_mode"`
	LastAlive        any               `json:"last_alive"`
	ControlLoopStats *ControlLoopStats `json:"control_loop_stats"`
	Error            any               `json:"error"`
}

// DaemonStatus is the response of GET /api/daemon/status.
type DaemonStatus struct {
	Type              string        `json:"type"`
	RobotName         string        `json:"robot_name"`
	State             string        `json:"state"`
	WirelessVersion   bool          `json:"wireless_version"`
	DesktopAppDaemon  bool          `json:"desktop_app_daemon"`
	SimulationEnabled bool          `json:"simulation_enabled"`
	MockupSimEnabled  bool          `json:"mockup_sim_enabled"`
	NoMedia           bool          `json:"no_media"`
	MediaReleased     bool          `json:"media_released"`
	CameraSpecsName   string        `json:"camera_specs_name"`
	BackendStatus     BackendStatus `json:"backend_status"`
	Error             any           `json:"error"`
	WlanIP            string        `json:"wlan_ip"`
	Version           string        `json:"version"`
	HardwareID        string        `json:"hardware_id"`
}

type restartResponse struct {
	JobID string `json:"job_id"`
}

// GetDaemonStatus returns the daemon's current status, including hardware
// backend health and motor control mode.
func (c *Client) GetDaemonStatus(ctx context.Context) (DaemonStatus, error) {
	var s DaemonStatus
	err := c.doJSON(ctx, http.MethodGet, "/api/daemon/status", nil, &s)
	return s, err
}

// RestartDaemon restarts the daemon process, which re-initializes its
// connection to the motor/hardware backend. Useful when a joint has
// silently dropped off the control loop (no error reported, motor mode
// still "enabled", but it stops responding to position commands) --
// cycling motor mode alone does not fix that, but a daemon restart does.
// Returns the daemon's internal job ID for the restart operation; the
// restart is asynchronous and briefly interrupts connectivity, so poll
// GetDaemonStatus afterward rather than assuming it's done when this
// returns.
func (c *Client) RestartDaemon(ctx context.Context) (string, error) {
	var resp restartResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/daemon/restart", nil, &resp)
	return resp.JobID, err
}
