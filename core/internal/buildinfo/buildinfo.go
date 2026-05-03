// Package buildinfo exposes build-time metadata baked into the binary
// via -ldflags at link time.
package buildinfo

// Version is the semantic version of this build. It is overwritten
// at link time via:
//
//	go build -ldflags "-X github.com/redstone-md/veil/core/internal/buildinfo.Version=vX.Y.Z"
//
// When unset, the binary reports "dev".
var Version = "dev"

// Commit is the git commit SHA this binary was built from.
// Overwritten at link time the same way as Version.
var Commit = "unknown"

// Date is the ISO-8601 build date. Overwritten at link time.
var Date = "unknown"
