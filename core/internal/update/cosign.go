// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package update

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// CosignVerifier checks that a downloaded blob was signed via the
// Sigstore (cosign keyless) flow, by an identity that matches the
// expected GitHub Actions release workflow.
//
// Wire shape: the GitHub release MUST publish a Sigstore bundle
// alongside the binary, named `<asset>.sigstore` (the cosign default
// output of `cosign sign-blob ... --bundle`). The verifier loads
// the bundle, fetches Sigstore's trusted root via TUF (cached on
// disk after the first call), and asserts:
//
//   - the bundle's cryptographic chain validates,
//   - the certificate's SAN matches Subject (e.g. the release
//     workflow's GitHub URI),
//   - the OIDC issuer matches Issuer (typically GitHub Actions),
//   - the supplied artifact bytes match the bundle's signature.
//
// If any check fails the binary is NOT installed.
type CosignVerifier struct {
	// BundleJSON is the raw bytes of the `*.sigstore` file
	// produced by cosign sign-blob --bundle.
	BundleJSON []byte

	// Subject is the expected SAN URI inside the leaf certificate.
	// For a GitHub Actions release workflow that path is
	// `https://github.com/<owner>/<repo>/.github/workflows/<file>@refs/tags/<tag>`.
	Subject string

	// Issuer is the expected OIDC issuer; for GitHub Actions this
	// is `https://token.actions.githubusercontent.com`.
	Issuer string

	// TrustedRootPath, when non-empty, loads a TrustedRoot JSON
	// file from disk instead of fetching one over the network.
	// Useful for air-gapped operators and for tests.
	TrustedRootPath string
}

// Verify satisfies the Verifier interface.
func (v CosignVerifier) Verify(blob []byte) error {
	if len(v.BundleJSON) == 0 {
		return errors.New("update/cosign: BundleJSON is empty")
	}
	if v.Subject == "" || v.Issuer == "" {
		return errors.New("update/cosign: Subject and Issuer are required")
	}

	b, err := bundle.NewBundle(nil)
	if err != nil {
		return fmt.Errorf("update/cosign: bundle scaffold: %w", err)
	}
	if err := b.UnmarshalJSON(v.BundleJSON); err != nil {
		return fmt.Errorf("update/cosign: bundle parse: %w", err)
	}

	trusted, err := v.loadTrustedRoot()
	if err != nil {
		return err
	}

	verifier, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("update/cosign: build verifier: %w", err)
	}

	identity, err := verify.NewShortCertificateIdentity(v.Issuer, "", v.Subject, "")
	if err != nil {
		return fmt.Errorf("update/cosign: identity: %w", err)
	}

	policy := verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(blob)),
		verify.WithCertificateIdentity(identity),
	)

	if _, err := verifier.Verify(b, policy); err != nil {
		return fmt.Errorf("update/cosign: verify: %w", err)
	}
	return nil
}

func (v CosignVerifier) loadTrustedRoot() (root.TrustedMaterial, error) {
	if v.TrustedRootPath != "" {
		raw, err := os.ReadFile(v.TrustedRootPath)
		if err != nil {
			return nil, fmt.Errorf("update/cosign: read trusted root: %w", err)
		}
		tr, err := root.NewTrustedRootFromJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("update/cosign: parse trusted root: %w", err)
		}
		return tr, nil
	}
	// Live TUF fetch from the Sigstore public-good instance.
	// First call writes a cache to the user's local TUF dir;
	// subsequent calls are offline-fast.
	live, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("update/cosign: live trusted root: %w", err)
	}
	return live, nil
}
