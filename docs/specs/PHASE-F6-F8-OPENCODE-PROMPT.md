# Phase F-6 F8 — OpenCode 구현 위임 Prompt

> 이 파일은 opencode 에 전달하는 작업 지시서다. 구현 전 반드시 전문을 읽어라.
> 진실 원천(source of truth): `docs/specs/PHASE-F6-F8-LOCALE.md`
> Branch: `feat/telegram-redesign`

---

## 1. Context

### 목적

Elnath 는 현재 system prompt 가 영어이므로 LLM 이 기본적으로 영어로 응답한다.
사용자가 한국어로 말해도 영어 응답이 나오는 UX 마찰이 v0.4.0 이후 지속 보고됨.

이 작업은 두 가지를 해결한다:

1. **응답 언어 자동화**: 매 user turn 마다 input 언어를 Unicode block heuristic 으로
   감지하여 system prompt 끝에 `Respond in {language}.` 한 줄 추가한다.
   **LLM 성능을 위해 system prompt 본문은 영어로 유지한다. 응답 언어 instruction 만 추가.**

2. **Locale-aware 시간 포맷**: Elnath 가 날짜/시간을 출력할 때 사용자 locale 에 맞는
   포맷 사용. 위치 확인 없이 PC system timezone(`time.Local`) 사용.

### 설계 결정 (locked — 변경 금지)

| ID  | 결정 |
|-----|------|
| Q18 | 언어 감지: Unicode block heuristic (외부 라이브러리 없음) |
| Q19 | Instruction 주입: system prompt 끝 1줄 append (prefix cache 보존) |
| Q20 | Locale 우선순위: detection 우선, 낮은 신뢰도 시 session 상속 → config → "en" |
| Q21 | 시간 포맷: PC system locale + `time.Local` |

---

## 2. Scope

### 신규 파일 (5개)

```
internal/locale/detector.go        (~80 LOC)
internal/locale/resolver.go        (~50 LOC)
internal/locale/formatter.go       (~40 LOC)
internal/locale/lang_name.go       (~15 LOC)   ← resolver.go 에 통합 가능
internal/prompt/locale_node.go     (~30 LOC)
```

### 수정 파일 (3개)

```
internal/prompt/         — RenderState 에 Locale string 필드 추가 (~5 LOC)
internal/config/config.go  — validate() locale switch 확장 (~5 LOC)
internal/conversation/manager.go  — per-turn locale 결정 wiring (~20 LOC)
```

### 테스트 파일 (4개, 신규)

```
internal/locale/detector_test.go
internal/locale/resolver_test.go
internal/locale/formatter_test.go
internal/prompt/locale_node_test.go
```

### **절대 건드리지 않는 파일**

- `internal/onboarding/i18n.go` — setup wizard UI 번역 전용. **import 도 금지.**
- 위 목록 외 파일. scope creep 금지.

---

## 3. Task (구현 순서 엄수)

작업 순서를 지키고, 각 파일 완성 후 `go build ./...` 로 compile error 0 확인 후 다음 단계 진행.

---

### Step 1. `internal/locale/detector.go`

Unicode block heuristic 언어 감지. **외부 의존성 없음. 순수 함수.**

```go
package locale

// blockCounts holds raw letter counts per Unicode category.
// Only letter-class and CJK runes are counted — emoji, punctuation, digits excluded.
type blockCounts struct {
    hangul   int // U+AC00–U+D7A3 Hangul Syllables + U+1100–U+11FF Jamo
    hiragana int // U+3040–U+309F
    katakana int // U+30A0–U+30FF
    han      int // U+4E00–U+9FFF CJK Unified Ideographs (core range)
    latin    int // U+0041–U+007A, U+00C0–U+024F
    cyrillic int // U+0400–U+04FF
    arabic   int // U+0600–U+06FF
    total    int // sum of all above
}

// DetectLanguage analyzes text using Unicode block ratios.
// Returns BCP-47 lang tag and confidence in [0, 1].
// Returns ("en", 0) for empty or very short input (total < 3).
func DetectLanguage(text string) (lang string, confidence float64)

func countBlocks(text string) blockCounts
```

**`countBlocks` pseudo-code** (정확히 구현):

