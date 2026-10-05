package reachymini

import (
	"context"
	"net/http"
	"net/url"
)

// SourceKind is where an app comes from.
type SourceKind string

const (
	SourceKindHFSpace            SourceKind = "hf_space"
	SourceKindDashboardSelection SourceKind = "dashboard_selection"
	SourceKindLocal              SourceKind = "local"
	SourceKindInstalled          SourceKind = "installed"
)

// AppState is the runtime status of an app.
type AppState string

const (
	AppStateStarting AppState = "starting"
	AppStateRunning  AppState = "running"
	AppStateDone     AppState = "done"
	AppStateStopping AppState = "stopping"
	AppStateError    AppState = "error"
)

// JobStatus is the status of an async install/update job.
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusInProgress JobStatus = "in_progress"
	JobStatusDone       JobStatus = "done"
	JobStatusFailed     JobStatus = "failed"
)

// AppInfo describes an app available from the robot's app store or already
// installed.
type AppInfo struct {
	// Name is the app's unique identifier, used e.g. by StartApp/RemoveApp.
	Name string `json:"name"`
	// SourceKind is where this app comes from (see SourceKind).
	SourceKind SourceKind `json:"source_kind"`
	// Description is a human-readable summary, if the source provides one.
	Description string `json:"description,omitempty"`
	// URL points at the app's source (e.g. its Hugging Face Space page),
	// if applicable to SourceKind.
	URL *string `json:"url,omitempty"`
	// Extra carries source-specific metadata not otherwise modeled here.
	Extra map[string]any `json:"extra,omitempty"`
}

// AppStatus is the status of a running (or just-started/stopped) app.
type AppStatus struct {
	// Info identifies which app this status is for.
	Info AppInfo `json:"info"`
	// State is the app's current runtime status (see AppState).
	State AppState `json:"state"`
	// Error is set when State is AppStateError, describing what went wrong.
	Error *string `json:"error,omitempty"`
}

// AppUpdateStatus is the result of checking one installed app for updates.
type AppUpdateStatus struct {
	// AppName is the installed app's name, matching AppInfo.Name.
	AppName string `json:"app_name"`
	// SpaceID is the app's Hugging Face Space identifier.
	SpaceID string `json:"space_id"`
	// InstalledSHA is the commit SHA currently installed.
	InstalledSHA string `json:"installed_sha"`
	// LatestSHA is the commit SHA currently published upstream.
	LatestSHA string `json:"latest_sha"`
	// UpdateAvailable reports whether InstalledSHA differs from LatestSHA.
	UpdateAvailable bool `json:"update_available"`
	// LastModified is when the upstream Space was last modified, if known.
	LastModified *string `json:"last_modified,omitempty"`
}

// AppUpdatesResponse is the response of GET /api/apps/check-updates.
type AppUpdatesResponse struct {
	// AppsWithUpdates lists only the installed apps that have an update
	// available (not every app that was checked).
	AppsWithUpdates []AppUpdateStatus `json:"apps_with_updates"`
	// AppsChecked is how many installed apps were checked for updates.
	AppsChecked int `json:"apps_checked"`
	// AppsSkipped is how many installed apps couldn't be checked (e.g. not
	// backed by a Hugging Face Space).
	AppsSkipped int `json:"apps_skipped"`
}

// JobInfo is the status of an async app install/update job (see
// InstallApp, InstallPrivateSpace, UpdateApp).
type JobInfo struct {
	// Command is the shell command the daemon is running for this job.
	Command string `json:"command"`
	// Status is the job's current lifecycle state (see JobStatus).
	Status JobStatus `json:"status"`
	// Logs are the job's captured output lines so far.
	Logs []string `json:"logs"`
}

type installPrivateSpaceRequest struct {
	SpaceID string `json:"space_id"`
}

// CheckAppUpdates checks installed apps for available updates. Set force to
// bypass any cached result.
func (c *Client) CheckAppUpdates(ctx context.Context, force bool) (AppUpdatesResponse, error) {
	path := "/api/apps/check-updates"
	if force {
		path += "?force=true"
	}
	var resp AppUpdatesResponse
	err := c.doJSON(ctx, http.MethodGet, path, nil, &resp)
	return resp, err
}

// GetCurrentAppStatus returns the currently running app's status, or nil if
// no app is running.
func (c *Client) GetCurrentAppStatus(ctx context.Context) (*AppStatus, error) {
	var status *AppStatus
	err := c.doJSON(ctx, http.MethodGet, "/api/apps/current-app-status", nil, &status)
	return status, err
}

