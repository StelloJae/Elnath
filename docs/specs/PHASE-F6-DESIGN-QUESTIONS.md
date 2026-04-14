# Phase F-6 Design Questions (Pre-spec)

**Status:** BRAINSTORMING (사용자 결정 후 spec 작성 → opencode 위임)
**Predecessor:** F-5 (LLM lesson extraction) DONE + F-5 Provider Patch DONE
**Goal:** 운영 견고성 + 진입 장벽 낮추기. 4 sub-feature 묶음 (LB6 + LB7 + F7 + F8)

---

## 0. 세션 시작 체크리스트

다음 세션 assistant 가 먼저 수행:

1. 메모리 read
   - `project_elnath.md`
   - `project_elnath_next_action.md`
   - `project_elnath_phase_f6_design.md` (사용자 합의 결정 잠정)
2. `git log -8 --oneline` — 이번 세션 5 commits 확인:
   - `e916d99` fix: router newWork cue
   - `97ecc9d` chore: ELNATH_LESSON_DUMP env
   - `9bcc84f` fix: telegram tool args
   - `a18d026` feat: F-5 provider patch
   - `d58e8bc` chore: dated model IDs
3. `git status` — 이 design-questions + 후속 spec 외 미커밋 검토
4. 참고 코드 parallel read:
   - **LB6**: `internal/llm/codex_oauth.go` (refresh 패턴 reference), `internal/config/`, `cmd/elnath/cmd_setup.go`, `~/.elnath/config.yaml`, `~/Library/LaunchAgents/com.elnath.daemon.plist`
   - **LB7**: `internal/agent/agent.go` (RunResult, retry), `internal/orchestrator/ralph.go` (이미 retry workflow), `internal/llm/anthropic.go` (HTTP error 처리), `internal/tools/`
   - **F7**: `internal/onboarding/`, `cmd/elnath/cmd_setup.go`, `cmd/elnath/cmd_run.go`, `internal/conversation/manager.go`, error 메시지가 사용자에 노출되는 모든 지점
   - **F8**: `internal/conversation/classifier.go`, `internal/prompt/builder.go`, `internal/config/config.go` (Locale field 이미 있음), `internal/onboarding/i18n.go`

## 0.1 LB6 / LB7 / F7 / F8 한 phase 로 묶는 이유

- **공통 테마**: 운영 신뢰 (LB6/LB7) + 사용자 도달 (F7/F8). 둘 다 "F-5 자율성 + dog-food" 단계 후 production 진입 전 마지막 안전망 구축.
- **공통 코드 영역 작음**: 4개가 거의 독립적이라 병렬 opencode 위임 가능 (sub-feature 별 1 prompt).
- **로드맵 효율**: F-6 한 phase 로 묶고 끝나면 Gate Retry v8 + Surpass Demo 로 진입.

---

## LB6 — Auth/Credential Portability

**의도**: token/auth 자료를 안전하게 백업·복구·다른 머신 이전. F-5 patch 직후 이번 세션에서 plist `EnvironmentVariables` 를 손으로 추가했어야 했던 경험이 직접적 동기.

### Q1: Export 자료 범위

#### 옵션

**A. Elnath 자료만**
- `~/.elnath/config.yaml`, `~/.elnath/data/elnath.db`, `~/.elnath/wiki.db`, `lessons.jsonl`, `lesson_cursors.jsonl`, breaker state, audit.jsonl, sessions JSONL
- 장점: scope 명확. 외부 의존성 없음.
- 단점: Codex OAuth (`~/.codex/auth.json`) 같은 외부 token 은 별도 손이전. 결국 사용자가 두 번 일함.

**B. 외부 OAuth 자료까지 흡수**
- A + `~/.codex/auth.json`
- (선택) macOS Keychain 의 `Claude Code-credentials` 도 옵션
- 장점: "한 번에" 이전. portability 의 진정한 의도 충족.
- 단점: 외부 도구 자료를 elnath 가 만지는 게 약한 layering 위반. Codex CLI 가 자체 export 명령 추가 시 충돌.

**C. Plugin point** — elnath 가 "어떤 파일을 export 할지" registry 를 노출. 기본은 A. Codex 는 plugin 으로 자기 자료 추가.
- 장점: 깔끔한 분리.
- 단점: plugin 시스템 별도 설계 부담. F-6 scope 늘어남.

