// Package sharelink encodes and decodes the `veil://` URI scheme
// used to distribute client configurations as a single shareable
// string (paste, QR code, email link).
//
// Wire format:
//
//	veil://<base64url-no-pad>(<json-encoded ClientConfig>)
//
// JSON is chosen over YAML because it round-trips deterministically
// and is the native format of every consuming language. The base64
// alphabet is URL-safe (RFC 4648 §5) and unpadded so the resulting
// link is robust against URL escaping.
package sharelink

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/redstone-md/veil/core/internal/config"
)

// Scheme is the URI prefix.
const Scheme = "veil://"

// Encode produces a shareable veil:// link from a ClientConfig.
func Encode(c *config.ClientConfig) (string, error) {
	if c == nil {
		return "", errors.New("sharelink: nil config")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("sharelink: marshal: %w", err)
	}
	return Scheme + base64.RawURLEncoding.EncodeToString(body), nil
}

// Decode parses a veil:// link back into a ClientConfig. The
// returned config is NOT validated; callers should run cfg.Validate
// before using it.
func Decode(link string) (*config.ClientConfig, error) {
	if !strings.HasPrefix(link, Scheme) {
		return nil, fmt.Errorf("sharelink: missing %q prefix", Scheme)
	}
	body := strings.TrimPrefix(link, Scheme)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("sharelink: base64: %w", err)
	}
	var cfg config.ClientConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("sharelink: json: %w", err)
	}
	return &cfg, nil
}
