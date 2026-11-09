# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root.
- **`docs/adr/`**: read ADRs that touch the area you're about to work in.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

This repo is **single-context**:

```
/
├── CONTEXT.md
├── docs/adr/
│   ├── 0001-....md
│   └── 0002-....md
└── internal/
```

## Relationship to coregateway

coreban is a downstream service of [coregateway](https://github.com/ejsadiarin/coregateway), usually checked out as the submodule `services/coreban` inside it. The gateway authenticates clients and proxies `/api/coreban/*` here with a short-lived signed internal token; coreban never sees sessions, API keys, or cookies.

Contracts the gateway owns are referenced by URL, not copied:

- Internal token claims (`sub`, `azp`, `scope`, `act_as`) and verification: [docs/auth-flow.md](https://github.com/ejsadiarin/coregateway/blob/main/docs/auth-flow.md), ADR-0001 and ADR-0002 in [docs/adr/](https://github.com/ejsadiarin/coregateway/tree/main/docs/adr).
- Proxy path and key scopes (`coreban:read|write|admin`): [docs/adding-a-service.md](https://github.com/ejsadiarin/coregateway/blob/main/docs/adding-a-service.md).

The gateway's `CONTEXT.md` stops at the gateway boundary: don't borrow its terms for coreban concepts. The verifier and `act_as` targeting helpers mirror `services/coredraw`; keep them in step when the gateway contract changes.

Changes spanning repos get one issue in each tracker, cross-linked. The repo that owns the user-visible outcome holds the tracking issue. The web UI lives in coregateway's `web/`, so UI work is filed there.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids (e.g. "task" for Card).

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0001 (status is the source of truth), but worth reopening because…_