#### 사전 추천: **B**

근거: 사용자 의도가 "다른 머신에서 elnath 가 그대로 동작" → Codex OAuth 없으면 메인 provider 안 뜸. plist 는 별도 (Q5 참조). Plugin (C) 은 매력적이나 F-6 scope 너무 커짐.

#### 사용자 결정
- [ ] A — Elnath 자료만
- [ ] B — A + Codex OAuth (추천)
- [ ] C — Plugin point
- [ ] 기타:

---

### Q2: 비밀 보호 방식

Export 결과물에 OAuth refresh token, Anthropic API key (있으면) 등이 포함됨. 평문 노출 위험.

#### 옵션

**A. Passphrase 대칭 암호화** (AES-256-GCM, scrypt KDF)
- 장점: 표준 방식. 어디서나 풀 수 있음.
- 단점: 사용자가 passphrase 기억해야 함. lost = 복구 불가.

**B. 평문 + warning**
- export 시 0o600 권한, README 에 "민감 정보 포함, 안전한 경로에 보관" 명시
- 장점: 단순. 즉시 사용 가능.
- 단점: 클라우드 백업 (iCloud, Dropbox 등) 으로 흘러갈 수 있음.

**C. macOS Keychain export 우회 (재인증 유도)**
- export 시 토큰 제외. import 후 재로그인 (`elnath setup oauth`) 강제.
- 장점: 토큰 노출 제로.
- 단점: 사용자 매번 재인증. portability 의 가치 약화 (특히 Codex OAuth 는 device flow 없음).

#### 사전 추천: **A**

근거: portability 의 본질 가치는 토큰 재발급 회피. 평문은 보안 사고 위험. Passphrase 는 사용자 부담 있지만 표준이고, lost passphrase 는 본인 책임 명시. C 는 portability 자체를 무력화.

#### 사용자 결정
- [ ] A — Passphrase 암호화 (추천)
- [ ] B — 평문 + warning
- [ ] C — Keychain 우회 (재인증)
- [ ] 기타:

---

### Q3: Import 시 충돌 처리

대상 머신에 이미 `~/.elnath/config.yaml`, db 등이 있을 때.

#### 옵션

**A. 무조건 abort** — `--force` 플래그 없으면 거부
- 장점: 안전. 실수 방지.
- 단점: 사용자가 매번 `--force` 타이핑.

**B. Backup-then-replace** — 기존을 `~/.elnath.bak.<timestamp>` 로 옮기고 import
- 장점: 자동 복구 가능. 사용자 중단 없음.
- 단점: 백업 누적. ~/.elnath 가 GB 단위면 디스크 압박.

**C. Interactive merge** — 파일별로 keep/replace 묻기
- 장점: 세밀한 제어.
- 단점: 비동기/스크립트화 어려움. 사용자 피로.

#### 사전 추천: **A** + import dry-run 모드

근거: portability 는 드물게 일어나는 작업. 안전 기본값 + `--force` 명시 동의 + `--dry-run` 으로 무엇이 변경될지 미리보기. B 는 자동 백업 매력적이지만 db 가 큰 경우 비용.

#### 사용자 결정
- [ ] A — abort + --force (추천)
- [ ] B — auto backup
- [ ] C — interactive merge
- [ ] 기타:

---

### Q4: CLI 표면

#### 옵션

**A. `elnath export` / `elnath import`** — 단일 명령 쌍
- 장점: 단순. 직관적.
- 단점: 옵션 (passphrase, scope) 이 늘면 한 명령에 옵션 폭증.

**B. `elnath portability export|import|list|verify`** — 서브커맨드
- 장점: 향후 list (백업 인벤토리), verify (결과물 무결성 검증) 추가 여지.
- 단점: 명령 길어짐.

**C. `elnath bundle create|extract|inspect`** — bundle 비유
- 장점: noun-verb 순. 단계 분리.
- 단점: 새 용어 학습 부담.

#### 사전 추천: **B**

근거: portability 가 한 phase 의 본질 기능. verify 는 import 직전 무결성 확인에 유용. list 는 백업 history 관리. 명령 길어지지만 docs 잘 쓰면 OK.

#### 사용자 결정
- [ ] A — export/import
- [ ] B — portability sub-commands (추천)
- [ ] C — bundle 비유
- [ ] 기타:

---

### Q5: launchd plist / systemd unit 처리

