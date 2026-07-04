package reachymini

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

// Frame is one decoded video frame from StreamCameraFrames: 8-bit RGB,
// row-major, Width*Height*3 bytes.
type Frame struct {
	Width, Height int
	RGB           []byte
	Timestamp     time.Time
}

// CameraStreamOptions configures StreamCameraFrames.
type CameraStreamOptions struct {
	// SignallingURL is the WebRTC signalling server's WebSocket URL, e.g.
	// "ws://localhost:8443". This is a *separate* connection from the
	// daemon's REST API -- see webrtc_signalling.go. Defaults to Client's
	// BaseURL host on port 8443 if empty.
	SignallingURL string

	// ProducerName optionally selects a specific producer by its
	// advertised meta name, if the signalling server lists more than one.
	// If empty, the first available producer is used.
	ProducerName string

	// FFmpegPath overrides the ffmpeg binary used to decode video to raw
	// RGB frames. Defaults to "ffmpeg" (resolved via PATH). This is a
	// runtime dependency of the calling machine, not a Go dependency --
	// there is no production-quality pure-Go decoder for either codec the
	// daemon may negotiate (see below).
	FFmpegPath string
}

// StreamCameraFrames connects to the daemon's WebRTC camera feed and
// decodes it to raw RGB frames, delivered on the returned channel until ctx
// is cancelled. Requires an `ffmpeg` binary on PATH (or set
// opts.FFmpegPath).
//
// The negotiated video codec isn't known until after WebRTC negotiation
// completes, so the decoder is started lazily from the OnTrack callback.
// Verified live end-to-end against Pollen's desktop simulator (signalling,
// WebRTC negotiation, VP8 depacketization, IVF muxing, and ffmpeg decode
// all confirmed producing real, correctly-decoded frames). Real hardware
// has been observed (in the daemon's own source) to use hardware H.264 on
// Raspberry Pi instead -- both codecs are handled here, but only the VP8
// path has actually been exercised against a live producer so far.
func (c *Client) StreamCameraFrames(ctx context.Context, opts CameraStreamOptions) (<-chan Frame, <-chan error, error) {
	specs, err := c.GetCameraSpecs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get camera specs: %w", err)
	}
	width, height := specs.DefaultResolution.Width, specs.DefaultResolution.Height
	if width == 0 || height == 0 {
		return nil, nil, fmt.Errorf("camera specs reported an empty default resolution")
	}

	sigURL := opts.SignallingURL
	if sigURL == "" {
		sigURL = defaultSignallingURL(c.BaseURL)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, sigURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dial signalling server %s: %w", sigURL, err)
	}

	sessionID, offerSDP, err := negotiateSession(conn, opts.ProducerName)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("negotiate session: %w", err)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("create peer connection: %w", err)
	}

	ffmpegPath := opts.FFmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	frames := make(chan Frame)
	errs := make(chan error, 1)

	var mu sync.Mutex
	var ffmpegCmd *exec.Cmd
	cleanup := func() {
		mu.Lock()
		if ffmpegCmd != nil && ffmpegCmd.Process != nil {
			_ = ffmpegCmd.Process.Kill()
		}
		mu.Unlock()
		pc.Close()
		conn.Close()
	}

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return // nil signals end-of-candidates; nothing to forward
		}
		if err := sendICECandidate(conn, sessionID, candidate.ToJSON()); err != nil {
			reportErr(ctx, errs, fmt.Errorf("send local ICE candidate: %w", err))
		}
	})

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}

		mimeType := strings.ToLower(track.Codec().MimeType)
		var inputFormat string
		var feed func(ffmpegIn io.WriteCloser)
		switch mimeType {
		case strings.ToLower(webrtc.MimeTypeH264):
			inputFormat = "h264"
			feed = func(ffmpegIn io.WriteCloser) { depacketizeH264(ctx, track, ffmpegIn, errs) }
		case strings.ToLower(webrtc.MimeTypeVP8):
			inputFormat = "ivf"
			feed = func(ffmpegIn io.WriteCloser) { depacketizeVP8(ctx, track, ffmpegIn, width, height, errs) }
		default:
			reportErr(ctx, errs, fmt.Errorf("unsupported video codec %q", track.Codec().MimeType))
			return
		}

		cmd, ffmpegIn, ffmpegOut, err := startFFmpegDecoder(ffmpegPath, inputFormat, width, height)
		if err != nil {
			reportErr(ctx, errs, fmt.Errorf("start ffmpeg decoder: %w", err))
			return
		}
		mu.Lock()
		ffmpegCmd = cmd
		mu.Unlock()

		go readRGBFrames(ctx, ffmpegOut, width, height, frames, errs)
		go feed(ffmpegIn)
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("set remote description: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("create answer: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("set local description: %w", err)
	}
	if err := sendAnswer(conn, sessionID, answer.SDP); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("send answer: %w", err)
	}

	go pumpSignalling(ctx, conn, pc, errs)

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	return frames, errs, nil
}

