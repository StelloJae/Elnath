# Hermes Agent: Exhaustive Audit

**Date**: 2026-04-12
**Source**: `~/.hermes/hermes-agent/` (production source, ~80K LOC)
**Method**: 8 parallel agents, 42K LOC line-by-line 전수 조사
**Files covered**: run_agent.py (9672), prompt_builder.py (990), file_tools.py (835), file_operations.py (1082), context_compressor.py (745), trajectory_compressor.py (1517), terminal_tool.py (1757), registry.py (335), approval.py (877), code_execution_tool.py (1347), auxiliary_client.py (2296), anthropic_adapter.py (1408), model_metadata.py (1005), error_classifier.py (792), smart_model_routing.py (194), memory_manager.py (367), credential_pool.py (1211), rate_limit_tracker.py (242), hermes_state.py (1300), gateway/run.py (7688), gateway/session.py (1082), batch_runner.py (1287), hermes_cli/config.py (2817), + 20 smaller files

---

## A. Agent Loop Architecture

### Main Loop (run_agent.py:7315-9257)
```
while api_call_count < max_iterations AND budget.remaining > 0:
    1. Check interrupt
    2. Consume one iteration from budget
    3. Build API messages (strip internal fields)
    4. Inject system prompt, prefill, ephemeral context
    5. Apply prompt caching breakpoints
    6. Sanitize orphaned tool results
    7. API call (with retry loop inside)
    8. Normalize response across 3 API modes
    9. If tool calls → validate, execute, append results, check compression, continue
    10. If no tool calls → final response, break
```

### Key Constants
- `max_iterations`: 90 (default, configurable via HERMES_MAX_ITERATIONS)
- `compression_threshold`: 50% of context window
- `compression_target_ratio`: 20% (tail protection)
- `terminal_timeout`: 180s
- `terminal_max_output`: 50,000 chars
- `result_persist_threshold`: 100,000 chars per tool
- `turn_budget`: 200,000 chars aggregate
- `preview_size`: 1,500 chars after persistence
- `file_read_max_chars`: 100,000
- `max_read_lines`: 2,000
- `max_line_length`: 2,000

---

## B. Anti-Exploration Mechanisms (Elnath에 가장 중요)

### B1. Read-Dedup (file_tools.py:327-354)
Per-task tracker: `{(resolved_path, offset, limit) → mtime}`. 파일이 변경되지 않았으면 stub 반환:
```
"File unchanged since last read. The content from the earlier read_file result in this conversation is still current — refer to that instead of re-reading."
```
mtime은 성공적 read 후 기록 (line 419-424). Context compression 후 dedup 캐시 클리어 (`reset_file_dedup()`, line 483-503).

### B2. Consecutive Loop Blocking (file_tools.py:401-442)
read_key = ("read", path, offset, limit). 같은 key로 연속 호출:
- **3회**: warning: `"You have read this exact file region {count} times consecutively. The content has not changed. Use the information you already have."`
- **4회**: HARD BLOCK: `"BLOCKED: You have read this exact file region {count} times in a row. The content has NOT changed. STOP re-reading and proceed with your task."`
- **search_files에도 동일 패턴** (line 670-706)

### B3. Inter-Tool Reset (file_tools.py:506-519)
`read_file`/`search_files` 이외의 tool 호출 시 consecutive 카운터 리셋. Dispatcher에서 자동 호출 (model_tools.py:489-494).

### B4. Codex Ack-Continuation (run_agent.py:1564-1633)
모델이 "I'll look into..."처럼 계획만 말하고 tool을 안 부르면 감지:
```
"[System: Continue now. Execute the required tool calls and only send your final answer after completing the task.]"
```
최대 2회 재시도.

### B5. Budget Pressure (run_agent.py:6789-6811)
- **70% 소진**: `"[BUDGET: Iteration {N}/{max}. {remaining} iterations left. Start consolidating your work.]"`
- **90% 소진**: `"[BUDGET WARNING: Iteration {N}/{max}. Only {remaining} iteration(s) left. Provide your final response NOW. No more tool calls unless absolutely critical.]"`
마지막 tool result의 JSON에 `_budget_warning` 키로 주입. Replay 시 strip.

