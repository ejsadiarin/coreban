# Kanban + Reminder tool

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
