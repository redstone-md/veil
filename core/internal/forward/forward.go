// Package forward implements server-side stream handling: for each
// accepted Veil stream it dials the requested target host:port and
// pipes the two byte streams together until either side closes.
//
// An optional Accountant hook is invoked for every byte that crosses
// the proxy, in either direction. The serve subcommand binds this
// hook to the user store to maintain per-user monthly usage and
// enforce per-user quotas.
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

// Accountant receives per-direction byte counts for every accepted
// stream. Implementations MUST be cheap (the Add call lands on the
// hot path) and MUST be safe for concurrent use.
type Accountant interface {
	// Add reports n bytes transferred. Direction is "tx" for client
	// → upstream and "rx" for upstream → client.
	Add(direction string, n int)
	// QuotaExceeded reports whether further forwarding is allowed.
	// When true, the forward server tears the stream down with
	// io.EOF on the next byte.
	QuotaExceeded() bool
}

// Server runs the AcceptStream loop on a session and forwards each
// accepted stream to its target address.
type Server struct {
	sess       *session.Session
	logger     *slog.Logger
	dialer     *net.Dialer
	accountant Accountant
}

// Options configures NewServer.
type Options struct {
	Logger     *slog.Logger
	Dialer     *net.Dialer
	Accountant Accountant
}

// NewServer constructs a forwarding server bound to sess. Pass a nil
// Options is allowed; sensible defaults are used.
func NewServer(sess *session.Session, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Dialer == nil {
		opts.Dialer = &net.Dialer{Timeout: DialTimeout}
	}
	return &Server{
		sess:       sess,
		logger:     opts.Logger,
		dialer:     opts.Dialer,
		accountant: opts.Accountant,
	}
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

	if s.accountant != nil && s.accountant.QuotaExceeded() {
		logger.Info("stream rejected: quota exceeded")
		return
	}

	upstream, err := s.dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		logger.Warn("dial upstream failed", "err", err)
		return
	}
	defer upstream.Close()
	logger.Info("upstream connected")

	pipe(st, upstream, s.accountant)
}

// pipe runs full-duplex io.Copy between a Veil stream and a TCP
// connection, optionally accounting bytes through an Accountant.
// It returns when either direction is closed.
func pipe(stream io.ReadWriteCloser, upstream net.Conn, acc Accountant) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(countedWriter{w: upstream, dir: "tx", acc: acc},
			countedReader{r: stream, dir: "tx", acc: acc})
		if tc, ok := upstream.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(countedWriter{w: stream, dir: "rx", acc: acc},
			countedReader{r: upstream, dir: "rx", acc: acc})
		_ = stream.Close()
	}()
	wg.Wait()
}

// countedReader and countedWriter bracket a Reader/Writer with
// optional byte accounting. Splitting accounting at the read side
// keeps it accurate even when the writer drops short or errors.
type countedReader struct {
	r   io.Reader
	dir string
	acc Accountant
}

func (c countedReader) Read(p []byte) (int, error) {
	if c.acc != nil && c.acc.QuotaExceeded() {
		return 0, io.EOF
	}
	n, err := c.r.Read(p)
	if n > 0 && c.acc != nil {
		c.acc.Add(c.dir, n)
	}
	return n, err
}

type countedWriter struct {
	w   io.Writer
	dir string
	acc Accountant
}

func (c countedWriter) Write(p []byte) (int, error) { return c.w.Write(p) }