### B6. Max Iterations Handler (run_agent.py:6852-7001)
Budget 소진 시:
1. `"You've reached the maximum number of tool-calling iterations. Provide a final response summarizing what you've found."` user message 주입
2. API call에서 **tools 키 자체 제거** → 물리적으로 tool call 불가
3. 1회 retry 후 fallback string

### B7. File Size Guards (3 layers)
1. **100K chars hard reject** (file_tools.py:362-383) — offset/limit 사용 안내
2. **2000 lines cap** (file_operations.py:296) — limit 파라미터 clamped
3. **2000 chars/line** (file_operations.py:387) — 긴 줄 truncation + "[truncated]"

### B8. Large File Hint (file_tools.py:392-399)
파일 > 512KB AND limit > 200 AND truncated일 때:
```
"This file is large ({size} bytes). Consider reading only the section you need with offset and limit to keep context usage efficient."
```

---

## C. Tool Descriptions (원문)

### read_file
```
Read a text file with line numbers and pagination. Use this instead of cat/head/tail in terminal. Output format: 'LINE_NUM|CONTENT'. Suggests similar filenames if not found. Use offset and limit for large files. Reads exceeding ~100K characters are rejected; use offset and limit to read specific sections of large files. NOTE: Cannot read images or binary files — use vision_analyze for images.
```

### write_file
```
Write content to a file, completely replacing existing content. Use this instead of echo/cat heredoc in terminal. Creates parent directories automatically. OVERWRITES the entire file — use 'patch' for targeted edits.
```

### patch
```
Targeted find-and-replace edits in files. Use this instead of sed/awk in terminal. Uses fuzzy matching (9 strategies) so minor whitespace/indentation differences won't break it. Returns a unified diff. Auto-runs syntax checks after editing.
Replace mode (default): find a unique string and replace it.
Patch mode: apply V4A multi-file patches for bulk changes.
```

### search_files
```
Search file contents or find files by name. Use this instead of grep/rg/find/ls in terminal. Ripgrep-backed, faster than shell equivalents.
Content search (target='content'): Regex search inside files. Output modes: full matches with line numbers, file paths only, or match counts.
File search (target='files'): Find files by glob pattern (e.g., '*.py', '*config*'). Also use this instead of ls — results sorted by modification time.
```

### terminal
```
Execute shell commands on a Linux environment. Filesystem usually persists between calls.

Do NOT use cat/head/tail to read files — use read_file instead.
Do NOT use grep/rg/find to search — use search_files instead.
Do NOT use ls to list directories — use search_files(target='files') instead.
Do NOT use sed/awk to edit files — use patch instead.
Do NOT use echo/cat heredoc to create files — use write_file instead.
Reserve terminal for: builds, installs, git, processes, scripts, network, package managers, and anything that needs a shell.
[...foreground/background/pty/workdir guidance...]
```

---

## D. System Prompt Assembly (12 layers)

1. **Identity** — SOUL.md or DEFAULT_AGENT_IDENTITY (60 words)
2. **Tool-aware guidance** — MEMORY_GUIDANCE, SESSION_SEARCH_GUIDANCE, SKILLS_GUIDANCE (conditional)
3. **Nous subscription** — managed web/image/TTS capabilities
4. **Tool-use enforcement** — model-conditional (GPT/Codex/Gemini/Grok only, Claude는 없음)
5. **User/gateway system message**
6. **Persistent memory** — frozen snapshot (MEMORY.md + USER.md)
7. **External memory provider**
8. **Skills index** — compact category tree, 60-char descriptions
9. **Context files** — first-match: .hermes.md > AGENTS.md > CLAUDE.md > .cursorrules (20K cap each)
10. **Timestamp + metadata**
11. **Alibaba model workaround** (alibaba provider only)
12. **Platform hint** (platform-specific formatting guidance)

Claude 모델은 추가 guidance가 없음 (instructions를 잘 따른다고 가정).

---

## E. Context Management

### 3-Layer Tool Result Control
1. **Per-tool truncation** — terminal: 50K chars (40% head / 60% tail)
2. **Per-result persistence** — > 100K chars → sandbox 파일 + 1.5K preview. `read_file`은 float('inf')로 면제 (persist→read→persist 무한루프 방지)
3. **Per-turn aggregate budget** — > 200K chars → biggest-first persistence

### 2-Phase Context Compression
**Phase 1 (무료)**: 오래된 tool results → `"[Old tool output cleared to save context space]"` placeholder
**Phase 2 (LLM 호출)**: middle turns → 9-section structured summary

