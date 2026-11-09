# Status is the source of truth; columns belong to a status

A card's status (Todo, In progress, Done — a fixed set shared by every board) is the only thing that says whether it is completed. Boards still define and order their own columns, but every column belongs to exactly one status and a card's column determines its status, so the two can never disagree. Reminders, filters, and queries read status, never column names.

We rejected fixed status-columns (every board identical — too rigid for boards like "Homelab" that want a Review stage) and an independent status stored beside the column (the two drift: a Done card shown under "Review").

**Consequences**: every board must keep at least one column per status, so a card can always be moved to any status; a column cannot be deleted while it holds cards. The fixed set maps onto VTODO `NEEDS-ACTION / IN-PROCESS / COMPLETED` should CalDAV return.
