---
title: "Security Audit"
type: analysis
tags: [skill]
name: audit-security
description: "Audit codebase for security vulnerabilities"
trigger: "/audit-security"
required_tools: [bash, read_file]
---

Perform a security audit of the current working directory. Procedure:

1. Identify the tech stack (language, frameworks, dependencies)
2. Check for common vulnerabilities:
   - Hardcoded secrets (API keys, passwords, tokens)
   - SQL injection (raw queries without parameterization)
   - XSS (unsanitized user input in HTML output)
   - Command injection (shell commands with user input)
   - Path traversal (file access with user-controlled paths)
   - Insecure dependencies (known CVEs)
3. For each finding:
   - **Severity**: CRITICAL / HIGH / MEDIUM / LOW
   - **Location**: file:line
   - **Description**: what's wrong
   - **Fix**: recommended remediation
4. Summary: total findings by severity, overall risk assessment