Summary template (9 sections):
```
## Goal
## Constraints & Preferences
## Progress (Done / In Progress / Blocked)
## Key Decisions
## Relevant Files
## Next Steps
## Critical Context
## Tools & Patterns
```

Iterative update prompt: "PRESERVE all existing information. Accumulate across compactions."

### Pre/Post Compression Actions
1. **Pre**: flush memories (모델에게 "중요한 것 기억" 1회 기회)
2. **Pre**: external memory provider notification
3. **Post**: TODO list re-injection (task tracking 생존)
4. **Post**: system prompt invalidation + rebuild
5. **Post**: session DB split (parent_session_id 링크)
6. **Post**: file dedup cache clear
7. **Post**: repeated compression warning (≥2회: "accuracy may degrade")
8. **Summary failure**: 10분 cooldown → 순수 삭제 (요약 없이)

---

## F. Error Handling

### 13-Category Error Classifier
`auth`, `auth_permanent`, `billing`, `rate_limit`, `overloaded`, `server_error`, `timeout`, `context_overflow`, `payload_too_large`, `model_not_found`, `format_error`, `thinking_signature`, `long_context_tier`, `unknown`

각 분류에 recovery 힌트: `retryable`, `should_compress`, `should_rotate_credential`, `should_fallback`

### Key Heuristics
- **402 disambiguation**: transient ("try again") → rate_limit(재시도), permanent → billing(fallback)
- **Disconnect + large session → context overflow**: `tokens > context * 0.6 OR > 120K OR > 200 msgs`
- **OpenRouter metadata.raw parsing**: wrapper 안의 실제 에러 추출

### Retry Strategy
- Jittered exponential backoff: 5s base, 120s max, golden-ratio decorrelated jitter
- Max 3 retries per API call
- Interruptible sleep (200ms increments)
- Fallback provider chain with per-turn primary restoration

---

## G. Smart Model Routing

Heuristic only (no LLM classification):
- max_simple_chars: 160
- max_simple_words: 28
- Blocks: multiline, code blocks, URLs, complex keywords (30+ words: debug, implement, refactor, etc.)
- 통과 시 cheap model로 라우팅

---

## H. Parallel Tool Execution

- Whitelist: `read_file`, `search_files`, `web_search`, `web_extract`, `vision_analyze`, `session_search`, `skills_list`, `skill_view`, + Home Assistant tools
- Path-scoped tools (`read_file`, `write_file`, `patch`): 다른 경로면 병렬 OK
- `clarify`: 절대 병렬 금지
- ThreadPoolExecutor, max_workers = min(num_tools, 8)

---

## I. Security

### Context File Injection Scanner (10 patterns)
`ignore previous instructions`, `do not tell the user`, `system prompt override`, `disregard your rules`, `bypass restrictions`, hidden HTML comments, hidden divs, `translate...and execute`, `curl...$KEY`, `cat .env`

### Invisible Unicode Detection
U+200B (ZWSP), U+200C (ZWNJ), U+200D (ZWJ), U+2060 (word joiner), U+FEFF (BOM), U+202A-U+202E (bidi)

### 41 API Key Prefix Redaction Patterns
sk-, ghp_, github_pat_, gho_, ghu_, ghs_, ghr_, xox[baprs]-, AIza, pplx-, fal_, fc-, bb_live_, gAAAA, AKIA, sk_live_, sk_test_, rk_live_, SG., hf_, r8_, npm_, pypi-, dop_v1_, doo_v1_, am_, sk_, tvly-, exa_, gsk_, syt_, retaindb_, hsk-, mem0_, brv_