이번 세션에서 plist `EnvironmentVariables` 손이전 했음. portability 가 이걸 자동화해야 의미 있음.

#### 옵션

**A. plist 도 export 자료에 포함** — 시스템별 (macOS/Linux) detect 후 적절한 위치로 import
- 장점: 진정한 portability. 사용자 0 손.
- 단점: macOS / Linux / Windows 별 처리 분기. plist 의 PATH 는 머신마다 다를 수 있어 import 시 재생성 필요할 수도.

**B. 별도 `elnath service install` 명령** — portability 와 분리. 새 머신에선 import 후 `service install` 수동 실행
- 장점: 책임 분리. plist 는 elnath 외부 도구 (launchctl) 영역.
- 단점: 두 단계 필요.

**C. 둘 다** — export bundle 에 plist 포함하고 import 시 옵션으로 install 묻기
- 장점: 유연.
- 단점: 코드 분기 늘어남.

#### 사전 추천: **B**

근거: plist 는 시스템 통합 영역. portability 는 elnath 자료에 집중하고, service install 은 별도 명령 (이미 `cmd_daemon_install.go` 가 있는 듯 — 확인 필요). 두 단계 명시가 책임 분리상 깔끔.

#### 사용자 결정
- [ ] A — plist 포함 (전부 자동)
- [ ] B — 별도 service install (추천)
- [ ] C — 둘 다
- [ ] 기타:

---

### Q6: Refresh rotation 표준화

LB6 의 부산 요건. 현재 Codex OAuth 만 자동 refresh. Anthropic OAuth 는 Phase 2 에서 deferred 됨.

#### 옵션

**A. F-6 에서 통일 처리** — Anthropic OAuth refresh 도 codex 패턴으로 구현
- 장점: 모든 provider 가 portable + auto-refresh. 일관된 운영 표면.
- 단점: F-6 scope 더 커짐 (~150 LOC).

**B. F-6 에서 인터페이스만 정의, 구현 deferred**
- `RefreshableProvider` interface (Refresh(ctx) error) 만 정의. Codex 가 implement. Anthropic 은 구현 안 하고 nil 반환.
- 장점: F-6 scope 작음. 표준화 인프라만.
- 단점: 실제 가치 미실현.

**C. F-6 에서 제외, 별도 Phase**
- 장점: F-6 scope 명확.
- 단점: portability 가 "토큰 만료 시 무용지물" 위험. lost passphrase 와 expired token 둘 다 고려해야.

#### 사전 추천: **A**

근거: 메모리에 "refresh rotation 은 portability 의 부산 요건" 명시. portability 만 있고 refresh 없으면 token 만료시 다시 portability 무의미. 같이 가야 운영 가치.

#### 사용자 결정
- [ ] A — 통일 처리 (추천)
- [ ] B — 인터페이스만
- [ ] C — 제외 (별도 Phase)
- [ ] 기타:

---

## LB7 — Fault Injection Framework

**의도**: chaos engineering. Production 운영 중 발생 가능한 failure mode 를 dev/test 환경에서 강제 재현 → agent loop 견고성 보증. dog-food 만으로는 발견 못 하는 edge case 잡기.

### Q7: Framework 통합 표면

#### 옵션

**A. 별도 CLI tool** (`elnath chaos run --scenario tool-flake-rate-50`)
- 장점: production daemon 과 완전 분리. 실수로 production 에 fault 켜질 위험 0.
- 단점: 같은 코드를 dev/test 에서만 돌리니 production 호환성 100% 보장 어려움.

**B. Daemon-integrated, env-gated** (`ELNATH_FAULT_PROFILE=tool-flake-rate-50` 시만 활성)
- 장점: 실제 production 코드 경로에 fault 주입. 가장 정확한 견고성 검증.
- 단점: env 누설 시 production 영향 위험. kill switch 필수.

**C. Test-only (Go test build tag)** — `//go:build chaos` 로 보호. 일반 빌드에 안 들어감
- 장점: production binary 에 chaos 코드 없음. 보안.
- 단점: integration test 환경에서만 가능. real daemon 에서 체크 못 함.

#### 사전 추천: **B**

근거: 진짜 견고성 검증은 real daemon code path 에서. env-gated + 강력한 kill switch (3중 가드: env + config + interactive confirm) 로 안전 확보. A 는 코드 경로 분리되어 가짜 검증.

