package reachymini

import (
	"context"
	"net/http"
	"strconv"
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
	// FaceTarget is the latest face seen by the daemon's head tracking,
	// equivalent to GetTrackedFace.
	FaceTarget FaceTarget `json:"face_target"`
}

type jobResponse struct {
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
	var resp jobResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/daemon/restart", nil, &resp)
	return resp.JobID, err
}

// StartDaemon starts the daemon's robot backend, optionally playing the
// wake-up animation once it's up. It is asynchronous and returns the
// daemon's job ID: GetJobStatus reports when the job is done, and
// GetDaemonStatus whether the daemon actually ended up running (the job
// finishes "done" even if the backend failed to start, and starting a
// running daemon is a no-op). The daemon answers 409 when another
// start/stop/restart job is already running.
func (c *Client) StartDaemon(ctx context.Context, wakeUp bool) (string, error) {
	var resp jobResponse
	path := "/api/daemon/start?wake_up=" + strconv.FormatBool(wakeUp)
	err := c.doJSON(ctx, http.MethodPost, path, nil, &resp)
	return resp.JobID, err
}

// StopDaemon stops the daemon's robot backend, optionally playing the
// go-to-sleep animation first. Asynchronous, like StartDaemon. The HTTP API
// stays reachable, so StartDaemon can bring the backend back, but endpoints
// that need the backend (movement, state, sounds...) answer 503 until then.
func (c *Client) StopDaemon(ctx context.Context, gotoSleep bool) (string, error) {
	var resp jobResponse
	path := "/api/daemon/stop?goto_sleep=" + strconv.FormatBool(gotoSleep)
	err := c.doJSON(ctx, http.MethodPost, path, nil, &resp)
	return resp.JobID, err
}

type robotName struct {
	Name *string `json:"name"`
}

// GetRobotName returns the robot's display name: the name it was last
// renamed to, else the daemon's configured default. It is "" only when
// neither exists.
func (c *Client) GetRobotName(ctx context.Context) (string, error) {
	var resp robotName
	err := c.doJSON(ctx, http.MethodGet, "/api/daemon/robot-name", nil, &resp)
	if resp.Name == nil {
		return "", err
	}
	return *resp.Name, err
}

// SetRobotName renames the robot (1-64 characters) and returns the name as
// stored, with surrounding whitespace trimmed. It persists across restarts
// and takes effect immediately in the daemon status and the robot's mDNS
// advertisement. The daemon answers 422 for an invalid name (including a
// blank one) and when it fails to save the name.
func (c *Client) SetRobotName(ctx context.Context, name string) (string, error) {
	var resp robotName
	err := c.doJSON(ctx, http.MethodPost, "/api/daemon/robot-name", robotName{Name: &name}, &resp)
	if resp.Name == nil {
		return "", err
	}
	return *resp.Name, err
}

// GetHardwareID returns the robot's unique hardware ID, an opaque 16-hex-char
// value derived from its audio device's USB serial, stable across reboots
// and OS reinstalls. It is "" when no
// robot is attached, e.g. a daemon running on a developer machine.
func (c *Client) GetHardwareID(ctx context.Context) (string, error) {
	var resp struct {
		HardwareID *string `json:"hardware_id"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/daemon/hardware-id", nil, &resp)
	if resp.HardwareID == nil {
		return "", err
	}
	return *resp.HardwareID, err
}

// RobotAppLockState says which managed app, if any, holds the robot.
type RobotAppLockState string

const (
	// RobotAppLockFree means no managed app holds the robot.
	RobotAppLockFree RobotAppLockState = "free"
	// RobotAppLockLocalApp means an app started by the daemon (StartApp)
	// is running.
	RobotAppLockLocalApp RobotAppLockState = "local_app"
	// RobotAppLockRemoteSession means a remote WebRTC client is connected
	// through Pollen's central signalling relay.
	RobotAppLockRemoteSession RobotAppLockState = "remote_session"
)

// RobotAppLockStatus is the response of GET
// /api/daemon/robot-app-lock-status.
type RobotAppLockStatus struct {
	// State says who holds the robot.
	State RobotAppLockState `json:"state"`
	// HolderName is the app name for RobotAppLockLocalApp, the generic
	// "remote" for RobotAppLockRemoteSession, and empty when free.
	HolderName string `json:"holder_name"`
}

// GetRobotAppLockStatus reports which managed app, if any, holds the robot.
// Clients talking to the daemon directly, like this SDK, bypass the lock, so
// it only reflects daemon-launched apps and remote sessions.
func (c *Client) GetRobotAppLockStatus(ctx context.Context) (RobotAppLockStatus, error) {
	var s RobotAppLockStatus
	err := c.doJSON(ctx, http.MethodGet, "/api/daemon/robot-app-lock-status", nil, &s)
	return s, err
}
