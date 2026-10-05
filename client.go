// Package reachymini is a Go client for the Reachy Mini daemon's REST/WebSocket
// API (the same interface used by Pollen Robotics' own TypeScript SDK and
// desktop app), so the robot can be piloted without writing any Python.
package reachymini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a Reachy Mini daemon reachable at BaseURL (e.g.
// "http://localhost:8000" or "http://<robot-host>:8000").
type Client struct {
	// BaseURL is the daemon's REST API root, with no trailing slash (e.g.
	// "http://localhost:8000"). Also used to derive the WebSocket URLs for
	// StreamFullState/StreamSetTarget, and, unless overridden, the WebRTC
	// signalling server URL for StreamCameraFrames.
	BaseURL string
	// HTTPClient issues all REST requests. Defaults to a client with a 10s
	// timeout (see New); replace it to change timeouts, add TLS config, etc.
	HTTPClient *http.Client
}

// New returns a Client pointed at baseURL, e.g. "http://localhost:8000".
func New(baseURL string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// APIError is returned when the daemon responds with a non-2xx status.
type APIError struct {
	// StatusCode is the HTTP status the daemon returned (e.g. 404, 500).
	StatusCode int
	// Body is the raw response body, usually a FastAPI JSON error detail.
	Body string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("reachy mini: unexpected status %d: %s", e.StatusCode, e.Body)
}

// doJSON issues an HTTP request with reqBody (if non-nil) JSON-encoded as the
// body, and decodes the response into respBody (if non-nil).
func (c *Client) doJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}

	if respBody != nil && len(data) > 0 {
		if err := json.Unmarshal(data, respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// doRaw issues a GET request and returns the raw response body, for
// endpoints that don't return JSON (e.g. binary file downloads).
func (c *Client) doRaw(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

// withQuery appends q to path, if q is non-empty.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// wsURL converts BaseURL into the matching ws:// or wss:// URL for path.
func (c *Client) wsURL(path string) string {
	switch {
	case strings.HasPrefix(c.BaseURL, "https://"):
		return "wss://" + strings.TrimPrefix(c.BaseURL, "https://") + path
	case strings.HasPrefix(c.BaseURL, "http://"):
		return "ws://" + strings.TrimPrefix(c.BaseURL, "http://") + path
	default:
		return "ws://" + c.BaseURL + path
	}
}
