# Changelog

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
- Research workflow deps not wired (always falls back to single)
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
