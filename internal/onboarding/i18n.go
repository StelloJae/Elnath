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

		"dir.title":       "Directory Setup",
		"dir.data":        "Data directory:",
		"dir.wiki":        "Wiki directory:",
		"dir.default":     "(default: %s)",
		"dir.navigate":    "enter to confirm • tab to next field • esc to go back",

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

		"mcp.title":         "MCP Server Catalog",
		"mcp.subtitle":      "Select MCP servers to integrate (space to toggle, enter to confirm)",
		"mcp.cat.dev":       "Development",
		"mcp.cat.research":  "Research",
		"mcp.cat.media":     "Media",
		"mcp.cat.testing":   "Testing",
		"mcp.cat.data":      "Data",
		"mcp.npm.warning":   "⚠ npm/npx not found. MCP servers require Node.js. Install from https://nodejs.org",
		"mcp.npm.ok":        "✓ npm/npx detected",
		"mcp.none":          "No servers selected — you can add them later in config.yaml",
		"mcp.navigate":      "↑/↓ move • space toggle • enter confirm • esc back",

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

		"setup.backup":  "Existing config backed up to %s",
		"setup.rerun":   "Re-running setup wizard...",
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

		"dir.title":       "디렉토리 설정",
		"dir.data":        "데이터 디렉토리:",
		"dir.wiki":        "위키 디렉토리:",
		"dir.default":     "(기본값: %s)",
		"dir.navigate":    "Enter 확인 • Tab 다음 필드 • Esc 뒤로",

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

		"mcp.title":         "MCP 서버 카탈로그",
		"mcp.subtitle":      "통합할 MCP 서버를 선택하세요 (Space 토글, Enter 확인)",
		"mcp.cat.dev":       "개발",
		"mcp.cat.research":  "연구",
		"mcp.cat.media":     "미디어",
		"mcp.cat.testing":   "테스팅",
		"mcp.cat.data":      "데이터",
		"mcp.npm.warning":   "⚠ npm/npx를 찾을 수 없습니다. MCP 서버는 Node.js가 필요합니다. https://nodejs.org 에서 설치하세요",
		"mcp.npm.ok":        "✓ npm/npx 감지됨",
		"mcp.none":          "선택된 서버 없음 — config.yaml에서 나중에 추가할 수 있습니다",
		"mcp.navigate":      "↑/↓ 이동 • Space 토글 • Enter 확인 • Esc 뒤로",

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

		"setup.backup":  "기존 설정이 %s에 백업되었습니다",
		"setup.rerun":   "설정 마법사를 다시 실행합니다...",
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

// Locales returns all supported locales.
func Locales() []Locale {
	return []Locale{En, Ko}
}
