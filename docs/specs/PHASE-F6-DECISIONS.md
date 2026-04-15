# Phase F-6 Decisions (Q1-Q21)

**Date:** 2026-04-14
**Status:** CONFIRMED — spec 작성 단계로 진입
**Reference:** `PHASE-F6-DESIGN-QUESTIONS.md` (21 questions), `project_elnath_phase_f6_design.md` (memory)

---

## 결정 표

| ID | 영역 | 질문 | 결정 | 비고 |
|----|----|----|-----|-----|
| Q1 | LB6 | Export 자료 범위 | **A** — Elnath 자료만 | Codex OAuth (`~/.codex/auth.json`) 는 사용자가 직접 손이전. layering 깔끔 유지. |
| Q2 | LB6 | 비밀 보호 | **A** — Passphrase AES-256-GCM (scrypt KDF) | lost passphrase = 복구 불가, 사용자 책임 명시. |
| Q3 | LB6 | Import 충돌 | **A** — abort + `--force` + `--dry-run` | 실수 방지. dry-run 으로 미리보기. |
| Q4 | LB6 | CLI 표면 | **B** — `elnath portability {export,import,list,verify}` | 서브커맨드. verify = bundle 무결성 검증. list = 백업 인벤토리. |
| Q5 | LB6 | plist/systemd | **B** — 별도 `elnath service install` | portability 와 책임 분리. 이전 후 수동 실행 2단계. |
| Q6 | LB6 | Refresh 표준화 | **B** — 인터페이스만 | `RefreshableProvider` interface 정의. Codex 가 implement. Anthropic 구현 defer (현재 OAuth 안 씀). |
| Q7 | LB7 | Framework 통합 | **B** — Daemon-integrated, env-gated | 실제 production 코드 경로 검증. kill switch 3중. |
| Q8 | LB7 | Fault 카테고리 | **1, 2, 3 만** — Tool / LLM / IPC | 4 (filesystem), 5 (network), 6 (time skew) 는 별도 phase 로 defer. |
| Q9 | LB7 | Corpus 크기 | **B** — 10 시나리오 | 3 카테고리 × ~3 시나리오. 1 cycle ~30분. |
| Q10 | LB7 | PASS/FAIL | **B** — Per-scenario threshold | 카테고리별 회복 특성 반영. maturity scorecard 통합. |
| Q11 | LB7 | Production guard | **B** — 3중 가드 | env + config + daemon 시작 시 stderr 빨간 경고 + 5초 wait. |
| Q12 | LB7 | Reporting | **C** — JSONL + Markdown | JSONL = 기계 / scorecard, Markdown = PR/사람. |
| Q13 | F7 | 첫 사용자 path | **A** — `elnath setup --quickstart` minimal mode | 기존 setup 확장. 새 명령 없음. |
| Q14 | F7 | 5분 metric | **B** — Local-only `~/.elnath/onboarding_metric.json` | 외부 전송 0. privacy 우선. |
| Q15 | F7 | Error 친절화 | **B + C 일부** — Top-N 직접 개선 + error code (ELN-XXX) prefix | 미래 catalog 확장 여지. |
| Q16 | F7 | Help 시스템 | **A** — man-page style 강화 | 핵심 명령 5개에 25-50 라인 자세한 help + 예제 3-5개. |
| Q17 | F7 | 첫 example | **B** — Setup 끝 demo 1개 | "Try a quick demo? [Y/n]" 자연 flow. |
| Q18 | F8 | 언어 감지 | **A** — Unicode block heuristic | 한글/한자/가나/라틴 비율. 0 의존성. |
| Q19 | F8 | Instruction 주입 | **A** — System prompt 끝 1줄 append | `Respond in {language}.` cache miss 허용. |
| Q20 | F8 | Locale 우선순위 | **B** — Detection 우선, config fallback | 매 turn detection. 불확실 시 config. |
| Q21 | F8 | Time 포맷 | **B** — PC system locale + tz | `time.LoadLocation` + locale 별 포맷. |

---

## LOC 추정 (최종)

- **LB6**: ~450 LOC (Q1 A → Codex auth.json 제외 -50, Q6 B → Anthropic refresh 구현 defer -100)
- **LB7**: ~700 LOC
- **F7**: ~400 LOC
- **F8**: ~250 LOC
- **총: ~1800 LOC**

---

## 4 Sub-feature 독립성

병렬 opencode 위임 안전:
- LB6 (portability CLI + crypto + refresh interface) — `cmd/elnath/`, `internal/portability/` (신규), `internal/llm/` interface 추가
- LB7 (fault injection framework) — `internal/fault/` (신규), `internal/agent/`, `internal/tools/`, `internal/llm/` hook 주입
- F7 (onboarding UX) — `cmd/elnath/cmd_setup.go`, `internal/onboarding/`, `internal/userfacingerr/` (신규)
- F8 (locale) — `internal/locale/` (신규), `internal/conversation/classifier.go`, `internal/prompt/builder.go`

**공유 파일 거의 없음** — 4 개 병렬 merge 안전.

---

## Defer 목록 (F-6 후)

- **LB6 외부 OAuth 흡수** (Codex auth.json, Keychain) — Q1 이 A 라서 제외. 필요해지면 별도 phase.
- **Anthropic OAuth refresh 구현** — Q6 이 B. 인터페이스만. 실제 구현은 Anthropic OAuth 재개 시.
- **LB7 4,5,6 카테고리** (filesystem / network / time skew) — Q8. 별도 chaos phase.
- **Credential rotation** (사고 대응 폐기/재발급) — LB6 scope 외. 보안 phase.

---

## 다음 단계

1. ✅ Decisions 기록 (이 파일)
2. Sub-feature spec 4 개 작성 (병렬):
   - `PHASE-F6-LB6-AUTH-PORTABILITY.md`
   - `PHASE-F6-LB7-FAULT-INJECTION.md`
   - `PHASE-F6-F7-ONBOARDING-UX.md`
   - `PHASE-F6-F8-LOCALE.md`
3. 각 spec self-critic + 사용자 검토
4. opencode prompt 4 개 작성 (sub-feature 별)
5. opencode 병렬 위임
