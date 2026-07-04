package reachymini

import (
	"context"
	"net/http"
)

// GetKinematicsInfo returns the daemon's kinematics configuration as a
// free-form map -- the daemon's OpenAPI spec declares this endpoint's
// response as an open object with no fixed schema.
func (c *Client) GetKinematicsInfo(ctx context.Context) (map[string]any, error) {
	var info map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/api/kinematics/info", nil, &info)
	return info, err
}

// GetURDF returns the robot's URDF description as a set of named file
// contents (e.g. a "urdf" key holding the XML).
func (c *Client) GetURDF(ctx context.Context) (map[string]string, error) {
	var urdf map[string]string
	err := c.doJSON(ctx, http.MethodGet, "/api/kinematics/urdf", nil, &urdf)
	return urdf, err
}

// GetKinematicsSTL downloads the raw STL mesh file named filename (as
// referenced by GetURDF/GetKinematicsInfo) and returns its bytes.
func (c *Client) GetKinematicsSTL(ctx context.Context, filename string) ([]byte, error) {
	// filename may itself contain "/" (the daemon route uses FastAPI's
	// {filename:path} converter), so it's appended raw, not escaped.
	return c.doRaw(ctx, http.MethodGet, "/api/kinematics/stl/"+filename)
}
