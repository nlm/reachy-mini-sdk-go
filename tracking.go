package reachymini

import (
	"context"
	"net/http"
)

// FaceTarget is the latest face seen by the daemon's own head-tracking
// detector (GET /api/media/tracking/face). X/Y are normalized to [-1, 1]
// in the tracker's image: X < 0 is the robot's left, Y > 0 is down.
// Pointers are nil when no face is detected. TS is on the daemon's clock.
type FaceTarget struct {
	Detected bool     `json:"detected"`
	X        *float64 `json:"x"`
	Y        *float64 `json:"y"`
	Roll     *float64 `json:"roll"`
	TS       *float64 `json:"ts"`
}

type trackingEnableRequest struct {
	Weight float64 `json:"weight"`
}

type trackingEnableResponse struct {
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
}

// EnableHeadTracking turns on the daemon's face detector and blends its aim
// into the head pose by weight (0..1). The detector only runs while
// weight > 0; at weight >= 1 the daemon ignores other head targets. It
// reports false when the daemon can't track (e.g. no camera).
//
// Observed on a Reachy Mini Wireless (daemon 1.11.0) with nothing else
// commanding the head: weight 1.0 turned it toward the face within a
// second, while 0.3 produced no visible motion.
//
// Requires daemon 1.11+ (older daemons answer 404).
func (c *Client) EnableHeadTracking(ctx context.Context, weight float64) (bool, error) {
	var resp trackingEnableResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/media/tracking/enable", trackingEnableRequest{Weight: weight}, &resp)
	return resp.Enabled, err
}

// DisableHeadTracking stops the daemon's face detector.
//
// Requires daemon 1.11+ (older daemons answer 404).
func (c *Client) DisableHeadTracking(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/media/tracking/disable", nil, nil)
}

// GetTrackedFace returns the latest face the daemon's detector observed.
//
// Requires daemon 1.11+ (older daemons answer 404).
func (c *Client) GetTrackedFace(ctx context.Context) (FaceTarget, error) {
	var resp struct {
		FaceTarget FaceTarget `json:"face_target"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/media/tracking/face", nil, &resp)
	return resp.FaceTarget, err
}
