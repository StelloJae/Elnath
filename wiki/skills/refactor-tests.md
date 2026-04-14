---
title: "Refactor Tests"
type: analysis
tags: [skill]
name: refactor-tests
description: "Refactor test suite for clarity and maintainability"
trigger: "/refactor-tests <package>"
required_tools: [bash, read_file, write_file]
---

Refactor tests in package {package}. Procedure:

1. List test files in {package}
2. For each test file:
   - Convert to table-driven tests where applicable
   - Extract shared setup into helpers
   - Improve assertion messages
   - Remove duplicated test logic
3. Run tests after each file to verify no regressions
4. Report: files changed, tests before/after count, any failures
