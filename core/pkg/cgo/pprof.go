// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

//go:build cgo

// Optional in-process pprof server, gated by the VEIL_PPROF_ADDR
// environment variable. Off by default — when the variable is unset
// nothing binds, nothing imports beyond the standard library, no
// HTTP listener exists.
//
// Used for triaging memory bloat in the desktop client. Set
// `VEIL_PPROF_ADDR=127.0.0.1:6060` before launching veil-desktop.exe
// and the loaded libveil DLL will expose the standard
// /debug/pprof/{heap,allocs,goroutine,profile,...} endpoints on that
// address.
//
// Hardening:
//   - Only loopback hosts (127.0.0.1, ::1, localhost) are accepted.
//     A non-loopback bind is silently rejected so the listener can
//     never be exposed beyond the local machine.
//   - The listener uses a fresh http.ServeMux populated only with
//     the pprof handlers, so it cannot accidentally pick up other
//     init-registered handlers from the surrounding process.
//   - No goroutine is spawned when the env var is unset.

package main

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"
)

const pprofEnvVar = "VEIL_PPROF_ADDR"

func init() {
	addr := strings.TrimSpace(os.Getenv(pprofEnvVar))
	if addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		slog.Warn("pprof: VEIL_PPROF_ADDR ignored; expected host:port", "value", addr, "err", err)
		return
	}
	if !isLoopbackHost(host) {
		slog.Warn("pprof: VEIL_PPROF_ADDR rejected; only loopback binds allowed", "host", host)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("pprof: listening (loopback only)", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("pprof: server exited", "err", err)
		}
	}()
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
