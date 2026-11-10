# Reminders are sent at most once

The dispatcher claims an occurrence (advances the reminder past it and commits) before it sends, one occurrence per transaction. If coreban dies between the commit and the send, that occurrence is lost; it is never sent twice, whether the process restarts mid-dispatch or two dispatchers run at once (`FOR UPDATE SKIP LOCKED`). A send ntfy refuses (such as a 403) is recorded on the reminder and not retried. A send that may get through later (ntfy unreachable or answering 5xx/429 after three quick tries, as when coreban comes back from a reboot before ntfy does) is put back and retried every 30 seconds, so a missed one-off still arrives once, late.

We rejected send-then-record (at-least-once): a crash after the send re-sends on restart, and a phone buzzing twice for one reminder is what the spec rules out. ntfy offers no idempotency key to deduplicate on its side.

**Consequences**: the loss window is one occurrence, for the length of one HTTP call. Putting back a send that ntfy answered with a 5xx can, rarely, repeat one that ntfy did publish before failing; we accept that for not losing reminders across a homelab reboot. A failure is visible in the reminder list (`last_delivery.error`) rather than silent, and the owner re-arms it by rescheduling.
