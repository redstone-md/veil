// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// FanInListener accepts from N underlying listeners and exposes a
// single Accept channel. It is the server-side glue that lets one
// session-handling code path serve every configured transport.
type FanInListener struct {
	logger *slog.Logger

	mu        sync.Mutex
	listeners []Listener
	closed    chan struct{}
	once      sync.Once

	accepted chan acceptResult
}

type acceptResult struct {
	conn Conn
	from string
	err  error
}

// NewFanIn returns an empty fan-in listener. Add backends with Add.
func NewFanIn(logger *slog.Logger) *FanInListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &FanInListener{
		logger:   logger,
		closed:   make(chan struct{}),
		accepted: make(chan acceptResult, 32),
	}
}

// Add starts a goroutine that pulls from ln until ln returns an error
// (typically because Close was called) and forwards every connection
// to the fan-in channel. The label is used for logging only.
func (f *FanInListener) Add(label string, ln Listener) {
	f.mu.Lock()
	f.listeners = append(f.listeners, ln)
	f.mu.Unlock()

	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				select {
				case f.accepted <- acceptResult{err: err, from: label}:
				case <-f.closed:
				}
				return
			}
			select {
			case f.accepted <- acceptResult{conn: conn, from: label}:
			case <-f.closed:
				_ = conn.Close()
				return
			}
		}
	}()
}

// Accept returns the next connection from any backend listener, or
// an error if ctx is cancelled or the fan-in is closed.
func (f *FanInListener) Accept(ctx context.Context) (Conn, error) {
	for {
		select {
		case r := <-f.accepted:
			if r.err != nil {
				// Per-backend errors are logged but not surfaced
				// unless every backend has died, which we detect
				// by the closed signal.
				f.logger.Warn("transport listener stopped",
					"transport", r.from, "err", r.err)
				continue
			}
			return r.conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.closed:
			return nil, errors.New("transport: fan-in closed")
		}
	}
}

// Close shuts down every backend listener.
func (f *FanInListener) Close() error {
	var first error
	f.once.Do(func() {
		close(f.closed)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, ln := range f.listeners {
			if err := ln.Close(); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}

// FallbackDialer tries a sequence of backends in order, returning
// the first connection that handshakes successfully. Failures are
// reported but never fatal until every option is exhausted.
type FallbackDialer struct {
	logger  *slog.Logger
	options []dialerOption
}

type dialerOption struct {
	label  string
	addr   string
	dialer Dialer
}

// NewFallback constructs an empty fall-back dialer.
func NewFallback(logger *slog.Logger) *FallbackDialer {
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackDialer{logger: logger}
}

// Add appends a backend. Order matters: earlier backends are tried
// first.
func (f *FallbackDialer) Add(label, addr string, d Dialer) {
	f.options = append(f.options, dialerOption{label: label, addr: addr, dialer: d})
}

// Dial walks the configured backends in order and returns the first
// connection that succeeds, along with the label of the backend that
// succeeded.
func (f *FallbackDialer) Dial(ctx context.Context) (Conn, string, error) {
	if len(f.options) == 0 {
		return nil, "", errors.New("transport: no backends configured")
	}
	var lastErr error
	for _, opt := range f.options {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		f.logger.Info("transport dial", "transport", opt.label, "addr", opt.addr)
		conn, err := opt.dialer.Dial(ctx, opt.addr)
		if err == nil {
			return conn, opt.label, nil
		}
		f.logger.Warn("transport dial failed",
			"transport", opt.label, "addr", opt.addr, "err", err)
		lastErr = err
	}
	return nil, "", fmt.Errorf("all transports failed: %w", lastErr)
}
