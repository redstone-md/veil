// Package forward implements server-side stream handling: for each
// accepted Veil stream it dials the requested target host:port and
// pipes the two byte streams together until either side closes.
package forward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/redstone-md/veil/core/internal/session"
)

// DialTimeout caps the time spent connecting to an upstream target.
const DialTimeout = 15 * time.Second

// Server runs the AcceptStream loop on a session and forwards each
// accepted stream to its target address.
type Server struct {
	sess   *session.Session
	logger *slog.Logger
	dialer *net.Dialer
}

// NewServer constructs a forwarding server bound to sess. The dialer
// argument may be nil to use a sensible default.
func NewServer(sess *session.Session, logger *slog.Logger, dialer *net.Dialer) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if dialer == nil {
		dialer = &net.Dialer{Timeout: DialTimeout}
	}
	return &Server{sess: sess, logger: logger, dialer: dialer}
}

// Run blocks accepting streams until ctx is cancelled or the session
// fails. Errors per-stream are logged but do not terminate the loop.
func (s *Server) Run(ctx context.Context) error {
	for {
		st, err := s.sess.AcceptStream(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		go s.handle(ctx, st)
	}
}

func (s *Server) handle(ctx context.Context, st *session.Stream) {
	defer st.Close()

	target := st.Target().String()
	logger := s.logger.With("stream", st.ID(), "target", target)

	upstream, err := s.dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		logger.Warn("dial upstream failed", "err", err)
		return
	}
	defer upstream.Close()
	logger.Info("upstream connected")

	pipe(st, upstream)
}

// pipe runs full-duplex io.Copy between a Veil stream and a TCP
// connection. It returns when either direction is closed.
func pipe(stream io.ReadWriteCloser, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, stream)
		// Half-close upstream so the remote end sees FIN and the
		// other goroutine eventually unblocks.
		if tc, ok := upstream.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stream, upstream)
		// Closing the stream's write side propagates EOF back to
		// the client.
		_ = stream.Close()
	}()
	wg.Wait()
}
