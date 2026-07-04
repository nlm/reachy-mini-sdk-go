package reachymini

import (
	"context"
	"net/http"
	"net/url"
)

// AudioParamPair is one (parameter name, values) pair in an audio config
// payload, as used by ApplyAudioConfig.
type AudioParamPair struct {
	// Name is the mixer parameter's name (backend-specific; see
	// ReadAudioParameter's doc comment).
	Name string `json:"name"`
	// Values are the parameter's new value(s) -- some mixer parameters are
	// scalar (one value), others are per-channel (one value each).
	Values []float64 `json:"values"`
}

type applyAudioConfigRequest struct {
	Config []AudioParamPair `json:"config"`
	Verify bool             `json:"verify"`
}

// ReadAudioParameterResponse is the response of
// GET /api/audio/config/parameter/{name}.
type ReadAudioParameterResponse struct {
	// Name echoes back the parameter name that was requested.
	Name string `json:"name"`
	// Values are the parameter's current value(s).
	Values []float64 `json:"values"`
}

// VolumeInfo reports the current volume level for a device.
type VolumeInfo struct {
	// Volume is the current level, 0-100.
	Volume int `json:"volume"`
	// Platform identifies the underlying audio backend/OS (e.g. "linux").
	Platform string `json:"platform"`
	// Device is the backend-specific name of the audio device this
	// volume applies to.
	Device string `json:"device"`
}

// TestSoundResult is the response of POST /api/volume/test-sound.
type TestSoundResult struct {
	// Status is a short machine-readable outcome (e.g. "ok").
	Status string `json:"status"`
	// Message is a human-readable description of the outcome.
	Message string `json:"message"`
}

// ApplyAudioConfig applies low-level audio mixer parameters. If verify is
// true (the daemon's own default), it reads each parameter back after
// writing to confirm it took effect.
func (c *Client) ApplyAudioConfig(ctx context.Context, config []AudioParamPair, verify bool) (bool, error) {
	var resp struct {
		Applied bool `json:"applied"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/api/audio/config/apply", applyAudioConfigRequest{Config: config, Verify: verify}, &resp)
	return resp.Applied, err
}

// ReadAudioParameter reads a single low-level audio mixer parameter by name.
// Valid names are specific to the daemon's audio backend and aren't
// enumerated anywhere in the API itself.
func (c *Client) ReadAudioParameter(ctx context.Context, name string) (ReadAudioParameterResponse, error) {
	var resp ReadAudioParameterResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/audio/config/parameter/"+url.PathEscape(name), nil, &resp)
	return resp, err
}

// GetVolume returns the current speaker volume.
func (c *Client) GetVolume(ctx context.Context) (VolumeInfo, error) {
	var v VolumeInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/volume/current", nil, &v)
	return v, err
}

// GetMicrophoneVolume returns the current microphone volume.
func (c *Client) GetMicrophoneVolume(ctx context.Context) (VolumeInfo, error) {
	var v VolumeInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/volume/microphone/current", nil, &v)
	return v, err
}

type volumeRequest struct {
	Volume int `json:"volume"`
}

// SetVolume sets the speaker volume (0-100).
func (c *Client) SetVolume(ctx context.Context, volume int) (VolumeInfo, error) {
	var v VolumeInfo
	err := c.doJSON(ctx, http.MethodPost, "/api/volume/set", volumeRequest{Volume: volume}, &v)
	return v, err
}

// SetMicrophoneVolume sets the microphone volume (0-100).
func (c *Client) SetMicrophoneVolume(ctx context.Context, volume int) (VolumeInfo, error) {
	var v VolumeInfo
	err := c.doJSON(ctx, http.MethodPost, "/api/volume/microphone/set", volumeRequest{Volume: volume}, &v)
	return v, err
}

// TestSound plays a short sound at the current volume -- useful for letting
// a user confirm audio is working after changing the volume.
func (c *Client) TestSound(ctx context.Context) (TestSoundResult, error) {
	var r TestSoundResult
	err := c.doJSON(ctx, http.MethodPost, "/api/volume/test-sound", nil, &r)
	return r, err
}
