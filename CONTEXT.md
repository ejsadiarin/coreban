# coreban

A personal kanban and reminder service behind coregateway: users organise work on boards and get nudged about it through push notifications.

## Language

### Ownership

**Owner**:
The user a board, reminder, or notification target belongs to, known to coreban only as the identity the gateway asserts. coreban keeps no other user data.
_Avoid_: account, member, principal

### Kanban

**Board**:
An owner's named collection of columns, private to that owner.
_Avoid_: project, workspace, list

**Status**:
Where a card stands in its lifecycle: one of a fixed set, Todo, In progress, or Done, shared by every board.
_Avoid_: state, stage, column

**Column**:
An ordered stage on one board that belongs to exactly one status; each board defines and orders its own. A card's column determines its status.
_Avoid_: lane, stage, list

**Card**:
A single piece of work on a board, sitting in exactly one column.
_Avoid_: task, todo, item, ticket, issue

**Due**:
When a card is expected to be finished: a calendar date, or a date and time. Independent of any reminder on the card.
_Avoid_: deadline, due date, end

**Label**:
A named tag defined on one board and applied to that board's cards.
_Avoid_: tag, category

**Archived**:
A card hidden from its board but kept; its reminders never fire. A note collapsed in its feed but kept, its last content still viewable. Distinct from deleted, which is permanent.
_Avoid_: hidden, closed, soft-deleted

**Completed**:
A card whose status is Done.
_Avoid_: done flag, closed, resolved, finished

### Reminders

**Reminder**:
A message the owner wants pushed to them on a schedule, optionally attached to a card.
_Avoid_: notification, alert, nudge, alarm

**Schedule**:
When a reminder fires: once at a moment, or recurring.
_Avoid_: trigger, cron, timer

**Notification target**:
Where an owner's reminders are delivered, such as their own ntfy topic.
_Avoid_: channel, subscription, topic

### Capture

**Note**:
An item in the owner's feed: either free text they posted (a thought, a todo, a link), or a window onto one linked record. It has no done state.
_Avoid_: message, post, entry, chat item

**Linked record**:
The one reminder, card, expense, or income a note shows. The note keeps no content of its own: it shows the record as it is now, and editing the note edits the record.
_Avoid_: attachment, reference, embed

**Feed**:
An owner's notes in the order they were posted, newest last, like a chat with themselves.
_Avoid_: chat, inbox, timeline, stream
