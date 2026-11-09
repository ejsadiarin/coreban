## Agent skills

### Issue tracker

Issues live as GitHub issues in `ejsadiarin/coreban`; use the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five triage labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. coreban is a downstream service of coregateway; contracts the gateway owns (internal token claims, proxy paths, key scopes) are referenced, not redefined here. See `docs/agents/domain.md`.
