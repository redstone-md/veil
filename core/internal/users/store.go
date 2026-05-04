// Package users implements the embedded user database that backs
// Veil's authentication and quota enforcement.
//
// The store is a single SQLite file (pure-Go driver
// `modernc.org/sqlite`, so no CGO is required). Schema migrations
// run automatically on Open.
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite"
)

// Status enumerates the recognised user statuses.
type Status string

// Status values.
const (
	StatusActive        Status = "active"
	StatusRevoked       Status = "revoked"
	StatusExpired       Status = "expired"
	StatusQuotaExceeded Status = "quota_exceeded"
)

// User is one row of the users table.
type User struct {
	ID                    string
	Name                  string
	PubkeyB64             string
	CreatedAt             time.Time
	ExpiresAt             *time.Time
	QuotaBytesPerMonth    *int64
	UsedBytesCurrentMonth int64
	QuotaPeriodStart      time.Time
	LastSeen              *time.Time
	Status                Status
	Notes                 string
	Tags                  []string
}

// AdminUser is one row of the admin_users table.
type AdminUser struct {
	Username     string
	PasswordHash []byte
	CreatedAt    time.Time
}

// Store is the user database handle.
type Store struct {
	db   *sql.DB
	path string

	// Per-user mutex map for accumulator updates. SQLite serialises
	// transactions on its own but in-memory accounting needs its own
	// guard so multiple sessions don't trip over each other.
	accumMu sync.Mutex
	accum   map[string]int64
}

// Open opens (or creates) a SQLite-backed user store at path. The
// schema is migrated to the latest version automatically.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("users: open db: %w", err)
	}
	// SQLite WAL mode handles concurrent reads + a single writer
	// well and survives crashes cleanly.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("users: enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("users: enable fk: %w", err)
	}
	s := &Store{db: db, path: path, accum: make(map[string]int64)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path returns the on-disk path the store was opened from.
func (s *Store) Path() string { return s.path }

// Close closes the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS migrations (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("users: ensure migrations table: %w", err)
	}
	var current int
	row := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM migrations`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("users: read schema version: %w", err)
	}
	for i, stmt := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("users: begin migration v%d: %w", v, err)
		}
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("users: apply migration v%d: %w", v, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO migrations(version, applied_at) VALUES (?, ?)`,
			v, time.Now().Unix(),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("users: record migration v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("users: commit migration v%d: %w", v, err)
		}
	}
	return nil
}

// Errors returned by Store operations.
var (
	ErrNotFound      = errors.New("users: not found")
	ErrDuplicateName = errors.New("users: name already exists")
	ErrDuplicateKey  = errors.New("users: pubkey already exists")
)

// CreateUser inserts a new user. The ID is auto-generated.
func (s *Store) CreateUser(ctx context.Context, name, pubkeyB64 string) (*User, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO users (id, name, pubkey_b64, created_at, quota_period_start, status)
VALUES (?, ?, ?, ?, ?, 'active')`,
		id, name, pubkeyB64, now, now,
	)
	if err != nil {
		return nil, classifyInsertErr(err, name, pubkeyB64)
	}
	return s.GetUser(ctx, id)
}

// GetUser fetches a user by ID.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	row := s.db.QueryRowContext(ctx, selectUserSQL+` WHERE id = ?`, id)
	return scanUser(row)
}

// GetUserByPubkey fetches a user by base64-encoded public key.
func (s *Store) GetUserByPubkey(ctx context.Context, pubkeyB64 string) (*User, error) {
	row := s.db.QueryRowContext(ctx, selectUserSQL+` WHERE pubkey_b64 = ?`, pubkeyB64)
	return scanUser(row)
}

// GetUserByName fetches a user by name.
func (s *Store) GetUserByName(ctx context.Context, name string) (*User, error) {
	row := s.db.QueryRowContext(ctx, selectUserSQL+` WHERE name = ?`, name)
	return scanUser(row)
}

// ListUsers returns every user in arbitrary order.
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, selectUserSQL+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("users: list query: %w", err)
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetStatus updates the user's status field.
func (s *Store) SetStatus(ctx context.Context, id string, st Status) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET status = ? WHERE id = ?`, string(st), id)
	if err != nil {
		return fmt.Errorf("users: set status: %w", err)
	}
	return notFoundIfZero(res)
}

// SetQuota sets (or clears, when bytes is nil) the per-month byte quota.
func (s *Store) SetQuota(ctx context.Context, id string, bytes *int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET quota_bytes_per_month = ? WHERE id = ?`,
		nullableInt64(bytes), id)
	if err != nil {
		return fmt.Errorf("users: set quota: %w", err)
	}
	return notFoundIfZero(res)
}

// SetExpiry sets (or clears, when t is nil) the user expiry.
func (s *Store) SetExpiry(ctx context.Context, id string, t *time.Time) error {
	var v interface{}
	if t != nil {
		v = t.Unix()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET expires_at = ? WHERE id = ?`, v, id)
	if err != nil {
		return fmt.Errorf("users: set expiry: %w", err)
	}
	return notFoundIfZero(res)
}

