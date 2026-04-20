package onboarding

// Locale represents a supported language.
type Locale string

const (
	En Locale = "en"
	Ko Locale = "ko"
)

var translations = map[Locale]map[string]string{
	En: {
		"welcome.title":      "Welcome to Elnath",
		"welcome.subtitle":   "Your autonomous AI assistant",
		"welcome.quick":      "Quick Start",
		"welcome.quick.desc": "API key → smoke test (2 min)",
		"welcome.full":       "Full Setup",
		"welcome.full.desc":  "All options: multi-API, directories, permissions, MCP (5 min)",
		"welcome.navigate":   "↑/↓ to select • enter to confirm",
		"welcome.version":    "v%s",

		"lang.title":    "Language / 언어",
		"lang.en":       "English",
		"lang.ko":       "한국어",
		"lang.navigate": "↑/↓ to select • enter to confirm",

		"apikey.title":       "API Key Setup",
		"apikey.prompt":      "Enter your Anthropic API key:",
		"apikey.placeholder": "sk-ant-...",
		"apikey.validating":  "Validating API key...",
		"apikey.valid":       "API key is valid!",
		"apikey.invalid":     "Invalid API key. Please try again.",
		"apikey.error":       "Validation failed: %s (key saved anyway)",
		"apikey.skip":        "Press enter to skip (can set later via ELNATH_ANTHROPIC_API_KEY)",
		"apikey.navigate":    "enter to confirm • esc to go back",

		"dir.title":    "Directory Setup",
		"dir.data":     "Data directory:",
		"dir.wiki":     "Wiki directory:",
		"dir.default":  "(default: %s)",
		"dir.navigate": "enter to confirm • tab to next field • esc to go back",

		"perm.title":             "Permission Mode",
		"perm.subtitle":          "Choose how Elnath handles tool execution permissions",
		"perm.default":           "Default",
		"perm.default.desc":      "Asks for confirmation on non-read-only tools not in allow/deny lists. Balanced safety and usability.",
		"perm.accept_edits":      "Accept Edits",
		"perm.accept_edits.desc": "Auto-approves file reads and edits. Asks for other tools like shell commands.",
		"perm.plan":              "Plan",
		"perm.plan.desc":         "Read-only mode. Denies all write and execute tools. Safe for reviewing plans.",
		"perm.bypass":            "Bypass",
		"perm.bypass.desc":       "Approves everything without prompting. Use only in trusted environments.",
		"perm.recommended":       "★ Recommended",
		"perm.navigate":          "↑/↓ to select • enter to confirm • esc to go back",

		"mcp.title":        "MCP Server Catalog",
		"mcp.subtitle":     "Select MCP servers to integrate (space to toggle, enter to confirm)",
		"mcp.cat.dev":      "Development",
		"mcp.cat.research": "Research",
		"mcp.cat.media":    "Media",
		"mcp.cat.testing":  "Testing",
		"mcp.cat.data":     "Data",
		"mcp.npm.warning":  "⚠ npm/npx not found. MCP servers require Node.js. Install from https://nodejs.org",
		"mcp.npm.ok":       "✓ npm/npx detected",
		"mcp.none":         "No servers selected — you can add them later in config.yaml",
		"mcp.navigate":     "↑/↓ move • space toggle • enter confirm • esc back",

		"done.title":   "Setup Complete!",
		"done.message": "Run 'elnath run' to start chatting.",
		"done.tip":     "Tip: Run 'elnath setup' anytime to reconfigure.",

		"summary.title":      "Configuration Summary",
		"summary.subtitle":   "Review your settings before saving",
		"summary.apikey":     "API Key",
		"summary.permission": "Permission Mode",
		"summary.mcp":        "MCP Servers",
		"summary.mcp.none":   "None selected",
		"summary.datadir":    "Data Directory",
		"summary.wikidir":    "Wiki Directory",
		"summary.confirm":    "Confirm & Save",
		"summary.edit":       "Edit Settings",
		"summary.navigate":   "↑/↓ to select • enter to confirm • esc to go back",
		"summary.masked":     "••••••••%s",

		"smoketest.title":    "Connection Test",
		"smoketest.testing":  "Testing connection to Anthropic API...",
		"smoketest.success":  "✓ Elnath is working!",
		"smoketest.fail":     "⚠ Connection test failed: %s",
		"smoketest.fail.tip": "Don't worry — you can fix this later. Your config has been saved.",
		"smoketest.skip":     "Skipping connection test (no API key configured)",
		"smoketest.continue": "Press enter to continue",
		"smoketest.response": "Response: %s",

		"progress.step": "Step %d of %d",

		"setup.backup":      "Existing config backed up to %s",
		"setup.rerun":       "Re-running setup wizard...",
		"setup.reconfigure": "Reconfiguration Mode",

		"cli.help": `Usage: elnath <command> [args]

Commands:
  run       Interactive chat mode
  setup     Re-run the setup wizard
  errors    Error catalog lookup
  daemon    Background daemon mode
  portability Export/import encrypted portability bundles
  research  Research task utilities
  lessons   Lessons management utilities
  eval      Benchmark/eval utilities
	 wiki      Wiki management (search, lint, ingest)
	 search    Search past conversations
	 version   Show version
  help      Show this help

Daemon subcommands:
  daemon start              Start the daemon (blocks until stopped)
  daemon submit <task>      Submit a task to the running daemon
  daemon status             List queued and running tasks
  daemon stop               Gracefully stop the running daemon
  daemon install            Install launchd plist for auto-start`,
		"cli.unknown_command":    "unknown command: %s",
		"cli.onboarding_error":   "onboarding: %s",
		"cli.setup_error":        "setup wizard: %s",
		"cli.config_load_error":  "load config: %s",
		"cli.write_config_error": "write config: %s",
		"cli.no_provider":        "No LLM provider configured. Set ELNATH_ANTHROPIC_API_KEY or add anthropic.api_key to config.yaml",
		"cmd.run.help": `USAGE
  elnath run [flags]

DESCRIPTION
  Start an interactive chat session with your configured LLM provider.
  Elnath maintains a persistent message history and can use tools (bash,
  file read/write, wiki, web fetch) to complete tasks autonomously.

  If no config exists, setup runs automatically on first launch.

FLAGS
  --non-interactive    Skip TUI onboarding; use env vars and defaults
  --principal <id>     Override the principal identity for this session
  --project-id <id>    Tag this session with a project identifier
  --config <path>      Use an alternative config file

EXAMPLES
  # Start interactive chat
  $ elnath run

  # Non-interactive (CI / scripted)
  $ ELNATH_ANTHROPIC_API_KEY=sk-ant-... elnath run --non-interactive

  # Use a project-scoped config
  $ elnath run --config ~/projects/myapp/.elnath/config.yaml

  # Run with explicit principal
  $ elnath run --principal alice

SEE ALSO
  elnath setup, elnath daemon, elnath wiki`,
		"cmd.setup.help": `USAGE
  elnath setup [--quickstart]

DESCRIPTION
  Launch the interactive setup wizard to configure your LLM provider,
  data directories, permission mode, and MCP servers. Running setup
  again reconfigures an existing installation (current values shown
  as defaults). Your existing config is backed up before overwriting.

FLAGS
  --quickstart    Minimal fast path: auto-detects Codex OAuth, applies
                  defaults, and runs a demo task. No TUI. (~1 min)
  --config <path> Use an alternative config file

EXAMPLES
  # Full interactive wizard
  $ elnath setup

  # Minimal 1-minute path (recommended for first-time users)
  $ elnath setup --quickstart

  # Reconfigure with a specific config file
  $ elnath setup --config ~/projects/myapp/.elnath/config.yaml

SEE ALSO
  elnath run, elnath errors list`,
		"cmd.wiki.help": `USAGE
  elnath wiki <subcommand> [args]

DESCRIPTION
  Manage Elnath's local knowledge base (wiki). The wiki stores
  Markdown pages with YAML frontmatter and is searchable via SQLite FTS5.
  Pages are linked to agent sessions for automatic context injection.

SUBCOMMANDS
  search <term>     Full-text search across all wiki pages
  get <path>        Show a specific page by path
  list              List all pages (title, path, updated)
  add <path>        Create a new page interactively
  delete <path>     Delete a page

EXAMPLES
  # Search for pages about authentication
  $ elnath wiki search authentication

  # View a specific page
  $ elnath wiki get /architecture/auth

  # List all pages
  $ elnath wiki list

  # Create a new page
  $ elnath wiki add /notes/my-note

SEE ALSO
  elnath run, elnath lessons`,
		"cmd.lessons.help": `USAGE
  elnath lessons <subcommand>

DESCRIPTION
  View and manage lessons Elnath has learned from past agent runs.
  Lessons influence future agent behaviour via persona parameters.
  LLM-extracted lessons (when enabled) are stored separately from
  manually curated ones.

SUBCOMMANDS
  list              List recent lessons (default: 20)
  stats             Show lesson store statistics and LLM extraction status
  delete <id>       Delete a lesson by ID

EXAMPLES
  # List recent lessons
  $ elnath lessons list

  # Show LLM extraction status and breaker state
  $ elnath lessons stats

  # Delete a specific lesson
  $ elnath lessons delete abc123

SEE ALSO
  elnath run, elnath wiki`,
		"cmd.portability.help": `USAGE
  elnath portability <subcommand>

DESCRIPTION
  Export, verify, and restore encrypted Elnath portability bundles.

SUBCOMMANDS
  export            Write a new encrypted bundle
  import            Restore a bundle into a target directory
  list              Show local export history
  verify            Decrypt and verify a bundle

EXAMPLES
  $ elnath --data-dir ~/.elnath portability export --out backup.eln
  $ elnath portability verify backup.eln --passphrase-file pass.txt
  $ elnath portability import backup.eln --dry-run --passphrase-file pass.txt`,
		"cmd.daemon.help": `USAGE
  elnath daemon <subcommand> [flags]

DESCRIPTION
  Manage the Elnath background daemon. The daemon accepts task requests
  via a Unix socket and runs agent sessions asynchronously. Use it for
  long-running tasks or integration with external automation.

SUBCOMMANDS
  start             Start the daemon in the background
  submit <task>     Submit a task to the running daemon
  status            Show queued and running tasks
  stop              Gracefully stop a running daemon
  install           Install launchd plist for auto-start

FLAGS
  --config <path>   Use an alternative config file
  --socket <path>   Override the Unix socket path

EXAMPLES
  # Start the daemon
  $ elnath daemon start

  # Submit a task
  $ elnath daemon submit "summarise the latest PRs"

  # Check daemon status
  $ elnath daemon status

  # Install launchd auto-start
  $ elnath daemon install

  # Stop the daemon
  $ elnath daemon stop

SEE ALSO
  elnath run, elnath errors ELN-030`,
	},
	Ko: {
		"welcome.title":      "Elnath에 오신 것을 환영합니다",
		"welcome.subtitle":   "자율형 AI 어시스턴트",
		"welcome.quick":      "빠른 시작",
		"welcome.quick.desc": "API 키 → 스모크 테스트 (2분)",
		"welcome.full":       "전체 설정",
		"welcome.full.desc":  "모든 옵션: 멀티 API, 디렉토리, 권한, MCP (5분)",
		"welcome.navigate":   "↑/↓ 선택 • Enter 확인",
		"welcome.version":    "v%s",

		"lang.title":    "Language / 언어",
		"lang.en":       "English",
		"lang.ko":       "한국어",
		"lang.navigate": "↑/↓ 선택 • Enter 확인",

		"apikey.title":       "API 키 설정",
		"apikey.prompt":      "Anthropic API 키를 입력하세요:",
		"apikey.placeholder": "sk-ant-...",
		"apikey.validating":  "API 키 검증 중...",
		"apikey.valid":       "API 키가 유효합니다!",
		"apikey.invalid":     "유효하지 않은 API 키입니다. 다시 시도해주세요.",
		"apikey.error":       "검증 실패: %s (키는 저장됩니다)",
		"apikey.skip":        "Enter로 건너뛰기 (나중에 ELNATH_ANTHROPIC_API_KEY로 설정 가능)",
		"apikey.navigate":    "Enter 확인 • Esc 뒤로",

		"dir.title":    "디렉토리 설정",
		"dir.data":     "데이터 디렉토리:",
		"dir.wiki":     "위키 디렉토리:",
		"dir.default":  "(기본값: %s)",
		"dir.navigate": "Enter 확인 • Tab 다음 필드 • Esc 뒤로",

		"perm.title":             "권한 모드",
		"perm.subtitle":          "Elnath의 도구 실행 권한 방식을 선택하세요",
		"perm.default":           "기본",
		"perm.default.desc":      "허용/차단 목록에 없는 비읽기 도구에 대해 확인을 요청합니다. 안전성과 편의성의 균형.",
		"perm.accept_edits":      "편집 허용",
		"perm.accept_edits.desc": "파일 읽기와 편집을 자동 승인합니다. 셸 명령 등 다른 도구는 확인을 요청합니다.",
		"perm.plan":              "계획",
		"perm.plan.desc":         "읽기 전용 모드. 모든 쓰기 및 실행 도구를 거부합니다. 계획 검토에 안전합니다.",
		"perm.bypass":            "우회",
		"perm.bypass.desc":       "모든 도구를 확인 없이 승인합니다. 신뢰할 수 있는 환경에서만 사용하세요.",
		"perm.recommended":       "★ 추천",
		"perm.navigate":          "↑/↓ 선택 • Enter 확인 • Esc 뒤로",

		"mcp.title":        "MCP 서버 카탈로그",
		"mcp.subtitle":     "통합할 MCP 서버를 선택하세요 (Space 토글, Enter 확인)",
		"mcp.cat.dev":      "개발",
		"mcp.cat.research": "연구",
		"mcp.cat.media":    "미디어",
		"mcp.cat.testing":  "테스팅",
		"mcp.cat.data":     "데이터",
		"mcp.npm.warning":  "⚠ npm/npx를 찾을 수 없습니다. MCP 서버는 Node.js가 필요합니다. https://nodejs.org 에서 설치하세요",
		"mcp.npm.ok":       "✓ npm/npx 감지됨",
		"mcp.none":         "선택된 서버 없음 — config.yaml에서 나중에 추가할 수 있습니다",
		"mcp.navigate":     "↑/↓ 이동 • Space 토글 • Enter 확인 • Esc 뒤로",

		"done.title":   "설정 완료!",
		"done.message": "'elnath run'을 실행하여 대화를 시작하세요.",
		"done.tip":     "팁: 'elnath setup'으로 언제든 재설정할 수 있습니다.",

		"summary.title":      "설정 요약",
		"summary.subtitle":   "저장 전 설정을 확인하세요",
		"summary.apikey":     "API 키",
		"summary.permission": "권한 모드",
		"summary.mcp":        "MCP 서버",
		"summary.mcp.none":   "선택 없음",
		"summary.datadir":    "데이터 디렉토리",
		"summary.wikidir":    "위키 디렉토리",
		"summary.confirm":    "확인 및 저장",
		"summary.edit":       "설정 수정",
		"summary.navigate":   "↑/↓ 선택 • Enter 확인 • Esc 뒤로",
		"summary.masked":     "••••••••%s",

		"smoketest.title":    "연결 테스트",
		"smoketest.testing":  "Anthropic API 연결 테스트 중...",
		"smoketest.success":  "✓ Elnath가 작동합니다!",
		"smoketest.fail":     "⚠ 연결 테스트 실패: %s",
		"smoketest.fail.tip": "걱정하지 마세요 — 나중에 수정할 수 있습니다. 설정은 저장되었습니다.",
		"smoketest.skip":     "연결 테스트 건너뜀 (API 키 미설정)",
		"smoketest.continue": "Enter를 눌러 계속",
		"smoketest.response": "응답: %s",

		"progress.step": "%d / %d 단계",

		"setup.backup":      "기존 설정이 %s에 백업되었습니다",
		"setup.rerun":       "설정 마법사를 다시 실행합니다...",
		"setup.reconfigure": "재설정 모드",

		"cli.help": `사용법: elnath <명령> [인자]

명령:
  run       대화형 채팅 모드
  setup     설정 마법사 다시 실행
  daemon    백그라운드 데몬 모드
  research  리서치 작업 유틸리티
  lessons   lesson 관리 유틸리티
  eval      벤치마크/평가 유틸리티
	 wiki      위키 관리 (검색, 린트, 수집)
	 search    이전 대화 검색
	 version   버전 표시
  help      이 도움말 표시

데몬 하위 명령:
  daemon start              데몬 시작 (종료까지 실행)
  daemon submit <작업>      실행 중인 데몬에 작업 제출
  daemon status             대기 및 실행 중인 작업 목록
  daemon stop               데몬 정상 종료
  daemon install            자동 시작 launchd plist 설치`,
		"cli.unknown_command":    "알 수 없는 명령: %s",
		"cli.onboarding_error":   "온보딩: %s",
		"cli.setup_error":        "설정 마법사: %s",
		"cli.config_load_error":  "설정 로드: %s",
		"cli.write_config_error": "설정 쓰기: %s",
		"cli.no_provider":        "LLM 프로바이더가 설정되지 않았습니다. ELNATH_ANTHROPIC_API_KEY를 설정하거나 config.yaml에 anthropic.api_key를 추가하세요",
		"cmd.run.help":           "",
		"cmd.setup.help":         "",
		"cmd.wiki.help":          "",
		"cmd.lessons.help":       "",
		"cmd.daemon.help":        "",
	},
}

// T looks up a translated string by key for the given locale.
// Falls back to English if the key is missing in the requested locale.
// Returns the key itself if not found in any locale.
func T(locale Locale, key string) string {
	if msgs, ok := translations[locale]; ok {
		if val, ok := msgs[key]; ok {
			return val
		}
	}
	if msgs, ok := translations[En]; ok {
		if val, ok := msgs[key]; ok {
			return val
		}
	}
	return key
}

// TOptional returns an untranslated empty string for unknown keys.
func TOptional(locale Locale, key string) string {
	if msgs, ok := translations[locale]; ok {
		if val, ok := msgs[key]; ok {
			return val
		}
	}
	return ""
}

// Locales returns all supported locales.
func Locales() []Locale {
	return []Locale{En, Ko}
}
