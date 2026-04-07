# Elnath

Autonomous AI assistant platform with native knowledge base and automatic workflow routing.

Elnath is a standalone Go daemon and interactive CLI that brings Claude Code-level execution quality to independent AI agents. It combines intelligent workflow routing, a native Markdown+SQLite knowledge base, and modular tool execution into a self-contained platform.

## Features

- **Interactive CLI and background daemon modes** — Use `elnath run` for interactive chat or `elnath daemon` for background job processing
- **Model-agnostic LLM support** — Anthropic Claude (primary), OpenAI, Responses API with pluggable provider interface
- **Native wiki with FTS5 hybrid search** — Markdown pages + SQLite full-text search index for Karpathy-style knowledge base
- **Intent classification and automatic workflow routing** — Message intent determines execution strategy: single agent, team, autopilot, ralph (verify loop), or research
- **5 workflow execution modes** — single (immediate), team (coordinated agents), autopilot (full autonomy), ralph (loop until verified), research (hypothesis → experiment → evaluate → wiki)
- **Self model with adaptive persona** — Maintains identity, system prompt, and persona that adjust based on context
- **Session persistence with fork support** — JSONL format for reproducible session history and branching
- **4 permission modes** — default (explicit approval), accept_edits (auto-approve changes), plan (show plan before execution), bypass (unrestricted)

## Quick Start

### Build

```bash
make build
```

### Set API Key

```bash
export ELNATH_ANTHROPIC_API_KEY=sk-ant-...
```

