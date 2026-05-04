# Veil — Scoop distribution (Windows)

`veil.json` is the canonical Scoop manifest for the `veil` CLI on
Windows. It lives in-tree so the manifest tracks the project's
release process; the actual Scoop bucket repository consumes it
from here.

## Repository layout

```
github.com/redstone-md/scoop-bucket               ← the bucket users add
└── bucket/
    └── veil.json                                  ← cp from here on release
```

## Adding the bucket (end users)

```powershell
scoop bucket add veil https://github.com/redstone-md/scoop-bucket
scoop install veil
```

## Per-release update procedure

The manifest's `checkver` + `autoupdate` blocks are configured so
that `scoop checkver veil` (run from inside the bucket repo) picks
up the latest `v*` tag from the upstream repository's GitHub
releases and rewrites the URL automatically. The hash field is
fetched from the published `checksums.txt` per release.

Manual edit (if you need to bump the manifest by hand):

1. Replace every occurrence of `0.0.0-placeholder` with the new
   version.
2. Replace the two `0000…0000` hashes with the SHA-256 of the
   matching binaries from `checksums.txt`.
3. Commit + push to the bucket repo.

A future revision automates this through a release-bot.

## Why `bin` rewrites the name to `veil`

The release artefacts use the per-platform suffix
(`veil-windows-amd64.exe`); Scoop's `bin` shorthand renames the
shim to plain `veil` so users invoke `veil version`, not
`veil-windows-amd64 version`.