```
for each rune r in text:
    if r is letter or CJK ideograph:
        counts.total++
        if 0xAC00 ≤ r ≤ 0xD7A3 OR 0x1100 ≤ r ≤ 0x11FF  → counts.hangul++
        elif 0x3040 ≤ r ≤ 0x309F                          → counts.hiragana++
        elif 0x30A0 ≤ r ≤ 0x30FF                          → counts.katakana++
        elif 0x4E00 ≤ r ≤ 0x9FFF                          → counts.han++
        elif (0x41 ≤ r ≤ 0x7A) OR (0xC0 ≤ r ≤ 0x24F)     → counts.latin++
        elif 0x0400 ≤ r ≤ 0x04FF                          → counts.cyrillic++
        elif 0x0600 ≤ r ≤ 0x06FF                          → counts.arabic++
```

**`DetectLanguage` pseudo-code** (정확히 구현):

```
if counts.total < 3:
    return ("en", 0.0)

hangulRatio   = counts.hangul   / counts.total
hiraganaRatio = counts.hiragana / counts.total
katakanaRatio = counts.katakana / counts.total
hanRatio      = counts.han      / counts.total
latinRatio    = counts.latin    / counts.total
jaKanaRatio   = hiraganaRatio + katakanaRatio

// 1. Korean: Hangul dominance — 최우선 (한자 섞여도 hangul > 0.3 이면 ko)
if hangulRatio > 0.3:
    return ("ko", hangulRatio)

// 2. Japanese: Hiragana/Katakana 존재 (Chinese text 는 kana 없음)
if jaKanaRatio > 0.15:
    conf = min(jaKanaRatio + hanRatio * 0.3, 1.0)
    return ("ja", conf)

// 3. Chinese: Han 지배 + kana 거의 없음
if hanRatio > 0.3 AND jaKanaRatio < 0.05:
    return ("zh", hanRatio)

// 4. Latin (English + 기타 Latin-script)
if latinRatio > 0.5:
    return ("en", latinRatio)

// 5. No dominant block
return ("en", 0.0)
```

**Bilingual mixed case** ("안녕 이 API 호출해서 JSON 파싱해줘" 패턴):
- 비라틴 block 중 하나라도 ratio ≥ 0.3 이면 해당 언어 선택.
- 위 pseudo-code 의 순서(hangul → kana → han → latin)대로 평가하면 자동 처리됨.
- confidence 가 threshold 미달인 경우는 `Resolver` 의 session 상속이 방어선.

---

### Step 2. `internal/locale/resolver.go`

Detection 결과 + session cache + config 를 합성하여 최종 locale 결정.

```go
package locale

const DefaultConfidenceThreshold = 0.6

// Resolve determines the final response locale for a turn.
//   current    = DetectLanguage result for this turn's input
//   sessionLast = locale used in the previous turn of this session ("" if first turn)
//   cfgLocale   = config.Locale value (already NormalizeConfig'd)
//
// Priority chain:
//   1. detection confidence >= threshold → use detection
//   2. sessionLast != ""               → inherit previous turn
//   3. cfgLocale != ""                 → use config
//   4. default                         → "en"
func Resolve(current DetectResult, sessionLast string, cfgLocale string) string

// DetectResult bundles DetectLanguage output for use across locale package.
type DetectResult struct {
    Lang       string
    Confidence float64
}

// NormalizeConfig canonicalizes the cfg.Locale value before Resolve:
//   ""     → "" (auto-detect mode)
//   "auto" → "" (explicit auto)
//   other  → strings.ToLower(strings.TrimSpace(v))
func NormalizeConfig(cfgLocale string) string

// LangToEnglishName maps BCP-47 tag to English display name for LLM instruction.
//   "ko" → "Korean"
//   "ja" → "Japanese"
//   "zh" → "Chinese"
//   "en" → ""   (empty — instruction 생략 트리거)
//   기타 → ""
func LangToEnglishName(lang string) string
```

**Session 상속 규칙 예시**:

- `conf = 0.85, lang = "ko"` → `"ko"` (명확한 신호)
- `conf = 0.3, sessionLast = "ko"` → `"ko"` (low conf, session 상속)
- `conf = 0.0, sessionLast = "ko"` → `"ko"` ("네" 같은 단일 확인 문자)
- `conf = 0.0, sessionLast = ""` → `"en"` (첫 turn, 짧은 input)
- `conf = 0.35, sessionLast = "en"` → `"en"` (bilingual 신호 있으나 session 영어 → flip 억제)

