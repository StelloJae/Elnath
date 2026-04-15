# Elnath

Autonomous AI assistant platform with native knowledge base and automatic workflow routing.

Elnath is a standalone Go daemon and interactive CLI that brings Claude Code-level execution quality to independent AI agents. It combines intelligent workflow routing, a native Markdown+SQLite knowledge base, MCP server integration, and modular tool execution into a self-contained platform.

## Features

- **Interactive CLI and background daemon modes** — Use `elnath run` for interactive chat or `elnath daemon` for background job processing
- **Model-agnostic LLM support** — Anthropic Claude (primary), OpenAI, Ollama with pluggable provider interface
- **Native wiki with FTS5 hybrid search** — Markdown pages + SQLite full-text search index for Karpathy-style knowledge base
- **Intent classification and automatic workflow routing** — Message intent determines execution strategy: single agent, team, autopilot, ralph (verify loop), or research
- **5 workflow execution modes** — single (immediate), team (coordinated agents), autopilot (full autonomy), ralph (loop until verified), research (hypothesis-driven)
- **MCP server integration** — Connect external tool servers via Model Context Protocol (stdio-based JSON-RPC)
- **Hook system** — Pre/post tool execution hooks for validation, formatting, and custom workflows
- **4 permission modes** — default (explicit approval), accept_edits (auto-approve file changes), plan (read-only), bypass (unrestricted)
- **Self model with adaptive persona** — Maintains identity, system prompt, and persona that adjust based on context
- **Session persistence with fork support** — JSONL transcripts stay primary while SQLite-backed history metadata helps reconcile latest-session resume selection
- **Cross-project intelligence** — Search wiki and conversation history across linked projects

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

The daemon persists progress as a machine-readable event envelope (`elnath.progress.v1`) and `elnath daemon status` renders the shared `message` field. That keeps progress updates concise in the CLI now while leaving the same schema reusable for future delivery bridges.

### Thin Telegram Operator Shell

When configured, `elnath telegram shell` exposes the Month 4 operator-only Telegram surface:

- `/status` — summarize the current daemon queue
- `/approvals` — list pending approval requests
- `/approve <id>` / `/deny <id>` — resolve a pending approval
- `/followup <session_id> <message>` — queue a session-bound follow-up on the shared runtime

Completed daemon tasks emit Telegram completion notifications once per task while the shell is running.
Only one Telegram poller should run per bot token; polling conflicts now fail fast with operator guidance instead of retrying forever.

Month 4 hardening is intentionally scope-locked: keep this surface limited to operator status, approvals, completion notifications, and session-bound follow-ups. Do not treat this shell as approval to add broader Telegram companion features.

## Closed Alpha Readiness

Month 4 lane-4 operator materials now live in the repo:

- `wiki/closed-alpha-setup.md` — install-to-first-task onboarding path
- `wiki/closed-alpha-runbook.md` — rehearsal checklist and evidence capture flow
- `wiki/closed-alpha-known-limits.md` — explicit pre-alpha constraints
- `scripts/run_month4_closed_alpha_checks.sh` — lint/test/build + telemetry verification bundle with optional report archival
- `scripts/alpha_telemetry_report.sh` — local SQLite summary for completion/session/timeout/approval/continuation signals

Use `bash scripts/run_month4_closed_alpha_checks.sh --report-out artifacts/month4-alpha-report.json` for the fast verification pass and a durable telemetry artifact per rehearsal.
This bundle stays fail-closed on product gaps: checked-in docs and telemetry helpers do **not** count as Telegram operator-shell implementation evidence.
Recent hardening also closed two structural follow-ups behind this operator flow: malformed planner output now degrades through deterministic recovery paths instead of opaque `parse subtasks JSON` failures, and latest-session resume now reconciles JSONL transcripts with SQLite-backed history metadata before choosing a continuation candidate. Historical rehearsal artifacts that mention the older failure modes should be read as time-scoped evidence, not current operator guidance.

## Commands

