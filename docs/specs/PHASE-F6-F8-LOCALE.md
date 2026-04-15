# Phase F-6 F8 — Locale-Aware Response Language

**Predecessor:** Phase F-6 F7 Onboarding UX (`PHASE-F6-F7-ONBOARDING-UX.md`)
**Status:** SPEC (decisions Q18-Q21 locked — `PHASE-F6-DECISIONS.md`)
**Scope:** ~250 LOC (`internal/locale/` 신규 패키지 + `internal/prompt/builder.go` 수정 + `internal/config/config.go` validation 확장)
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

### Why

사용자가 한국어로 말하면 한국어로, 영어로 말하면 영어로 자연스럽게 응답하는 Elnath 를 만든다. 현재 Elnath 는 system prompt 가 영어이므로 LLM 은 기본적으로 영어로 응답한다. 한국어 input 에 한국어로 응답시키려면 system prompt 에 응답 언어 instruction 을 명시해야 한다.

목표 두 가지:

1. **응답 언어 자동화**: 매 user turn 마다 input 언어를 감지하여 system prompt 끝에 `Respond in {language}.` 한 줄 추가. LLM 성능을 위해 system prompt 본문은 영어 유지.
2. **Locale-aware 시간 포맷**: Elnath 가 날짜/시간을 출력할 때 사용자 locale 에 맞는 포맷 사용. 위치 확인 없이 PC system timezone 사용.

### Why Now

F-5 Provider Patch 후 Elnath 가 실사용 단계에 들어섰다. 사용자는 한국어로 대화하는데 응답이 영어로 나오는 UX 마찰이 v0.4.0 이후 지속적으로 보고됨. F-7 Onboarding 과 같은 phase 에 포함되어 setup 완료 직후부터 올바른 언어 응답이 보장되어야 한다.

---

## 1. Decisions (F-6 Q18-Q21 확정)

| ID | Question | Answer | Rationale |
|----|----------|--------|-----------|
| Q18 | Input 언어 감지 | **A** — Unicode block heuristic | 0 외부 의존성. μs 수준. 한글/한자/가나/라틴 구분으로 실사용 커버. |
| Q19 | Instruction 주입 위치 | **A** — System prompt 끝 1줄 append | Cache prefix 보존. locale 변경 시 끝 1줄만 cache miss. |
| Q20 | Locale 우선순위 | **B** — Detection 우선, config fallback | 동적 응답. 한국어↔영어 mix 자연 허용. config 는 ambiguous case 결정. |
| Q21 | Time/date 포맷 | **B** — PC system locale + tz | 사용자 명시 요구. `time.LoadLocation("Local")`. locale 별 포맷 string 매핑. |

---

## 2. Architecture

```
User message (매 turn)
       │
       ▼
┌──────────────────────────────┐
│  locale.Detector             │
│  DetectLanguage(text string) │
│  → lang string, conf float64 │
│  (Unicode block heuristic)   │
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐
│  locale.Resolver             │
│  Resolve(lang, conf, cfg)    │
│  conf ≥ 0.6 → use detection  │
│  conf < 0.6 → use cfg.Locale │
│  cfg empty  → "en" (default) │
└──────────────┬───────────────┘
               │ resolved locale (e.g. "ko")
               ▼
┌──────────────────────────────────────────────┐
│  prompt.Builder.Build(ctx, state)            │
│  state.Locale = resolved locale              │
│  last node: LocaleInstructionNode            │
│  → appends "\n\nRespond in Korean."          │
│    (skipped if locale == "en")               │
└──────────────┬───────────────────────────────┘
               │ final system prompt
               ▼
         LLM API call
               │
               ▼
      Response in detected language

──────────────────────────────────────────────

Separate: locale.Formatter (time/date display)
┌──────────────────────────────┐
│  FormatTime(t, locale)       │
│  t.In(time.Local)            │
│  switch locale → format str  │
└──────────────────────────────┘
       Used by: tools, prompts that output dates
```

**Detection 은 conversation manager 의 매 turn 처리 지점에서 호출됨.**
`Manager.Run()` → user message 수신 후, `provider.Chat()` 호출 전에 `Resolver.Resolve()` 를 실행하고 `RenderState.Locale` 을 설정.

