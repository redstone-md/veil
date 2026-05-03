// Package transport defines the common interface that VWP/1
// transport adapters implement, and houses the individual adapters
// (QUIC, TLS-Reality, WebSocket-over-TLS, HTTP/3 MASQUE) in
// sub-packages.
//
// In the v0 prototype only the QUIC adapter is provided.
package transport

import (
	"context"
	"io"
	"net"
)

// Conn is a duplex byte-stream produced by an adapter. It carries a
// single VWP/1 session.
//
// Implementations MAY also satisfy net.Conn; callers that need
// deadlines should type-assert.
type Conn interface {
	io.ReadWriteCloser

	// LocalAddr returns the local network address of the connection,
	// or nil if not applicable to the underlying transport.
	LocalAddr() net.Addr
	// RemoteAddr returns the remote network address of the connection,
	// or nil if not applicable to the underlying transport.
	RemoteAddr() net.Addr
}

// Listener accepts incoming connections from a single transport
// adapter.
type Listener interface {
	// Accept blocks until a connection arrives or the context is
	// cancelled.
	Accept(ctx context.Context) (Conn, error)
	// Close shuts the listener down. Subsequent Accept calls return
	// an error.
	Close() error
}

// Dialer initiates outbound connections through a transport adapter.
type Dialer interface {
	Dial(ctx context.Context, addr string) (Conn, error)
}
