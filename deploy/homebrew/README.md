# Veil — Homebrew distribution

`veil.rb` in this directory is the canonical Homebrew formula for
the `veil` CLI. It lives in-tree so the formula tracks the
project's release process; the actual Homebrew tap repository
consumes it from here.

## Repository layout

```
github.com/redstone-md/homebrew-tap            ← the tap users add
└── Formula/
    └── veil.rb                                ← cp from here on release
```

## Adding the tap (end users)

```bash
brew tap redstone-md/tap
brew install veil
```

## Per-release update procedure

Per `docs/RELEASING.md`, the release pipeline produces
`checksums.txt` listing the SHA-256 of every per-platform binary.
To advance the tap to a new version:

1. Bump `version` in `veil.rb` to the new tag (without the leading `v`).
2. For each `on_<os> { on_<cpu> { sha256 ... } }` block, replace the
   placeholder `00…00` with the SHA-256 of the matching artefact
   from `checksums.txt`. The asset names follow the
   `veil-<os>-<arch>` pattern published by the release workflow.
3. Copy the file into the tap repository's `Formula/` directory
   and open a PR against `main`.

A future revision automates step 1-3 via a release-bot
(github-action that opens the tap PR on `release.yml` success).

## Building from source vs. binary install

The formula installs the published release binary for the
operator's CPU. Operators who prefer building from source can do
so directly via `go install ./cmd/veil` against this repository;
the formula is a convenience for the common case.
