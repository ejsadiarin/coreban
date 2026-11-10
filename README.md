# coreban

Kanban boards and ntfy reminders, served as a downstream service behind
[coregateway](https://github.com/ejsadiarin/coregateway). Vocabulary:
`CONTEXT.md`; decisions: `docs/adr/`.

## Running

| | |
|---|---|
| Port | `6972` |
| Liveness | `GET /health` — no token |
| API | `/api/coreban/*` — gateway internal token only (audience `coreban`), else 401 |
| Verified health | `GET /api/coreban/health` — `{"status":"ok","owner":…}` once token + database work |
| Boards | `GET/POST /api/coreban/boards`, `GET/PATCH/DELETE /api/coreban/boards/{id}` — see `coreban-api.http` |
| Columns | `POST /api/coreban/boards/{id}/columns`, `PATCH/DELETE …/columns/{columnID}`, `POST …/columns/{columnID}/move` — see `coreban-api.http` |
| Cards | `GET/POST /api/coreban/boards/{id}/cards` (`?archived=true` lists archived), `GET/PATCH/DELETE …/cards/{cardID}`, `POST …/cards/{cardID}/move` — see `coreban-api.http` |
| Labels | `GET/POST /api/coreban/boards/{id}/labels`, `PATCH/DELETE …/labels/{labelID}`; on a card `PUT/DELETE …/cards/{cardID}/labels/{labelID}` |
| Settings | `GET/PUT /api/coreban/settings` — notification target (`ntfy_topic`) and IANA `timezone` |
| Reminders | `GET/POST /api/coreban/reminders`, `GET/PATCH/DELETE …/reminders/{id}` — see `coreban-api.http` |

coreban refuses to start if it cannot fetch the gateway's JWKS
(`JWT_JWKS_URL`). Copy `.env.example` to `.env`, then:

```sh
psql "$MIGRATION_URL" -f internal/db/bootstrap.sql   # once per environment
make migrate-up      # or, in the image: ./coreban-api migrate
make run
```

## ntfy server

Reminders go to `NTFY_URL` with `NTFY_TOKEN`. Each owner's notification
target is only a topic name on that server: never a URL, always prefixed
`coreban-` (enforced by the API and the database), and claimed by one owner
at a time. The prefix only protects anything if the server enforces it too:

```yaml
# server.yml: nobody reads or writes without an account
auth-file: /var/lib/ntfy/user.db
auth-default-access: deny-all
```

```sh
ntfy user add coreban                          # coreban's own account: role user, never admin (admins bypass ACLs)
ntfy access coreban 'coreban-*' write-only     # write to coreban-* and nothing else
ntfy token add coreban                         # → NTFY_TOKEN

ntfy access <you> coreban-<name> read-only     # each owner subscribes with their own account
```

With anonymous access denied, a topic name is not a secret anyone can
subscribe to, and a leaked `NTFY_TOKEN` cannot reach topics outside
`coreban-*`.

## Reminders

The dispatcher runs inside `coreban-api` when `NTFY_URL` is set. It sleeps
until the next reminder's moment (or a create, reschedule, or settings
save wakes it), so an idle coreban leaves the database alone. Each
occurrence goes out at most once (ADR-0002):

- **On time**: sent at its moment.
- **Missed** (coreban was down, or the owner had no notification target):
  a one-off is sent once when it can be, titled "Late reminder" with when
  it was set for. A recurring reminder skips what it missed and carries on
  from its next occurrence; nothing is replayed.
- **ntfy not reachable yet** (down, or answering 5xx, after three quick
  tries): put back and retried every 30 seconds.
- **Refused** (ntfy answered 4xx, such as 403 for a topic coreban's token
  can't write): not retried; the reminder's `last_delivery.error` says why,
  and rescheduling it re-arms it.

A schedule is a one-off (`{"kind": "once", "at": …}`, an RFC 3339 instant
or a wall-clock time with no offset read in the owner's timezone) or a
recurring preset at a time of day in the owner's timezone: `daily`,
`weekly` on `weekdays` (`MO` … `SU`), or `every` N `days` counting `from` a
date. Recurring schedules are stored as an iCalendar RRULE with a floating
DTSTART, so they follow the owner when they change timezone; daylight
saving keeps them at their local time. Responses render times in the
owner's zone. An owner may have at most `REMINDER_CAP` reminders.

A reminder can be about a card (`card_id`; its message defaults to the
card's title, and `GET …/reminders?card_id=` lists a card's). `{"kind":
"due"}` reminds once at the card's due: its moment, or **09:00 in the
owner's timezone** on a date-only due; it does not follow later changes to
the due. While the card is completed (in a Done column) or archived, its
reminders are skipped when they come due (a one-off's moment is used up,
a recurring one carries on from its next occurrence) and listed as
`paused`; nothing is deleted. Deleting the card deletes them.

## Queries

SQL lives in `internal/db/queries/`; `make sqlc` regenerates the typed Go in
`internal/db/sqlc/` (sqlc v1.31.1) from those queries and the migrations.
Never edit the generated files; `make sqlc-check` fails when they're stale.

## Tests

```sh
make test        # unit + integration (needs Docker)
make test-unit   # no Docker
```

Integration tests use `internal/testharness`: `Postgres(t)` gives a shared
disposable Postgres scoped to the `coreban` schema with migrations applied,
and `Router(t, Identity{Owner: …})` drives the production router with that
identity injected in place of a gateway token. Create a fresh owner per test.

---

## Notes: Kanban + Reminder tool

Kanban: pushed via ntfy server
Reminders: pushed via ntfy server

# Reminder tool for various topics (for spaced repetition)

## Topics

- `ps` tool with `awk` practice

```bash
ps -eo pid,ppid,stat,cmd | awk '$4 == "sleep" {print $2}' | xargs -I {} kill -9 {}
ps aux --sort=%mem | tail -n 6
```

- CKA exam questions (see DumbITGuy playlist, killerkoda, etc.)

- daily quests as todos (workout, programming)
- anime/manga/manhwa/manhua updates
- tech/dev daily news
- backlog clearing/old reads to revisit
- "Core reminds me to..." (philosophical, grounded mental)
- self learn, have strong fundamentals (language-agnostic)
- job hop for growth potential
- the work you do needs to be broadly communicated for visibility in order to be considered for high ratings and compensation increases.
- programming is just moving data around and managing state.
- academic/organization work
- job application/openings (opt, since i have linkedin, etc.)