// startFFmpegDecoder spawns ffmpeg reading inputFormat ("h264" or "ivf") on
// stdin and writing raw RGB24 frames scaled to width x height on stdout.
// Scaling to a fixed size guarantees a predictable frame byte size for
// readRGBFrames regardless of the stream's actual encoded resolution.
func startFFmpegDecoder(ffmpegPath, inputFormat string, width, height int) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command(ffmpegPath,
		"-loglevel", "error",
		"-f", inputFormat,
		"-i", "pipe:0",
		"-vf", fmt.Sprintf("scale=%d:%d", width, height),
		"-pix_fmt", "rgb24",
		"-f", "rawvideo",
		"pipe:1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("start %s: %w (is ffmpeg installed and on PATH?)", ffmpegPath, err)
	}
	return cmd, stdin, stdout, nil
}

// depacketizeH264 reads RTP packets from track, reassembles Annex-B H.264
// NAL units, and writes them to ffmpeg's stdin, until ctx is cancelled or
// the track ends.
func depacketizeH264(ctx context.Context, track *webrtc.TrackRemote, ffmpegIn io.WriteCloser, errs chan<- error) {
	depacketizer := &codecs.H264Packet{}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		packet, _, err := track.ReadRTP()
		if err != nil {
			reportErr(ctx, errs, fmt.Errorf("read RTP packet: %w", err))
			return
		}

		nal, err := depacketizer.Unmarshal(packet.Payload)
		if err != nil || len(nal) == 0 {
			continue
		}
		if _, err := ffmpegIn.Write(nal); err != nil {
			reportErr(ctx, errs, fmt.Errorf("write to ffmpeg: %w", err))
			return
		}
	}
}

// depacketizeVP8 reads RTP packets from track, reassembles complete VP8
// frames (accumulating fragments until the RTP marker bit signals the last
// packet of a frame), and writes them to ffmpeg's stdin as an IVF stream
// (ffmpeg has no raw-VP8-elementary-stream demuxer, so frames need a
// minimal container -- IVF is the simplest one both easy to hand-write and
// natively supported by ffmpeg's `-f ivf`).
func depacketizeVP8(ctx context.Context, track *webrtc.TrackRemote, ffmpegIn io.WriteCloser, width, height int, errs chan<- error) {
	if err := writeIVFHeader(ffmpegIn, width, height); err != nil {
		reportErr(ctx, errs, fmt.Errorf("write IVF header: %w", err))
		return
	}

	depacketizer := &codecs.VP8Packet{}
	var frame []byte
	var frameNum uint64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		packet, _, err := track.ReadRTP()
		if err != nil {
			reportErr(ctx, errs, fmt.Errorf("read RTP packet: %w", err))
			return
		}

		payload, err := depacketizer.Unmarshal(packet.Payload)
		if err != nil {
			continue
		}
		frame = append(frame, payload...)

		if !packet.Marker {
			continue // more fragments still to come for this frame
		}
		if len(frame) > 0 {
			if err := writeIVFFrame(ffmpegIn, frame, frameNum); err != nil {
				reportErr(ctx, errs, fmt.Errorf("write IVF frame: %w", err))
				return
			}
			frameNum++
		}
		frame = frame[:0]
	}
}

// writeIVFHeader writes a 32-byte IVF file header declaring a VP8 stream of
// the given dimensions. Frame count and frame rate are placeholders (0 and
// 30/1): this is a live stream, not a file, and ffmpeg's IVF demuxer
// doesn't require them to be accurate to decode.
func writeIVFHeader(w io.Writer, width, height int) error {
	header := make([]byte, 32)
	copy(header[0:4], "DKIF")
	binary.LittleEndian.PutUint16(header[4:6], 0)  // version
	binary.LittleEndian.PutUint16(header[6:8], 32) // header size
	copy(header[8:12], "VP80")
	binary.LittleEndian.PutUint16(header[12:14], uint16(width))
	binary.LittleEndian.PutUint16(header[14:16], uint16(height))
	binary.LittleEndian.PutUint32(header[16:20], 30) // frame rate numerator
	binary.LittleEndian.PutUint32(header[20:24], 1)  // frame rate denominator
	binary.LittleEndian.PutUint32(header[24:28], 0)  // frame count (unknown)
	binary.LittleEndian.PutUint32(header[28:32], 0)  // reserved
	_, err := w.Write(header)
	return err
}

// writeIVFFrame writes one IVF frame record: a 12-byte header (4-byte
// little-endian size, 8-byte little-endian timestamp/frame number) followed
// by the raw VP8 frame bytes.
func writeIVFFrame(w io.Writer, frame []byte, frameNum uint64) error {
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(frame)))
	binary.LittleEndian.PutUint64(header[4:12], frameNum)
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(frame)
	return err
}

// readRGBFrames reads fixed-size raw RGB24 frames from ffmpeg's stdout and
// delivers them on frames until stdout closes or an error occurs.
func readRGBFrames(ctx context.Context, ffmpegOut io.ReadCloser, width, height int, frames chan<- Frame, errs chan<- error) {
	defer close(frames)
	frameSize := width * height * 3
	for {
		buf := make([]byte, frameSize)
		if _, err := io.ReadFull(ffmpegOut, buf); err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				reportErr(ctx, errs, fmt.Errorf("read decoded frame: %w", err))
			}
			return
		}
		frames <- Frame{Width: width, Height: height, RGB: buf, Timestamp: time.Now()}
	}
}
