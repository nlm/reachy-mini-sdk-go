package reachymini

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// oggPageHeaderLen is the fixed part of an Ogg page header, before the
// segment table (RFC 3533 section 6).
const oggPageHeaderLen = 27

// readOggPackets reads an Ogg bitstream from r and calls fn with each
// complete packet, in order, reassembling packets that span pages. It
// returns nil when r ends cleanly at a page boundary, or fn's first error.
// pion's oggreader isn't used because it returns whole page payloads,
// losing packet boundaries when a page carries more than one packet.
// Page CRCs aren't checked: the input is a local ffmpeg pipe, not a file
// or network stream that could be corrupted.
func readOggPackets(r io.Reader, fn func(packet []byte) error) error {
	var header [oggPageHeaderLen]byte
	var packet []byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read ogg page header: %w", err)
		}
		if !bytes.Equal(header[:4], []byte("OggS")) {
			return fmt.Errorf("bad ogg capture pattern %q", header[:4])
		}
		segments := make([]byte, header[26])
		if _, err := io.ReadFull(r, segments); err != nil {
			return fmt.Errorf("read ogg segment table: %w", noEOF(err))
		}
		payloadLen := 0
		for _, s := range segments {
			payloadLen += int(s)
		}
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("read ogg page payload: %w", noEOF(err))
		}
		// Header type bit 0 flags a page that continues the previous
		// page's last packet; without it, any partial packet is stale.
		if header[5]&0x01 == 0 {
			packet = nil
		}
		for _, s := range segments {
			packet = append(packet, payload[:s]...)
			payload = payload[s:]
			// A lacing value below 255 ends the packet.
			if s < 255 {
				if err := fn(packet); err != nil {
					return err
				}
				packet = nil
			}
		}
	}
}

// noEOF turns io.EOF into io.ErrUnexpectedEOF, for reads that started
// mid-page.
func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
