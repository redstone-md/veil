# Contributing to Veil

Thank you for your interest in contributing. Veil exists to make
free, censorship-resistant networking available to everyone, and
that goal needs more hands than any single maintainer can provide.

This document covers what you need to know before opening an issue
or pull request.

---

## Ground rules

1. **Be respectful.** Read [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
2. **Discuss large changes first.** Open an issue with the `proposal`
   label before writing more than ~300 lines of new code, so we can
   align on direction.
3. **Stay focused on the user.** Veil's primary users are people
   trying to access the open internet under hostile network conditions.
   Every change should ultimately serve that user, not architectural
   purity.
4. **Security first.** If you find a vulnerability, do **not** open a
   public issue. Follow the disclosure process in [SECURITY.md](SECURITY.md).

---

## How to contribute

### Reporting bugs

- Search existing issues first.
- Use the "Bug report" template (when available).
- Include: Veil version, OS, transport in use, and reproduction steps.
- Do **not** include private keys, full user configs, or unredacted IPs
  in public issues.

### Suggesting features

- Open an issue with the `proposal` label.
- Describe the user problem first, the proposed solution second.
- Reference the [PRD](PRD.md) if the feature is in scope or explicitly
  out of scope.

### Submitting code

1. Fork the repository.
2. Create a feature branch (`feat/<short-name>` or `fix/<short-name>`).
3. Make your changes. Keep PRs focused — one logical change per PR.
4. Ensure tests pass locally:
   ```bash
   cd core
   go test ./...
   go vet ./...
   ```
5. Open a pull request. Fill in the PR template.
6. Be patient — reviews may take time during the pre-alpha phase.

### Documentation

Documentation changes are very welcome and don't require any of the
proposal-discussion overhead above. Just open a PR.

---

## Coding standards

### Go

- Target Go 1.22+. Use the standard `gofmt` / `goimports` formatting.
- Lint with `golangci-lint` (config at the repo root); CI enforces.
- Errors: wrap with context (`fmt.Errorf("doing X: %w", err)`),
  do not swallow errors silently.
- No `panic` in library code paths; reserve panics for genuinely
  unrecoverable invariant violations.
- Logging: use `log/slog`; never log secrets, tokens, or full IPs
  at info level.
- Public APIs: doc-comment every exported symbol.

### Cryptographic code

- **Do not** implement new cryptographic primitives. Use the existing
  Noise / standard library / audited dependencies.
- Cryptographic changes require sign-off from a maintainer with
  cryptography background and **must** include test vectors.

### Wire protocol changes

Any change to the on-the-wire format requires:
1. An RFC-style proposal as an issue.
2. An update to [docs/PROTOCOL.md](docs/PROTOCOL.md) in the same PR
   as the implementation.
3. A protocol version bump if the change is not backward-compatible.

### Tests

- Unit test new logic.
- Integration tests live under `core/test/integration/` and run against
  a real `veil` binary in a Docker network.
- Aim for behaviour coverage, not line coverage; tests should describe
  what the system does for the user.

---

## Commit messages

We use **Conventional Commits**. Subject ≤ 50 characters; body wraps at 72.

Examples:
```
feat(transport): add WebSocket-over-TLS adapter
fix(noise): reject handshakes with replayed nonces
docs(threat-model): clarify mitigations for active SNI probing
```

Scopes: `core`, `transport`, `crypto`, `dpi`, `proxy`, `admin`, `users`,
`installer`, `clients`, `deploy`, `sdks`, `docs`, `ci`.

Sign off your commits with `git commit -s` (Developer Certificate of Origin).

---

## Developer Certificate of Origin

By contributing to this project, you certify that you have the right
to submit your contribution under the project's license.
We use the [Developer Certificate of Origin 1.1](https://developercertificate.org/).
The `Signed-off-by` line in your commit (added by `git commit -s`)
indicates this certification.

---

## Licensing of contributions

By submitting a contribution, you agree that your contribution will be
licensed under the [Apache License 2.0](LICENSE), the same license that
covers the project as a whole.

You retain copyright to your contribution.

---

## Where to start

Look for issues tagged:
- `good first issue` — small, well-scoped tasks for newcomers.
- `help wanted` — tasks the maintainers actively need help with.
- `documentation` — non-code contributions.

If nothing appeals, ask on the discussion forum what would help most
right now.
