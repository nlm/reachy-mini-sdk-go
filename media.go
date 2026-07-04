package reachymini

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

type playSoundRequest struct {
	File string `json:"file"`
}

type soundsResponse struct {
	Files []string `json:"files"`
}

// PlaySound plays a sound file already stored on the robot (see ListSounds).
func (c *Client) PlaySound(ctx context.Context, file string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/media/play_sound", playSoundRequest{File: file}, nil)
}

// StopSound stops whatever sound is currently playing.
func (c *Client) StopSound(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/media/stop_sound", nil, nil)
}

// ClearIncomingAudio discards any buffered incoming audio (e.g. from the
// microphone/direction-of-arrival pipeline).
func (c *Client) ClearIncomingAudio(ctx context.Context) (map[string]string, error) {
	var resp map[string]string
	err := c.doJSON(ctx, http.MethodPost, "/api/media/clear_incoming_audio", nil, &resp)
	return resp, err
}

// ListSounds lists the sound files available on the robot.
func (c *Client) ListSounds(ctx context.Context) ([]string, error) {
	var resp soundsResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/media/sounds", nil, &resp)
	return resp.Files, err
}

// DeleteSound removes a sound file from the robot.
func (c *Client) DeleteSound(ctx context.Context, filename string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/media/sounds/"+url.PathEscape(filename), nil, nil)
}

// UploadSound uploads the local file at localPath to the robot's sound library.
func (c *Client) UploadSound(ctx context.Context, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(localPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/media/sounds/upload", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return nil
}
