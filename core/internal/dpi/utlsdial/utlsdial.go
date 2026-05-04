// Package utlsdial wraps refraction-networking/utls so the WSS
// transport can hand its TLS handshake to a library that mimics a
// real browser's ClientHello on the wire.
//
// Without this, a Veil WSS session is trivially distinguishable from
// browser HTTPS traffic by JA3/JA4 fingerprint regardless of how
// well the application-layer behaviour is masked.
package utlsdial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
)

// Fingerprint identifies one of the supported browser ClientHello
// presets. The string values match the YAML configuration field.
type Fingerprint string

// Supported fingerprints. The Auto variants follow the refraction
// project's "latest stable" track for the corresponding browser and
// are the recommended default.
const (
	FingerprintNone        Fingerprint = ""
	FingerprintChromeAuto  Fingerprint = "chrome"
	FingerprintFirefoxAuto Fingerprint = "firefox"
	FingerprintSafariAuto  Fingerprint = "safari"
	FingerprintIOSAuto     Fingerprint = "ios"
	FingerprintAndroid11   Fingerprint = "android11"
	FingerprintEdgeAuto    Fingerprint = "edge"
	FingerprintRandomized  Fingerprint = "random"
)

// dialTimeout caps the TCP + TLS handshake duration.
const dialTimeout = 20 * time.Second

// Options parameterises a uTLS dial.
type Options struct {
	// Fingerprint selects the ClientHello preset.
	Fingerprint Fingerprint

	// SNI is the server name advertised in TLS. If empty the
	// caller is expected to derive it from the dial address.
	SNI string

	// InsecureSkipVerify disables certificate validation. Required
	// while the TLS-Reality identity model is not in place.
	InsecureSkipVerify bool

	// NextProtos is the ALPN list. WSS uses {"http/1.1"}; pass nil
	// to omit the ALPN extension.
	NextProtos []string
}

// Dial opens a TCP connection to addr, performs a uTLS handshake
// using opt.Fingerprint, and returns the established connection.
func Dial(ctx context.Context, network, addr string, opt Options) (net.Conn, error) {
	if opt.SNI == "" {
		return nil, errors.New("utlsdial: SNI is required")
	}
	d := &net.Dialer{Timeout: dialTimeout}
	tcp, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("utlsdial: tcp: %w", err)
	}

	cfg := &utls.Config{
		ServerName:         opt.SNI,
		InsecureSkipVerify: opt.InsecureSkipVerify,
		NextProtos:         opt.NextProtos,
		MinVersion:         utls.VersionTLS12,
	}

	helloID, err := selectHelloID(opt.Fingerprint)
	if err != nil {
		_ = tcp.Close()
		return nil, err
	}

	uConn := utls.UClient(tcp, cfg, helloID)
	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("utlsdial: handshake: %w", err)
	}
	return uConn, nil
}

func selectHelloID(fp Fingerprint) (utls.ClientHelloID, error) {
	switch fp {
	case FingerprintNone, FingerprintChromeAuto:
		return utls.HelloChrome_Auto, nil
	case FingerprintFirefoxAuto:
		return utls.HelloFirefox_Auto, nil
	case FingerprintSafariAuto:
		return utls.HelloSafari_Auto, nil
	case FingerprintIOSAuto:
		return utls.HelloIOS_Auto, nil
	case FingerprintAndroid11:
		return utls.HelloAndroid_11_OkHttp, nil
	case FingerprintEdgeAuto:
		return utls.HelloEdge_Auto, nil
	case FingerprintRandomized:
		return utls.HelloRandomizedALPN, nil
	default:
		return utls.ClientHelloID{}, fmt.Errorf("utlsdial: unknown fingerprint %q", fp)
	}
}
