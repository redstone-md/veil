// Package acme wraps caddyserver/certmagic so a Veil deployment can
// obtain and auto-renew Let's Encrypt certificates without operators
// having to run an external certbot.
//
// Usage from a transport adapter (e.g. wsstr):
//
//	mgr, err := acme.NewManager(acme.Config{
//	    CacheDir: "/var/lib/veil/acme",
//	    Email:    "ops@example.com",
//	    Domains:  []string{"vps.example.com"},
//	})
//	if err != nil { return err }
//	tlsCfg := mgr.TLSConfig()  // pass to tls.Listener
package acme

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/caddyserver/certmagic"
)

// Config parameterises a certificate manager.
type Config struct {
	// CacheDir is the directory certmagic writes its private keys
	// and issued certificates to. The directory is created if it
	// does not exist; permissions on the keys default to 0o600.
	CacheDir string

	// Email is the contact address registered with the ACME CA.
	// Required by Let's Encrypt for expiry-warning emails.
	Email string

	// Domains is the list of host names to provision certificates
	// for. The ACME challenge is HTTP-01 by default; the manager
	// binds :80 transparently to serve ACME challenges.
	Domains []string

	// Staging routes the ACME requests to Let's Encrypt's staging
	// environment instead of production. Use during development to
	// avoid burning the production rate limit; certs from the
	// staging CA are NOT trusted by browsers.
	Staging bool

	// Logger receives operational events from certmagic.
	Logger *slog.Logger
}

// Manager owns a certmagic.Config for a set of domains.
type Manager struct {
	cfg     Config
	magic   *certmagic.Config
	domains []string
}

// NewManager builds a Manager and synchronously requests certificates
// for every domain in cfg. The first call may take ~10 seconds while
// the ACME challenge round-trips with the CA.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.CacheDir == "" {
		return nil, errors.New("acme: CacheDir is required")
	}
	if cfg.Email == "" {
		return nil, errors.New("acme: Email is required")
	}
	if len(cfg.Domains) == 0 {
		return nil, errors.New("acme: at least one Domain is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	storage := &certmagic.FileStorage{Path: filepath.Clean(cfg.CacheDir)}

	// certmagic wants a chicken-and-egg dance because every Cache
	// entry can in principle have a different Config. We use one
	// Config for everything, so we hand it back from the
	// GetConfigForCert closure.
	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(_ certmagic.Certificate) (*certmagic.Config, error) {
			return magic, nil
		},
	})
	magic = certmagic.New(cache, certmagic.Config{
		Storage: storage,
	})
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		Email:  cfg.Email,
		Agreed: true,
		CA: func() string {
			if cfg.Staging {
				return certmagic.LetsEncryptStagingCA
			}
			return certmagic.LetsEncryptProductionCA
		}(),
	})
	magic.Issuers = []certmagic.Issuer{issuer}

	cfg.Logger.Info("acme manager initialising",
		"cache_dir", cfg.CacheDir,
		"email", cfg.Email,
		"domains", cfg.Domains,
		"staging", cfg.Staging,
	)
	if err := magic.ManageSync(context.Background(), cfg.Domains); err != nil {
		return nil, fmt.Errorf("acme: manage: %w", err)
	}
	return &Manager{
		cfg:     cfg,
		magic:   magic,
		domains: cfg.Domains,
	}, nil
}

// TLSConfig returns a TLS configuration that performs SNI-based
// certificate selection from the managed cache. The returned
// *tls.Config is suitable for tls.Listener and HTTP/3 server use.
func (m *Manager) TLSConfig() *tls.Config {
	return m.magic.TLSConfig()
}

// Domains returns the domain list this manager handles, primarily
// for logging.
func (m *Manager) Domains() []string {
	out := make([]string, len(m.domains))
	copy(out, m.domains)
	return out
}