#### 사용자 결정
- [ ] A — 별도 CLI
- [ ] B — Daemon env-gated (추천)
- [ ] C — Test-only build tag
- [ ] 기타:

---

### Q8: Fault 주입 카테고리

#### 옵션 (복수 선택 가능, 우선순위)

**카테고리 후보**:
1. **Tool result fault** — 특정 tool 호출이 일정 비율로 error 반환 (예: bash 30%, read_file 10%)
2. **LLM provider fault** — 응답 timeout / 429 / 500 / malformed JSON 시뮬레이션
3. **IPC fault** — daemon socket 응답 latency / drop
4. **Filesystem fault** — disk full / permission denied / EAGAIN
5. **Network fault** — DNS fail / partial read / TLS handshake fail
6. **Time skew fault** — system clock 점프 (token expiry edge case)

#### 사전 추천: **1, 2, 3** (1차), **4, 5** (2차), **6** (3차)

근거:
- **1차 (필수)**: tool/LLM/IPC 는 daemon 일상 핵심 경로. 여기서 견고성 입증 안 되면 production 위험. 개당 ~50-100 LOC.
- **2차 (옵션)**: filesystem/network 는 이미 OS 가 어느 정도 표준 처리. 추가 시나리오는 가치 있지만 우선순위 낮음.
- **3차 (defer)**: time skew 는 edge case. F-7 또는 별도.

#### 사용자 결정
- [ ] 1, 2, 3 만 (1차, 추천)
- [ ] 1, 2, 3, 4, 5 (1차+2차)
- [ ] 모두 (1-6)
- [ ] 기타:

---

### Q9: 시나리오 corpus 크기

#### 옵션

**A. 작은 corpus (5 시나리오)** — 카테고리당 1-2개 대표
- 장점: 빠른 측정. CI 통합 쉬움.
- 단점: 견고성 coverage 약함.

**B. 중간 corpus (10 시나리오)** — 카테고리 조합 + edge case
- 장점: 실전 신뢰. Gate 식 PASS/FAIL 으로 의미 있는 quality bar.
- 단점: 1 cycle ~30분. CI 부담.

**C. 큰 corpus (20+ 시나리오)** — 시나리오 마트릭스 (fault type × intensity × workflow)
- 장점: 마트릭스 coverage. 회귀 감지 강력.
- 단점: 1 cycle 1-2시간. 유지 비용 큼.

#### 사전 추천: **B**

근거: F-6 scope 와 ROI 균형. 10개면 카테고리 조합도 포함. CI 부담은 nightly 로 분리. 처음부터 20+ 는 시나리오 작성 노력 vs 가치 비례 X.

#### 사용자 결정
- [ ] A — 5 시나리오
- [ ] B — 10 시나리오 (추천)
- [ ] C — 20+ 시나리오
- [ ] 기타:

---

### Q10: PASS/FAIL 기준

#### 옵션

**A. 시나리오 100% 통과 = Gate PASS**
- 장점: 단순 명확. 하나라도 실패하면 release block.
- 단점: 1 시나리오 flaky 면 전체 block.

**B. Per-scenario threshold** (예: tool fault 90% 회복, LLM fault 80% 회복)
- 장점: 각 카테고리 특성 반영. 현실적 quality bar.
- 단점: threshold 정의가 자의적. 매뉴얼 튜닝 필요.

**C. 종합 점수 (예: 평균 회복률 ≥ 85%)**
- 장점: 시나리오 변동에 robust.
- 단점: 한 카테고리가 0% 여도 다른이 보완 → 약점 은닉.

#### 사전 추천: **B**

근거: chaos engineering 표준 패턴. 카테고리별 회복 특성 다름 (LLM 429 회복 기대치 ≠ tool error 회복 기대치). Maturity scorecard v2 에 통합 시 카테고리별 점수가 더 informative.

#### 사용자 결정
- [ ] A — 100% 통과
- [ ] B — Per-scenario threshold (추천)
- [ ] C — 종합 점수
- [ ] 기타:

---

### Q11: Production safety guard

#### 옵션 (다중 가드)

**A. Env var only** — `ELNATH_FAULT_PROFILE` 있으면 활성
- 장점: 단순.
- 단점: env 누설 위험.

