package reachymini

import (
	"context"
	"net/http"
)

// EnableWobbling turns on the daemon's audio-reactive head wobbling: audio
// played on the robot (PlaySound, incoming WebRTC audio) is analysed and
// converted into subtle head movements layered on top of the head target.
// The daemon still reports success when it has no media server (e.g.
// started with no media), in which case nothing wobbles.
func (c *Client) EnableWobbling(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/media/wobbling/enable", nil, nil)
}

// DisableWobbling turns off audio-reactive head wobbling and resets the
// head offsets it applied back to zero.
func (c *Client) DisableWobbling(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/media/wobbling/disable", nil, nil)
}
