package reachymini

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os/exec"
	"testing"
)

// oggPage builds one Ogg page holding the given lacing values and payload.
func oggPage(continued bool, lacing []byte, payload []byte) []byte {
	h := make([]byte, oggPageHeaderLen)
	copy(h, "OggS")
	if continued {
		h[5] = 0x01
	}
	h[26] = byte(len(lacing))
	out := append(h, lacing...)
	return append(out, payload...)
}

func collectOggPackets(t *testing.T, data []byte) ([][]byte, error) {
	t.Helper()
	var got [][]byte
	err := readOggPackets(bytes.NewReader(data), func(p []byte) error {
		got = append(got, append([]byte(nil), p...))
		return nil
	})
	return got, err
}

func TestReadOggPacketsSplitsAndJoins(t *testing.T) {
	big := bytes.Repeat([]byte{'b'}, 300) // 255 + 45, spans two pages
	var data []byte
	// Page 1: packet "aa", packet "c", then the first 255 bytes of big.
	data = append(data, oggPage(false, []byte{2, 1, 255}, append([]byte("aac"), big[:255]...))...)
	// Page 2: the rest of big, then "dd".
	data = append(data, oggPage(true, []byte{45, 2}, append(append([]byte(nil), big[255:]...), "dd"...))...)

	got, err := collectOggPackets(t, data)
	if err != nil {
		t.Fatalf("readOggPackets: %v", err)
	}
	want := [][]byte{[]byte("aa"), []byte("c"), big, []byte("dd")}
	if len(got) != len(want) {
		t.Fatalf("got %d packets, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("packet %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadOggPacketsExact255PacketEndsWithZeroLacing(t *testing.T) {
	p := bytes.Repeat([]byte{'x'}, 255)
	got, err := collectOggPackets(t, oggPage(false, []byte{255, 0}, p))
	if err != nil {
		t.Fatalf("readOggPackets: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], p) {
		t.Fatalf("got %d packets, want one of 255 bytes", len(got))
	}
}

func TestReadOggPacketsDropsUncontinuedPartial(t *testing.T) {
	// Page 1 ends mid-packet but page 2 doesn't claim to continue it.
	data := oggPage(false, []byte{255}, bytes.Repeat([]byte{'x'}, 255))
	data = append(data, oggPage(false, []byte{1}, []byte("y"))...)
	got, err := collectOggPackets(t, data)
	if err != nil {
		t.Fatalf("readOggPackets: %v", err)
	}
	if len(got) != 1 || string(got[0]) != "y" {
		t.Fatalf("got %q, want [y]", got)
	}
}

func TestReadOggPacketsTruncated(t *testing.T) {
	page := oggPage(false, []byte{4}, []byte("abcd"))
	for _, n := range []int{10, oggPageHeaderLen, len(page) - 1} {
		_, err := collectOggPackets(t, page[:n])
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("truncated at %d: err = %v, want io.ErrUnexpectedEOF", n, err)
		}
	}
}

func TestReadOggPacketsBadCapture(t *testing.T) {
	page := oggPage(false, []byte{1}, []byte("a"))
	copy(page, "Nope")
	if _, err := collectOggPackets(t, page); err == nil {
		t.Fatal("want error for bad capture pattern")
	}
}

func TestReadOggPacketsStopsOnCallbackError(t *testing.T) {
	stop := errors.New("stop")
	calls := 0
	err := readOggPackets(bytes.NewReader(oggPage(false, []byte{1, 1}, []byte("ab"))), func([]byte) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want stop after 1", err, calls)
	}
}

func TestFFmpegOpusEncodePipeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	const sampleRate, seconds = 22050, 1
	pcm := make([]byte, sampleRate*2*seconds)
	for i := 0; i < len(pcm)/2; i++ {
		// Square wave, loud enough not to be encoded as silence.
		v := int16(8000)
		if (i/50)%2 == 0 {
			v = -v
		}
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
	}

	cmd, stdin, stdout, err := startFFmpegOpusEncoder("ffmpeg", sampleRate, 1)
	if err != nil {
		t.Fatalf("startFFmpegOpusEncoder: %v", err)
	}
	go func() {
		_, _ = stdin.Write(pcm)
		_ = stdin.Close()
	}()

	var packets [][]byte
	if err := readOggPackets(stdout, func(p []byte) error {
		packets = append(packets, append([]byte(nil), p...))
		return nil
	}); err != nil {
		t.Fatalf("readOggPackets: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}

	if len(packets) < 2 || !bytes.HasPrefix(packets[0], []byte("OpusHead")) || !bytes.HasPrefix(packets[1], []byte("OpusTags")) {
		t.Fatalf("want OpusHead, OpusTags headers first, got %d packets", len(packets))
	}
	audio := packets[2:]
	// 1 s of 20 ms frames is 50, plus up to a few for encoder priming/padding.
	if len(audio) < 50 || len(audio) > 55 {
		t.Errorf("got %d audio packets, want ~50", len(audio))
	}
	for i, p := range audio {
		if ms := opusTOCFrameMs(p[0]); ms != opusFrameDuration {
			t.Fatalf("packet %d is a %v ms frame, want %d", i, ms, opusFrameDuration)
		}
	}
}

// opusTOCFrameMs returns the frame duration an Opus packet's TOC byte
// declares (RFC 6716 section 3.1).
func opusTOCFrameMs(toc byte) float64 {
	config := toc >> 3
	switch {
	case config < 12: // SILK: 10, 20, 40, 60 ms
		return []float64{10, 20, 40, 60}[config%4]
	case config < 16: // Hybrid: 10, 20 ms
		return []float64{10, 20}[config%2]
	default: // CELT: 2.5, 5, 10, 20 ms
		return []float64{2.5, 5, 10, 20}[config%4]
	}
}

func TestOpusTOCFrameMs(t *testing.T) {
	for _, tt := range []struct {
		config byte
		want   float64
	}{{1, 20}, {3, 60}, {9, 20}, {13, 20}, {14, 10}, {16, 2.5}, {31, 20}} {
		if got := opusTOCFrameMs(tt.config << 3); got != tt.want {
			t.Errorf("config %d = %v ms, want %v", tt.config, got, tt.want)
		}
	}
}