**B. Env + config + interactive confirm** — env 있고 config 에 `fault_injection.enabled: true` 있고 daemon 시작 시 stderr 에 빨간 경고 + 5초 wait
- 장점: 3중 가드. 실수 방지.
- 단점: 약간 번거로움.

**C. Build tag** — production binary 빌드 시 fault 코드 자체 제외
- 장점: 코드 자체가 production 에 없음. 100% 안전.
- 단점: dev/test 빌드와 production 빌드 분리. CI 복잡.

#### 사전 추천: **B**

근거: 안전과 사용성 균형. C 는 production 코드 경로 분리되어 견고성 검증 가치 약화 (Q7 답과 일관). B 는 사용자가 의도적으로 활성화해야만 동작. daemon 로그에 명시적 표시.

#### 사용자 결정
- [ ] A — Env only
- [ ] B — 3중 가드 (추천)
- [ ] C — Build tag
- [ ] 기타:

---

### Q12: 결과 reporting

#### 옵션

**A. JSONL 파일** — 각 시나리오 결과 한 줄
- 장점: 기계 가독. 스크립트 처리 쉬움.
- 단점: 사람 읽기 어려움.

**B. Markdown report** — 시나리오별 PASS/FAIL + 회복 통계 + recommendation
- 장점: 사람 친화. PR 첨부 가능.
- 단점: 기계 처리 부담.

**C. 둘 다** — JSONL (raw) + Markdown (summary)
- 장점: 양쪽 niche 충족.
- 단점: 코드 양 +30%.

#### 사전 추천: **C**

근거: F-1 stats 처럼 양쪽 다 가치. JSONL 은 maturity scorecard 에 통합. Markdown 은 사용자/PR review. 코드 양은 적당.

#### 사용자 결정
- [ ] A — JSONL only
- [ ] B — Markdown only
- [ ] C — 둘 다 (추천)
- [ ] 기타:

---

## F7 — Onboarding & UX Accessibility (β)

**의도**: 진입 장벽 낮추기. 5분 안에 첫 task. 친절한 에러. 자세한 help. 표준 a11y (α) 가 아닌 onboarding/UX 친절성 (β).

### Q13: 첫 사용자 도달 경로

#### 옵션

**A. Setup wizard 강화** — 기존 `cmd/elnath/cmd_setup.go` + `internal/onboarding/` 확장. interactive prompt 로 1) provider 선택 → 2) credential 설정 → 3) 첫 wiki dir 생성 → 4) 첫 example task 자동 실행
- 장점: 기존 코드 재사용. 단계 명확.
- 단점: 명령행 환경 한계 (긴 option 입력 어려움).

**B. New `elnath quickstart` 명령** — setup 보다 더 가벼운 5분 path. provider 가정 (Codex OAuth 우선 자동 detect), 모든 단계 default 로 진행, "Y/n" 만 묻기
- 장점: 가장 빠른 도달.
- 단점: setup 과 중복 기능.

**C. 둘 다** — quickstart 는 5분 path, setup 은 advanced 모드
- 장점: 사용자 선호 분기.
- 단점: 두 명령 maintenance 부담.

#### 사전 추천: **A** + 기존 setup 의 minimal mode 추가

근거: 새 명령 추가보다 기존 setup 강화가 코드 단순. `elnath setup --quickstart` 같은 flag 로 minimal 모드. 명령 표면 작게 유지.

#### 사용자 결정
- [ ] A — Setup wizard 강화 + minimal mode (추천)
- [ ] B — 새 quickstart 명령
- [ ] C — 둘 다
- [ ] 기타:

---

### Q14: 5분 metric 측정 방법

#### 옵션

**A. 측정 안 함** — 5분은 design goal 일 뿐. 실측 metric 미수집
- 장점: telemetry 부담 없음. 사용자 privacy 100%.
- 단점: design goal 검증 불가. 회귀 발견 어려움.

**B. Local-only metric** — `~/.elnath/onboarding_metric.json` 에 단계별 timestamp 만 기록. 외부 전송 0
- 장점: privacy 보존. 디버그 시 사용자에게 "이 파일 보내달라" 가능.
- 단점: aggregate 분석 불가.

**C. Opt-in telemetry** — 기본 off, `elnath telemetry enable` 시 step duration 만 anonymized 로 전송
- 장점: 진정한 회귀 감지.
- 단점: telemetry 인프라 별도 구축 (server, privacy 정책 등). F-6 scope 폭증.

