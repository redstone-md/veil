// Package auth abstracts how a server resolves an incoming Noise XK
// peer-static key into an authorisation decision plus user metadata.
//
// Two backends are provided: a flat-file authorized_keys backend
// for the simplest deployments, and a SQLite-backed store backend
// for deployments that want quotas, status flags, and the admin
// HTTP API.
package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/redstone-md/veil/core/internal/users"
)

// Result is what Verify returns on success.
type Result struct {
	// UserID is empty for the flat-file backend.
	UserID string
	// Name is a human-readable label for logging.
	Name string
}

// Authenticator decides whether a client public key may proceed.
type Authenticator interface {
	Verify(ctx context.Context, pubkeyB64 string) (*Result, error)
}

// ErrUnauthorized is returned by Verify when the key is not allowed.
var ErrUnauthorized = errors.New("auth: unauthorized")

// FileBackend authenticates against a flat file of base64 public
// keys, one per line. Lines starting with '#' and blank lines are
// ignored.
type FileBackend struct {
	path string

	mu      sync.RWMutex
	allowed map[string]struct{}
}

// LoadFile constructs a FileBackend from the file at path.
func LoadFile(path string) (*FileBackend, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read authorized keys: %w", err)
	}
	allowed := make(map[string]struct{})
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Sanity-check: reject lines that don't look like base64
		// (we use base64.StdEncoding for keypair persistence).
		if !looksBase64(line) {
			return nil, fmt.Errorf("authorized_keys line %d: not base64", i+1)
		}
		allowed[line] = struct{}{}
	}
	return &FileBackend{path: path, allowed: allowed}, nil
}

// Verify implements Authenticator.
func (f *FileBackend) Verify(_ context.Context, pubkeyB64 string) (*Result, error) {
	f.mu.RLock()
	_, ok := f.allowed[pubkeyB64]
	f.mu.RUnlock()
	if !ok {
		return nil, ErrUnauthorized
	}
	return &Result{Name: shortName(pubkeyB64)}, nil
}

// Path returns the file backing this authenticator.
func (f *FileBackend) Path() string { return f.path }

// Count returns how many keys the backend currently allows.
func (f *FileBackend) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.allowed)
}

// StoreBackend authenticates against a SQLite-backed user store.
type StoreBackend struct {
	store *users.Store
}

// NewStoreBackend wraps an opened user store.
func NewStoreBackend(s *users.Store) *StoreBackend { return &StoreBackend{store: s} }

// Verify implements Authenticator. The user must exist and have
// status == active. Expired or revoked users are rejected here so
// the rest of the stack does not need to re-check.
func (b *StoreBackend) Verify(ctx context.Context, pubkeyB64 string) (*Result, error) {
	u, err := b.store.GetUserByPubkey(ctx, pubkeyB64)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	if u.Status != users.StatusActive {
		return nil, fmt.Errorf("%w: status=%s", ErrUnauthorized, u.Status)
	}
	return &Result{UserID: u.ID, Name: u.Name}, nil
}

func shortName(pubkeyB64 string) string {
	if len(pubkeyB64) <= 12 {
		return pubkeyB64
	}
	return pubkeyB64[:12] + "…"
}

func looksBase64(s string) bool {
	// Conservative check: every character is in the base64 alphabet.
	for i := 0; i < len(s); i++ {
		c := s[i]
		isBase64 := (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '+' || c == '/' || c == '='
		if !isBase64 {
			return false
		}
	}
	return true
}
