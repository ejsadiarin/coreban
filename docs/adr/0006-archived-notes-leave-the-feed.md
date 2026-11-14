# Archived notes leave the Feed

Archiving a note hides it from the Feed. Until now an archived note stayed in its place, collapsed, which kept the Feed honest but cluttered: with every budget record in the Feed (ADR-0005), deleted expenses and old notes the owner is done with piled up as collapsed rows. The Feed now leaves archived notes out unless the owner asks for them (`?archived=include`, the web's "Show archived"), where each sits in its place with Restore and Permanently delete as before. This holds for every kind of note.

This changes how budget notes are deleted from the Feed. **Delete** removes the expense or income from corefinance and leaves its note behind as one muted "(deleted)" line, showing the snapshot ADR-0003 describes; the note is not archived. Archiving that line then hides it. A live budget note offers Delete, not Archive, so the record is never hidden while it still counts in the budget.

We rejected keeping archived notes in place and adding a separate "hide" action. Two ways of putting a note away, one that still shows and one that doesn't, is a distinction nobody would remember.
