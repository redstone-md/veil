// Package config defines the configuration shape for the veil binary
// and provides parsing helpers for both server and client modes.
//
// The schema is intentionally small in the pre-alpha; it will grow
// alongside the protocol implementation.
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// TransportType names a wire-level transport adapter. Values match
// the strings used in YAML configuration.
type TransportType string

const (
	TransportQUIC    TransportType = "quic"
	TransportWSS     TransportType = "wss"
	TransportReality TransportType = "reality" // not yet implemented
)

// ServerTransport configures one wire-level listener on the server.
type ServerTransport struct {
	// Type is the transport adapter to bind ("quic", "wss", ...).
	Type TransportType `yaml:"type"`

	// Listen is the host:port the adapter binds to.
	Listen string `yaml:"listen"`

	// CertFile and KeyFile are the TLS certificate and private key
	// for transports that terminate TLS (WSS, Reality fallback). When
	// empty for a TLS-terminating transport, an in-memory self-signed
	// certificate is generated at startup; this is appropriate for
	// testing but not for production.
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`

	// Path is the HTTP path the WSS adapter accepts upgrades on.
	// Defaults to "/" when empty.
	Path string `yaml:"path"`

	// TargetSNI is the host name the Reality adapter impersonates.
	// Required for type=reality. Both peers MUST agree.
	TargetSNI string `yaml:"target_sni"`

	// TargetAddr is the host:port Reality splices unauthorised
	// (probe) traffic to. Defaults to "<TargetSNI>:443" when empty.
	TargetAddr string `yaml:"target_addr"`
}

// ServerConfig describes a server deployment.
type ServerConfig struct {
	// Transports lists every wire-level adapter the server should
	// bind. A typical Phase 2 deployment exposes both QUIC and WSS
	// so clients can fall back if UDP is filtered.
	Transports []ServerTransport `yaml:"transports"`

	// StaticKeyPath is the filesystem path holding the server's
	// long-term Noise XK static keypair. The file is created on
	// first run if absent.
	StaticKeyPath string `yaml:"static_key_path"`

	// AuthorizedKeysPath is the legacy authentication source: a
	// flat file with one base64-encoded client public key per line.
	// New deployments should use UserDBPath instead; this field
	// remains supported as a fallback for existing setups and as
	// the simplest possible single-file deployment.
	AuthorizedKeysPath string `yaml:"authorized_keys_path"`

	// UserDBPath is the SQLite-backed user store. When set, it
	// supersedes AuthorizedKeysPath: clients are authenticated
	// against the database, quotas are enforced, and the admin
	// HTTP API operates on the same store.
	UserDBPath string `yaml:"user_db_path"`
}

// ClientServer describes one reachable Veil server endpoint a client
// may connect through. A client config can list several; the dialer
// tries them in order with fall-back semantics.
type ClientServer struct {
	// Type is the transport adapter to use ("quic", "wss", ...).
	Type TransportType `yaml:"type"`

	// Addr is the network address of the endpoint (host:port).
	Addr string `yaml:"addr"`

	// SNI overrides the TLS Server Name Indication for TLS-based
	// transports. When empty, the dialer uses Addr's host part. A
	// value other than the real server hostname is meaningful only
	// for Reality / CDN-fronted deployments where the certificate
	// chain need not match.
	SNI string `yaml:"sni"`

	// Insecure disables the client's TLS certificate verification
	// for this endpoint. Defaults to true while we do not yet have
	// a Reality-style identity-pinning story; the Noise XK layer is
	// the real authentication anchor in either case.
	Insecure *bool `yaml:"insecure"`

	// Path is the URL path to upgrade against (WSS only). Must
	// match the server's configured path. Defaults to "/".
	Path string `yaml:"path"`

	// Fingerprint selects a uTLS browser ClientHello preset for
	// TCP-based transports (WSS, Reality). Recognised values:
	//   "" or "chrome"   – HelloChrome_Auto (default)
	//   "firefox"        – HelloFirefox_Auto
	//   "safari"         – HelloSafari_Auto
	//   "ios"            – HelloIOS_Auto
	//   "android11"      – HelloAndroid_11_OkHttp
	//   "edge"           – HelloEdge_Auto
	//   "random"         – HelloRandomizedALPN
	//   "off"            – disable uTLS, use stdlib crypto/tls
	Fingerprint string `yaml:"fingerprint"`
}

// InsecureSkipVerify reports whether TLS certificate verification is
// disabled. Returns true when Insecure is unset, matching the
// pre-alpha default.
func (s *ClientServer) InsecureSkipVerify() bool {
	if s.Insecure == nil {
		return true
	}
	return *s.Insecure
}

// ClientConfig describes a client connecting to one or more servers.
type ClientConfig struct {
	// Servers lists every reachable server endpoint, in preference
	// order. The first endpoint that completes a handshake wins.
	Servers []ClientServer `yaml:"servers"`

	// ServerStaticKeyB64 is the server's known long-term Noise XK
	// public key, base64-encoded. This is the authentication anchor;
	// changing it changes the server's identity.
	ServerStaticKeyB64 string `yaml:"server_static_key_b64"`

	// StaticKeyPath is the path to the client's own Noise XK static
	// keypair. Created on first run if absent.
	StaticKeyPath string `yaml:"static_key_path"`

	// SOCKS5Listen is the host:port the local SOCKS5 proxy binds to.
	// Defaults to "127.0.0.1:1080" when empty.
	SOCKS5Listen string `yaml:"socks5_listen"`

	// Decoy controls the optional cover-traffic generator.
	Decoy DecoyConfig `yaml:"decoy"`
}

// DecoyConfig configures the cover-traffic generator. Defaults are
// chosen to be cheap and safe; users opt in to higher intensity.
type DecoyConfig struct {
	// Enabled turns the engine on or off. Disabled by default for
	// the pre-alpha.
	Enabled bool `yaml:"enabled"`

	// Region narrows the SNI pool. Empty means "global".
	Region string `yaml:"region"`

	// Concurrency caps how many simultaneous decoy requests run.
	Concurrency int `yaml:"concurrency"`

	// IntervalMS is the mean interval (milliseconds) between
	// decoy requests. The actual interval is randomised around
	// this value to avoid a periodic signal.
	IntervalMS int `yaml:"interval_ms"`

	// ShardSize, when >0, restricts this client to that many SNI
	// pool entries selected deterministically from the local key
	// fingerprint. Smaller shards make per-user patterns more
	// distinct.
	ShardSize int `yaml:"shard_size"`

	// Fingerprint selects the uTLS browser preset for cover
	// requests. Empty defaults to "chrome".
	Fingerprint string `yaml:"fingerprint"`
}

// Validate returns an error if the server configuration is incomplete.
func (c *ServerConfig) Validate() error {
	if len(c.Transports) == 0 {
		return errors.New("server.transports must not be empty")
	}
	for i, t := range c.Transports {
		if t.Type == "" {
			return fmt.Errorf("server.transports[%d].type is required", i)
		}
		if t.Listen == "" {
			return fmt.Errorf("server.transports[%d].listen is required", i)
		}
	}
	if c.StaticKeyPath == "" {
		return errors.New("server.static_key_path is required")
	}
	if c.AuthorizedKeysPath == "" && c.UserDBPath == "" {
		return errors.New("server.user_db_path or server.authorized_keys_path is required")
	}
	return nil
}

// Validate returns an error if the client configuration is incomplete.
func (c *ClientConfig) Validate() error {
	if len(c.Servers) == 0 {
		return errors.New("client.servers must not be empty")
	}
	for i, s := range c.Servers {
		if s.Type == "" {
			return fmt.Errorf("client.servers[%d].type is required", i)
		}
		if s.Addr == "" {
			return fmt.Errorf("client.servers[%d].addr is required", i)
		}
	}
	if c.ServerStaticKeyB64 == "" {
		return errors.New("client.server_static_key_b64 is required")
	}
	if c.StaticKeyPath == "" {
		return errors.New("client.static_key_path is required")
	}
	return nil
}

// LoadServer reads and validates a server configuration from path.
func LoadServer(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadClient reads and validates a client configuration from path.
func LoadClient(path string) (*ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ClientConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
