// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package realitytr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HelloInfo is the subset of TLS ClientHello fields the Reality
// transport needs to make a routing decision.
//
// Raw retains the full ClientHello bytes so the caller can replay
// them when splicing to the real target on an auth miss.
type HelloInfo struct {
	// SNI is the Server Name Indication, lowercased. Empty when
	// the extension is absent or a non-host_name name type appears.
	SNI string

	// SessionID is the TLS SessionID field (0–32 octets).
	SessionID []byte

	// Raw is the full record bytes that contained this ClientHello,
	// from the leading TLS record header through the end of the
	// handshake message. The caller may replay it to the real
	// target verbatim.
	Raw []byte
}

// Errors returned by ParseClientHello.
var (
	ErrNotTLSRecord    = errors.New("realitytr: not a TLS record")
	ErrNotClientHello  = errors.New("realitytr: not a ClientHello")
	ErrTruncatedRecord = errors.New("realitytr: TLS record truncated")
	ErrMalformedHello  = errors.New("realitytr: malformed ClientHello")
)

// readRecordTimeoutBytes caps how many bytes ParseClientHello pulls
// from the reader before giving up. Real Chrome ClientHellos are
// well under 2 KiB; allowing 8 KiB leaves margin for future
// extensions while still bounding adversarial slowloris-style reads.
const readRecordCap = 8 * 1024

// ParseClientHello reads one TLS handshake record from r, parses
// the ClientHello inside, and returns the extracted HelloInfo. The
// Raw field of the result holds every byte read so the caller can
// replay them to a downstream connection.
//
// On malformed or non-TLS input the function returns the bytes
// already consumed so the caller can decide whether to splice
// them anyway.
func ParseClientHello(r io.Reader) (*HelloInfo, []byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, nil, fmt.Errorf("realitytr: read tls header: %w", err)
	}

	// TLS record header:
	//   ContentType(1) Version(2) Length(2)
	if hdr[0] != 0x16 { // handshake
		return nil, hdr, ErrNotTLSRecord
	}
	recLen := int(binary.BigEndian.Uint16(hdr[3:5]))
	if recLen <= 0 || recLen > readRecordCap-5 {
		return nil, hdr, ErrTruncatedRecord
	}

	body := make([]byte, recLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, append(hdr, body[:0]...), fmt.Errorf("realitytr: read tls record: %w", err)
	}
	raw := append(hdr, body...)

	if len(body) < 4 {
		return nil, raw, ErrMalformedHello
	}
	// Handshake header:
	//   HandshakeType(1) Length(3)
	if body[0] != 0x01 { // ClientHello
		return nil, raw, ErrNotClientHello
	}
	bodyLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if bodyLen != len(body)-4 {
		// Some stacks send fragmented Hellos across multiple
		// records; v0 does not handle that. The caller can still
		// splice the bytes through.
		return nil, raw, ErrMalformedHello
	}

	hello := body[4:]
	info := &HelloInfo{Raw: raw}
	if err := parseHelloBody(hello, info); err != nil {
		return nil, raw, err
	}
	return info, raw, nil
}

func parseHelloBody(b []byte, out *HelloInfo) error {
	// ClientHello layout:
	//   ProtocolVersion legacy_version (2)
	//   Random          random          (32)
	//   opaque          legacy_session_id<0..32>
	//   CipherSuite     cipher_suites<2..2^16-1>
	//   opaque          legacy_compression_methods<1..2^8-1>
	//   Extension       extensions<0..2^16-1>      (TLS 1.3 only,
	//                                               but Chrome+TLS1.2
	//                                               always sends some)
	if len(b) < 2+32+1 {
		return ErrMalformedHello
	}
	off := 2 + 32 // skip version + random

	// SessionID
	sidLen := int(b[off])
	off++
	if sidLen > 32 || off+sidLen > len(b) {
		return ErrMalformedHello
	}
	out.SessionID = append([]byte(nil), b[off:off+sidLen]...)
	off += sidLen

	// CipherSuites
	if off+2 > len(b) {
		return ErrMalformedHello
	}
	csLen := int(binary.BigEndian.Uint16(b[off : off+2]))
	off += 2
	if off+csLen > len(b) {
		return ErrMalformedHello
	}
	off += csLen

	// Compression methods
	if off+1 > len(b) {
		return ErrMalformedHello
	}
	cmLen := int(b[off])
	off++
	if off+cmLen > len(b) {
		return ErrMalformedHello
	}
	off += cmLen

	// Extensions: optional in TLS 1.2; if absent we are done.
	if off >= len(b) {
		return nil
	}
	if off+2 > len(b) {
		return ErrMalformedHello
	}
	extsLen := int(binary.BigEndian.Uint16(b[off : off+2]))
	off += 2
	if off+extsLen > len(b) {
		return ErrMalformedHello
	}
	exts := b[off : off+extsLen]
	for i := 0; i+4 <= len(exts); {
		extType := binary.BigEndian.Uint16(exts[i : i+2])
		extLen := int(binary.BigEndian.Uint16(exts[i+2 : i+4]))
		if i+4+extLen > len(exts) {
			return ErrMalformedHello
		}
		extBody := exts[i+4 : i+4+extLen]
		if extType == 0x00 { // server_name
			if name, ok := parseServerNameExt(extBody); ok {
				out.SNI = name
			}
		}
		i += 4 + extLen
	}
	return nil
}

// parseServerNameExt parses a ServerNameList extension and returns
// the first host_name SNI it encounters.
//
// Layout:
//
//	ServerNameList<1..2^16-1>
//	  length        u16
//	  entries:
//	    NameType    u8     (host_name = 0)
//	    opaque      HostName<1..2^16-1>
func parseServerNameExt(b []byte) (string, bool) {
	if len(b) < 2 {
		return "", false
	}
	listLen := int(binary.BigEndian.Uint16(b[:2]))
	if 2+listLen > len(b) {
		return "", false
	}
	entries := b[2 : 2+listLen]
	for i := 0; i+3 <= len(entries); {
		nameType := entries[i]
		nameLen := int(binary.BigEndian.Uint16(entries[i+1 : i+3]))
		if i+3+nameLen > len(entries) {
			return "", false
		}
		if nameType == 0x00 {
			return lowerASCII(string(entries[i+3 : i+3+nameLen])), true
		}
		i += 3 + nameLen
	}
	return "", false
}

func lowerASCII(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
