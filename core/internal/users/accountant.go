// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package users

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Accountant maintains per-user usage and quota enforcement state for
// the lifetime of a single session. It satisfies the
// forward.Accountant interface but is defined here to avoid an
// import cycle.
type Accountant struct {
	store    *Store
	userID   string
	quotaCap int64 // 0 == unlimited
	used     int64 // accumulator within this session
	logger   *slog.Logger

	exceeded atomic.Bool

	// flushedAt records when we last hit the store-side accumulator.
	flushedAt time.Time

	// flushEvery sets the minimum delay between flush calls so a
	// chatty session doesn't hammer SQLite.
	flushEvery time.Duration
}

// NewAccountant constructs a session-scoped accountant. user must
// be the User row at session start; subsequent quota changes from
// the admin UI take effect on the next session, not within an
// existing one.
func NewAccountant(store *Store, user *User, logger *slog.Logger) *Accountant {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Accountant{
		store:      store,
		userID:     user.ID,
		used:       user.UsedBytesCurrentMonth,
		logger:     logger,
		flushedAt:  time.Now(),
		flushEvery: 30 * time.Second,
	}
	if user.QuotaBytesPerMonth != nil {
		a.quotaCap = *user.QuotaBytesPerMonth
	}
	if a.quotaCap > 0 && a.used >= a.quotaCap {
		a.exceeded.Store(true)
	}
	return a
}

// Add satisfies forward.Accountant.
func (a *Accountant) Add(direction string, n int) {
	if n <= 0 || a == nil {
		return
	}
	atomic.AddInt64(&a.used, int64(n))
	a.store.AccumulateBytes(a.userID, int64(n))
	if a.quotaCap > 0 && atomic.LoadInt64(&a.used) >= a.quotaCap {
		a.markExceeded()
	}
	if time.Since(a.flushedAt) > a.flushEvery {
		a.flushedAt = time.Now()
		go a.flushOnce()
	}
}

// QuotaExceeded satisfies forward.Accountant.
func (a *Accountant) QuotaExceeded() bool {
	if a == nil {
		return false
	}
	return a.exceeded.Load()
}

func (a *Accountant) markExceeded() {
	if a.exceeded.Swap(true) {
		return
	}
	a.logger.Info("user quota exceeded; further forwarding rejected",
		"user_id", a.userID, "used", atomic.LoadInt64(&a.used), "quota", a.quotaCap)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.store.SetStatus(ctx, a.userID, StatusQuotaExceeded); err != nil {
			a.logger.Warn("failed to mark user quota_exceeded", "err", err)
		}
	}()
}

func (a *Accountant) flushOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.store.FlushAccumulator(ctx); err != nil {
		a.logger.Warn("flush accumulator failed", "err", err)
	}
}

// FlushFinal forces a synchronous flush. Call once at session
// teardown so the last bytes are not lost.
func (a *Accountant) FlushFinal(ctx context.Context) {
	if a == nil {
		return
	}
	if _, err := a.store.FlushAccumulator(ctx); err != nil {
		a.logger.Warn("final flush accumulator failed", "err", err)
	}
}
