# v1.0 Launch Checklist

Operational checklist a maintainer walks before marking a tagged
release as the v1.0 GA — i.e. the first release we recommend for
trust-bearing deployments. Do not flip "GA" until every item
below is checked.

The list is ordered roughly by lead time: items toward the top
are the ones that take weeks or months in calendar time, items
toward the bottom are ones that take hours.

---

## 1. Security

- [ ] **External audit complete.** Findings logged in
      `docs/AUDIT_PREP.md` "Prior reviews" table. Every
      Critical / High finding has an associated commit on
      `main` and a regression test where reasonable.
- [ ] **Threat model re-reviewed** against the audit's findings.
      Adversary capabilities updated, mitigations re-checked,
      residual-risk section reflects the post-audit state.
- [ ] **Wire-protocol surface frozen for v1.x.** Any breaking
      change requires a major version bump.
- [ ] **Crypto choices re-validated** — primitives still
      considered safe at the chosen parameter sizes by
      independent reviewers.
- [ ] **Secrets handling reviewed** — no key material in logs
      at INFO level, all bcrypt costs at-or-above 12, all
      keypair files default mode 0600.
- [ ] **Disclosure process exercised** at least once via a
      synthetic finding so the SECURITY.md flow is not first
      tested under fire.
- [ ] **Bug bounty programme** announced (or, if budget did
      not allow, a "Security Hall of Fame" in lieu).

## 2. Build supply chain

- [ ] **Reproducible builds verified.** Independent rebuild on
      a clean machine produces byte-identical artefacts (modulo
      the documented exception for embedded timestamps).
- [ ] **SLSA Level 3** attestations attached to every release
      asset.
- [ ] **Dependencies pinned** with `go.sum` and Cargo lock
      files; renovate / dependabot configured to surface
      upstream CVEs within 24h.
- [ ] **govulncheck clean** on the release commit.
- [ ] **Cosign signing key story documented** in
      `docs/RELEASING.md` and at least one alternate maintainer
      has tested cutting a release end-to-end.

## 3. Code quality and tests

- [ ] **CI matrix passes** on the release commit across every
      configured target.
- [ ] **Fuzz harness has run continuously** for at least 30
      days against `main` with no crashes; OSS-Fuzz integration
      is live.
- [ ] **Race detector clean** in the integration test job.
- [ ] **End-to-end smoke** runs on a real VPS (not just the
      CI Docker network) at least once per release cycle.
- [ ] **Performance baseline established** — handshake latency,
      throughput, and memory footprint numbers in the release
      notes; regressions block subsequent v1.x patch releases.

## 4. Documentation

- [ ] **README**, **PRD**, **PROTOCOL.md**, **THREAT_MODEL.md**,
      **AUDIT_PREP.md**, **INSTALL.md**, **RELEASING.md** all
      reviewed and dated within the last 30 days.
- [ ] **Roadmap reflects post-v1 plans** — what stays
      pre-1.0 work, what becomes the v1.x patch series, what is
      v2 territory.
- [ ] **Architecture decision records** for every shipped
      transport (QUIC, WSS, Reality, MASQUE, edge backends);
      every "Status" line is up to date.
- [ ] **Operator install guide** validated by at least one
      non-maintainer end-to-end.

## 5. Distribution channels

- [ ] **GitHub Releases** carry signed binaries + sigstore
      bundles + per-binary SBOMs.
- [ ] **Docker images** at `ghcr.io/redstone-md/veil:v1.0.0` and
      `:latest`; image manifest is signed and the entry point
      runs as non-root.
- [ ] **Homebrew tap** updated.
- [ ] **Scoop bucket** updated (Windows).
- [ ] **AUR / Nix flake / Snap** entries updated where
      maintainers exist.
- [ ] **F-Droid** metadata reviewed — only blocked on Android
      client landing (Phase 4.5+).

## 6. Communication

- [ ] **Release notes** at the top of the GitHub Release page
      cover: what is new, what changed, what to migrate, known
      issues, security advisories closed.
- [ ] **Blog post / project page** announcement drafted and
      reviewed by at least two maintainers.
- [ ] **Audit report** published alongside the release (or
      linked, if the auditor publishes their own copy).
- [ ] **Contact channels** documented (issues, discussions,
      security advisory, Matrix / Discord if those exist).
- [ ] **Funding page** updated; OpenCollective ledger reviewed
      and posted.

## 7. Operations

- [ ] **Maintainer succession plan** — at least one named
      backup maintainer with `admin` access and exposure to the
      release process.
- [ ] **DNS and CDN owned by the project**, not by an
      individual maintainer's personal accounts.
- [ ] **Sigstore Fulcio identity** — the canonical workflow
      Subject is documented in three places (RELEASING.md,
      `veil update apply` default, and in the audit report) so
      end users can independently verify the signing identity.
- [ ] **Public test deployment** running the GA build for at
      least 7 days under realistic traffic.

---

## When this list is complete

Flip the GitHub Release from pre-release to GA, push the
announcement, and add an entry to `CHANGELOG.md` collapsing the
Unreleased section into `## [v1.0.0] – YYYY-MM-DD`.

Open a fresh `[Unreleased]` block above it for whatever ships
next.