> **Note**: `DetectLanguage` 반환값을 `DetectResult{Lang, Confidence}` 로 wrap 하여 이 함수에 전달하도록 `detector.go` 와 시그니처를 맞춰라. `detector.go` 를 먼저 구현했다면 반환 타입을 `(string, float64)` 에서 `DetectResult` 로 바꾸거나, 여기서 wrapping 하면 된다. 일관성 유지.

---

### Step 3. `internal/locale/formatter.go`

Locale-aware 시간/날짜 포맷. **`time.Local` 사용 — 네트워크 없음.**

```go
package locale

import "time"

// FormatTime formats t in PC system timezone with locale-specific layout.
// Falls back to UTC + default format if time.LoadLocation("Local") fails.
func FormatTime(t time.Time, locale string) string

// FormatDate formats t as date-only in locale-specific layout.
func FormatDate(t time.Time, locale string) string
```

**포맷 매핑** (정확히 이 Go layout string 사용):

| locale | FormatTime layout | FormatDate layout |
|--------|-------------------|-------------------|
| `ko`   | `"2006년 1월 2일 15:04"` | `"2006년 1월 2일"` |
| `ja`   | `"2006年1月2日 15:04"` | `"2006年1月2日"` |
| `zh`   | `"2006年1月2日 15:04"` | `"2006年1月2日"` |
| `en` / 기타 | `"2006-01-02 15:04 MST"` | `"2006-01-02"` |

`time.LoadLocation("Local")` 실패 시 panic 없이 UTC fallback.

---

### Step 4. `internal/prompt/locale_node.go` (신규 파일)

**Priority 999 는 절대 변경하지 않는다.**

```go
package prompt

import (
    "context"
    "fmt"
    "github.com/stello/elnath/internal/locale"
)

// LocaleInstructionNode appends "Respond in {Language}." to the system prompt.
// Priority 999 — protected: never dropped by token-budget logic.
// Skipped when locale is "en" or empty (LLM default is English).
type LocaleInstructionNode struct{}

func (n *LocaleInstructionNode) Name() string  { return "locale_instruction" }
func (n *LocaleInstructionNode) Priority() int { return 999 }

func (n *LocaleInstructionNode) Render(ctx context.Context, state *RenderState) (string, error) {
    if state == nil || state.Locale == "" || state.Locale == "en" {
        return "", nil
    }
    name := locale.LangToEnglishName(state.Locale)
    if name == "" {
        return "", nil
    }
    return fmt.Sprintf("Respond in %s.", name), nil
}
```

**Priority 999 확인**: `internal/prompt/builder.go` 의 budget-drop 로직 (약 line 102-105)에
`priority >= 999` 가드가 이미 존재한다. `LocaleInstructionNode.Priority()` 가 999 를 반환하면
자동으로 보호된다. 구현 전 `grep -n "999" internal/prompt/builder.go` 로 확인.

**Builder 등록**: `LocaleInstructionNode` 는 Builder 에 **마지막으로** 등록하여 system prompt 끝에 붙게 한다. 등록 코드는 `internal/conversation/manager.go` 또는 Builder 를 초기화하는 곳에서 추가.

---

### Step 5. `internal/prompt/` — `RenderState.Locale` 필드 추가

`RenderState` struct 가 있는 파일을 먼저 grep 으로 찾아라:

```bash
grep -rn "RenderState" internal/prompt/
```

찾은 파일에서 `RenderState` struct 에 필드 추가:

```go
type RenderState struct {
    TokenBudget int
    Locale      string // resolved locale tag: "ko", "ja", "zh", "" means en
    // ... 기존 필드 유지
}
```

---

### Step 6. `internal/config/config.go` — `validate()` 확장

현재 validate() 의 locale switch:

```go
switch cfg.Locale {
case "", "en", "ko":
default:
    return fmt.Errorf("unsupported locale: %q (supported: en, ko)", cfg.Locale)
}
```

변경 후:

```go
switch cfg.Locale {
case "", "auto", "en", "ko", "ja", "zh":
default:
    return fmt.Errorf("unsupported locale: %q (supported: en, ko, ja, zh, auto)", cfg.Locale)
}
```

