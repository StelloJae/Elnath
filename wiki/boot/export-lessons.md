---
title: "Export Lessons to Wiki"
type: boot-task
schedule: "daily 03:00"
silent: true
---

Check lessons.jsonl for high-confidence lessons that haven't been exported to wiki yet.
For each new lesson, append it to wiki/self/lessons.md with date, topic, and text.
Keep the page under 200 lines — rotate oldest entries when exceeded.