---

## 3. Implementation

### 3.0 기존 `internal/onboarding/` 와의 관계

`internal/onboarding/i18n.go` 는 **setup wizard UI 의 정적 번역** 을 담당한다. `T(locale, key)` 로 wizard 화면 텍스트를 번역하는 것이 전부이며, onboarding `Locale` 타입은 `string("en"|"ko")` 상수다.

`internal/locale/` 은 **agent 응답 언어의 동적 감지·결정** 을 담당한다. 두 패키지는 역할이 다르므로 통합하지 않는다:

- onboarding 은 setup 중 사용자가 UI 언어를 선택하는 once-only flow.
- locale 은 대화 매 turn 마다 input 을 분석하는 runtime flow.
- 의존 방향: locale 패키지는 onboarding 을 import 하지 않는다.

---

### 3.1 `internal/locale/` (신규 패키지)

#### 3.1.1 `detector.go` (~80 LOC)

Unicode block heuristic 으로 언어 감지. 외부 의존성 없음. 순수 함수.

```go
package locale

// DetectLanguage analyzes text using Unicode block ratios and returns
// a BCP-47 language tag and a confidence score in [0, 1].
// Returns ("en", 0) for empty or very short inputs.
func DetectLanguage(text string) (lang string, confidence float64)

// blockCounts holds raw letter counts per Unicode category.
type blockCounts struct {
    hangul   int // U+AC00–U+D7A3 Hangul Syllables + U+1100–U+11FF Jamo
    hiragana int // U+3040–U+309F
    katakana int // U+30A0–U+30FF
    han      int // U+4E00–U+9FFF CJK Unified Ideographs (core range)
    latin    int // U+0041–U+007A, U+00C0–U+024F
    cyrillic int // U+0400–U+04FF
    arabic   int // U+0600–U+06FF
    total    int // all letter-class runes (hangul+hiragana+katakana+han+latin+cyrillic+arabic)
}

func countBlocks(text string) blockCounts
```

반환 lang 값: `"ko"`, `"ja"`, `"zh"`, `"en"` (4개 + 미분류 fallback `"en"`).

#### 3.1.2 `resolver.go` (~50 LOC)

Detection 결과와 config 를 합성하여 최종 locale 결정.

```go
package locale

const DefaultConfidenceThreshold = 0.6

// Resolve determines the final response locale for a turn.
//   lang     = detected language from DetectLanguage
//   conf     = detection confidence
//   cfgLocale = config.Locale value ("", "auto", "en", "ko", "ja", "zh")
func Resolve(lang string, conf float64, cfgLocale string) string

// NormalizeConfig canonicalizes config.Locale:
//   "" and "auto" → "" (means auto-detect mode)
//   other → lowercase trimmed value
func NormalizeConfig(cfgLocale string) string

// LangToEnglishName maps a BCP-47 tag to the English name for LLM instruction.
//   "ko" → "Korean", "ja" → "Japanese", "zh" → "Chinese", "en" → "English"
func LangToEnglishName(lang string) string
```

#### 3.1.3 `formatter.go` (~40 LOC)

Locale-aware 시간 포맷. PC system timezone 사용 (`time.LoadLocation("Local")`).

```go
package locale

import "time"

// FormatTime formats t using PC system timezone and a locale-specific layout.
// It never makes network calls or location lookups — "Local" is the OS tz.
func FormatTime(t time.Time, locale string) string

// FormatDate formats t as date-only in locale-specific layout.
func FormatDate(t time.Time, locale string) string
```

포맷 매핑:

| locale | time layout | date layout |
|--------|-------------|-------------|
| `ko` | `"2006년 1월 2일 15:04"` | `"2006년 1월 2일"` |
| `ja` | `"2006年1月2日 15:04"` | `"2006年1月2日"` |
| `zh` | `"2006年1月2日 15:04"` | `"2006年1月2日"` |
| default (`en`, 기타) | `"2006-01-02 15:04 MST"` | `"2006-01-02"` |

---

### 3.2 `internal/prompt/builder.go` 수정 (~15 LOC 추가)

`RenderState` 에 `Locale string` 필드를 추가하고, `LocaleInstructionNode` 를 `internal/prompt/` 패키지에 신규 파일로 추가한다.

