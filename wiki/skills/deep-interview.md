---
title: "Deep Interview"
type: analysis
tags: [skill]
name: deep-interview
description: "Clarify ambiguous requests before executing"
trigger: "/deep-interview"
required_tools: []
model: ""
---

Before executing any task, clarify the user's intent through structured questions.

Procedure:
1. Identify what is ambiguous or underspecified in the request
2. Ask 3-5 focused clarifying questions (all in one message if in Telegram)
3. Wait for user responses
4. Summarize the refined requirements
5. Confirm with user before proceeding
6. Execute the clarified task

If the request is already clear and specific, skip the interview and execute directly.
Do not interview for simple, unambiguous tasks.
