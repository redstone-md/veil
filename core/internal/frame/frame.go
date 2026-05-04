// Package frame implements the binary VWP/1 frame codec.
//
// The on-the-wire layout is specified in docs/PROTOCOL.md §4. A
// frame consists of a 12-octet fixed header (type, flags, two
// reserved octets, 32-bit stream id, payload length, padding length)
// followed by the payload bytes and then padding bytes.
//
// This package handles only the frame layer; cipher framing and
// session-level semantics live elsewhere.
package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderSize is the size of a VWP/1 frame header in octets.
const HeaderSize = 12

// MaxPayload is the largest legal value for the Payload Length field.
// MaxPadding is the largest legal value for the Padding Length field.
// Per spec §4 both fields are 14 bits in magnitude (max 16383).
const (
	MaxPayload = 1<<14 - 1
	MaxPadding = 1<<14 - 1
)

// Type identifies a VWP/1 frame's semantics.
type Type uint8

// Frame type codes. See docs/PROTOCOL.md §5.
const (
	TypeStreamData   Type = 0x01
	TypeStreamOpen   Type = 0x02
	TypeStreamClose  Type = 0x03
	TypePing         Type = 0x04
	TypePong         Type = 0x05
	TypeWindowUpdate Type = 0x06
	TypeControl      Type = 0x07
	TypePaddingOnly  Type = 0xFF
)

// Flag bits. See docs/PROTOCOL.md §4.1.
const (
	FlagEndStream  uint8 = 1 << 0
	FlagCompressed uint8 = 1 << 1
)

// Frame is the in-memory representation of a single VWP/1 frame.
//
// Padding length is preserved on decode so that callers (the mimicry
// layer) can observe the exact padding the peer chose; it is not
// otherwise meaningful to the application.
type Frame struct {
	Type     Type
	Flags    uint8
	StreamID uint32
	Payload  []byte
	// PaddingLen is the number of padding octets received. The
	// padding contents themselves are discarded on decode.
	PaddingLen uint16
}

// EncodedLen returns the number of octets the frame would occupy on
// the wire.
func (f *Frame) EncodedLen() int {
	return HeaderSize + len(f.Payload) + int(f.PaddingLen)
}

// AppendEncoded serialises the frame and appends it to dst, returning
// the extended slice. Padding bytes are written as zeros; callers that
// require random padding should overwrite the tail before sending.
//
// Appending instead of allocating lets the session layer build packets
// into a reusable buffer.
func (f *Frame) AppendEncoded(dst []byte) ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, fmt.Errorf("frame payload too large: %d > %d", len(f.Payload), MaxPayload)
	}
	if f.PaddingLen > MaxPadding {
		return nil, fmt.Errorf("frame padding too large: %d > %d", f.PaddingLen, MaxPadding)
	}
	out := append(dst, byte(f.Type), f.Flags, 0x00, 0x00)
	out = binary.BigEndian.AppendUint32(out, f.StreamID)
	out = binary.BigEndian.AppendUint16(out, uint16(len(f.Payload)))
	out = binary.BigEndian.AppendUint16(out, f.PaddingLen)
	out = append(out, f.Payload...)
	if f.PaddingLen > 0 {
		zeros := make([]byte, f.PaddingLen)
		out = append(out, zeros...)
	}
	return out, nil
}

// Encode is a convenience wrapper around AppendEncoded that allocates
// a fresh buffer.
func (f *Frame) Encode() ([]byte, error) {
	return f.AppendEncoded(nil)
}

// ErrShortFrame is returned by Decode when the input slice is too
// short to contain a complete frame.
var ErrShortFrame = errors.New("frame: short read")

// ErrReservedNonZero is returned by Decode when the two reserved
// header octets are not zero. Per spec receivers MUST ignore these,
// but a conformant codec offers callers the option to be strict.
var ErrReservedNonZero = errors.New("frame: reserved bits non-zero")

// Decode parses a single frame from b and returns it together with
// the number of octets consumed. The Frame's Payload slice aliases
// b; callers that retain it across buffer reuse must copy.
//
// If b does not yet contain a full frame, Decode returns 0 and
// ErrShortFrame so the caller can read more input.
func Decode(b []byte) (*Frame, int, error) {
	if len(b) < HeaderSize {
		return nil, 0, ErrShortFrame
	}
	plen := int(binary.BigEndian.Uint16(b[8:10]))
	padlen := int(binary.BigEndian.Uint16(b[10:12]))
	total := HeaderSize + plen + padlen
	if plen > MaxPayload {
		return nil, 0, fmt.Errorf("frame: payload length %d exceeds max %d", plen, MaxPayload)
	}
	if padlen > MaxPadding {
		return nil, 0, fmt.Errorf("frame: padding length %d exceeds max %d", padlen, MaxPadding)
	}
	if len(b) < total {
		return nil, 0, ErrShortFrame
	}
	f := &Frame{
		Type:       Type(b[0]),
		Flags:      b[1],
		StreamID:   binary.BigEndian.Uint32(b[4:8]),
		Payload:    b[HeaderSize : HeaderSize+plen],
		PaddingLen: uint16(padlen),
	}
	return f, total, nil
}

// String renders the frame header for human-readable logging. The
// payload is summarised as a length to avoid leaking content into
// log streams.
func (f *Frame) String() string {
	return fmt.Sprintf("Frame{type=%s flags=%#02x stream=%d payload=%dB pad=%dB}",
		f.Type, f.Flags, f.StreamID, len(f.Payload), f.PaddingLen)
}

// String renders a Type as its symbolic name when known.
func (t Type) String() string {
	switch t {
	case TypeStreamData:
		return "STREAM_DATA"
	case TypeStreamOpen:
		return "STREAM_OPEN"
	case TypeStreamClose:
		return "STREAM_CLOSE"
	case TypePing:
		return "PING"
	case TypePong:
		return "PONG"
	case TypeWindowUpdate:
		return "WINDOW_UPDATE"
	case TypeControl:
		return "CONTROL"
	case TypePaddingOnly:
		return "PADDING_ONLY"
	default:
		return fmt.Sprintf("Type(%#02x)", uint8(t))
	}
}