// InstallApp installs app and returns the daemon's raw install job response
// (the daemon's OpenAPI spec declares no fixed schema for it beyond
// string-to-string). Poll GetJobStatus to track progress.
func (c *Client) InstallApp(ctx context.Context, app AppInfo) (map[string]string, error) {
	var resp map[string]string
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/install", app, &resp)
	return resp, err
}

// InstallPrivateSpace installs a private Hugging Face Space by ID.
func (c *Client) InstallPrivateSpace(ctx context.Context, spaceID string) (map[string]string, error) {
	var resp map[string]string
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/install-private-space", installPrivateSpaceRequest{SpaceID: spaceID}, &resp)
	return resp, err
}

// GetJobStatus returns the status of a background job: an install/update
// started by InstallApp, InstallPrivateSpace, or UpdateApp, or a daemon
// start/stop/restart (StartDaemon, StopDaemon, RestartDaemon).
func (c *Client) GetJobStatus(ctx context.Context, jobID string) (JobInfo, error) {
	var info JobInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/apps/job-status/"+url.PathEscape(jobID), nil, &info)
	return info, err
}

// ListAvailableApps lists all apps available to install, across all sources.
func (c *Client) ListAvailableApps(ctx context.Context) ([]AppInfo, error) {
	var apps []AppInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/apps/list-available", nil, &apps)
	return apps, err
}

// ListAvailableAppsBySource lists apps available from a single source.
func (c *Client) ListAvailableAppsBySource(ctx context.Context, source SourceKind) ([]AppInfo, error) {
	var apps []AppInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/apps/list-available/"+url.PathEscape(string(source)), nil, &apps)
	return apps, err
}

// RemoveApp uninstalls appName.
func (c *Client) RemoveApp(ctx context.Context, appName string) (map[string]string, error) {
	var resp map[string]string
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/remove/"+url.PathEscape(appName), nil, &resp)
	return resp, err
}

// RestartCurrentApp restarts whichever app is currently running.
func (c *Client) RestartCurrentApp(ctx context.Context) (AppStatus, error) {
	var status AppStatus
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/restart-current-app", nil, &status)
	return status, err
}

// StartApp starts appName. The daemon refuses (HTTP 400) if an app is
// already running, so call StopCurrentApp first. A remote WebRTC session
// holding the robot is evicted; use StartAppNoEvict to fail instead.
func (c *Client) StartApp(ctx context.Context, appName string) (AppStatus, error) {
	var status AppStatus
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/start-app/"+url.PathEscape(appName), nil, &status)
	return status, err
}

// StartAppNoEvict is StartApp, except that it fails (HTTP 400) rather than
// evicting a remote WebRTC session that holds the robot (see
// GetRobotAppLockStatus).
func (c *Client) StartAppNoEvict(ctx context.Context, appName string) (AppStatus, error) {
	var status AppStatus
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/start-app/"+url.PathEscape(appName)+"/no-evict", nil, &status)
	return status, err
}

type startupApp struct {
	StartupApp *string `json:"startup_app"`
}

// GetStartupApp returns the app the daemon starts automatically, or "" if
// none is set. The daemon starts it when the robot first wakes up, and when
// a touch on the antennas wakes an idle robot. It never evicts a remote
// WebRTC session to do so.
func (c *Client) GetStartupApp(ctx context.Context) (string, error) {
	var resp startupApp
	err := c.doJSON(ctx, http.MethodGet, "/api/apps/startup-app", nil, &resp)
	if resp.StartupApp == nil {
		return "", err
	}
	return *resp.StartupApp, err
}

// SetStartupApp sets the app the daemon starts automatically (see
// GetStartupApp); "" clears it. The app must already be installed (HTTP 400
// otherwise). The change applies without a daemon restart.
func (c *Client) SetStartupApp(ctx context.Context, appName string) error {
	var req startupApp
	if appName != "" {
		req.StartupApp = &appName
	}
	return c.doJSON(ctx, http.MethodPut, "/api/apps/startup-app", req, nil)
}

// StopCurrentApp stops whichever app is currently running.
func (c *Client) StopCurrentApp(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/apps/stop-current-app", nil, nil)
}

// UpdateApp updates appName to its latest available version.
func (c *Client) UpdateApp(ctx context.Context, appName string) (map[string]string, error) {
	var resp map[string]string
	err := c.doJSON(ctx, http.MethodPost, "/api/apps/update/"+url.PathEscape(appName), nil, &resp)
	return resp, err
}

// ResetAppsCache clears the daemon's cached app store listing. Note this
// endpoint is mounted at /cache/reset-apps, not under /api like everything
// else -- that's how the daemon itself exposes it.
func (c *Client) ResetAppsCache(ctx context.Context) (map[string]string, error) {
	var resp map[string]string
	err := c.doJSON(ctx, http.MethodPost, "/cache/reset-apps", nil, &resp)
	return resp, err
}
