// Package decoy generates cover-traffic HTTPS requests to popular
// domains drawn from the SNI pool, blending the Veil client's
// outbound flow into a plausible browsing pattern.
//
// This is the simplest layer of the Veil mimicry stack: real GETs
// to real popular hosts. Higher-fidelity layers (timing/size
// distribution mimicry, decoy responses on the server side) will
// land in subsequent revisions.
package decoy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/redstone-md/veil/core/internal/dpi/snipool"
	"github.com/redstone-md/veil/core/internal/dpi/utlsdial"
)

// Defaults tuned to be cheap (~1 req/min/worker on average) so
// users do not unintentionally generate large bandwidth.
const (
	DefaultConcurrency = 2
	DefaultIntervalMS  = 60_000
	httpRequestTimeout = 15 * time.Second
)

// Config configures the decoy engine.
type Config struct {
	// Region narrows the SNI pool to a regional subset. Empty or
	// "global" uses the entire pool.
	Region snipool.Region

	// UserKey opts the engine into a per-user pool shard so two
	// Veil clients on the same network do not produce identical
	// decoy patterns. Empty means "use the full filtered pool".
	UserKey string
	// ShardSize caps the per-user shard size. Ignored when UserKey
	// is empty.
	ShardSize int

	// Concurrency caps the number of concurrent workers. <=0 falls
	// back to DefaultConcurrency.
	Concurrency int

	// IntervalMS is the mean inter-request interval. The actual
	// wait is jittered uniformly in [0.5x, 1.5x] of this value.
	IntervalMS int

	// Fingerprint selects the uTLS browser preset for outbound
	// HTTPS handshakes. Empty defaults to Chrome.
	Fingerprint utlsdial.Fingerprint
}

// Engine runs the cover-traffic generator.
type Engine struct {
	cfg     Config
	pool    *snipool.Pool
	logger  *slog.Logger
	picks   []snipool.Entry
	httpDo  func(ctx context.Context, method, url string) (*http.Response, error)
	stopped chan struct{}
}

// New constructs an Engine that draws from the given pool.
func New(pool *snipool.Pool, cfg Config, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultConcurrency
	}
	if cfg.IntervalMS <= 0 {
		cfg.IntervalMS = DefaultIntervalMS
	}

	e := &Engine{
		cfg:     cfg,
		pool:    pool,
		logger:  logger,
		stopped: make(chan struct{}),
	}
	if cfg.UserKey != "" && cfg.ShardSize > 0 {
		e.picks = pool.Shard(cfg.Region, cfg.UserKey, cfg.ShardSize)
	} else {
		e.picks = pool.Filter(cfg.Region)
	}
	e.httpDo = e.defaultHTTPDo
	return e
}

// Run blocks until ctx is cancelled, spinning Concurrency goroutines
// that each fire periodic GETs against random pool entries.
func (e *Engine) Run(ctx context.Context) error {
	if len(e.picks) == 0 {
		return errors.New("decoy: empty pool after filter")
	}

	e.logger.Info("decoy started",
		"region", e.cfg.Region,
		"pool_size", len(e.picks),
		"concurrency", e.cfg.Concurrency,
		"interval_ms", e.cfg.IntervalMS,
		"fingerprint", e.cfg.Fingerprint,
	)

	var wg sync.WaitGroup
	wg.Add(e.cfg.Concurrency)
	for i := 0; i < e.cfg.Concurrency; i++ {
		go e.worker(ctx, i, &wg)
	}
	wg.Wait()
	close(e.stopped)
	return nil
}

// Stopped returns a channel that closes once Run has fully exited.
func (e *Engine) Stopped() <-chan struct{} { return e.stopped }

func (e *Engine) worker(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()
	src := rand.New(rand.NewSource(time.Now().UnixNano() ^ int64(uint64(id)*0x9E3779B97F4A7C15)))

	for {
		// First sleep some jittered fraction of the interval so
		// workers do not all fire at startup.
		wait := jitterMS(src, e.cfg.IntervalMS)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(wait) * time.Millisecond):
		}

		entry := e.picks[src.Intn(len(e.picks))]
		path := pickPath(src)
		url := "https://" + entry.Domain + path
		reqCtx, cancel := context.WithTimeout(ctx, httpRequestTimeout)
		resp, err := e.httpDo(reqCtx, http.MethodGet, url)
		cancel()
		if err != nil {
			e.logger.Debug("decoy request failed",
				"url", url, "err", err.Error())
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		e.logger.Debug("decoy request ok",
			"url", url, "status", resp.StatusCode)
	}
}

// jitterMS returns a wait in [0.5x, 1.5x] of mean.
func jitterMS(src *rand.Rand, meanMS int) int {
	if meanMS <= 0 {
		return 1000
	}
	min := meanMS / 2
	width := meanMS // gives range [meanMS/2, meanMS/2 + meanMS] == [0.5x, 1.5x]
	return min + src.Intn(width+1)
}

// pickPath returns one of a small set of plausible browser-like
// request paths. Real browsing has more variety; this is a rough
// approximation that beats hammering "/".
func pickPath(src *rand.Rand) string {
	candidates := []string{
		"/",
		"/favicon.ico",
		"/robots.txt",
		"/sitemap.xml",
		"/manifest.json",
		"/.well-known/security.txt",
	}
	return candidates[src.Intn(len(candidates))]
}

func (e *Engine) defaultHTTPDo(ctx context.Context, method, url string) (*http.Response, error) {
	fp := e.cfg.Fingerprint
	if fp == "" {
		fp = utlsdial.FingerprintChromeAuto
	}
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			return utlsdial.Dial(ctx, network, addr, utlsdial.Options{
				Fingerprint: fp,
				SNI:         host,
				NextProtos:  []string{"http/1.1"},
				// Real cover-traffic must validate TLS certs to
				// be plausible: a real browser would. We tolerate
				// failures gracefully (logged at debug, skipped).
				InsecureSkipVerify: false,
			})
		},
		ResponseHeaderTimeout: httpRequestTimeout,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{},
	}
	client := &http.Client{Transport: transport, Timeout: httpRequestTimeout}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("decoy build req: %w", err)
	}
	req.Header.Set("User-Agent", browserUA(fp))
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	return client.Do(req)
}

func browserUA(fp utlsdial.Fingerprint) string {
	switch fp {
	case utlsdial.FingerprintFirefoxAuto:
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0"
	case utlsdial.FingerprintSafariAuto, utlsdial.FingerprintIOSAuto:
		return "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15"
	case utlsdial.FingerprintEdgeAuto:
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0"
	default:
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	}
}
