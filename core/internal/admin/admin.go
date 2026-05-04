// Package admin implements the embedded HTTP admin endpoint for a
// Veil server: a small REST surface over the user store, plus a
// minimal HTML dashboard. Authentication is HTTP Basic backed by
// the admin_users table in the same SQLite database.
//
// The admin server SHOULD be bound to 127.0.0.1 in production and
// reached via SSH local-forwarding. Binding to a public interface
// emits a loud warning at startup; nothing else stops you, but the
// log says it.
package admin

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redstone-md/veil/core/internal/buildinfo"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/users"
)

//go:embed assets/index.html
var indexHTML []byte

// Server is the embedded admin HTTP service.
type Server struct {
	store  *users.Store
	logger *slog.Logger
	addr   string
	srv    *http.Server
}

// Config parameterises a Server.
type Config struct {
	// Addr is the host:port to bind. Recommended: "127.0.0.1:8443".
	Addr string

	// Store is the opened user database that the API operates on.
	Store *users.Store

	// Logger receives operational events.
	Logger *slog.Logger
}

// New constructs the admin server.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("admin: store is required")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8443"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	host, _, _ := net.SplitHostPort(cfg.Addr)
	if host == "0.0.0.0" || host == "::" || host == "" {
		cfg.Logger.Warn("admin endpoint bound to public interface",
			"addr", cfg.Addr,
			"hint", "bind to 127.0.0.1 and expose via SSH local forward")
	}

	s := &Server{
		store:  cfg.Store,
		logger: cfg.Logger,
		addr:   cfg.Addr,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/dashboard", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("/api/users", s.requireAuth(s.handleUsers))
	mux.HandleFunc("/api/users/", s.requireAuth(s.handleUserOne))
	mux.HandleFunc("/api/version", s.handleVersion)
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// Run blocks serving requests until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
	s.logger.Info("admin listening", "addr", s.addr)
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("admin: serve: %w", err)
	}
	return nil
}

// requireAuth wraps a handler with HTTP Basic auth backed by the
// admin_users table.
func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			s.respondAuth(w)
			return
		}
		if err := s.store.VerifyAdminPassword(r.Context(), user, pass); err != nil {
			// constant-time-ish: same delay regardless of which
			// failure happened, mostly to discourage username
			// enumeration via timing.
			time.Sleep(100 * time.Millisecond)
			_ = subtle.ConstantTimeCompare([]byte(user), []byte("x"))
			s.respondAuth(w)
			return
		}
		h(w, r)
	}
}

