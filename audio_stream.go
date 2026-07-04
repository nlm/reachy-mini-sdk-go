package reachymini

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

// AudioStreamOptions configures StreamMicrophoneAudio.
type AudioStreamOptions struct {
	// SignallingURL is the WebRTC signalling server's WebSocket URL, e.g.
	// "ws://localhost:8443". Same signalling server as CameraStreamOptions
	// (see camera_stream.go) -- defaults to Client's BaseURL host on port
	// 8443 if empty. Opening this alongside a concurrent StreamCameraFrames
	// call is fine: the daemon's webrtcsink producer supports multiple
	// simultaneous consumer sessions.
	SignallingURL string

	// ProducerName optionally selects a specific producer by its
	// advertised meta name, if the signalling server lists more than one.
	// If empty, the first available producer is used.
	ProducerName string

	// FFmpegPath overrides the ffmpeg binary used to decode Opus to raw
	// PCM. Defaults to "ffmpeg" (resolved via PATH).
	FFmpegPath string
}

// AudioSampleRate is the sample rate (Hz) of the PCM chunks
// StreamMicrophoneAudio delivers. Opus's RTP clock rate is fixed at 48000 by
// RFC 7587 regardless of the codec's actual internal audio bandwidth, so
// this is a constant rather than something negotiated per-track.
const AudioSampleRate = 48000

// AudioChannels is the channel count of the PCM chunks StreamMicrophoneAudio
// delivers. The decoder always downmixes to mono so callers don't need to
// discover the negotiated channel count asynchronously from inside OnTrack.
const AudioChannels = 1

// StreamMicrophoneAudio connects to the daemon's WebRTC feed and decodes the
// robot microphone's Opus audio track to raw signed 16-bit little-endian PCM,
// delivered on the returned channel until ctx is cancelled. Requires an
// `ffmpeg` binary on PATH (or set opts.FFmpegPath).
//
// The daemon's webrtcsink producer carries both a video track (see
// StreamCameraFrames) and this audio track in the same negotiated session;
// this function only consumes the audio one. Unlike H.264/VP8 depacketizing,
// Opus needs no reassembly across RTP packets (one packet is one Opus
// frame), so the incoming RTP stream is simply muxed into an Ogg/Opus
// container (github.com/pion/webrtc/v4/pkg/media/oggwriter) and piped into
// ffmpeg for decoding.
//
// This mirrors the official Python SDK's ReachyMini(media_backend="webrtc")
// + mini.media.start_recording()/get_audio_sample() path (see
// examples/sound_record.py in pollen-robotics/reachy_mini), which relies on
// the same underlying daemon capability. It has not yet been exercised
// against a live robot or simulator -- see the README's "Known issues".
func (c *Client) StreamMicrophoneAudio(ctx context.Context, opts AudioStreamOptions) (<-chan []byte, <-chan error, error) {
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

	pcmChunks := make(chan []byte)
	errs := make(chan error, 1)

	var mu sync.Mutex
	var ffmpegCmd *exec.Cmd
	var oggw *oggwriter.OggWriter
	cleanup := func() {
		mu.Lock()
		if oggw != nil {
			_ = oggw.Close()
		}
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
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}

		mimeType := strings.ToLower(track.Codec().MimeType)
		if mimeType != strings.ToLower(webrtc.MimeTypeOpus) {
			reportErr(ctx, errs, fmt.Errorf("unsupported audio codec %q", track.Codec().MimeType))
			return
		}

		// The Ogg/Opus container declares the track's own negotiated clock
		// rate/channels (needed to build valid Opus headers), independent
		// of the ffmpeg output format below.
		sampleRate := int(track.Codec().ClockRate)
		channels := int(track.Codec().Channels)
		if channels == 0 {
			channels = 1
		}

		cmd, ffmpegIn, ffmpegOut, err := startFFmpegAudioDecoder(ffmpegPath)
		if err != nil {
			reportErr(ctx, errs, fmt.Errorf("start ffmpeg decoder: %w", err))
			return
		}
		w, err := oggwriter.NewWith(ffmpegIn, uint32(sampleRate), uint16(channels))
		if err != nil {
			reportErr(ctx, errs, fmt.Errorf("create ogg/opus writer: %w", err))
			_ = ffmpegIn.Close()
			_ = cmd.Process.Kill()
			return
		}
		mu.Lock()
		ffmpegCmd = cmd
		oggw = w
		mu.Unlock()

		go readPCMChunks(ctx, ffmpegOut, pcmChunks, errs)
		go depacketizeOpus(ctx, track, w, ffmpegIn, errs)
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

	return pcmChunks, errs, nil
}

// startFFmpegAudioDecoder spawns ffmpeg reading an Ogg/Opus container on
// stdin and writing raw signed 16-bit little-endian mono PCM at
// AudioSampleRate on stdout, regardless of the input track's own channel
// count -- see AudioSampleRate/AudioChannels.
func startFFmpegAudioDecoder(ffmpegPath string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command(ffmpegPath,
		"-loglevel", "error",
		"-f", "ogg",
		"-i", "pipe:0",
		"-ar", fmt.Sprintf("%d", AudioSampleRate),
		"-ac", fmt.Sprintf("%d", AudioChannels),
		"-f", "s16le",
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

// depacketizeOpus reads RTP packets from track and muxes them into w (an
// Ogg/Opus container writer feeding ffmpeg's stdin), until ctx is cancelled
// or the track ends. Unlike H.264/VP8, Opus needs no reassembly across
// packets -- one RTP packet carries exactly one Opus frame.
func depacketizeOpus(ctx context.Context, track *webrtc.TrackRemote, w *oggwriter.OggWriter, ffmpegIn io.WriteCloser, errs chan<- error) {
	defer ffmpegIn.Close()
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

		if err := w.WriteRTP(packet); err != nil {
			reportErr(ctx, errs, fmt.Errorf("write to ogg/opus writer: %w", err))
			return
		}
	}
}

// readPCMChunks reads raw PCM from ffmpeg's stdout and delivers it in
// fixed-size chunks on pcmChunks until stdout closes or an error occurs.
func readPCMChunks(ctx context.Context, ffmpegOut io.ReadCloser, pcmChunks chan<- []byte, errs chan<- error) {
	defer close(pcmChunks)
	const chunkSize = 4096
	for {
		buf := make([]byte, chunkSize)
		n, err := ffmpegOut.Read(buf)
		if n > 0 {
			pcmChunks <- buf[:n]
		}
		if err != nil {
			if err != io.EOF {
				reportErr(ctx, errs, fmt.Errorf("read decoded audio: %w", err))
			}
			return
		}
	}
}
