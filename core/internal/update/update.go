// Package update implements `veil update`: a self-contained
// release-channel client that talks to the GitHub Releases API,
// downloads the asset matching the running platform, verifies its
// SHA-256 against the published `checksums.txt`, and atomically
// replaces the running binary.
//
// The integrity story today is checksums-only. Sigstore (cosign
// keyless) signature verification is the immediate next step and is
// tracked under Phase 6 (release machinery); this package defines
// the seam — Verifier — that the cosign integration will plug into.
//
// Cross-platform replace semantics:
//   - On Unix-like systems we rename the running binary aside,
//     install the new one in its place, and chmod +x. The kernel
//     keeps the running process bound to the old inode, so the
//     update takes effect on the next launch.
//   - On Windows we cannot overwrite a running .exe; the new
//     binary is staged next to the old one and a `.veil-update`
//     marker file points at it. The `veil update --apply` output
//     prints the operator-facing follow-up (rename + restart).
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultRepo is the upstream release source.
const DefaultRepo = "redstone-md/veil"

// httpTimeout caps how long any single HTTP call may run.
const httpTimeout = 60 * time.Second

// Release is the subset of the GitHub Releases API response we
// consume. JSON tags match the upstream schema.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Body        string    `json:"body"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

// Client talks to a GitHub-shaped releases API.
type Client struct {
	Repo       string
	HTTPClient *http.Client
}

// New constructs a Client targeting repo (defaults to DefaultRepo).
func New(repo string) *Client {
	if repo == "" {
		repo = DefaultRepo
	}
	return &Client{
		Repo:       repo,
		HTTPClient: &http.Client{Timeout: httpTimeout},
	}
}

// Latest fetches the most recent non-draft, non-prerelease release.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	u := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: latest: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("update: latest: HTTP %d: %s",
			res.StatusCode, strings.TrimSpace(string(body)))
	}
	var r Release
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("update: latest decode: %w", err)
	}
	return &r, nil
}

// Verifier validates a downloaded blob. Implementations include:
//
//   - ChecksumVerifier  — SHA-256 of the blob against an expected hex.
//   - (planned) CosignVerifier — Sigstore keyless signature.
//
// Apply runs every registered verifier in order and aborts on the
// first failure.
type Verifier interface {
	Verify(blob []byte) error
}

// ChecksumVerifier is a SHA-256 verifier.
type ChecksumVerifier struct {
	ExpectedHex string
}

// Verify satisfies Verifier.
func (v ChecksumVerifier) Verify(blob []byte) error {
	got := sha256.Sum256(blob)
	want, err := hex.DecodeString(strings.TrimSpace(v.ExpectedHex))
	if err != nil {
		return fmt.Errorf("update: bad expected sha256: %w", err)
	}
	if len(want) != len(got) {
		return errors.New("update: checksum length mismatch")
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("update: sha256 mismatch: want %s got %x",
				v.ExpectedHex, got)
		}
	}
	return nil
}

// AssetForPlatform picks the release asset whose name matches the
// running OS/architecture combination. The naming convention is the
// same one CI emits today: `veil-<os>-<arch>[.exe]`.
func AssetForPlatform(r *Release) (*Asset, error) {
	want := fmt.Sprintf("veil-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for i := range r.Assets {
		if r.Assets[i].Name == want {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("update: no asset matched %q in release %s", want, r.TagName)
}

// FetchChecksum looks up the per-asset SHA-256 from a `checksums.txt`
// asset attached to the release. The file format follows
// `sha256sum -b` output: `<hex>  <filename>` (two spaces).
//
// The checksum asset MUST be named exactly `checksums.txt` for the
// integrity story to work; release pipelines that use a different
// name need an extra config knob (added when needed).
func (c *Client) FetchChecksum(ctx context.Context, r *Release, assetName string) (string, error) {
	for _, a := range r.Assets {
		if a.Name == "checksums.txt" {
			data, err := c.download(ctx, a.DownloadURL)
			if err != nil {
				return "", err
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// "<hex> *filename" or "<hex>  filename"
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				name := strings.TrimPrefix(fields[len(fields)-1], "*")
				if name == assetName {
					return fields[0], nil
				}
			}
			return "", fmt.Errorf("update: %s not in checksums.txt", assetName)
		}
	}
	return "", errors.New("update: release has no checksums.txt asset")
}

// Download fetches a binary blob through the release URL. The body
// is read fully into memory; release binaries are small enough
// (~30 MiB) that this beats juggling a stream + temp-file dance.
func (c *Client) Download(ctx context.Context, a *Asset) ([]byte, error) {
	return c.download(ctx, a.DownloadURL)
}

func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("update: download: HTTP %d: %s",
			res.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(res.Body)
}

// Replace installs blob at targetPath atomically (best-effort
// across OSes). On Unix this is a same-directory rename; on Windows
// we stage next to the running exe and emit ErrPendingRestart so the
// caller can communicate the manual swap to the operator.
//
// Verifiers run BEFORE the on-disk write so a corrupted blob is
// never persisted.
func Replace(targetPath string, blob []byte, verifiers ...Verifier) error {
	for _, v := range verifiers {
		if v == nil {
			continue
		}
		if err := v.Verify(blob); err != nil {
			return err
		}
	}
	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".veil-update-*")
	if err != nil {
		return fmt.Errorf("update: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("update: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("update: close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil && runtime.GOOS != "windows" {
		cleanup()
		return fmt.Errorf("update: chmod tmp: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows refuses to rename over the running .exe. Move it
		// aside; if the rename succeeds we install the new file in
		// the original path. The aside copy is left next to the new
		// file so a failed startup can roll back manually.
		aside := targetPath + ".old"
		_ = os.Remove(aside)
		if err := os.Rename(targetPath, aside); err != nil {
			cleanup()
			return fmt.Errorf("update: move running binary aside: %w", err)
		}
		if err := os.Rename(tmpName, targetPath); err != nil {
			// Try to restore.
			_ = os.Rename(aside, targetPath)
			return fmt.Errorf("update: install new binary: %w", err)
		}
		return ErrPendingRestart
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		cleanup()
		return fmt.Errorf("update: rename: %w", err)
	}
	return ErrPendingRestart
}

// ErrPendingRestart is returned by Replace when the on-disk binary
// has been swapped successfully but the caller's running process
// still holds the old image. The CLI surfaces it as an info message
// rather than an error.
var ErrPendingRestart = errors.New("update: install complete; restart to use the new binary")
