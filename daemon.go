package reachymini

import (
	"context"
	"net/http"
)

// ControlLoopStats reports low-level motor control loop timing/health.
type ControlLoopStats struct {
	// MeanControlLoopFrequency is the average control loop rate, in Hz.
	MeanControlLoopFrequency float64 `json:"mean_control_loop_frequency"`
	// MaxControlLoopInterval is the longest gap observed between control
	// loop iterations, in seconds -- large values indicate the loop
	// stalled or was delayed.
	MaxControlLoopInterval float64 `json:"max_control_loop_interval"`
	// NbError is the cumulative count of control loop errors.
	NbError int `json:"nb_error"`
	// MotorController names the low-level motor driver/backend in use.
	MotorController string `json:"motor_controller"`
}

// BackendStatus reports the motor/hardware backend's health. Ready has been
// observed false even during normal operation, so don't gate on it; State on
// the enclosing DaemonStatus is the more reliable liveness signal.
type BackendStatus struct {
	// Ready is the backend's self-reported readiness (unreliable -- see
	// the type-level comment above).
	Ready bool `json:"ready"`
	// MotorControlMode is the current motor control mode (see
	// MotorControlMode), mirroring GetMotorStatus.
	MotorControlMode MotorControlMode `json:"motor_control_mode"`
	// LastAlive is a backend-specific liveness timestamp/marker with no
	// fixed schema in the daemon's OpenAPI spec.
	LastAlive any `json:"last_alive"`
	// ControlLoopStats is nil if the backend hasn't reported stats yet.
	ControlLoopStats *ControlLoopStats `json:"control_loop_stats"`
	// Error is a backend-specific error payload with no fixed schema,
	// non-nil if the backend is in an error state.
	Error any `json:"error"`
}

// DaemonStatus is the response of GET /api/daemon/status.
type DaemonStatus struct {
	// Type identifies this status payload's shape/version.
	Type string `json:"type"`
	// RobotName is the robot's configured hostname/name.
	RobotName string `json:"robot_name"`
	// State is the daemon's own lifecycle state (e.g. "running") -- the
	// most reliable liveness signal, more so than BackendStatus.Ready.
	State string `json:"state"`
	// WirelessVersion reports whether this is the wireless hardware
	// variant of the robot.
	WirelessVersion bool `json:"wireless_version"`
	// DesktopAppDaemon reports whether this daemon is running embedded in
	// Pollen's desktop app rather than on real hardware.
	DesktopAppDaemon bool `json:"desktop_app_daemon"`
	// SimulationEnabled reports whether motor commands are being
	// simulated rather than sent to real hardware.
	SimulationEnabled bool `json:"simulation_enabled"`
	// MockupSimEnabled reports whether a lightweight mockup simulation
	// backend is in use (distinct from the full desktop simulator).
	MockupSimEnabled bool `json:"mockup_sim_enabled"`
	// NoMedia reports whether audio/video subsystems are disabled for
	// this daemon instance.
	NoMedia bool `json:"no_media"`
	// MediaReleased reports whether media devices have been released
	// (e.g. to let another process use the camera/mic).
	MediaReleased bool `json:"media_released"`
	// CameraSpecsName is the detected camera's name, matching
	// CameraSpecs.Name.
	CameraSpecsName string `json:"camera_specs_name"`
	// BackendStatus reports the motor/hardware backend's health.
	BackendStatus BackendStatus `json:"backend_status"`
	// Error is a daemon-level error payload with no fixed schema, non-nil
	// if the daemon itself is in an error state.
	Error any `json:"error"`
	// WlanIP is the robot's current WiFi IP address, if connected.
	WlanIP string `json:"wlan_ip"`
	// Version is the daemon software's version string.
	Version string `json:"version"`
	// HardwareID uniquely identifies this physical robot.
	HardwareID string `json:"hardware_id"`
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