`"auto"` 는 `NormalizeConfig()` 에서 `""` 로 정규화. 사용자가 `config.yaml` 에
`locale: auto` 로 명시적으로 자동 감지를 표현할 수 있게 한다.

변경 줄 수: 3줄. 다른 코드 건드리지 않는다.

---

### Step 7. `internal/conversation/manager.go` — locale wiring

`Manager` 의 turn 처리 흐름에서 user message 수신 직후, `provider.Chat()` 호출 전에
locale 을 결정하여 `RenderState.Locale` 에 주입한다.

먼저 현재 코드 구조 파악:

```bash
grep -n "RenderState\|promptBuilder\|provider.Chat\|ProcessTurn\|func.*Run" \
    internal/conversation/manager.go | head -40
```

추가할 로직:

```go
// resolveLocale detects the language of userMsg and returns the locale tag
// to use for this turn's system prompt instruction.
func (m *Manager) resolveLocale(userMsg string) string {
    lang, conf := locale.DetectLanguage(userMsg)
    result := locale.DetectResult{Lang: lang, Confidence: conf}

    cfgLocale := ""
    if m.cfg != nil {
        cfgLocale = locale.NormalizeConfig(m.cfg.Locale)
    }
    return locale.Resolve(result, m.lastLocale, cfgLocale)
}
```

`Manager` struct 에 두 필드 추가:
- `cfg *config.Config` — locale config 접근 (이미 있으면 skip)
- `lastLocale string` — session 내 직전 turn 의 locale (session 상속용)

turn 처리 지점에서:

```go
resolved := m.resolveLocale(userMessage)
m.lastLocale = resolved   // session 상속을 위해 캐시

state := &prompt.RenderState{
    TokenBudget: m.maxContextTokens,
    Locale:      resolved,
    // ... 기존 필드
}
systemPrompt, err := m.promptBuilder.Build(ctx, state)
```

`NewManager` 생성자 또는 `WithConfig` 옵션 메서드로 `*config.Config` 주입.
기존 생성자 시그니처는 가능한 한 유지. 기존 호출부가 nil 을 전달해도 안전하게 동작.

`LocaleInstructionNode` 를 Builder 에 등록하는 코드도 이 파일 또는 Builder 초기화 지점에 추가:

```go
builder.Register(&prompt.LocaleInstructionNode{})
```

---

## 4. Critical Invariants (절대 위반 금지)

**Priority 999**: `LocaleInstructionNode.Priority()` 는 반드시 `999` 를 반환한다.
`internal/prompt/builder.go` 의 `>= 999` 가드가 이 노드를 budget-drop 에서 보호한다.
값을 바꾸면 토큰 부족 시 언어 instruction 이 사라진다.

**`Locale == "en"` → 빈 문자열 반환**: `LocaleInstructionNode.Render()` 는
locale 이 `"en"` 이거나 빈 문자열일 때 `""` 를 반환한다. 영어가 LLM default 이므로
불필요한 instruction 오염 방지.

**`total < 3` → confidence 0**: `DetectLanguage` 는 letter-class rune 이 3개 미만이면
`("en", 0.0)` 을 반환한다. 단일 문자 한국어 ("네")는 confidence 0 → Resolver 가
sessionLast 로 fallback. 세션이 없으면 "en". 의도된 동작.

**Session 상속 (Q20-B)**: Resolver 는 confidence < threshold 일 때 sessionLast 를
cfgLocale 보다 우선한다. 한·영 bilingual mixed input 에서 한국어 세션 유지가 핵심.

**Bilingual flip 억제**: `conf = 0.35, sessionLast = "en"` → `"en"`. session 상속은
양방향이다. 영어 세션 중 한국어 기술어 섞인 input 이 와도 flip 하지 않는다.

**`NormalizeConfig("auto") → ""`**: `NormalizeConfig` 는 `"auto"` 를 `""` 로
정규화한다. Resolver 내부에서 cfgLocale 비교 전에 항상 NormalizeConfig 를 통과시켜라.

**`internal/locale/` 은 `internal/onboarding/` 를 import 하지 않는다**: 두 패키지는
역할이 다르다. locale 패키지에서 onboarding 관련 심볼 참조 금지.

**`time.Local` 사용**: `FormatTime` / `FormatDate` 는 `time.LoadLocation("Local")` 로
PC system timezone 을 가져온다. 에러 시 panic 없이 UTC fallback.

