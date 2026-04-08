# Changelog

## v0.3.0 (2026-04-08)

### Onboarding Wizard
- **Bubbletea TUI wizard**: Polished first-run experience with Charm CLI aesthetics (Lipgloss styling, spinners, progress indicators)
- **Dual-path onboarding**: Quick Start (3 steps, 2 min) and Full Setup (8 steps, 5 min)
- **Full CLI i18n**: English/Korean with locale persistence in config.yaml, `ELNATH_LOCALE` env var
- **Non-interactive fallback**: `--non-interactive` flag + environment variable config for CI/pipe environments
- **MCP catalog**: Curated checkbox list of MCP servers grouped by category
- **API key live validation**: Spinner + real HTTP call to verify keys
- **Rerun mode**: `elnath setup` pre-fills existing config values with "reconfigure" badge

### Architecture Fixes
- **Wiki Karpathy completion**: LLM-based entity/concept extraction pipeline (`extract.go`), auto-creates typed wiki pages from conversations at session end
- **Research workflow wired**: `ResearchDeps` now injected — research intent triggers the full hypothesis→experiment→wiki loop
- **Context window compression activated**: `CompressMessages()` with LLM topic-based summarization (was always hard-snipping). Configurable `compress_threshold` and `max_context_tokens` in config.yaml
- **IntentWikiQuery routing**: Wiki queries now correctly classified and routed
- **RoutingContext heuristic**: File-count estimation enables team workflow for multi-file tasks
- **Session dual-persist**: `SessionPersister` interface for SQLite backup alongside JSONL primary
- **OpenAI output tokens**: Cost tracking now includes completion tokens

### Claude Code Feature Parity
- **Prompt caching**: `cache_control: ephemeral` on system prompt + last tool definition. Cache read/write token tracking
- **Parallel tool execution**: 2-phase pattern — sequential permission check, then goroutine-parallel execution with ordered result collection
- **Web search**: Real DuckDuckGo HTML search (stub removed)
- **Session resume**: `--continue` (latest session) and `--session <id>` CLI flags
- **Extended thinking**: `ThinkingBlock` type, `ThinkingBudget` parameter, SSE thinking delta handling
- **Image support**: `ImageBlock` with base64 source, Anthropic API image format marshaling
- **Token estimation**: JSON/code density detection (~3.5 chars/token vs ~4 for prose), image block estimation

### Distribution
- **goreleaser**: Cross-platform release builds (darwin/linux/windows, amd64/arm64)
- **Homebrew**: `brew install StelloJae/tap/elnath` (via goreleaser auto-generated formula)
- **go install**: `go install github.com/stello/elnath/cmd/elnath@latest`

### Improvements
- Locale validation in config (rejects unsupported locales)
- 70+ new tests across onboarding, config, wiki, and tools packages
- Test coverage: onboarding 76.6%, config 92.6%, wiki 66%+, all packages 60%+

### Known Limitations (carried from v0.2.0)
- Auto-doc session ingestion does not filter sensitive information
- MCP response ID not validated
- Daemon bypasses orchestrator/wiki/session layers
- Bash/ReadTool output size unbounded

## v0.2.0 (2026-04-07)

### New Features
- **MCP server integration**: Connect external tool servers via Model Context Protocol (stdio JSON-RPC). Tools are auto-discovered and registered with `mcp_` prefix.
- **Hook system**: Pre/post tool execution hooks for validation, formatting, and custom workflows. Configurable via `hooks` in config.yaml.
- **Ollama provider**: Local LLM support via Ollama with configurable base URL and model.
- **Cross-project intelligence**: Search wiki and conversation history across linked Elnath projects.
- **Persona presets**: Switch agent persona via `--persona` flag for context-appropriate behavior.
- **Hierarchical summarization**: Context window management with multi-level conversation compression.
- **OpenAI tool_use support**: Function calling support for OpenAI and Responses API providers.

### Bug Fixes
- **Permission tool name mismatch** (CRITICAL): `isReadOnly` and `isEditTool` now use actual registered tool names (`read_file`, `write_file`, `edit_file`, `git`) instead of stale names (`read`, `write`, `edit`, `git_log`). Wiki and conversation search tools are now correctly classified.
- **Permission not propagated to orchestrator** (CRITICAL): `WorkflowConfig` now carries a `Permission` field, ensuring all agents created by workflows (single, team, ralph) respect the configured permission mode.
- **Intent routing**: Complex tasks default to team workflow; added `wiki_query` intent.
- **Tool working directory**: Tools now execute in the current working directory, not `dataDir`.
- **Model provider routing**: Model name prefix selects the correct provider automatically.

### Improvements
- Test coverage: all packages at 60%+ (config 92%, mcp 68%, orchestrator 88%, tools 85%)
- 22,500+ Go LOC across 13 packages
- README rewritten for public release with MCP/Hooks/Permission documentation

### Known Limitations
- ~~Research workflow deps not wired~~ → Fixed in v0.3.0
- Auto-doc session ingestion does not filter sensitive information
- MCP response ID not validated
- Daemon bypasses orchestrator/wiki/session layers
- Bash/ReadTool output size unbounded

## v0.1.0 (2026-03-28)

Initial release.

- Agent loop with message-array-only state
- Anthropic Claude and OpenAI providers
- Tool executor: bash, file read/write/edit/glob/grep, git, web fetch/search
- Wiki with FTS5 hybrid search
- 5 workflow modes: single, team, autopilot, ralph, research
- Session persistence (JSONL) with fork support
- Background daemon with Unix socket IPC
- 4 permission modes: default, accept_edits, plan, bypass
- Self model with adaptive system prompt