| Command | Purpose | Example |
|---------|---------|---------|
| `run` | Interactive chat mode | `elnath run` |
| `daemon start` | Start background daemon | `elnath daemon start` |
| `daemon submit` | Submit job to daemon | `elnath daemon submit "summarize logs"` |
| `daemon status` | Show queued and running jobs | `elnath daemon status` |
| `daemon stop` | Stop daemon | `elnath daemon stop` |
| `daemon install` | Install daemon as system service | `elnath daemon install` |
| `telegram shell` | Run the thin Telegram operator shell | `elnath telegram shell` |
| `wiki search` | Full-text search wiki | `elnath wiki search "authentication"` |
| `wiki lint` | Validate wiki structure | `elnath wiki lint` |
| `wiki rebuild` | Rebuild FTS5 index | `elnath wiki rebuild` |
| `wiki list` | List all wiki pages | `elnath wiki list` |
| `search` | Search past conversations | `elnath search "deployment issue"` |
| `portability export` | Write encrypted backup bundle | `elnath portability export --out backup.eln` |
| `portability verify` | Decrypt and integrity-check a bundle | `elnath portability verify backup.eln` |
| `portability import` | Restore bundle into a data directory | `elnath portability import backup.eln --dry-run` |
| `portability list` | Show local export history | `elnath portability list` |
| `chaos list` | List fault-injection scenarios | `elnath chaos list` |
| `chaos run` | Execute a fault-injection scenario | `elnath chaos run tool-bash-transient-fail` |
| `chaos report` | Render a chaos run as Markdown | `elnath chaos report latest` |
| `errors` | Look up an ELN-XXX error code | `elnath errors ELN-001` or `elnath errors list` |
| `version` | Show version | `elnath version` |
| `help` | Show command help | `elnath help` |

## Architecture

```
cmd/elnath/           CLI dispatcher and REPL
internal/
  agent/              Agent loop: message -> LLM -> tools -> repeat
  config/             YAML + environment configuration
  conversation/       Intent classification, context compression, history
  core/               App lifecycle, dual SQLite DB, logging, error handling
  daemon/             Unix socket IPC, worker pool, job queue
  llm/                Provider interface, Anthropic/OpenAI/Ollama implementations
  mcp/                MCP client (stdio JSON-RPC), tool adapter
  orchestrator/       Workflow routing (single/team/autopilot/ralph/research)
  research/           Hypothesis -> experiment -> evaluate loop
  self/               Identity, persona, system prompt
  tools/              Bash, File (read/write/edit/glob/grep), Git, Web
  wiki/               Store, FTS5 index, hybrid search, auto-documentation
```

### Core Design

**Message array as sole state**: The agent loop uses a message array as its only state. No hidden state machines, no magic — just messages in, messages out.

**Pluggable LLM providers**: The `llm.Provider` interface supports Anthropic, OpenAI, Ollama, and the OpenAI Responses API. Providers are selected automatically based on available API keys.

**Tool execution with permissions**: Tools are modular and implement a simple interface (`Name`, `Description`, `Schema`, `Execute`). The permission engine checks every tool call against the configured mode before execution.

**Wiki as knowledge base**: Markdown pages with YAML frontmatter are indexed into SQLite FTS5. RAG context is injected into system prompts automatically when relevant wiki content exists.

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
  allow: []       # tools always allowed (bypass permission check)
  deny: []        # tools always denied (overrides allow)

telegram:
  enabled: false
  bot_token: ""   # or ELNATH_TELEGRAM_BOT_TOKEN
  chat_id: ""     # or ELNATH_TELEGRAM_CHAT_ID
  api_base_url: "" # optional, defaults to https://api.telegram.org
  poll_timeout_seconds: 30 # or ELNATH_TELEGRAM_POLL_TIMEOUT_SECONDS

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
| `ELNATH_OPENAI_API_KEY` | OpenAI API key | `sk-...` |
| `ELNATH_DATA_DIR` | Database directory | `~/.elnath/data` |
| `ELNATH_WIKI_DIR` | Wiki pages directory | `~/.elnath/wiki` |
| `ELNATH_LOG_LEVEL` | Logging level | `info`, `debug`, `warn`, `error` |
| `ELNATH_PERMISSION_MODE` | Permission mode | `default`, `accept_edits`, `plan`, `bypass` |
| `ELNATH_TELEGRAM_ENABLED` | Enable Telegram operator shell config | `true` |
| `ELNATH_TELEGRAM_BOT_TOKEN` | Telegram bot token | `123456:ABC...` |
| `ELNATH_TELEGRAM_CHAT_ID` | Telegram operator chat ID | `123456789` |
| `ELNATH_TELEGRAM_POLL_TIMEOUT_SECONDS` | Telegram long-poll timeout | `30` |
| `ELNATH_OLLAMA_BASE_URL` | Ollama server URL | `http://localhost:11434` |
| `ELNATH_OLLAMA_MODEL` | Ollama model name | `llama3.2` |

Priority: environment variables override config file values.

### MCP Servers

Connect external tool servers via [Model Context Protocol](https://modelcontextprotocol.io/). Elnath launches each server as a subprocess and communicates over stdio using JSON-RPC.

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]

  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      - "GITHUB_TOKEN=ghp_..."

  - name: postgres
    command: npx
    args: ["-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/mydb"]