For OpenAI or other providers, see [Configuration](#configuration).

### Interactive Mode

```bash
./elnath run
```

Start an interactive chat session. Type messages naturally; intent is classified automatically.

### Background Daemon

```bash
# Start daemon
./elnath daemon start

# Submit a job
./elnath daemon submit "analyze project structure"

# Check status
./elnath daemon status

# Stop daemon
./elnath daemon stop
```

## Commands

| Command | Purpose | Example |
|---------|---------|---------|
| `run` | Interactive chat mode | `elnath run` |
| `daemon start` | Start background daemon | `elnath daemon start` |
| `daemon submit` | Submit job to daemon | `elnath daemon submit "summarize logs"` |
| `daemon status` | Show queued and running jobs | `elnath daemon status` |
| `daemon stop` | Stop daemon | `elnath daemon stop` |
| `daemon install` | Install daemon as system service | `elnath daemon install` |
| `wiki search` | Full-text search wiki | `elnath wiki search "authentication"` |
| `wiki lint` | Validate wiki structure | `elnath wiki lint` |
| `wiki rebuild` | Rebuild FTS5 index | `elnath wiki rebuild` |
| `wiki list` | List all wiki pages | `elnath wiki list` |
| `research` | Start research workflow | `elnath research "can we use X?"` |
| `version` | Show version | `elnath version` |
| `help` | Show command help | `elnath help` |

## Architecture

```
cmd/elnath/           CLI dispatcher and REPL
internal/
  agent/              Agent loop: message → LLM → tools → repeat
  config/             YAML + environment configuration
  conversation/       Intent classification, context compression, history
  core/               App lifecycle, dual SQLite DB, logging, error handling
  daemon/             Unix socket IPC, worker pool, job queue
  llm/                Provider interface, Anthropic/OpenAI/Responses implementations
  orchestrator/       Workflow routing (single/team/autopilot/ralph/research)
  research/           Hypothesis → experiment → evaluate loop
  self/               Identity, persona, system prompt
  tools/              Bash, File (read/write/edit/delete/ls), Git, Web
  wiki/               Store, FTS5 index, hybrid search, page ingest, validation
```

### Core Interfaces

**Agent Loop**: The `agent` package implements the core message-processing loop. State is a message array only; no hidden state machines.

**LLM Provider**: Pluggable interface supporting Anthropic, OpenAI, and Responses API. Key pool and cost tracking built in.

**Tool Executor**: Modular tools with streaming support. Stream callback pattern: `Stream(ctx, req, cb func(StreamEvent)) error`

**Wiki Store**: Markdown pages with YAML frontmatter + SQLite FTS5 index. Hybrid search combines full-text and metadata queries.

**Daemon IPC**: Unix socket with JSON protocol. Worker pool processes jobs from queue concurrently.

## Configuration

### Config File

Create `~/.elnath/config.yaml`:

```yaml
data_dir: ~/.elnath/data
wiki_dir: ~/.elnath/wiki
log_level: info

anthropic:
  api_key: ${ELNATH_ANTHROPIC_API_KEY}
  model: claude-sonnet-4-20250514

permission:
  mode: default

daemon:
  socket_path: ~/.elnath/daemon.sock
  max_workers: 3

research:
  max_rounds: 5
  cost_cap_usd: 5.0
```

### Environment Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `ELNATH_ANTHROPIC_API_KEY` | Anthropic API key | `sk-ant-...` |
| `ELNATH_OPENAI_API_KEY` | OpenAI API key (if using OpenAI) | `sk-...` |
| `ELNATH_DATA_DIR` | Database directory | `~/.elnath/data` |
| `ELNATH_WIKI_DIR` | Wiki pages directory | `~/.elnath/wiki` |
| `ELNATH_LOG_LEVEL` | Logging level | `info`, `debug`, `warn`, `error` |
| `ELNATH_PERMISSION_MODE` | Permission mode | `default`, `accept_edits`, `plan`, `bypass` |

Priority: environment variables override config file.

## Wiki

The wiki is a knowledge base combining Markdown pages with SQLite full-text search.

### Page Format

Pages are Markdown with YAML frontmatter:

```markdown
---
title: Authentication
tags: [security, api]
---

# Authentication

Bearer token required for all API endpoints...
```

### Searching

```bash
elnath wiki search "authentication methods"
elnath wiki search "tag:security"
```

### Maintaining

```bash
# Validate all pages
elnath wiki lint

# Rebuild FTS5 index (after manual edits)
elnath wiki rebuild

# List all pages
elnath wiki list
```

## Workflows

Intent classification routes messages to one of five workflows:

### Single

Immediate response from a single LLM call. For questions, clarifications, one-off tasks.

### Team

Coordinated multi-agent execution. Router agent breaks work into tasks, executes in parallel, synthesizes results. For feature development, refactoring, complex analysis.

### Autopilot

Full autonomous execution from goal to completion. Research, planning, implementation, verification. For ambitious features, architectural decisions, large refactors. Loops until success or cost cap reached.

### Ralph

Verification loop: execute → verify → refine → repeat until success criteria met. For bug fixes, tests, critical systems. Always terminates before proceeding.

### Research

Hypothesis-driven investigation: propose hypothesis → design experiment → execute → evaluate → update wiki. For exploratory work, understanding design tradeoffs, evaluating technologies.

## Requirements

- **Go 1.25+** — Uses modernc.org/sqlite for pure Go SQLite (no CGo required)
- **macOS or Linux** — Tested on both platforms
- **API key** — Anthropic Claude API, OpenAI, or other supported provider

## Building from Source

```bash
# Clone repository
git clone https://github.com/stello/elnath
cd elnath

# Build
make build

# Run tests with race detector
make test

# Run linter
make lint

# Build and run interactive mode
make run
```

## License

Apache License 2.0. See LICENSE file for details.

## Documentation

- [IMPLEMENTATION_PLAN.md](./IMPLEMENTATION_PLAN.md) — Full implementation plan with phase guide
- [ADR-001-v01-architecture.md](./ADR-001-v01-architecture.md) — Architecture decision record
- [CLAW_CODE_ANALYSIS.md](./CLAW_CODE_ANALYSIS.md) — Claude Code internal architecture reference
- [ULTRAPLAN_PROMPT.md](./ULTRAPLAN_PROMPT.md) — Original specification with smoke tests
