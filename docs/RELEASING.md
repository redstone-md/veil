# Releasing Veil

This document is the operational checklist a maintainer follows
to cut a tagged release. The actual heavy lifting is in
`.github/workflows/release.yml` — this file says when to push the
tag, how to verify the result, and what to do if something has
gone wrong.

## Audience

Maintainers with `write` access to the upstream repository. End
users do not need to read this document.

## Preconditions

- `main` is green (CI is passing).
- `CHANGELOG.md` `[Unreleased]` section is up to date and
  describes everything since the previous release.
- The next version number is decided. Veil follows
  [Semantic Versioning](https://semver.org/); pre-1.0 releases
  are tagged `v0.MINOR.PATCH` with the strong pre-release
  understanding that any minor bump may break the wire protocol
  or the configuration shape.
- For pre-alpha milestones use `v0.MINOR.PATCH-alpha.N` /
  `-beta.N` / `-rc.N` suffixes.

## Cut a release

1. **Move `[Unreleased]` to the new version**:
   - Edit `CHANGELOG.md`, rename `## [Unreleased]` to
     `## [vX.Y.Z] – YYYY-MM-DD`, and start a fresh empty
     `[Unreleased]` section above it.
   - Add a comparison link at the bottom of the file:
     `[vX.Y.Z]: https://github.com/redstone-md/veil/releases/tag/vX.Y.Z`.
   - Commit on `main`:

     ```bash
     git checkout main
     git pull
     # …edit CHANGELOG.md…
     git commit -am "release: vX.Y.Z"
     git push
     ```

2. **Push the tag**:

   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

3. **Watch the run**: open the Actions tab and follow the
   `Release` workflow. It will:
   - Build `veil` for `linux/amd64`, `linux/arm64`,
     `darwin/amd64`, `darwin/arm64`, `windows/amd64`.
   - Build `libveil.{so,dylib,dll}` on the matching native runners.
   - Generate `checksums.txt`.
   - Sign every artefact with cosign keyless (the workflow's
     GitHub OIDC identity is the signer).
   - Generate per-binary SBOM via syft.
   - Upload everything to a draft GitHub Release.

4. **Verify the artefacts**: the Release page should now contain
   five `veil-*` binaries, three `libveil-*` shared libraries,
   one `veil.h`, one `checksums.txt`, the matching `*.sigstore`
   bundles, and the `*.sbom.json` SBOMs.

5. **Spot-check the signature** locally:

   ```bash
   curl -L -o veil-linux-amd64       https://github.com/redstone-md/veil/releases/download/vX.Y.Z/veil-linux-amd64
   curl -L -o veil-linux-amd64.sigstore https://github.com/redstone-md/veil/releases/download/vX.Y.Z/veil-linux-amd64.sigstore

   cosign verify-blob \
     --bundle veil-linux-amd64.sigstore \
     --certificate-identity 'https://github.com/redstone-md/veil/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
     --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
     veil-linux-amd64
   ```

6. **Smoke-test the auto-updater** end-to-end (only after at
   least one prior tagged release exists):

   ```bash
   ./veil update check
   ./veil update apply --cosign
   ./veil version
   ```

7. **Edit the release notes**: the workflow generates them from
   the commit log; tidy headings into "What's new", "Bug fixes",
   "Operator-facing changes" sections so non-developers can read
   them.

8. **Publish**: flip the GitHub Release from "Draft" to
   "Published".

## Rollback

If a release ships and is found to be broken:

1. **Mark the GitHub Release as a pre-release** so the
   auto-updater stops offering it (`update.Latest` queries
   `/releases/latest`, which excludes pre-releases).
2. Investigate and fix on `main`. Bump to `vX.Y.(Z+1)` rather
   than reusing the bad version.
3. After the fix release, **delete the broken tag's release
   page**. Do NOT delete the git tag itself — auditors and
   forensic users may want to refer to it.

## Signing-key custody

We use Sigstore **keyless** signing, which means we have no
long-term private key to rotate. Each release is signed by an
ephemeral key bound to the workflow's GitHub OIDC identity, and
the binding is recorded in the public Rekor transparency log.

If a maintainer suspects compromise of the GitHub Actions identity
(the only trust root we have), the response is:

1. Lock the repository (revoke any compromised PATs / keys).
2. Open a CVE / Security Advisory describing the window of
   possible compromise.
3. Re-publish a signed checksum manifest covering the
   pre-incident releases so users can re-verify them out-of-band.

## Distribution channels (planned)

- **GitHub Releases** — primary; lands automatically via the
  workflow above.
- **Docker** — `ghcr.io/redstone-md/veil:vX.Y.Z` and `:latest`.
  The release workflow does not yet push images; that lands in
  Phase 6.5 alongside the signing of the OCI manifest.
- **Homebrew tap** — `redstone-md/tap/veil` (on the roadmap).
- **Scoop bucket** (Windows) — on the roadmap.
- **F-Droid** — once the Android client lands (Phase 4.5).
