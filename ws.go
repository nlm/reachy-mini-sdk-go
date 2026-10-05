package reachymini

import (
	"context"
	"math"
	"strconv"

	"github.com/gorilla/websocket"
)

// StreamFullState streams FullState frames from WS /api/state/ws/full until
// ctx is cancelled, with the daemon's default fields and rate (10 Hz). The
// returned channels are closed when the stream ends; drain errs (buffered,
// size 1) to see why.
func (c *Client) StreamFullState(ctx context.Context) (<-chan FullState, <-chan error, error) {
	return c.StreamFullStateWithOptions(ctx, StreamFullStateOptions{})
}

// StreamFullStateOptions configures StreamFullStateWithOptions.
type StreamFullStateOptions struct {
	FullStateOptions
	// Frequency is the frame rate, in Hz. Zero, negative or infinite keeps
	// the daemon's default (10 Hz).
	Frequency float64
}

// StreamFullStateWithOptions is StreamFullState with the fields and rate
// selected by opts.
func (c *Client) StreamFullStateWithOptions(ctx context.Context, opts StreamFullStateOptions) (<-chan FullState, <-chan error, error) {
	q := opts.query()
	// The WebSocket has no with_control_mode parameter.
	q.Del("with_control_mode")
	if opts.Frequency > 0 && !math.IsInf(opts.Frequency, 1) {
		q.Set("frequency", strconv.FormatFloat(opts.Frequency, 'g', -1, 64))
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL(withQuery("/api/state/ws/full", q)), nil)
	if err != nil {
		return nil, nil, err
	}

	states := make(chan FullState)
	errs := make(chan error, 1)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	go func() {
		defer close(states)
		defer close(errs)
		for {
			var frame FullState
			if err := conn.ReadJSON(&frame); err != nil {
				select {
				case errs <- err:
				default:
				}
				return
			}
			select {
			case states <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	return states, errs, nil
}

// StreamSetTarget opens WS /api/move/ws/set_target and forwards every
// FullBodyTarget sent on the returned channel to the robot, until ctx is
// cancelled or the channel is closed. Useful for continuous/teleop control
// (e.g. driving the head from a joystick loop) where the lower per-call
// latency of a persistent connection matters; for one-off commands use
// SetTarget instead.
func (c *Client) StreamSetTarget(ctx context.Context) (chan<- FullBodyTarget, <-chan error, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL("/api/move/ws/set_target"), nil)
	if err != nil {
		return nil, nil, err
	}

	targets := make(chan FullBodyTarget)
	errs := make(chan error, 1)

	// Drain and discard anything the server sends. This endpoint isn't
	// expected to deliver data, but performing a read is what lets gorilla's
	// default ping handler run and answer the server's keepalive pings with
	// pongs -- without it a purely write-only stream is silently dropped by
	// the daemon on its ping/idle timeout, which breaks long-lived
	// (continuous/teleop) use. It also surfaces a server-initiated close
	// promptly. gorilla permits one concurrent reader alongside the writer
	// below, and the pong is sent via WriteControl, which is safe to call
	// concurrently with WriteJSON. Exits when the writer goroutine closes conn.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	go func() {
		defer close(errs)
		defer conn.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case target, ok := <-targets:
				if !ok {
					return
				}
				if err := conn.WriteJSON(target); err != nil {
					select {
					case errs <- err:
					default:
					}
					return
				}
			}
		}
	}()

	return targets, errs, nil
}
