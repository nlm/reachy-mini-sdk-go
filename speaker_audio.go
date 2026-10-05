package reachymini

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

// opusFrameDuration is the duration of each Opus frame the speaker encoder
// produces, in milliseconds. 20 ms is the WebRTC norm.
const opusFrameDuration = 20

// startFFmpegOpusEncoder spawns ffmpeg reading raw signed 16-bit
// little-endian PCM (sampleRate Hz, channels channels) on stdin and writing
// mono 48 kHz Opus in an Ogg container on stdout, one 20 ms frame per
// packet. Input probing is disabled and pages are flushed per packet, so
// frames come out as soon as they're encoded (~100 ms after their input)
// rather than in bursts.
func startFFmpegOpusEncoder(ffmpegPath string, sampleRate, channels int) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command(ffmpegPath,
		"-loglevel", "error",
		// Raw PCM needs no probing; ffmpeg's default analysis would
		// otherwise buffer ~5 s of a real-time stream before encoding.
		"-probesize", "32",
		"-analyzeduration", "0",
		"-f", "s16le",
		"-ar", strconv.Itoa(sampleRate),
		"-ac", strconv.Itoa(channels),
		"-i", "pipe:0",
		"-ar", "48000",
		"-ac", "1",
		"-c:a", "libopus",
		"-frame_duration", strconv.Itoa(opusFrameDuration),
		"-page_duration", strconv.Itoa(opusFrameDuration*1000),
		"-flush_packets", "1",
		"-f", "ogg",
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
