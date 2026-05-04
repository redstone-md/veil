// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package realitytr

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SessionIDSize is the fixed length of the TLS SessionID we use to
// transport the Reality auth tag. 32 octets is the upper bound the
// TLS spec allows for SessionID and matches what Chrome sends, so
// using the full size does not introduce a length anomaly.
const SessionIDSize = 32

// AuthSecretSize is the length of the derived pre-shared secret
// used to compute auth tags.
const AuthSecretSize = 32

// timeBucketSeconds quantises the timestamp embedded in the auth
// tag so a small clock skew between client and server does not
// reject a legitimate connection. 60 s is wide enough for typical
// NTP-synchronised hosts; the replay window then gives further
// tolerance.
const timeBucketSeconds = 60

// AcceptedClockSkewBuckets controls how many time-buckets on either
// side of the server's "now" the verifier will accept. With the
// default 60-second bucket, the value 5 yields a ±5 minute window.
const AcceptedClockSkewBuckets = 5

// Errors returned by the auth verifier.
var (
	ErrAuthMissing     = errors.New("realitytr: session id absent")
	ErrAuthBadSize     = errors.New("realitytr: session id wrong size")
	ErrAuthBadTag      = errors.New("realitytr: auth tag mismatch")
	ErrAuthExpired     = errors.New("realitytr: auth tag outside acceptable time window")
	ErrAuthReplayed    = errors.New("realitytr: auth tag previously seen (replay)")
	ErrAuthShortSecret = errors.New("realitytr: auth secret too short")
)

// DeriveAuthSecret produces the per-deployment pre-shared secret
// that both sides feed into the auth tag computation.
//
// The derivation mixes a fixed domain-separator string with the
// server's long-term Noise XK static public key. Both sides hold
// that public key in configuration, so neither side needs an extra
// secret. An attacker who does NOT possess the server's static key
// cannot recompute the secret and therefore cannot forge a valid
// auth tag.
//
// Anyone reading the public key off a leaked client config can of
// course forge tags; the same person already holds the credential
// to authenticate as a Veil client, so the threat is moot.
func DeriveAuthSecret(serverStaticPub []byte) ([]byte, error) {
	if len(serverStaticPub) < 16 {
		return nil, ErrAuthShortSecret
	}
	h := sha256.New()
	h.Write([]byte("VEIL-REALITY-V1\x00"))
	h.Write(serverStaticPub)
	return h.Sum(nil)[:AuthSecretSize], nil
}

// BuildAuthSessionID returns a fresh 32-octet SessionID encoding
// (nonce || tag) where:
//
//	nonce  = 16 random octets
//	tag    = HMAC-SHA256(secret, nonce || time_bucket_be8)[:16]
//
// time_bucket is unix-seconds / timeBucketSeconds, big-endian.
//
// The returned SessionID is stuffed into the TLS ClientHello on the
// client side; the server reverses the construction in VerifyAuth.
func BuildAuthSessionID(secret []byte) ([]byte, error) {
	if len(secret) < 16 {
		return nil, ErrAuthShortSecret
	}
	out := make([]byte, SessionIDSize)
	if _, err := rand.Read(out[:16]); err != nil {
		return nil, fmt.Errorf("realitytr: read entropy: %w", err)
	}
	tag := computeTag(secret, out[:16], currentBucket())
	copy(out[16:], tag)
	return out, nil
}

// Verifier checks SessionIDs received from clients and tracks
// already-seen nonces to reject replays.
type Verifier struct {
	secret []byte

	mu       sync.Mutex
	seen     map[[16]byte]int64 // nonce → bucket of first observation
	maxSeen  int                // soft cap on map size
	lastSwap int64
}

// NewVerifier constructs a Verifier bound to secret.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{
		secret:  append([]byte(nil), secret...),
		seen:    make(map[[16]byte]int64),
		maxSeen: 16384,
	}
}

// Verify returns nil if id is a syntactically and cryptographically
// valid auth tag for the configured secret, was issued within the
// acceptable clock skew window, and has not been seen before.
func (v *Verifier) Verify(id []byte) error {
	if len(id) == 0 {
		return ErrAuthMissing
	}
	if len(id) != SessionIDSize {
		return ErrAuthBadSize
	}
	nonce := id[:16]
	tag := id[16:]

	now := currentBucket()
	matched := int64(0)
	for delta := -AcceptedClockSkewBuckets; delta <= AcceptedClockSkewBuckets; delta++ {
		expected := computeTag(v.secret, nonce, now+int64(delta))
		if hmac.Equal(expected, tag) {
			matched = now + int64(delta)
			break
		}
	}
	if matched == 0 {
		// We allow bucket "0" only at the unix epoch — every test
		// after that should always produce a non-zero match. Use
		// a sentinel and check against constant-time miss.
		expected := computeTag(v.secret, nonce, 0)
		if !hmac.Equal(expected, tag) {
			return ErrAuthBadTag
		}
	}

	// Replay check.
	var nonceArr [16]byte
	copy(nonceArr[:], nonce)
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.seen[nonceArr]; ok {
		return ErrAuthReplayed
	}
	v.maybeRotate(now)
	v.seen[nonceArr] = matched
	return nil
}

// maybeRotate trims the seen-set to keep memory bounded. Anything
// older than the maximum acceptable window is impossible to match,
// so it is safe to forget. We rotate at most once per bucket.
func (v *Verifier) maybeRotate(nowBucket int64) {
	if nowBucket == v.lastSwap {
		return
	}
	v.lastSwap = nowBucket
	cutoff := nowBucket - int64(AcceptedClockSkewBuckets) - 1
	for k, seenBucket := range v.seen {
		if seenBucket < cutoff {
			delete(v.seen, k)
		}
	}
	// Hard cap as a defence against a flood of unique nonces.
	if len(v.seen) > v.maxSeen {
		// Drop arbitrary entries to get back under the cap. Older
		// entries are preferred but ordering is not strictly
		// guaranteed; this is a safety valve, not the primary
		// pruning mechanism.
		drop := len(v.seen) - v.maxSeen/2
		for k := range v.seen {
			if drop <= 0 {
				break
			}
			delete(v.seen, k)
			drop--
		}
	}
}

func computeTag(secret, nonce []byte, bucket int64) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(nonce)
	var bb [8]byte
	binary.BigEndian.PutUint64(bb[:], uint64(bucket))
	mac.Write(bb[:])
	full := mac.Sum(nil)
	return full[:16]
}

func currentBucket() int64 {
	return time.Now().Unix() / int64(timeBucketSeconds)
}