### Write Path Restrictions
- System paths: /etc/, /boot/, /usr/lib/systemd/, docker.sock
- Sensitive files: ~/.ssh/*, ~/.aws/*, ~/.gnupg/*, ~/.kube/*, /etc/sudoers*, ~/.docker/*, ~/.azure/*, ~/.config/gh/*
- HERMES_WRITE_SAFE_ROOT: sandbox 외부 쓰기 금지

### Dangerous Command Detection (30+ patterns)
rm -rf, chmod 777, mkfs, dd, SQL DROP, systemctl stop, kill -9 -1, fork bombs, curl|bash, etc.
ANSI escape + null byte + Unicode NFKC normalization 후 매칭 (obfuscation 방지)

---

## J. Unique Patterns (Claude Code에 없는 것)

1. **execute_code tool** — Python 스크립트로 여러 tool을 프로그래밍 방식으로 호출. 3+ tool call chains을 1 turn으로 압축. Budget refund 적용.
2. **Codex ack-continuation detection** — 계획만 말하고 실행 안 하면 강제 재시도
3. **Staleness detection** — 외부에서 파일 수정 시 write 전 경고
4. **Post-write timestamp refresh** — 자기가 쓴 파일에 false staleness 방지
5. **Dedup reset on compression** — 압축 후 원본 내용이 사라지므로 dedup 캐시 클리어
6. **search_files에도 loop blocking** — read뿐 아니라 search도 같은 패턴
7. **8-strategy fuzzy match** — edit 시 whitespace/indentation 차이 자동 해결
8. **Filesystem checkpoints before mutations** — write/patch 전 rollback 가능한 snapshot
9. **Pre-compression memory flush** — 압축 전 중요 정보 저장 기회
10. **TODO re-injection after compression** — task state 생존
11. **Agent cache with config signature** — 메시지 간 agent 인스턴스 재사용
12. **Sentinel concurrency guard** — async 환경에서 중복 agent 방지
13. **Context length 10-step resolution** — explicit config → persistent cache → /models API → local server → Anthropic API → OpenRouter → Nous → models.dev → hardcoded → 128K fallback
14. **Thinking-budget exhaustion detection** — 모든 토큰을 reasoning에 써서 visible content 없을 때 감지
15. **Deterministic call IDs** — sha256(fn:args:idx)[:12]로 prompt cache 보존

---

## K. Elnath Gate Retry에 적용할 것 (우선순위 순)

### MUST (P0)
1. Tool description 재작성 — "Use this instead of X" 패턴 + behavioral steering
2. Read-dedup + consecutive loop blocking (file_tools.py 패턴 Go 포팅)
3. search에도 loop blocking 적용
4. Budget pressure injection (70%/90%)
5. Max iterations handler with tools removal

### SHOULD (P1)
6. Tool result size cap (100K per-tool, 200K per-turn)
7. Codex ack-continuation detection (계획만 말하면 강제 실행)
8. Staleness detection on write
9. Post-write timestamp refresh
10. Dedup reset on context compression

### NICE (P2)
11. Tool name repair via fuzzy match
12. Duplicate tool call dedup
13. Structured error classifier (13 categories)
14. 8-strategy fuzzy match for edit
15. Context file injection scanner

---

## L. Phase 2 전수 조사 — 잔여 ~25K LOC (8팀 병렬, 2026-04-12)

### L1. Gateway Platform Adapters (17 platforms)

**공통 아키텍처**: `BasePlatformAdapter` ABC → `connect/disconnect/send/get_chat_info` 필수, `send_image/voice/video/document/typing/edit` 선택. 모든 인바운드 → `MessageEvent` 정규화. 모든 미디어 → 로컬 캐시 다운로드 (플랫폼 CDN URL은 만료됨).

**플랫폼별 핵심 패턴**:

| Platform | Transport | Max Msg | Markdown | 고유 패턴 |
|----------|-----------|---------|----------|----------|
| Telegram | Long-poll/Webhook | 4096 | MarkdownV2 (엄격) | 앨범 배칭, 텍스트 배칭 0.6s, 포럼 토픽, 스티커 비전 분석, DNS-over-HTTPS 네트워크 우회 |
| Discord | WebSocket | 2000 | 패스쓰루 | 양방향 음성(RTP/Opus), 자동 스레드, 슬래시 커맨드 100개 |
| Slack | Socket Mode | 39000 | mrkdwn | AI Assistant API, 멀티 워크스페이스, 스레드 컨텍스트 fetching |
| Feishu | WS/Webhook | - | md post | 스레드 브리징, 영속 dedup, 배치 시스템, 카드 액션 라우팅, 웹훅 HMAC+rate limit |
| Matrix | nio sync | - | HTML | E2EE(Megolm), 키 관리, 암호화 미디어 복호화, 자동 스레드 |
| WeCom | WebSocket | - | markdown | 커스텀 JSON-RPC WS 프로토콜, 512KB 청크 미디어 업로드, 세션 웹훅 응답 |
| API Server | HTTP REST | - | - | OpenAI-compatible /v1/chat/completions + /v1/responses + /v1/runs SSE, idempotency 캐시 |
| WhatsApp | Node.js bridge | - | - | 서브프로세스 관리, 2단계 헬스체크, 세션 락 |
| Signal | SSE inbound | - | - | JSON-RPC outbound, 헬스 모니터 120s, Unicode 멘션 렌더링, 편집 메시지 |
| Email | IMAP/SMTP | - | - | 자동 발신자 필터링, UID dedup, 스레드 헤더(In-Reply-To/References) |
| SMS | Twilio webhook | 1600 | 없음 | TwiML 응답, Basic Auth |
| BlueBubbles | REST+webhook | - | 없음 | iMessage GUID 해석, Private API 기능 감지, tapback 리액션 |
| Mattermost | REST+WS | - | 패스쓰루 | 최경량 어댑터, 외부 SDK 없음 |
| DingTalk | Stream SDK | - | markdown | 세션 웹훅 응답 패턴, SDK 스레드 브리징 |
| HomeAssistant | WS events | - | - | 이벤트 기반(state_changed), 엔티티별 cooldown |
| Webhook | HTTP receiver | - | - | 라우트 기반, GitHub/GitLab HMAC, 프롬프트 템플릿, 크로스플랫폼 전달 |

**공통 패턴 (Elnath 필수 구현)**:
1. Token lock (`acquire_scoped_lock`) — 중복 인스턴스 방지
2. Message dedup cache — TTL+size cap dict
3. SSRF protection — 모든 URL fetch 전
4. 로컬 미디어 캐시 — 플랫폼 CDN URL 만료 대비
5. 문서 텍스트 주입 — .md/.txt 100KB 이하 → event.text에 직접 주입
6. @mention 게이트 — 그룹에서 멘션 필요, free-response 채널 bypass
7. 플랫폼별 마크다운 변환 — placeholder 기반 보호 패턴
8. 타이핑 인디케이터 — 플랫폼마다 다름 (one-shot/persistent loop/setStatus)
9. 인터랙티브 승인 — inline keyboard/discord.ui.View/Block Kit

### L2. CLI Architecture

**Setup/Auth (16K LOC)**:
- 17 provider OAuth 지원 (Nous, Codex, Anthropic, Copilot, Qwen + 12 API-key)
- Credential pool: fill_first/round_robin/least_used/random 전략, 소진 cooldown 1시간
- Profile system: `~/.hermes/profiles/<name>/`로 완전 격리, 쉘 래퍼 스크립트
- First-run guard → setup wizard (Quick/Full), 비대화형 감지
- Auth store: fcntl file lock, atomic write, 0600 권한

**CLI Tools & UX (15K LOC)**:
- Plugin system: 10 lifecycle hooks, pre_llm_call context injection, curses TUI
- Doctor --fix: 자가 치유 진단 도구 (디렉토리 생성, 설정 마이그레이션, WAL 체크포인트)
- Skin engine: YAML 기반 데이터 드리븐 테마, 상속 모델
- Model switching: 2-path 해석 (explicit provider vs alias cascade), DirectAlias 사용자 단축키
- MCP config: add/remove/list/test/configure/serve 전체 라이프사이클
- Skills Hub: 보안 파이프라인 (fetch→quarantine→scan→policy→confirm→install)
- Command registry: 단일 진실 소스, 플랫폼별 파생 (Telegram 32자, Discord 100자)

### L3. Browser/Vision/TTS/Image (Roadmap Note)

| 도구 | 아키텍처 | 핵심 결정 | 외부 의존성 |
|------|---------|----------|------------|
| Browser | 4 backend (cloud/CDP/local/Camofox), accessibility tree | SSRF+redirect guard, 비활성 5분 reaper, atexit cleanup | agent-browser CLI, Node.js |
| Vision | async download + base64 → auxiliary LLM | SSRF redirect hook, magic byte 검증, 3회 retry | 설정된 vision 모델 |
| TTS | 5 provider (Edge/ElevenLabs/OpenAI/MiniMax/NeuTTS) | Opus for Telegram, sentence pipeline streaming | provider별 SDK |
| Image | FAL.ai FLUX 2 Pro + 2x upscale | Sync API (async lifecycle 문제 회피), managed gateway fallback | fal_client SDK |
| Voice | Push-to-talk, sounddevice | CoreAudio hang 방지 timeout, Whisper hallucination filter | sounddevice, numpy |
| STT | 5 provider (faster-whisper/CLI/Groq/OpenAI/Mistral) | Singleton 모델, 자동 감지 cascade | faster_whisper |

### L4. RL Training Pipeline

**GRPO + On-Policy Distillation**:
- 대상 모델: Qwen/Qwen3-8B, LoRA rank 32
- 프레임워크: Tinker-Atropos (3 프로세스: API server + trainer+sglang + environment)
- AgenticOPDEnv: tool result에서 dense token-level training signal 추출
- Trajectory 수집 → ScoredDataGroup → trainer GRPO 업데이트
- Toolset distributions: 15개 확률 분포로 훈련 데이터 다양성 확보
- mini_swe_runner: 단일 terminal tool + 완료 시그널, 기본 max 15 iterations

### L5. Core Tools 잔여

| Tool | LOC | 핵심 패턴 |
|------|-----|----------|
| web_tools | 2101 | 4 backend (Exa/Firecrawl/Parallel/Tavily), LLM 요약 파이프라인, SSRF+policy |
| mcp_tool | 2186 | 전용 event loop, 자동 재연결, sampling rate limit, OSV malware check |
| session_search | 504 | FTS5 + LLM 요약, 세션 lineage 해석, 현재 세션 제외 |
| send_message | 982 | 17 플랫폼 라우팅, 세션 미러링, cron 중복 방지 |
| cronjob | 532 | 통합 action tool, prompt injection 스캔 10패턴, 스크립트 경로 containment |
| process_registry | 990 | 200KB rolling buffer, JSON checkpoint 크래시 복구, 완료 notification |
| todo | 268 | 압축 후 active items 재주입, merge/replace 모드 |
| mixture_of_agents | 562 | 4 frontier LLM 병렬 → Claude Opus 통합, graceful degradation |
| website_policy | 282 | 30초 TTL 캐시, fail-open, glob 패턴 |
| env_passthrough | 107 | ContextVar 세션 격리, skill 선언 기반 동적 allowlist |

### L6. Security Infrastructure

| 레이어 | 구현 |
|--------|------|
| Tirith scanner | 외부 Rust 바이너리, cosign provenance 검증, homograph/pipe-to-shell/injection |
| OSV malware check | MCP 패키지 설치 전 Google OSV API, MAL-* advisory만 차단 |
| Skills guard | 103 regex 패턴 12 카테고리 + LLM second pass + 구조 이상 탐지 |
| Credential files | HERMES_HOME containment, symlink sanitization, ContextVar 격리 |
| Context file scanner | 10 prompt injection 패턴 + invisible unicode 감지 |
| Memory scanner | 12 threat 패턴 (injection, exfiltration, SSH backdoor) |
| 41 API key prefix redaction | sk-, ghp_, AKIA, AIza 등 |
| Dangerous command patterns | 30+ 패턴, ANSI+null+NFKC 정규화 후 매칭 |

### L7. Protocol Surfaces

**MCP Server** (10 tools over stdio):
conversations_list, conversation_get, messages_read, attachments_fetch, events_poll, events_wait, messages_send, channels_list, permissions_list_open, permissions_respond

**ACP Server** (JSON-RPC over stdio):
initialize, authenticate, new_session, load_session, resume_session, fork_session, list_sessions, prompt, cancel, set_session_model, set_session_mode, set_config_option + 슬래시 커맨드 (/help, /model, /tools, /context, /reset, /compact, /version)

### L8. Sandbox Environments (6 backends)

| Backend | 보안 모델 | 영속성 | 파일 동기화 |
|---------|----------|--------|------------|
| Docker | --cap-drop ALL +3, --no-new-privileges, --pids-limit 256 | bind mount overlay | credential+skills ro mount |
| Modal | SDK sandbox | filesystem snapshot | base64 bash push, mtime dedup |
| Singularity | --containall --no-home | writable overlay | N/A |
| Daytona | SDK sandbox | stop/resume | SDK upload, mtime dedup |
| SSH | ControlMaster multiplex | remote fs | rsync |
| Managed Modal | HTTP gateway | snapshot before terminate | 미지원 (direct 권장) |
