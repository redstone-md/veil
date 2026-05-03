# Security Policy

Veil is security-critical software. People rely on it to access information
under hostile network conditions, sometimes at significant personal risk.
Vulnerabilities in Veil have real-world consequences. We take reports
seriously and ask that you do the same.

---

## Supported versions

While Veil is in pre-alpha, only the `main` branch is supported.

After the v1.0 release, the latest minor version and the previous
minor version will receive security fixes.

---

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please report vulnerabilities privately via one of the following channels:

1. **GitHub Security Advisory:** Go to the project's Security tab and
   click "Report a vulnerability". This is the preferred channel because
   it integrates with our CVE assignment workflow.
2. **Encrypted email:** *(PGP key and address will be published before v1.0.
   Until then, use the GitHub Security Advisory channel.)*

When reporting, please include:

- A description of the vulnerability and its impact.
- Steps to reproduce, ideally with a minimal proof of concept.
- The affected version(s) (commit SHA if you tested `main`).
- Your name / handle for credit, if you wish to be credited.
- Whether you intend to publish your own write-up, and if so, when.

---

## What to expect

- **Acknowledgement:** within 72 hours.
- **Initial assessment:** within 7 days, including severity rating
  and a tentative timeline.
- **Fix and release:** the timeline depends on severity. Critical
  issues (RCE, crypto break, mass user de-anonymisation) are
  prioritised over everything else.
- **Disclosure:** we follow a coordinated disclosure model. We will
  agree on a public disclosure date with you. The default window is
  90 days from initial report, but can be shorter for trivial fixes
  or longer for issues requiring extensive remediation.
- **Credit:** unless you request anonymity, you will be credited in
  the advisory and in the release notes.

---

## Out of scope

The following are **not** considered vulnerabilities for the purposes
of this policy:

- Self-inflicted issues (running an outdated version, misconfigured
  admin UI exposed to the internet against the in-product warnings,
  etc.).
- Theoretical attacks against the underlying cryptographic primitives
  (Noise, X25519, ChaCha20-Poly1305) without a demonstrable break.
- Denial of service requiring resources greater than typical adversary
  capabilities at the targeted user's scale.
- Issues in third-party dependencies — please report those upstream
  first; we will track and update once a fix is available.

---

## Threat model

Before reporting, please skim the
[threat model](docs/THREAT_MODEL.md) to understand which adversaries,
assets, and assumptions are in scope. A reported "issue" that is
explicitly out of the threat model's scope (for example, "the server
operator can see traffic metadata") will be closed as expected behaviour.

If you believe the threat model itself is incomplete or wrong, that is
also a valuable contribution — open an issue to discuss.

---

## Bug bounty

Veil does not currently offer a paid bug bounty. The project is
donation-funded and has no commercial backing. If a sufficient donation
runway is established, a bounty programme will be considered post-v1.0.

In the meantime, we offer recognition: high-quality reports will be
credited prominently in advisories, release notes, and a published
"Security Hall of Fame".

---

## Reproducible builds and supply chain

All Veil release binaries are built reproducibly from tagged source
and signed with [Sigstore](https://www.sigstore.dev/). To verify
a release artifact:

```
# (Verification commands will be documented at the time of the first
#  signed release.)
```

If you suspect a tampered release artifact, please report it via the
security channels above.
