package reachymini

import (
	"io"
	"os/exec"
	"sync"

	"github.com/pion/webrtc/v4"
)

// decoderProc is a running ffmpeg decoder for one WebRTC track.
type decoderProc struct {
	cmd *exec.Cmd
	// pipes are the parent's ends of ffmpeg's stdin/stdout.
	pipes []io.Closer
	// done is closed once the goroutines feeding and draining ffmpeg
	// have returned; the draining one closes the stream's output channel.
	done <-chan struct{}
}

// trackDecoder runs at most one decoder for a stream and tears it down
// exactly once. The decoder starts from pion's OnTrack callback, which can
// fire at any time, including while the stream is shutting down; under mu,
// either stop hasn't begun and the decoder registers itself, or it has and
// the decoder never starts.
type trackDecoder struct {
	// closeOut closes the stream's output channel; stop calls it when no
	// decoder ever started (otherwise the decoder's drain goroutine does).
	closeOut func()

	mu       sync.Mutex
	stopping bool
	proc     *decoderProc
}

// start runs startFn and keeps its decoder, unless a decoder already
// started or stop has begun. It reports whether startFn ran.
func (d *trackDecoder) start(startFn func() (decoderProc, error)) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopping || d.proc != nil {
		return false, nil
	}
	p, err := startFn()
	if err != nil {
		return true, err
	}
	d.proc = &p
	return true, nil
}

// stop kills the decoder, waits for its goroutines and reaps the process,
// or closes the output channel if no decoder ever started. Callers close
// the peer connection first, so goroutines reading RTP return. Safe to
// call more than once; later calls return immediately.
func (d *trackDecoder) stop() {
	d.mu.Lock()
	if d.stopping {
		d.mu.Unlock()
		return
	}
	d.stopping = true
	p := d.proc
	d.mu.Unlock()

	if p == nil {
		d.closeOut()
		return
	}
	_ = p.cmd.Process.Kill()
	// Unblock the goroutines even if a forking ffmpeg wrapper left a
	// child holding the pipes open.
	for _, c := range p.pipes {
		_ = c.Close()
	}
	<-p.done
	_ = p.cmd.Wait() // reap: only after nothing reads its pipes any more
}

// drainTrack discards a track's packets in the background until it ends
// (when the peer connection closes), so unused tracks don't back up.
func drainTrack(track *webrtc.TrackRemote) {
	go func() {
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
		}
	}()
}

// goDecoder runs feed and drain in goroutines and returns a channel closed
// once both have returned.
func goDecoder(feed, drain func()) <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		feed()
	}()
	go func() {
		defer wg.Done()
		drain()
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}
