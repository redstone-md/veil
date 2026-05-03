# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for the
Veil project.

An ADR is a short document that captures a significant architectural
decision: the context, the choice, the alternatives considered, and
the consequences.

## Format

We use a lightweight format inspired by Michael Nygard's original
[ADR template](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions.html):

```
# ADR-NNNN: Title

**Status:** Proposed | Accepted | Superseded by ADR-XXXX | Deprecated
**Date:** YYYY-MM-DD
**Deciders:** (names or handles)

## Context

What is the issue we are addressing?

## Decision

What we decided.

## Alternatives considered

Other options and why we did not choose them.

## Consequences

What becomes easier, what becomes harder, what trade-offs we accept.
```

## Numbering

ADRs are numbered sequentially starting at 0001. Once an ADR is
merged, its number MUST NOT change. Superseding ADRs reference
the older one but do not renumber it.

## When to write an ADR

- A choice that will be hard to reverse.
- A choice that future contributors will reasonably ask "why?" about.
- A choice that diverges from the obvious or industry-default option.

If you find yourself explaining the same decision to multiple people,
write an ADR.

## Index

- [ADR-0001 — Initial technology choices](ADR-0001-initial-tech-choices.md)