**`internal/prompt/locale_node.go`** (신규, ~30 LOC):

```go
package prompt

import (
    "context"
    "fmt"
    "github.com/stello/elnath/internal/locale"
)

// LocaleInstructionNode appends "Respond in {Language}." to the system prompt.
// Skipped entirely when resolved locale is "en" (LLM default).
// Priority 999 (protected — never budget-dropped).
type LocaleInstructionNode struct{}

func (n *LocaleInstructionNode) Name() string     { return "locale_instruction" }
func (n *LocaleInstructionNode) Priority() int    { return 999 }
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

`RenderState` (`internal/prompt/node.go` 또는 `types.go`) 에 `Locale string` 필드 추가:

```go
// RenderState carries dynamic per-turn context for prompt rendering.
type RenderState struct {
    TokenBudget int
    Locale      string // resolved locale tag, e.g. "ko", "ja", "zh", "" for en
    // ... existing fields
}
```

`Builder` 등록 순서: `LocaleInstructionNode` 는 마지막으로 등록하여 system prompt 끝에 위치하게 한다. Builder 의 `Build()` 는 등록 순서대로 join 하므로 마지막 Register 가 끝에 붙는다.

---

### 3.3 `internal/conversation/manager.go` 수정 (~20 LOC)

`Manager` 에 locale resolver 를 통한 per-turn locale 결정 로직 추가.

```go
// resolveLocale detects the language of userMsg and returns the locale tag
// to use for this turn's system prompt instruction.
func (m *Manager) resolveLocale(userMsg string) string {
    lang, conf := locale.DetectLanguage(userMsg)
    cfgLocale := ""
    if m.cfg != nil {
        cfgLocale = m.cfg.Locale
    }
    return locale.Resolve(lang, conf, cfgLocale)
}
```

호출 지점: `Manager` 의 turn 처리 메서드 (`Run` 또는 `ProcessTurn`) 에서 user message 수신 직후 `RenderState` 를 구성할 때:

```go
state := &prompt.RenderState{
    TokenBudget: m.maxContextTokens,
    Locale:      m.resolveLocale(userMessage),
}
systemPrompt, err := m.promptBuilder.Build(ctx, state)
```

`Manager` struct 에 `cfg *config.Config` 필드 추가 (또는 `locale string` 직접 저장). 기존 `NewManager` 생성자 signature 확인 후 `WithConfig(*config.Config)` 메서드 또는 생성자 파라미터로 주입.

---

### 3.4 `internal/config/config.go` validation 확장

현재 `validate()` 는 `locale` 에 `"en"` 과 `"ko"` 만 허용한다:

```go
switch cfg.Locale {
case "", "en", "ko":
default:
    return fmt.Errorf("unsupported locale: %q (supported: en, ko)", cfg.Locale)
}
```

F8 구현 후 `"ja"` 와 `"zh"` 를 추가하고 에러 메시지도 갱신:

```go
switch cfg.Locale {
case "", "auto", "en", "ko", "ja", "zh":
default:
    return fmt.Errorf("unsupported locale: %q (supported: en, ko, ja, zh, auto)", cfg.Locale)
}
```

`"auto"` 는 `""` 와 동의어로 처리 (NormalizeConfig 에서 정규화). 사용자가 config.yaml 에 `locale: auto` 로 명시적으로 자동 감지를 표현할 수 있게 한다.

---

## 4. Detection Algorithm 상세

### 4.1 Unicode block 분류 pseudo-code

```
func DetectLanguage(text) → (lang, confidence):
    counts = countBlocks(text)

    if counts.total < 3:
        return ("en", 0.0)   // too short, no confidence

    hangulRatio    = counts.hangul / counts.total
    hiraganaRatio  = counts.hiragana / counts.total
    katakanaRatio  = counts.katakana / counts.total
    hanRatio       = counts.han / counts.total
    jaKanaRatio    = hiraganaRatio + katakanaRatio

    // 1. Korean: Hangul syllable dominance
    if hangulRatio > 0.3:
        return ("ko", hangulRatio)

    // 2. Japanese: Hiragana/Katakana presence beats Han-only detection
    //    (Japanese text always uses kana; Chinese text does not)
    if jaKanaRatio > 0.15:
        conf = jaKanaRatio + hanRatio * 0.3   // han reinforces ja
        return ("ja", min(conf, 1.0))

    // 3. Chinese: CJK Han with negligible kana
    if hanRatio > 0.3 AND jaKanaRatio < 0.05:
        return ("zh", hanRatio)

    // 4. Latin default (English + all other Latin-script languages)
    //    Latin-only input → "en" with confidence = latinRatio
    latinRatio = counts.latin / counts.total
    if latinRatio > 0.5:
        return ("en", latinRatio)

    // 5. No dominant block → low confidence, fallback to "en"
    return ("en", 0.0)
