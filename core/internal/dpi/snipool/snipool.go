// Package snipool maintains a list of high-popularity domains that
// Veil uses as cover-traffic targets and (in a future Reality
// release) as transport SNI choices.
//
// The default pool is a small, hand-curated starter set covering
// the regions Veil targets in v1. A larger pool sourced from
// signed Tranco snapshots will land later; the Pool type already
// supports merging in arbitrary external entries via Replace.
package snipool

import (
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// Region identifies a geopolitical bucket. The values are lowercase
// ISO-style strings; "global" is the catch-all.
type Region string

// Recognised regions. Other values are accepted but will not match
// any embedded entries.
const (
	RegionGlobal Region = "global"
	RegionRU     Region = "ru"
	RegionCN     Region = "cn"
	RegionIR     Region = "ir"
	RegionEU     Region = "eu"
	RegionUS     Region = "us"
)

// Entry is one domain in the pool together with its provenance and
// popularity weight. Higher weight ⇒ more likely to be picked.
type Entry struct {
	Domain string
	Region Region
	// Weight follows a Zipf-like distribution: top entries get the
	// most pulls, the long tail is sampled rarely. Pre-normalisation
	// values in [1, 1000] are typical.
	Weight float64
}

// Pool is the in-memory SNI store. Safe for concurrent reads after
// construction; writes (Replace) take an internal lock.
type Pool struct {
	mu      sync.RWMutex
	entries []Entry
	// Cached cumulative-weight tables, one per filter signature, so
	// repeated Pick calls do not re-sort.
	cacheMu sync.Mutex
	cache   map[string]*cumTable
}

type cumTable struct {
	domains []string
	cumWts  []float64
	total   float64
}

// New returns a Pool seeded with the embedded starter list.
func New() *Pool {
	p := &Pool{cache: make(map[string]*cumTable)}
	p.entries = append(p.entries, defaultEntries()...)
	return p
}

// Len reports how many entries the pool currently holds.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

// Replace atomically swaps the pool contents. Useful for loading a
// fresh Tranco snapshot at runtime.
func (p *Pool) Replace(entries []Entry) {
	p.mu.Lock()
	p.entries = append(p.entries[:0], entries...)
	p.mu.Unlock()
	p.cacheMu.Lock()
	p.cache = make(map[string]*cumTable)
	p.cacheMu.Unlock()
}

// Filter returns the subset of entries whose Region matches region.
// region == RegionGlobal returns the full pool.
func (p *Pool) Filter(region Region) []Entry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if region == "" || region == RegionGlobal {
		out := make([]Entry, len(p.entries))
		copy(out, p.entries)
		return out
	}
	var out []Entry
	for _, e := range p.entries {
		if e.Region == region || e.Region == RegionGlobal {
			out = append(out, e)
		}
	}
	return out
}

// Pick draws one entry from the region-filtered pool using its
// weight distribution. The seed parameter pins the random source so
// the same call returns the same answer; pass 0 for nondeterministic
// selection.
//
// Pick returns the empty string when the filtered pool is empty.
func (p *Pool) Pick(region Region, seed int64) string {
	tbl := p.tableFor(region)
	if tbl == nil || tbl.total == 0 {
		return ""
	}
	src := rand.New(rand.NewSource(rngSeed(seed)))
	v := src.Float64() * tbl.total
	idx := sort.SearchFloat64s(tbl.cumWts, v)
	if idx >= len(tbl.domains) {
		idx = len(tbl.domains) - 1
	}
	return tbl.domains[idx]
}

// Shard returns a deterministic subset of the region-filtered pool
// chosen from a hash of userKey. The same userKey always yields the
// same shard; different userKey values get different shards. This
// is the per-user mixing that prevents ML correlation across users
// of the same Veil deployment.
//
// size is clamped to the available filtered count; pass <=0 to get
// the entire filtered set.
func (p *Pool) Shard(region Region, userKey string, size int) []Entry {
	all := p.Filter(region)
	if size <= 0 || size >= len(all) {
		return all
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(userKey))
	src := rand.New(rand.NewSource(int64(h.Sum64()) | 1))
	src.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	return all[:size]
}

func (p *Pool) tableFor(region Region) *cumTable {
	key := strings.ToLower(string(region))
	p.cacheMu.Lock()
	if tbl, ok := p.cache[key]; ok {
		p.cacheMu.Unlock()
		return tbl
	}
	p.cacheMu.Unlock()

	filtered := p.Filter(region)
	if len(filtered) == 0 {
		return nil
	}
	tbl := &cumTable{
		domains: make([]string, 0, len(filtered)),
		cumWts:  make([]float64, 0, len(filtered)),
	}
	for _, e := range filtered {
		w := e.Weight
		if w <= 0 {
			w = 1
		}
		tbl.total += w
		tbl.domains = append(tbl.domains, e.Domain)
		tbl.cumWts = append(tbl.cumWts, tbl.total)
	}
	p.cacheMu.Lock()
	p.cache[key] = tbl
	p.cacheMu.Unlock()
	return tbl
}

func rngSeed(seed int64) int64 {
	if seed != 0 {
		return seed
	}
	return time.Now().UnixNano()
}

// zipfWeight returns a Zipf-like weight for rank r (1-indexed). Used
// by defaultEntries to match a realistic popularity distribution.
func zipfWeight(rank int) float64 {
	if rank < 1 {
		rank = 1
	}
	return math.Pow(float64(rank), -0.9) * 1000
}