#### 사전 추천: **B**

근거: privacy 우선 (사용자 메모리 patterns). Local-only 면 사용자가 디버그 협조 시 자발적으로 공유. C 는 인프라 큰 부담. A 는 검증 불가능.

#### 사용자 결정
- [ ] A — 측정 안 함
- [ ] B — Local-only metric (추천)
- [ ] C — Opt-in telemetry
- [ ] 기타:

---

### Q15: Error 메시지 친절화 범위

#### 옵션

**A. 모든 user-facing error 표준 wrapping** — 신규 `internal/userfacingerr` 패키지. error type 별 "what + why + how to fix" 템플릿 강제
- 장점: 일관된 UX. 모든 진입점에서 적용.
- 단점: 큰 리팩터. 각 호출자 수정 필요. ~300 LOC.

**B. Top-N high-friction error 만 개선** — 사용자가 자주 마주치는 5-10개 error path 만 (예: "no provider configured", "permission denied", "wiki not initialized")
- 장점: 80/20. ROI 높음.
- 단점: 일관성 부족. 새 error path 추가 시 친절성 보장 X.

**C. Error catalog** — 모든 error 에 stable code (예: ELN-001) 부여. catalog 에 상세 설명 + 해결책. CLI 출력엔 code + 1-line, 사용자가 `elnath errors ELN-001` 로 자세히 조회
- 장점: indirect 형식. catalog 갱신만으로 친절성 개선 가능.
- 단점: 사용자가 한 번 더 명령 입력해야 자세히 봄.

#### 사전 추천: **B + C 일부** (top-N 직접 개선 + error code prefix 도입)

근거: 완전 표준화 (A) 는 큰 리팩터 비용. C 의 catalog 만 도입 + B 로 top-N 만 개선이 ROI 최적. error code 는 미래 catalog 확장 여지.

#### 사용자 결정
- [ ] A — 모든 error 표준 wrapping
- [ ] B — Top-N 만
- [ ] C — Error catalog
- [ ] B + C 일부 (추천)
- [ ] 기타:

---

### Q16: Help 시스템 강화

#### 옵션

**A. man-page style** — 각 명령 `elnath <cmd> --help` 에 자세한 설명 + 예제 3-5개
- 장점: 표준. CLI 사용자에게 익숙.
- 단점: long help 가독성 부담. 신규 사용자에겐 over.

**B. In-CLI tutorial** — `elnath tutorial` 명령. 단계별 walkthrough (5-10 steps). 각 step 마다 실제 명령 실행 demo
- 장점: 학습 효과 큼. 5분 도달 metric 직접 지원.
- 단점: 새 코드 ~200 LOC. 유지 부담.

**C. Web docs 분리** — `elnath docs` 가 브라우저로 docs 사이트 open. CLI 는 단순 유지
- 장점: rich 콘텐츠 가능. update 분리.
- 단점: 외부 hosting 필요. offline 사용 못 함.

#### 사전 추천: **A** + 핵심 명령 5개에 자세한 help

근거: B 의 tutorial 매력 있지만 F-6 scope 부담. 일단 help 강화 (각 명령 25-50 라인) 로 시작. tutorial 은 사용자 피드백 후 별도. C 는 외부 인프라.

#### 사용자 결정
- [ ] A — man-page style 강화 (추천)
- [ ] B — In-CLI tutorial
- [ ] C — Web docs
- [ ] 기타:

---

### Q17: 첫 example task

신규 사용자가 setup 직후 즉시 try 할 수 있는 sample.

#### 옵션

**A. Built-in example registry** — `elnath examples list|run <name>` 명령. 5-10 sample task (간단 read/write/wiki/research)
- 장점: 즉시 hands-on. learn-by-doing.
- 단점: example 작성 + 유지 부담.

**B. Setup 마지막에 자동 demo task 1개** — setup 끝나면 "Try a quick demo? [Y/n]" → "what is 2+2?" 같은 minimal task 실행 + 결과 보여줌
- 장점: setup flow 안에서 자연스러움. 별도 명령 학습 불요.
- 단점: 1개만 가능. 다양성 X.

**C. README example 만** — 코드 변경 0. README 에 example 5개 명시
- 장점: 가장 가벼움.
- 단점: README 안 읽는 사용자 누락.

#### 사전 추천: **B**