```

### 4.2 countBlocks pseudo-code

```
func countBlocks(text) → blockCounts:
    for each rune r in text:
        if r is letter or CJK ideograph:
            counts.total++
            if 0xAC00 ≤ r ≤ 0xD7A3 OR 0x1100 ≤ r ≤ 0x11FF:
                counts.hangul++
            elif 0x3040 ≤ r ≤ 0x309F:
                counts.hiragana++
            elif 0x30A0 ≤ r ≤ 0x30FF:
                counts.katakana++
            elif 0x4E00 ≤ r ≤ 0x9FFF:
                counts.han++
            elif (0x41 ≤ r ≤ 0x7A) OR (0xC0 ≤ r ≤ 0x24F):
                counts.latin++
            elif 0x0400 ≤ r ≤ 0x04FF:
                counts.cyrillic++
            elif 0x0600 ≤ r ≤ 0x06FF:
                counts.arabic++
    return counts
```

### 4.3 Confidence 계산 요약

| 조건 | confidence | 비고 |
|------|-----------|----|
| `counts.total < 3` | `0.0` | 신호 없음 (짧은 input, 숫자·공백·이모지 only 포함) |
| dominant ratio > 0.5 | `dominant_ratio` | 명확한 단일 언어 |
| dominant ratio ≥ 0.3 AND runner_up ratio < 0.3 | `dominant_ratio` | 비영어 스크립트가 비지배적이지만 존재 (한국어 프로즈 + 소수 라틴) |
| 두 block 이 모두 ratio ≥ 0.3 | `max(hangul, kana+kana_sum, han) - latin_ratio` | **bilingual mixed case** (예: "안녕 hello API"). 비라틴 스크립트가 존재하면 그 ratio 를 confidence 에 반영해 영어 fallback 회피. |
| latin dominant (ratio > 0.5) | `latin_ratio` | 영어로 판정 |
| 아무것도 해당 없음 | `0.0` | |

**Bilingual 대응 정책** (H1 review 반영):

한국어 프로즈에 영어 기술 용어가 섞인 "안녕 이 API 호출해서 JSON 파싱해줘" 같은 패턴은 대표적 사용 시나리오. `hangul_ratio ≈ 0.4`, `latin_ratio ≈ 0.5` 같은 분포에서 단순 `dominant - runner_up` 은 ~0.1 → threshold 0.6 미달 → 영어 fallback 으로 flip 하는 것은 부자연스럽다. 따라서:

- 비라틴 (hangul / kana / han / cyrillic / arabic) 블록 중 하나라도 **ratio ≥ 0.3** 이면 해당 언어를 dominant 로 선택하고 confidence = `max(non_latin_ratio_sum - latin_ratio * 0.5, 0.3)`.
- 이 규칙 덕분에 한·영 mixed 입력은 confidence ≥ 0.3 확보 → threshold (기본 0.6) 은 통과 못 하지만, **Q20-B 의 "session 내 직전 turn 의 locale 상속"** 규칙 (§3.2 `Resolver`) 으로 한국어 유지.
- `DefaultConfidenceThreshold = 0.6` 은 유지. 상속 규칙이 bilingual case 의 실질 방어선.

**Session 상속 규칙** (§3.2 `Resolver` 확장):

```
Resolve(currentInput, sessionLastLocale, cfgLocale):
    d = DetectLanguage(currentInput)
    if d.confidence >= threshold:
        return d.lang         // 명확한 신호
    if sessionLastLocale != "":
        return sessionLastLocale   // 직전 turn 상속 (mixed case 안정화)
    if cfgLocale != "":
        return cfgLocale
    return "en"
