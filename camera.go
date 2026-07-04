package reachymini

import (
	"context"
	"net/http"
)

// ResolutionInfo is a single camera resolution entry.
type ResolutionInfo struct {
	// Name is a short label for this resolution mode (e.g. "1080p").
	Name string `json:"name"`
	// Width, Height are the frame dimensions in pixels.
	Width  int `json:"width"`
	Height int `json:"height"`
	// FPS is the frame rate this resolution is streamed at.
	FPS int `json:"fps"`
	// CropFactor is the sensor crop applied to reach this resolution
	// (1.0 = full sensor field of view; >1.0 = cropped/zoomed in).
	CropFactor float64 `json:"crop_factor"`
}

// CameraSpecs is the response of GET /api/camera/specs: full camera
// specifications as detected by the daemon, including the intrinsic matrix
// K and distortion coefficients D (standard OpenCV pinhole camera model).
type CameraSpecs struct {
	// Name identifies the camera module/model the daemon detected.
	Name string `json:"name"`
	// AvailableResolutions lists every resolution/FPS mode this camera
	// supports.
	AvailableResolutions []ResolutionInfo `json:"available_resolutions"`
	// DefaultResolution is the mode StreamCameraFrames requests frames at.
	DefaultResolution ResolutionInfo `json:"default_resolution"`
	// K is the 3x3 intrinsic camera matrix (focal lengths and principal
	// point), row-major, for DefaultResolution's dimensions.
	K [][]float64 `json:"K"`
	// D is the lens distortion coefficients, in OpenCV's order
	// (k1, k2, p1, p2, k3, ...).
	D []float64 `json:"D"`
}

// GetCameraSpecs returns the robot's camera specifications.
func (c *Client) GetCameraSpecs(ctx context.Context) (CameraSpecs, error) {
	var s CameraSpecs
	err := c.doJSON(ctx, http.MethodGet, "/api/camera/specs", nil, &s)
	return s, err
}
