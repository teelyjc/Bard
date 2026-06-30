package audio

import (
	"fmt"
	"io"
)

// ReadOggOpus reads opus audio packets from an ogg bitstream, calling fn for
// each complete packet. Skips OpusHead and OpusTags header packets. Returns
// when fn returns false or the stream ends.
func ReadOggOpus(r io.Reader, fn func([]byte) bool) error {
	hdr := make([]byte, 27)
	var carry []byte // accumulates bytes for packets that span multiple pages

	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		if string(hdr[:4]) != "OggS" {
			return fmt.Errorf("ogg: invalid capture pattern")
		}

		numSegs := int(hdr[26])
		segs := make([]byte, numSegs)
		if _, err := io.ReadFull(r, segs); err != nil {
			return err
		}

		pageSize := 0
		for _, s := range segs {
			pageSize += int(s)
		}
		page := make([]byte, pageSize)
		if _, err := io.ReadFull(r, page); err != nil {
			return err
		}

		offset := 0
		for _, segLen := range segs {
			carry = append(carry, page[offset:offset+int(segLen)]...)
			offset += int(segLen)

			if segLen < 255 {
				// Packet is complete — skip Opus header packets
				pkt := carry
				carry = nil
				if len(pkt) >= 8 && (string(pkt[:8]) == "OpusHead" || string(pkt[:8]) == "OpusTags") {
					continue
				}
				if len(pkt) > 0 && !fn(pkt) {
					return nil
				}
			}
			// segLen == 255 means the packet continues into the next segment/page
		}
	}
}