```

**단일 문자 Korean fallback 허용**: "네", "응", "ㅇ" 같은 짧은 확인 메시지는 `total < 3` 으로 confidence 0 이 되지만, session 상속 규칙으로 직전 locale (보통 한국어 유지) → `"ko"` 응답. 사용자가 처음부터 "네" 로 시작하면 cfg/en fallback — 이는 의도된 동작 (컨텍스트 없이 한국어 추론 불가).

---

## 5. Supported Languages Matrix

초기 지원 언어 4개. 나머지는 모두 `"en"` fallback.

| Tag | 언어 | Detection trigger | Instruction | Time format |
|-----|------|------------------|-------------|-------------|
| `en` | English | latin dominant, 또는 fallback | (생략 — LLM default) | `2006-01-02 15:04 MST` |
| `ko` | Korean | hangul_ratio > 0.3 | `Respond in Korean.` | `2006년 1월 2일 15:04` |
| `ja` | Japanese | kana_ratio > 0.15 | `Respond in Japanese.` | `2006年1月2日 15:04` |
| `zh` | Chinese (Simplified) | han_ratio > 0.3 AND kana < 0.05 | `Respond in Chinese.` | `2006年1月2日 15:04` |

**확장 경로**: 향후 `"ru"` (Cyrillic), `"ar"` (Arabic) 은 `countBlocks` 에 이미 카운팅되어 있으므로 `DetectLanguage` switch 에 케이스 추가만으로 지원 가능.

---

## 6. Tests

### 6.1 `internal/locale/detector_test.go`

Table-driven tests. 각 언어별 10+ 케이스.

| test case | input | expected lang | expected conf ≥ |
|-----------|-------|---------------|-----------------|
| 순수 한국어 | "안녕하세요 반갑습니다" | "ko" | 0.8 |
| 한영 혼합 | "안녕 hello world" | "ko" | 0.5 |
| 순수 영어 | "hello world how are you" | "en" | 0.8 |
| 일본어 히라가나 | "こんにちは世界" | "ja" | 0.6 |
| 일본어 카타카나 | "コンピュータ" | "ja" | 0.6 |
| 중국어 한자 | "你好世界" | "zh" | 0.6 |
| 짧은 영어 | "ok" | "en" | 0.0 (no confidence) |
| 짧은 한국어 | "네" | "en" | 0.0 (too short) |
| 숫자/기호만 | "123 !@#" | "en" | 0.0 |
| 빈 문자열 | "" | "en" | 0.0 |

### 6.2 `internal/locale/resolver_test.go`

| detection | conf | sessionLast | cfg | expected result | 의도 |
|-----------|------|-------------|-----|-----------------|----|
| "ko" | 0.85 | "" | "" | "ko" | 명확 한국어 |
| "ko" | 0.85 | "en" | "en" | "ko" | detection 우선 |
| "en" | 0.3 | "" | "ko" | "ko" | low conf → cfg |
| "en" | 0.3 | "" | "" | "en" | 전부 empty → default |
| "ko" | 0.55 | "" | "" | "en" | below threshold, session 없음 → default |
| "ko" | 0.55 | "ko" | "" | "ko" | **session 상속** (bilingual mixed, 한국어 세션 지속) |
| "en" | 0.0 | "ko" | "" | "ko" | **single-char Korean 상속** ("네" 같은 짧은 확인) |
| "en" | 0.0 | "" | "" | "en" | 처음부터 짧은 input → default |
| "ja" | 0.7 | "ko" | "ko" | "ja" | detection 우선 (high conf override session) |
| any | any | any | "auto" | (auto == "" 정규화) | `NormalizeConfig()` 후 비교 |
| "ko" | 0.35 | "en" | "ko" | "en" | bilingual 신호 있지만 session 이 영어 → 영어 유지 (flip 억제) |

### 6.3 `internal/locale/formatter_test.go`

각 locale 별 `FormatTime` / `FormatDate` 출력 검증. `time.FixedZone("KST", 9*3600)` 을 주입하여 timezone 독립적 테스트.

### 6.4 `internal/prompt/locale_node_test.go`

- `Locale == "en"` → `Render()` 반환 빈 문자열
- `Locale == "ko"` → `"Respond in Korean."`
- `state == nil` → 빈 문자열
- Priority 999 (protected — budget-drop 로직이 건너뜀)

### 6.5 Cache behavior

System prompt 에 `LocaleInstructionNode` 가 마지막에 붙으므로 prefix cache (system prompt 앞부분) 는 locale 변경 시에도 보존된다. 테스트: 동일 locale 연속 turn → system prompt 동일 (cache hit 가능). locale flip → 끝 1줄만 다름.

---

## 7. Scope Boundaries

### F8 포함 (In-scope)

- `internal/locale/` 패키지 (detector, resolver, formatter)
- `internal/prompt/locale_node.go` + `RenderState.Locale` 필드
- `internal/conversation/manager.go` — locale 결정 후 `RenderState` 에 주입
- `internal/config/config.go` — `validate()` 에 `"ja"`, `"zh"`, `"auto"` 추가
- Telegram path: 동일 `Manager.Run()` 경로를 거치므로 별도 작업 없이 자동 적용

### F8 제외 (Out-of-scope)

- 스페인어, 독일어 등 Latin-script 다국어 구분 (Latin block 내 sub-classification)
- LLM 기반 언어 감지 (Q18-C defer)
- 동적 언어 목록 확장 API 또는 플러그인
- onboarding UI 언어와 agent 응답 언어 강제 동기화
- `internal/onboarding/i18n.go` 수정

---

## 8. Verification Gates

| Gate | 조건 | 통과 기준 |
|------|------|---------|
| G1 | `go test ./internal/locale/...` | PASS |
| G2 | `go test ./internal/prompt/...` | PASS |
| G3 | `go test ./internal/conversation/...` | PASS |
| G4 | `go test -race ./internal/locale/...` | PASS (race-free) |
| G5 | `go build ./...` | 0 compile errors |
| G6 | E2E — 한국어 input send | response in Korean |
| G7 | E2E — 영어 input (after Korean) | response flips to English |
| G8 | E2E — `locale: ko` config + English input | config override works (conf < 0.6 일 때) |
| G9 | `go vet ./internal/locale/...` | 0 issues |

G6-G8 은 `elnath run` 대화형 모드에서 수동 확인. 자동화 E2E 는 G5 통과 후 선택.

---

## 9. Commit Template

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

---

## 10. OpenCode Prompt Pointer

OpenCode 에 아래와 같이 위임한다. 이 spec 파일 경로를 첨부하여 컨텍스트로 제공.

```
Read docs/specs/PHASE-F6-F8-LOCALE.md fully before writing any code.