근거: setup → demo 자연 flow. 5분 metric 의 마지막 step. A 의 examples 명령은 attractive 하나 F-6 scope. README 는 보완재.

#### 사용자 결정
- [ ] A — Examples 명령
- [ ] B — Setup 끝에 demo (추천)
- [ ] C — README example 만
- [ ] 기타:

---

## F8 — Locale

**의도**: 다국어 사용자 응답. system prompt 영어 유지 (LLM 성능 보장). 응답 언어는 input 자동 감지 + config 오버라이드. Time = PC system tz (위치 확인 안 함).

### Q18: Input 언어 감지 메커니즘

#### 옵션

**A. Heuristic (Unicode block 분석)** — 한글/한자/일본 가나 / 라틴 비율 보고 분류
- 장점: 0 외부 의존성. fast (μs 수준).
- 단점: 짧은 input ("ok", "y") 분류 불안. 라틴 알파벳 쓰는 다국어 (영/스페인/독일) 구분 못 함.

**B. langid 라이브러리** — Go port (예: `github.com/chrisport/go-lang-detector`) 사용
- 장점: 정확. 50+ 언어.
- 단점: 외부 dep. 메모리/binary 증가. CGo 없는지 확인 필요.

**C. LLM 분류** — 첫 user message 를 LLM 에 "this language is: " 분류
- 장점: 가장 정확.
- 단점: 응답 latency 증가. cost.

**D. Hybrid** — A 우선, ambiguous (confidence 낮음) 경우만 B 또는 C fallback
- 장점: 빠른 path + 정확 path 균형.
- 단점: 코드 분기 늘어남.

#### 사전 추천: **A** + config override 강화

근거: 사용자 환경 (한국어 대화 patterns 메모리 명시) 에선 한글 vs 영문 구분만 잘 되면 충분. Unicode block 으로 충분. Config 가 우선이라 emergency override 가능.

#### 사용자 결정
- [ ] A — Unicode heuristic (추천)
- [ ] B — langid library
- [ ] C — LLM 분류
- [ ] D — Hybrid
- [ ] 기타:

---

### Q19: 응답 언어 instruction 주입 위치

System prompt 영어 유지. 응답 언어를 LLM 에 어떻게 알릴지.

#### 옵션

**A. System prompt 끝에 1줄 append** — `\n\nRespond in {detected_language}.` (영어 instruction)
- 장점: cache 영향 적음 (system prefix 안정). 단순.
- 단점: cache miss 발생 (응답 언어 바뀔 때마다 system prompt 끝 1줄 변경).

**B. User message 에 prepend** — 매 user message 앞에 `[Respond in Korean]\n` 같은 hint
- 장점: system prompt cache 100% 보존.
- 단점: 매 turn instruction. user message 오염.

**C. Conversation manager 단계 — assistant response 후 강제 translate** (overkill)
- 단점: cost + latency 2배.

#### 사전 추천: **A**

근거: cache miss 1회는 큰 cost 아님 (system 변경 시점만). B 는 user message 마다 오염. C 는 과도.

#### 사용자 결정
- [ ] A — System prompt append (추천)
- [ ] B — User message prepend
- [ ] C — Translate
- [ ] 기타:

---

### Q20: Locale config 우선순위

#### 옵션

**A. 명시적 config 우선** — `cfg.Locale != ""` 면 무조건 그 언어. detection 무시
- 장점: 사용자 control 보장.
- 단점: config 한 번 설정하면 input 영어로 와도 한국어 응답 (사용자 의도 X 가능).

**B. Detection 우선, config 는 default fallback** — 매 turn detection. detection 불확실 시 config locale 사용
- 장점: 동적 응답. 사용자가 영문/한국어 mix 해도 적절.
- 단점: config 의 "이 언어로 고정" 의도 약화.

**C. Per-session lock** — 첫 turn 에서 detection → 세션 끝까지 그 언어 유지
- 장점: 일관성. 사용자 혼란 없음.
- 단점: 세션 중 언어 바꾸기 X.

#### 사전 추천: **B**

근거: 자연스러운 multilingual flow. config 는 ambiguous case 만 결정. C 는 lock 이 답답할 수 있음.

#### 사용자 결정
- [ ] A — Config 우선
- [ ] B — Detection 우선 (추천)
- [ ] C — Per-session lock
- [ ] 기타:

---

### Q21: Time/date locale-aware 포맷

