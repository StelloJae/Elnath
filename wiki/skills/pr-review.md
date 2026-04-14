---
title: "PR Review"
type: analysis
tags: [skill]
name: pr-review
description: "Review PR with security and quality focus"
trigger: "/pr-review <pr_number>"
required_tools: [bash, read_file]
---

Review PR #{pr_number}. Procedure:

1. Run `gh pr diff {pr_number}` to see changes
2. For each changed file:
   - Security: check for injection, auth bypass, secret exposure
   - Quality: naming, complexity, error handling
   - Tests: verify test coverage for changes
3. Output format per file:
   - **File**: path
   - **CRITICAL/HIGH/MEDIUM/LOW**: issue description
   - **Suggestion**: fix recommendation
4. End with overall assessment: APPROVE, REQUEST_CHANGES, or COMMENT