Implement F8 Locale in this order:
1. internal/locale/detector.go — DetectLanguage + countBlocks + blockCounts
2. internal/locale/resolver.go — Resolve + NormalizeConfig + LangToEnglishName
3. internal/locale/formatter.go — FormatTime + FormatDate
4. internal/prompt/locale_node.go — LocaleInstructionNode (Priority=999)
5. Add Locale field to RenderState in internal/prompt/ (find the struct first with grep)
6. internal/config/config.go — add "ja", "zh", "auto" to validate() switch
7. internal/conversation/manager.go — resolveLocale() + wire into turn processing
8. Write tests: locale/detector_test.go, locale/resolver_test.go,
   locale/formatter_test.go, prompt/locale_node_test.go

Run after each file: go build ./... (zero errors required before next file).
Run at the end: go test -race ./internal/locale/... ./internal/prompt/...
```

---

## 11. Risks & Mitigations

| Risk | 영향 | Mitigation |
|------|------|-----------|
| 짧은 input 오분류 ("ok", "네", "y") | 응답 언어 flip | `total < 3` → confidence 0, fallback to config/previous. Q20-B 의 threshold 로 커버. |
| 한자 포함 한국어 (`漢字` 가 포함된 문장) | ko 가 ja/zh 로 오분류 | Hangul check 가 최우선 (ratio > 0.3). 한자가 섞여도 한글이 지배적이면 ko 판정. |
| 이모지/URL 이 dominant | 의미 없는 분류 | `countBlocks` 는 letter/CJK rune 만 카운팅. 이모지·URL 는 `total` 에 포함 안 됨 → total 작아 confidence 낮음. |
| cache miss 과도 (locale flip 빈번) | LLM cost 증가 | System prompt prefix 는 동일하므로 prefix cache 보존. 끝 1줄만 변경. locale flip 은 사용자가 언어를 실제로 바꿀 때만 발생. |
| `time.LoadLocation("Local")` 실패 | formatter panic | error 무시하고 UTC fallback. `FormatTime` 은 실패해도 `t.UTC()` 기반 default format 반환. |
| `config.Locale = "ko"` + 영어 input (높은 confidence) | config override 무시됨 | Q20-B 결정: detection 우선이 정책. 사용자가 강제 고정 원하면 confidence threshold 와 무관한 강제 모드가 필요하나 이번 scope 외. 문서화. |

---

## 12. Estimated LOC Breakdown

| 파일 | 신규/수정 | 추정 LOC |
|------|---------|---------|
| `internal/locale/detector.go` | 신규 | ~80 |
| `internal/locale/resolver.go` | 신규 | ~50 |
| `internal/locale/formatter.go` | 신규 | ~40 |
| `internal/prompt/locale_node.go` | 신규 | ~30 |
| `internal/prompt/` RenderState 수정 | 수정 | ~5 |
| `internal/config/config.go` validate() | 수정 | ~5 |
| `internal/conversation/manager.go` | 수정 | ~20 |
| `internal/locale/detector_test.go` | 신규 | ~60 |
| `internal/locale/resolver_test.go` | 신규 | ~40 |
| `internal/locale/formatter_test.go` | 신규 | ~30 |
| `internal/prompt/locale_node_test.go` | 신규 | ~25 |
| **총계** | | **~385 LOC** |

결정 문서의 추정 ~250 LOC 은 production code 만 기준. 테스트 포함 시 ~385 LOC.

---

## 13. Next After This Spec

F8 spec 검토 및 승인 후:

1. **opencode 병렬 위임** — LB6, LB7, F7, F8 4개 sub-feature 를 병렬 opencode 세션으로 위임. 각 세션은 독립 파일 수정 (공유 파일 충돌 없음).
2. **순서**: F8 은 `internal/locale/` (순수 신규 패키지) 부터 시작하므로 merge conflict 위험 최소. `manager.go` 수정이 마지막.
3. **gate 검증**: G1-G5 자동 (`go test -race ./...`). G6-G8 수동.
4. **다음 phase**: F-6 완료 후 Phase F-7 (실거래 데이터 축적 후 1분 틱 전략, `project_stella_ml_full_strategy_roadmap.md` 참조) 또는 별도 priority 결정.

---

## 14. Spec-Stage Decisions

이 섹션은 spec 작성 중 발견된 구현-단계 결정 사항을 기록한다.

| # | 결정 사항 | 근거 |
|---|----------|------|
| D1 | `LocaleInstructionNode.Priority() = 999` | budget 감소 시 절대 drop 안 함. 응답 언어 지시는 핵심 기능. LB6 spec 의 동일 패턴 참조. |
| D2 | `"en"` locale 는 instruction 생략 | LLM default 가 영어. 불필요한 system prompt 오염 방지. |
| D3 | `"auto"` config 값을 `validate()` 에서 허용 | 사용자가 YAML 에 명시적으로 `locale: auto` 작성 가능하게. `NormalizeConfig()` 가 `""` 로 정규화. |
| D4 | `countBlocks` 는 letter/CJK rune 만 카운팅 | 이모지, 구두점, 숫자는 언어 신호 없음. total 에 포함하면 confidence 희석. |
| D5 | Hangul 체크를 가나·한자보다 앞에 배치 | 한자 포함 한국어 텍스트에서 han block 이 카운팅되어도 ko 로 올바르게 분류. |
| D6 | Telegram path 별도 처리 불필요 | Telegram message 는 `Manager` 동일 경로를 거침. `resolveLocale()` 이 자동 적용. |
| D7 | `Manager` 에 `cfg *config.Config` 필드 추가 | locale config 접근을 위해 필요. 기존 `NewManager(db, dataDir)` 에 `WithConfig` 빌더 메서드 추가 (기존 signature 미변경). |