// RegenKey atomically replaces the user's stored public key.
func (s *Store) RegenKey(ctx context.Context, id, pubkeyB64 string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET pubkey_b64 = ? WHERE id = ?`, pubkeyB64, id)
	if err != nil {
		return classifyInsertErr(err, "", pubkeyB64)
	}
	return notFoundIfZero(res)
}

// DeleteUser removes a user row entirely.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("users: delete: %w", err)
	}
	return notFoundIfZero(res)
}

// AccumulateBytes increments the running per-month usage counter
// for a user. Callers buffer values in memory and flush periodically;
// this keeps the per-byte path cheap.
func (s *Store) AccumulateBytes(id string, n int64) {
	if n <= 0 {
		return
	}
	s.accumMu.Lock()
	s.accum[id] += n
	s.accumMu.Unlock()
}

// FlushAccumulator persists the in-memory accumulator to the database
// in a single transaction. Returns the total number of bytes flushed.
func (s *Store) FlushAccumulator(ctx context.Context) (int64, error) {
	s.accumMu.Lock()
	pending := s.accum
	s.accum = make(map[string]int64, len(pending))
	s.accumMu.Unlock()
	if len(pending) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		// Restore accum so we don't lose counts.
		s.accumMu.Lock()
		for k, v := range pending {
			s.accum[k] += v
		}
		s.accumMu.Unlock()
		return 0, fmt.Errorf("users: flush begin: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
UPDATE users
SET used_bytes_current_month = used_bytes_current_month + ?,
    last_seen = ?
WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("users: flush prepare: %w", err)
	}
	defer stmt.Close()
	now := time.Now().Unix()
	var total int64
	for id, n := range pending {
		if _, err := stmt.ExecContext(ctx, n, now, id); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("users: flush update %s: %w", id, err)
		}
		total += n
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("users: flush commit: %w", err)
	}
	return total, nil
}

// ResetMonthlyQuotas zeroes used_bytes_current_month for every user
// whose quota_period_start is older than 30 days, advancing the
// period start forward.
func (s *Store) ResetMonthlyQuotas(ctx context.Context) error {
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
UPDATE users
SET used_bytes_current_month = 0,
    quota_period_start = ?
WHERE quota_period_start <= ?`, now, cutoff)
	if err != nil {
		return fmt.Errorf("users: reset quotas: %w", err)
	}
	return nil
}

// CountActive returns the number of users with status = active.
func (s *Store) CountActive(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE status = 'active'`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// --- admin user operations ---

// CreateAdminUser inserts (or replaces) an admin login.
func (s *Store) CreateAdminUser(ctx context.Context, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("users: bcrypt: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO admin_users(username, password_hash, created_at)
VALUES (?, ?, ?)
ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash`,
		username, hash, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("users: insert admin: %w", err)
	}
	return nil
}

// VerifyAdminPassword checks a username/password pair. Returns nil
// on success, ErrNotFound when the user does not exist, or
// bcrypt.ErrMismatchedHashAndPassword on bad password.
func (s *Store) VerifyAdminPassword(ctx context.Context, username, password string) error {
	var hash []byte
	row := s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM admin_users WHERE username = ?`, username)
	if err := row.Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("users: lookup admin: %w", err)
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password))
}

// CountAdmins returns how many admin logins exist.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// --- helpers ---

const selectUserSQL = `SELECT
    id, name, pubkey_b64, created_at, expires_at,
    quota_bytes_per_month, used_bytes_current_month, quota_period_start,
    last_seen, status, notes, tags
FROM users`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanUser(row *sql.Row) (*User, error) {
	u, err := scanUserRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func scanUserRow(r rowScanner) (*User, error) {
	var (
		u           User
		expires     sql.NullInt64
		quota       sql.NullInt64
		periodStart int64
		lastSeen    sql.NullInt64
		status      string
		notes       sql.NullString
		tagsRaw     sql.NullString
		created     int64
	)
	if err := r.Scan(
		&u.ID, &u.Name, &u.PubkeyB64, &created, &expires,
		&quota, &u.UsedBytesCurrentMonth, &periodStart,
		&lastSeen, &status, &notes, &tagsRaw,
	); err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(created, 0)
	u.QuotaPeriodStart = time.Unix(periodStart, 0)
	if expires.Valid {
		t := time.Unix(expires.Int64, 0)
		u.ExpiresAt = &t
	}
	if quota.Valid {
		v := quota.Int64
		u.QuotaBytesPerMonth = &v
	}
	if lastSeen.Valid {
		t := time.Unix(lastSeen.Int64, 0)
		u.LastSeen = &t
	}
	u.Status = Status(status)
	u.Notes = notes.String
	if tagsRaw.Valid && tagsRaw.String != "" {
		_ = json.Unmarshal([]byte(tagsRaw.String), &u.Tags)
	}
	return &u, nil
}

func classifyInsertErr(err error, name, pubkey string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "users.name"):
		return fmt.Errorf("%w: %s", ErrDuplicateName, name)
	case strings.Contains(msg, "users.pubkey_b64"):
		return fmt.Errorf("%w: %s", ErrDuplicateKey, pubkey)
	default:
		return fmt.Errorf("users: insert: %w", err)
	}
}

func notFoundIfZero(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("users: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullableInt64(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("users: gen id: %w", err)
	}
	// Set version 4 / variant RFC4122 bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}
