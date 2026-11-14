# Every budget record has a note, on its own date

Every corefinance expense and income has a note in its owner's feed, placed on the record's own date, so the Feed reads as the budget's history. This supersedes the part of ADR-0003 that says only records captured through the feed have a note; the rest of ADR-0003 stands: a budget note keeps no content of its own and shows the record live.

coreban still can't call corefinance, so the web keeps the notes in sync. When the Feed opens, and when the Budget page creates a record, the web sends the records it read from corefinance to `POST /notes/sync`. That creates a note for any record without one and moves a note whose local date is no longer its record's date. Repeating a sync changes nothing, because a record has at most one note per owner.

A note's place in the feed is `posted_at`, not posting order: when it was posted, or, for a budget note, the record's date (at the record's creation time if it was created that day, otherwise noon). The feed pages by `(posted_at, seq)`. A note can now appear above the page someone is reading when an older record is synced. We accepted that: a budget history in the wrong order is worse than a page that grows.

Notes record their `origin`. A note the sync made (`budget`) is deleted with its record, either when the Budget page deletes the record or when the Feed finds it gone. A note posted from the Feed (`feed`) keeps the record's snapshot, as ADR-0003 describes, because the owner wrote it there on purpose.

We rejected building the history as a separate timeline that reads corefinance directly. That needs no syncing, but it keeps budget entries out of the Feed, where the owner's thoughts sit next to them. We rejected having corefinance create the notes: services hold no credentials to call each other, and adding them would be a larger change than a sync the web already has the data for.