---

## 5. 지원 언어

초기 지원 4개. 나머지는 `"en"` fallback.

| Tag | 언어 | Detection trigger | LLM instruction |
|-----|------|-----------------|-----------------|
| `en` | English | latin dominant 또는 fallback | (생략 — LLM default) |
| `ko` | Korean | `hangulRatio > 0.3` | `Respond in Korean.` |
| `ja` | Japanese | `jaKanaRatio > 0.15` | `Respond in Japanese.` |
| `zh` | Chinese | `hanRatio > 0.3 AND jaKanaRatio < 0.05` | `Respond in Chinese.` |

Cyrillic / Arabic 은 `countBlocks` 까지만 카운팅. switch 확장은 future scope.

---

## 6. Tests

### `internal/locale/detector_test.go`

Table-driven. `go test -race` 통과 필수.

| 케이스 | input | expected lang | expected conf ≥ |
|--------|-------|---------------|----------------|
| 순수 한국어 | `"안녕하세요 반갑습니다"` | `"ko"` | 0.8 |
| 한영 혼합 | `"안녕 hello world"` | `"ko"` | 0.5 |
| 순수 영어 | `"hello world how are you"` | `"en"` | 0.8 |
| 일본어 히라가나 | `"こんにちは世界"` | `"ja"` | 0.6 |
| 일본어 카타카나 | `"コンピュータ"` | `"ja"` | 0.6 |
| 중국어 한자 | `"你好世界"` | `"zh"` | 0.6 |
| 짧은 영어 | `"ok"` | `"en"` | 0.0 (no confidence) |
| 짧은 한국어 | `"네"` | `"en"` | 0.0 (too short) |
| 숫자/기호만 | `"123 !@#"` | `"en"` | 0.0 |
| 빈 문자열 | `""` | `"en"` | 0.0 |
| bilingual mixed | `"안녕 이 API 호출해줘"` | `"ko"` | 0.3 |

### `internal/locale/resolver_test.go`

| detection | conf | sessionLast | cfgLocale | expected | 의도 |
|-----------|------|-------------|-----------|----------|------|
| `"ko"` | 0.85 | `""` | `""` | `"ko"` | 명확 한국어 |
| `"ko"` | 0.85 | `"en"` | `"en"` | `"ko"` | detection 우선 |
| `"en"` | 0.3 | `""` | `"ko"` | `"ko"` | low conf → cfg |
| `"en"` | 0.3 | `""` | `""` | `"en"` | 전부 empty → default |
| `"ko"` | 0.55 | `""` | `""` | `"en"` | threshold 미달, session 없음 |
| `"ko"` | 0.55 | `"ko"` | `""` | `"ko"` | **session 상속** (bilingual mixed) |
| `"en"` | 0.0 | `"ko"` | `""` | `"ko"` | **단일 문자 상속** ("네" 케이스) |
| `"en"` | 0.0 | `""` | `""` | `"en"` | 첫 turn, 짧은 input → default |
| `"ja"` | 0.7 | `"ko"` | `"ko"` | `"ja"` | high conf detection 우선 |
| any | any | any | `"auto"` | (NormalizeConfig 후 `""` 처리) | `"auto"` 정규화 |
| `"ko"` | 0.35 | `"en"` | `"ko"` | `"en"` | session 영어 → flip 억제 |

### `internal/locale/formatter_test.go`

`time.FixedZone("KST", 9*3600)` 을 주입하여 timezone 독립 테스트.
각 locale (`ko`, `ja`, `zh`, `en`) 별 `FormatTime` / `FormatDate` 출력 검증.
예: `ko` FormatTime → `"2024년 3월 15일 14:30"` 포맷 확인.

### `internal/prompt/locale_node_test.go`

- `Locale == "en"` → `Render()` 빈 문자열
- `Locale == "ko"` → `"Respond in Korean."`
- `Locale == "ja"` → `"Respond in Japanese."`
- `Locale == "zh"` → `"Respond in Chinese."`
- `state == nil` → 빈 문자열
- `Priority()` → `999`
- Cache behavior: 동일 locale 연속 turn → system prompt 동일

---

## 7. Verification Gates

모든 gate 통과 후 종료 보고.

