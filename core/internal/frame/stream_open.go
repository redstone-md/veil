// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// StreamType differentiates the substrate carried inside a stream.
type StreamType uint8

// Stream type codes. See docs/PROTOCOL.md §5.1.
const (
	StreamTypeReliable StreamType = 0x01
	StreamTypeDatagram StreamType = 0x02
)

// Address-type codes inside a STREAM_OPEN payload.
const (
	addrIPv4   = 0x01
	addrIPv6   = 0x02
	addrDomain = 0x03
)

// Address is a destination endpoint carried in a STREAM_OPEN payload.
//
// Exactly one of IP or Host is non-empty.
type Address struct {
	// IP is set for AddrIPv4 / AddrIPv6 forms.
	IP net.IP
	// Host is set for the domain form (RFC 1035 hostname).
	Host string
	// Port is the TCP destination port.
	Port uint16
}

// String renders the address as host:port suitable for net.Dial.
func (a Address) String() string {
	if a.IP != nil {
		return net.JoinHostPort(a.IP.String(), portStr(a.Port))
	}
	return net.JoinHostPort(a.Host, portStr(a.Port))
}

func portStr(p uint16) string { return fmt.Sprintf("%d", p) }

// StreamOpenPayload is the decoded STREAM_OPEN frame payload.
type StreamOpenPayload struct {
	StreamType    StreamType
	InitialWindow uint32
	Target        Address
}

// Encode serialises a STREAM_OPEN payload per docs/PROTOCOL.md §5.1.
func (p *StreamOpenPayload) Encode() ([]byte, error) {
	addrBlob, err := encodeAddress(p.Target)
	if err != nil {
		return nil, err
	}
	if len(addrBlob) > MaxPayload-7 {
		return nil, errors.New("stream open: address too long")
	}
	out := make([]byte, 0, 7+len(addrBlob))
	out = append(out, byte(p.StreamType))
	out = binary.BigEndian.AppendUint32(out, p.InitialWindow)
	out = binary.BigEndian.AppendUint16(out, uint16(len(addrBlob)))
	out = append(out, addrBlob...)
	return out, nil
}

// DecodeStreamOpen parses a STREAM_OPEN payload.
func DecodeStreamOpen(b []byte) (*StreamOpenPayload, error) {
	if len(b) < 7 {
		return nil, errors.New("stream open: short payload")
	}
	mlen := int(binary.BigEndian.Uint16(b[5:7]))
	if len(b) < 7+mlen {
		return nil, errors.New("stream open: metadata truncated")
	}
	addr, err := decodeAddress(b[7 : 7+mlen])
	if err != nil {
		return nil, err
	}
	return &StreamOpenPayload{
		StreamType:    StreamType(b[0]),
		InitialWindow: binary.BigEndian.Uint32(b[1:5]),
		Target:        addr,
	}, nil
}

func encodeAddress(a Address) ([]byte, error) {
	switch {
	case a.IP != nil && a.IP.To4() != nil:
		out := make([]byte, 0, 1+4+2)
		out = append(out, addrIPv4)
		out = append(out, a.IP.To4()...)
		out = binary.BigEndian.AppendUint16(out, a.Port)
		return out, nil
	case a.IP != nil:
		out := make([]byte, 0, 1+16+2)
		out = append(out, addrIPv6)
		out = append(out, a.IP.To16()...)
		out = binary.BigEndian.AppendUint16(out, a.Port)
		return out, nil
	case a.Host != "":
		if len(a.Host) > 255 {
			return nil, errors.New("stream open: hostname too long")
		}
		out := make([]byte, 0, 1+1+len(a.Host)+2)
		out = append(out, addrDomain)
		out = append(out, byte(len(a.Host)))
		out = append(out, a.Host...)
		out = binary.BigEndian.AppendUint16(out, a.Port)
		return out, nil
	default:
		return nil, errors.New("stream open: empty address")
	}
}

func decodeAddress(b []byte) (Address, error) {
	if len(b) < 1 {
		return Address{}, errors.New("stream open: empty address blob")
	}
	switch b[0] {
	case addrIPv4:
		if len(b) != 1+4+2 {
			return Address{}, fmt.Errorf("stream open: ipv4 wrong size %d", len(b))
		}
		return Address{
			IP:   net.IP(b[1:5]),
			Port: binary.BigEndian.Uint16(b[5:7]),
		}, nil
	case addrIPv6:
		if len(b) != 1+16+2 {
			return Address{}, fmt.Errorf("stream open: ipv6 wrong size %d", len(b))
		}
		return Address{
			IP:   net.IP(b[1:17]),
			Port: binary.BigEndian.Uint16(b[17:19]),
		}, nil
	case addrDomain:
		if len(b) < 4 {
			return Address{}, errors.New("stream open: domain too short")
		}
		hlen := int(b[1])
		if len(b) != 1+1+hlen+2 {
			return Address{}, fmt.Errorf("stream open: domain wrong size %d for hlen %d", len(b), hlen)
		}
		return Address{
			Host: string(b[2 : 2+hlen]),
			Port: binary.BigEndian.Uint16(b[2+hlen : 2+hlen+2]),
		}, nil
	default:
		return Address{}, fmt.Errorf("stream open: unknown address type %#02x", b[0])
	}
}
