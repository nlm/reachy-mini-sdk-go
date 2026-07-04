package reachymini

import (
	"context"
	"net/http"
)

// ResolutionInfo is a single camera resolution entry.
type ResolutionInfo struct {
	Name       string  `json:"name"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        int     `json:"fps"`
	CropFactor float64 `json:"crop_factor"`
}

// CameraSpecs is the response of GET /api/camera/specs: full camera
// specifications as detected by the daemon, including the intrinsic matrix
// K and distortion coefficients D (standard OpenCV pinhole camera model).
type CameraSpecs struct {
	Name                 string           `json:"name"`
	AvailableResolutions []ResolutionInfo `json:"available_resolutions"`
	DefaultResolution    ResolutionInfo   `json:"default_resolution"`
	K                    [][]float64      `json:"K"`
	D                    []float64        `json:"D"`
}

// GetCameraSpecs returns the robot's camera specifications.
func (c *Client) GetCameraSpecs(ctx context.Context) (CameraSpecs, error) {
	var s CameraSpecs
	err := c.doJSON(ctx, http.MethodGet, "/api/camera/specs", nil, &s)
	return s, err
}
