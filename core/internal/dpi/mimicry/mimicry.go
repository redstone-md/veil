// Package mimicry shapes outgoing Veil traffic so its packet-size
// distribution and inter-arrival cadence look like one of a small
// set of pre-recorded reference activities (browsing, video,
// messaging idle, …) rather than like a generic VPN tunnel.
//
// The shaping is best-effort: it adds latency and bandwidth
// overhead, both of which are tunable through profile choice. The
// "none" profile disables shaping entirely.
package mimicry

import (
	"math/rand"
	"sync"
	"time"
)

// Profile selects a reference activity to mimic.
type Profile string

// Recognised profile identifiers. The values match the YAML field.
const (
	ProfileNone      Profile = ""
	ProfileBrowse    Profile = "browse"
	ProfileVideo     Profile = "video"
	ProfileMessaging Profile = "messaging"
	ProfileSearch    Profile = "search"
)

// Shaper applies one profile to an outgoing frame stream.
//
// PadTarget(currentLen) returns the target plaintext length the
// caller should pad up to before encryption. NextDelay returns the
// delay that should elapse before the next write hits the wire.
//
// Shapers are safe for concurrent use; the internal RNG is locked.
type Shaper struct {
	profile Profile
	mu      sync.Mutex
	rng     *rand.Rand

	sizeBuckets []int
	sizeWeights []int
	totalWeight int
	iatMicrosLo int
	iatMicrosHi int
}

// New builds a Shaper for the given profile. ProfileNone returns
// nil, which downstream callers MUST handle as "shaping disabled".
func New(p Profile, seed int64) *Shaper {
	if p == ProfileNone {
		return nil
	}
	s := &Shaper{profile: p}
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	s.rng = rand.New(rand.NewSource(seed))
	s.fillProfile()
	return s
}

// PadTarget returns a target plaintext length >= currentLen drawn
// from the profile's size distribution. If currentLen exceeds the
// largest bucket, currentLen is returned unchanged (rare in practice;
// the largest bucket is set to the maximum payload).
func (s *Shaper) PadTarget(currentLen int) int {
	if s == nil {
		return currentLen
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pick := s.weightedPick()
	if pick < currentLen {
		// Find the smallest bucket >= currentLen.
		for _, b := range s.sizeBuckets {
			if b >= currentLen {
				return b
			}
		}
		return currentLen
	}
	return pick
}

// NextDelay returns a uniformly-distributed inter-arrival delay
// from the profile's [lo, hi] range. Returns zero for the disabled
// shaper.
func (s *Shaper) NextDelay() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.iatMicrosHi <= s.iatMicrosLo {
		return time.Duration(s.iatMicrosLo) * time.Microsecond
	}
	span := s.iatMicrosHi - s.iatMicrosLo
	v := s.iatMicrosLo + s.rng.Intn(span)
	return time.Duration(v) * time.Microsecond
}

// Profile returns the active profile identifier.
func (s *Shaper) Profile() Profile {
	if s == nil {
		return ProfileNone
	}
	return s.profile
}

func (s *Shaper) weightedPick() int {
	v := s.rng.Intn(s.totalWeight)
	acc := 0
	for i, w := range s.sizeWeights {
		acc += w
		if v < acc {
			return s.sizeBuckets[i]
		}
	}
	return s.sizeBuckets[len(s.sizeBuckets)-1]
}

// fillProfile sets size-bucket weights and inter-arrival range.
//
// Distributions here are hand-curated approximations rather than
// recorded captures. Recorded profiles will replace these once a
// privacy-respecting capture pipeline is in place; until then the
// shape is good enough to break trivial flow classifiers.
//
// Reading guide:
//   - sizeBuckets are payload sizes in bytes.
//   - sizeWeights index-aligned with buckets; integer weights, no
//     normalisation required.
//   - iatMicrosLo / iatMicrosHi are the inter-arrival range in
//     microseconds (uniform within the range).
func (s *Shaper) fillProfile() {
	switch s.profile {
	case ProfileBrowse:
		// HTTP browsing: a few small requests + bursty responses.
		s.sizeBuckets = []int{96, 256, 512, 1280, 4096, 8192, 14336}
		s.sizeWeights = []int{20, 30, 20, 10, 10, 8, 2}
		s.iatMicrosLo, s.iatMicrosHi = 0, 8_000

	case ProfileVideo:
		// Video streaming: medium-large packets, even cadence.
		s.sizeBuckets = []int{512, 1280, 4096, 8192, 14336, 16000}
		s.sizeWeights = []int{5, 15, 25, 30, 20, 5}
		s.iatMicrosLo, s.iatMicrosHi = 1_000, 12_000

	case ProfileMessaging:
		// Messaging idle: tiny keep-alives + small bursts.
		s.sizeBuckets = []int{64, 128, 256, 512, 1024}
		s.sizeWeights = []int{40, 25, 15, 12, 8}
		s.iatMicrosLo, s.iatMicrosHi = 5_000, 60_000

	case ProfileSearch:
		// Search-and-browse: medium requests, rapid-fire bursts.
		s.sizeBuckets = []int{96, 256, 512, 1280, 4096, 8192}
		s.sizeWeights = []int{15, 25, 25, 15, 12, 8}
		s.iatMicrosLo, s.iatMicrosHi = 200, 5_000

	default:
		// Should not happen — New filters ProfileNone before calling.
		s.sizeBuckets = []int{1280}
		s.sizeWeights = []int{1}
		s.iatMicrosLo, s.iatMicrosHi = 0, 0
	}
	for _, w := range s.sizeWeights {
		s.totalWeight += w
	}
}
