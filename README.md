# e2e evidence

Proof artifacts for closing issues; not code. Nothing here is built or shipped.

## issue-1 — coreban v1 spec (2026-11-10)

Run against a disposable local stack: Postgres 17 (+pg_uuidv7) and ntfy
2.37.2 in Docker (`auth-default-access: deny-all`, coreban's account
write-only on `coreban-*`), with the gateway (`:18080`), `coreban-api`
(`:6972`, `REMINDER_CAP=5`) and the web dev server (`:3001`) run natively.
UI flows were driven with Playwright (headless Chromium, 1440×900).
Deliveries were read back from ntfy as a separate read-only subscriber
account.

- `shots/`: UI screenshots, numbered in test order
- `api-e2e.sh` / `api-e2e.out`: API-key scopes, targeted reads,
  token-only access and fail-closed boot (stories 53–56)
- `mint.mjs`: mints gateway-shaped EdDSA tokens for the direct-to-service checks