```

MCP tools are registered with an `mcp_` prefix (e.g., `mcp_read_file` from the filesystem server). Server failures are non-fatal — a server that fails to start is logged and skipped.

### Hooks

Hooks run shell commands before or after tool execution. Use them for auto-formatting, validation, notifications, or custom workflows.

```yaml
hooks:
  - matcher: "edit_file"
    post_command: "gofmt -w ${TOOL_FILE}"

  - matcher: "bash"
    pre_command: "echo 'Running: ${TOOL_INPUT}' >> ~/.elnath/audit.log"

  - matcher: "*"
    post_command: "notify-send 'Elnath' 'Tool ${TOOL_NAME} completed'"
```

- `matcher`: glob pattern matched against tool names (`*` matches all)
- `pre_command`: runs before tool execution; if it fails, the tool call is denied
- `post_command`: runs after tool execution

### Permission Modes

| Mode | Behavior |
|------|----------|
| `default` | Prompts for approval on non-read-only tools |
| `accept_edits` | Auto-approves file read/write/edit tools; prompts for bash, git, MCP |
| `plan` | Only allows read-only tools (read_file, glob, grep, wiki_search, etc.) |
| `bypass` | Approves everything without prompting |

Tools are classified as:
- **Read-only**: `read_file`, `glob`, `grep`, `web_fetch`, `web_search`, `wiki_search`, `wiki_read`, `conversation_search`
- **Edit**: `write_file`, `edit_file`, `wiki_write`
- **Exec**: `bash`, `git` (require explicit approval in default/accept_edits modes)

### Cross-Project Intelligence

Link other Elnath projects to search across their wiki and conversation history:

```yaml
projects:
  - name: backend
    data_dir: ~/projects/backend/.elnath/data
    wiki_dir: ~/projects/backend/.elnath/wiki
  - name: frontend
    data_dir: ~/projects/frontend/.elnath/data
    wiki_dir: ~/projects/frontend/.elnath/wiki
```

## Portability (Backup & Restore)

`elnath portability export` writes an AES-256-GCM encrypted bundle (`.eln`) that
can be verified, inspected, and restored later. Bundles stream in 16 MiB chunks
so multi-hundred-MB data directories do not require loading the whole payload
into memory. Passphrases shorter than 8 characters are rejected; 8-11 character
passphrases prompt for confirmation on a TTY (or emit a warning on stderr in
non-interactive use); 12+ characters pass silently.

### Selecting what to include with `--scope`

By default a bundle includes every category: `config`, `db`, `wiki`, `lessons`,
and `sessions`. Pass `--scope` with a comma-separated subset to limit it. This
matters most when the sessions directory grows large — hundreds of JSONL
transcripts can dominate bundle size and export time.

```bash
# Full bundle (everything)
elnath portability export --out ~/backups/full.eln

# Lean bundle without sessions (useful when sessions dir is hundreds of MB)
elnath portability export --scope config,db,wiki,lessons --out ~/backups/lean.eln

# Sessions-only archive before pruning old transcripts
elnath portability export --scope sessions --out ~/backups/sessions-$(date +%F).eln
```

Unknown scope names produce `unknown portability scope: <name>` so typos fail
fast instead of silently excluding data.

### Verify and restore

```bash
elnath portability verify bundle.eln                         # integrity check
elnath portability import bundle.eln --dry-run               # preview file plan
elnath portability import bundle.eln --target ~/restored     # actual restore
```

`list` prints local export history from `<data-dir>/portability/history/` (one
JSON record per export) so you can track when and where recent bundles were
written.

## Workflows

Intent classification routes messages to one of five workflows:

### Single

Immediate response from a single LLM call. For questions, clarifications, one-off tasks.

### Team

Coordinated multi-agent execution. Router agent breaks work into subtasks, executes in parallel, synthesizes results. For feature development, refactoring, complex analysis.

### Autopilot

Full autonomous execution from goal to completion. Research, planning, implementation, verification. For ambitious features, architectural decisions, large refactors.

### Ralph

Verification loop: execute -> verify -> refine -> repeat until success criteria met. For bug fixes, tests, critical systems.

### Research

Hypothesis-driven investigation: propose hypothesis -> design experiment -> execute -> evaluate -> update wiki. For exploratory work, understanding design tradeoffs, evaluating technologies.

## Requirements

- **Go 1.25+** — Uses modernc.org/sqlite for pure Go SQLite (no CGo required)
- **macOS or Linux** — Tested on both platforms
- **API key** — At least one LLM provider: Anthropic Claude, OpenAI, or Ollama (local)

## Building from Source

```bash
git clone https://github.com/stello/elnath
cd elnath

make build    # Build binary
make test     # Run tests with race detector
make lint     # go vet + staticcheck
make run      # Build and run interactive mode
```

## License

Apache License 2.0. See LICENSE file for details.