```bash
cd /Users/stello/elnath

# G1-G4: locale 패키지
go vet ./internal/locale/...
go test -race ./internal/locale/...

# G5-G6: prompt 패키지
go vet ./internal/prompt/...
go test -race ./internal/prompt/...

# G7-G8: conversation + config
go vet ./internal/conversation/... ./internal/config/...
go test -race ./internal/conversation/... ./internal/config/...

# G9: 전체 빌드
make build
```

**Smoke test** (G5 통과 후 수동):

```bash
# 한국어 입력 → 한국어 응답 확인
echo "안녕, 지금 뭐해?" | elnath run --one-shot

# 영어 입력 → 영어 응답
echo "what is 2+2?" | elnath run --one-shot

# bilingual + session 상속 확인
elnath run
# 대화형: "안녕" 입력 → 한국어 응답 확인
# 이어서 "ok give me json format" 입력 → 두 번째도 한국어 응답 유지 확인
# (session 상속: low conf 영어 input 이어도 직전 ko 세션 유지)
```

---

## 8. Commit Template

```
feat(locale): add per-turn language detection and response instruction

- internal/locale: Unicode block heuristic detector, priority resolver,
  locale-aware time formatter
- internal/prompt: LocaleInstructionNode appends "Respond in X." to
  system prompt (skipped for English)
- internal/conversation: Manager resolves locale per turn, injects into
  RenderState before provider.Chat()
- internal/config: validate() accepts ja, zh, auto in locale field

Closes: F-6 F8
```

커밋은 하지 말 것. stello 가 직접 commit.

---

## 9. Self-Review Checklist

작업 완료 전 직접 확인:

- [ ] `internal/locale/` 이 `internal/onboarding/` 를 import 하지 않음 (`grep -r "onboarding" internal/locale/`)
- [ ] `LocaleInstructionNode.Priority()` 반환값 = `999`
- [ ] `Locale == "en"` 또는 `Locale == ""` 시 `Render()` 빈 문자열 반환
- [ ] `DetectLanguage`: `total < 3` → `("en", 0.0)` 반환
- [ ] `Resolver` 의 sessionLast fallback 구현 (conf < threshold 시 sessionLast 우선)
- [ ] `NormalizeConfig("auto")` → `""`
- [ ] `config.go` `validate()` switch 에 `"ja"`, `"zh"`, `"auto"` 추가됨
- [ ] `go test -race ./internal/locale/...` PASS
- [ ] `go test -race ./internal/prompt/...` PASS
- [ ] `make build` 성공
- [ ] 한국어 smoke: `echo "안녕, 지금 뭐해?" | elnath run --one-shot` → 한국어 응답 확인
- [ ] 영어 smoke: `echo "what is 2+2?" | elnath run --one-shot` → 영어 응답 확인
- [ ] bilingual session 상속 smoke: 대화형에서 "안녕" 후 영어 기술어 섞인 입력도 한국어 유지
- [ ] Commit 메시지 위 템플릿과 일치
- [ ] 기존 테스트 회귀 없음 (`go test -race ./...` 전체 PASS)

---

## 10. Scope Boundaries

### F8 포함 (구현)

- `internal/locale/` 신규 패키지 전체
- `internal/prompt/locale_node.go` + `RenderState.Locale` 필드
- `internal/conversation/manager.go` locale wiring
- `internal/config/config.go` validate() 확장

### F8 제외 (defer — 건드리지 말 것)

- langid 등 외부 라이브러리 교체
- LLM 기반 언어 분류
- per-session 언어 강제 lock 모드
- zh-TW / zh-Hant 구분 (간체/번체)
- `"ru"` (Cyrillic), `"ar"` (Arabic) detection switch 확장
- opt-in telemetry
- `internal/onboarding/i18n.go` 수정
- Telegram 별도 wiring (Manager.Run() 경로를 공유하므로 자동 적용됨)

---

## 11. 완료 보고 형식

작업 종료 시:

1. 신규/수정 파일 목록
2. `go test -race ./internal/locale/... ./internal/prompt/... ./internal/conversation/... ./internal/config/...` PASS 요약 (신규 테스트 개수)
3. `go vet` + `make build` 결과
4. Smoke test 결과 (한국어/영어/bilingual 각 1줄)
5. Self-review checklist — 전 항목 체크 여부