#### 옵션

**A. 영어 ISO 8601 통일** — 항상 `2026-04-14T16:25:00Z` 형식
- 장점: 일관. 기계 가독.
- 단점: 사람 친화 X.

**B. PC system locale 에 맞춘 포맷** — `time.LoadLocation(...)` + locale 별 포맷 string. 한국: "2026년 4월 14일 16:25"
- 장점: 사람 친화.
- 단점: locale 별 포맷 매핑 필요.

**C. Detection 언어에 맞춘 포맷** — Q18 detection 결과를 시간 포맷에도 적용
- 장점: 응답 언어와 시간 포맷 일관.
- 단점: locale 매핑 코드 + detection 의존.

#### 사전 추천: **B**

근거: 사용자 명시적 요구 ("PC system tz 사용"). PC system locale + tz 두 가지 함께 사용. C 는 매 응답마다 detection 영향, 일관성 X.

#### 사용자 결정
- [ ] A — ISO 8601 통일
- [ ] B — PC system locale (추천)
- [ ] C — Detection 언어 매칭
- [ ] 기타:

---

## 종합 결정 표 (사용자 검토용)

| ID | 영역 | Q | 사전 추천 | 결정 |
|----|----|----|-----|-----|
| Q1 | LB6 | Export 자료 범위 | B (A + Codex OAuth) | |
| Q2 | LB6 | 비밀 보호 | A (Passphrase) | |
| Q3 | LB6 | Import 충돌 | A (abort + --force) | |
| Q4 | LB6 | CLI 표면 | B (portability sub-commands) | |
| Q5 | LB6 | plist 처리 | B (별도 service install) | |
| Q6 | LB6 | Refresh 표준화 | A (통일 처리) | |
| Q7 | LB7 | Framework 통합 | B (Daemon env-gated) | |
| Q8 | LB7 | Fault 카테고리 | 1, 2, 3 만 (1차) | |
| Q9 | LB7 | Corpus 크기 | B (10 시나리오) | |
| Q10 | LB7 | PASS/FAIL | B (per-scenario threshold) | |
| Q11 | LB7 | Production guard | B (3중 가드) | |
| Q12 | LB7 | Reporting | C (JSONL + Markdown) | |
| Q13 | F7 | 첫 사용자 path | A (Setup 강화 + minimal mode) | |
| Q14 | F7 | 5분 metric | B (Local-only) | |
| Q15 | F7 | Error 친절화 | B + C 일부 (top-N + error code) | |
| Q16 | F7 | Help 시스템 | A (man-page 강화) | |
| Q17 | F7 | 첫 example | B (Setup 끝 demo) | |
| Q18 | F8 | 언어 감지 | A (Unicode heuristic) | |
| Q19 | F8 | Instruction 주입 | A (system prompt append) | |
| Q20 | F8 | Locale 우선순위 | B (detection 우선) | |
| Q21 | F8 | Time 포맷 | B (PC system locale) | |

---

## Estimated scope

추천 답안 전체 채택 시 sub-feature 별 LOC 추정:

- **LB6**: ~600 LOC (export/import/encrypt + CLI sub-commands + refresh 표준화)
- **LB7**: ~700 LOC (framework + 10 시나리오 + reporting + 3중 가드)
- **F7**: ~400 LOC (setup 강화 + error wrapping top-N + help)
- **F8**: ~250 LOC (heuristic detection + system prompt append + locale time)

**총 ~1950 LOC**. F-5 (~710) 의 ~2.7배. 4 sub-feature 병렬 opencode 위임으로 ~5-7일 작업.

옵션 별 변경 시:
- Q8 모든 카테고리 선택 → +400 LOC
- Q9 20+ 시나리오 → +600 LOC
- Q15 A (모든 error wrapping) → +500 LOC
- Q16 B (in-CLI tutorial) → +200 LOC

---

## 다음 단계 (사용자 결정 후)

1. ✅ Design questions 완성 (이번 세션) ← **여기**
2. 사용자 결정 record (Q1-Q21 답)
3. F-6 spec 4개 (sub-feature 별 PHASE-F6-LB6/LB7/F7/F8.md) — 다음 세션
4. opencode prompt 4개 (sub-feature 별 PHASE-F6-LB6-OPENCODE-PROMPT.md 등) — 다음 세션
5. opencode 병렬 위임 — 다음 세션 이후

