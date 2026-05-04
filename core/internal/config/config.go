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

// ServerConfig describes a server deployment.
type ServerConfig struct {
	// Listen is the host:port the QUIC transport binds to.
	Listen string `yaml:"listen"`

	// StaticKeyPath is the filesystem path holding the server's
	// long-term Noise XK static keypair. The file is created on
	// first run if absent.
	StaticKeyPath string `yaml:"static_key_path"`

	// AuthorizedKeysPath is the filesystem path to a list of
	// client static public keys (one base64 per line) permitted
	// to handshake with this server. In v0 this is the user
	// management mechanism; later it is replaced by the embedded
	// SQLite user store.
	AuthorizedKeysPath string `yaml:"authorized_keys_path"`
}

// ClientConfig describes a client connecting to a server.
type ClientConfig struct {
	// ServerAddr is the host:port of the target server.
	ServerAddr string `yaml:"server_addr"`

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
}

// Validate returns an error if the server configuration is incomplete.
func (c *ServerConfig) Validate() error {
	if c.Listen == "" {
		return errors.New("server.listen is required")
	}
	if c.StaticKeyPath == "" {
		return errors.New("server.static_key_path is required")
	}
	if c.AuthorizedKeysPath == "" {
		return errors.New("server.authorized_keys_path is required")
	}
	return nil
}

// Validate returns an error if the client configuration is incomplete.
func (c *ClientConfig) Validate() error {
	if c.ServerAddr == "" {
		return errors.New("client.server_addr is required")
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