func (s *Server) respondAuth(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Veil Admin"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": buildinfo.Version,
		"commit":  buildinfo.Commit,
		"date":    buildinfo.Date,
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.ListUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats := struct {
		Users     int   `json:"users_total"`
		Active    int   `json:"users_active"`
		BytesUsed int64 `json:"bytes_used_current_month"`
		Quotas    int   `json:"users_with_quota"`
		QuotaUsed int   `json:"users_at_quota"`
	}{}
	for _, u := range all {
		stats.Users++
		if u.Status == users.StatusActive {
			stats.Active++
		}
		stats.BytesUsed += u.UsedBytesCurrentMonth
		if u.QuotaBytesPerMonth != nil {
			stats.Quotas++
			if u.UsedBytesCurrentMonth >= *u.QuotaBytesPerMonth {
				stats.QuotaUsed++
			}
		}
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		all, err := s.store.ListUsers(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, mapUsers(all))
	case http.MethodPost:
		var body struct {
			Name      string `json:"name"`
			PubkeyB64 string `json:"pubkey_b64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		// When the caller does not bring its own keypair, generate
		// one server-side and return the private half once in the
		// response. Mirrors the CLI's `veil user add` (without
		// --pubkey) so the GUI / installer can ship a one-shot
		// share-link without forcing the operator to pre-generate
		// keys on the client.
		var generatedPriv []byte
		if body.PubkeyB64 == "" {
			kp, gerr := crypto.GenerateKeypair()
			if gerr != nil {
				http.Error(w, "key generation failed: "+gerr.Error(), http.StatusInternalServerError)
				return
			}
			body.PubkeyB64 = base64.StdEncoding.EncodeToString(kp.Public)
			generatedPriv = kp.Private
		}
		u, err := s.store.CreateUser(r.Context(), body.Name, body.PubkeyB64)
		if err != nil {
			if errors.Is(err, users.ErrDuplicateName) || errors.Is(err, users.ErrDuplicateKey) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		view := mapUser(u)
		if generatedPriv != nil {
			// One-shot return; the server keeps no copy. This is
			// surfaced to the operator inside the admin GUI / API
			// response only — never logged or persisted.
			view.PrivkeyB64 = base64.StdEncoding.EncodeToString(generatedPriv)
		}
		writeJSON(w, http.StatusCreated, view)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleUserOne(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		u, err := s.store.GetUser(r.Context(), id)
		if err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, mapUser(u))
	case len(parts) == 1 && r.Method == http.MethodPatch:
		s.handleUserPatch(w, r, id)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := s.store.DeleteUser(r.Context(), id); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "regen" && r.Method == http.MethodPost:
		var body struct {
			PubkeyB64 string `json:"pubkey_b64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if body.PubkeyB64 == "" {
			http.Error(w, "pubkey_b64 required", http.StatusBadRequest)
			return
		}
		if err := s.store.RegenKey(r.Context(), id, body.PubkeyB64); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleUserPatch(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Status             *string `json:"status,omitempty"`
		QuotaBytesPerMonth *int64  `json:"quota_bytes_per_month,omitempty"`
		ClearQuota         bool    `json:"clear_quota,omitempty"`
		ExpiresAtUnix      *int64  `json:"expires_at,omitempty"`
		ClearExpiry        bool    `json:"clear_expiry,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Status != nil {
		if err := s.store.SetStatus(r.Context(), id, users.Status(*body.Status)); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
	}
	if body.ClearQuota {
		if err := s.store.SetQuota(r.Context(), id, nil); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
	} else if body.QuotaBytesPerMonth != nil {
		if err := s.store.SetQuota(r.Context(), id, body.QuotaBytesPerMonth); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
	}
	if body.ClearExpiry {
		if err := s.store.SetExpiry(r.Context(), id, nil); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
	} else if body.ExpiresAtUnix != nil {
		t := time.Unix(*body.ExpiresAtUnix, 0)
		if err := s.store.SetExpiry(r.Context(), id, &t); err != nil {
			s.respondNotFoundOrErr(w, err)
			return
		}
	}
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		s.respondNotFoundOrErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mapUser(u))
}

func (s *Server) respondNotFoundOrErr(w http.ResponseWriter, err error) {
	if errors.Is(err, users.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// userView is the JSON-marshalable representation of a user.
//
// PrivkeyB64 is populated only on the response from a server-generated
// keypair (POST /api/users with empty pubkey_b64) — the server keeps
// no copy after that single response. All other endpoints leave it
// blank.
type userView struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	PubkeyB64             string `json:"pubkey_b64"`
	PrivkeyB64            string `json:"privkey_b64,omitempty"`
	CreatedAt             int64  `json:"created_at"`
	ExpiresAt             *int64 `json:"expires_at,omitempty"`
	QuotaBytesPerMonth    *int64 `json:"quota_bytes_per_month,omitempty"`
	UsedBytesCurrentMonth int64  `json:"used_bytes_current_month"`
	LastSeen              *int64 `json:"last_seen,omitempty"`
	Status                string `json:"status"`
}

func mapUser(u *users.User) userView {
	v := userView{
		ID:                    u.ID,
		Name:                  u.Name,
		PubkeyB64:             u.PubkeyB64,
		CreatedAt:             u.CreatedAt.Unix(),
		Status:                string(u.Status),
		UsedBytesCurrentMonth: u.UsedBytesCurrentMonth,
	}
	if u.ExpiresAt != nil {
		ts := u.ExpiresAt.Unix()
		v.ExpiresAt = &ts
	}
	if u.QuotaBytesPerMonth != nil {
		q := *u.QuotaBytesPerMonth
		v.QuotaBytesPerMonth = &q
	}
	if u.LastSeen != nil {
		ts := u.LastSeen.Unix()
		v.LastSeen = &ts
	}
	return v
}

func mapUsers(in []*users.User) []userView {
	out := make([]userView, 0, len(in))
	for _, u := range in {
		out = append(out, mapUser(u))
	}
	return out
}

// _ keeps strconv referenced even if removed from later code paths.
var _ = strconv.Itoa
