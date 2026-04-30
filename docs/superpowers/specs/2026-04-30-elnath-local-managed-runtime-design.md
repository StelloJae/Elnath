# Elnath Local Managed Runtime — Design Spec (2026-04-30)

- **Date**: 2026-04-30 KST
- **Status**: Approved — brainstorm 승인 + critic review 반영 완료
- **Topic**: Anthropic Managed Agents에서 영감을 받은 Elnath-native local managed runtime
- **Scope**: 설계/전환 전략/검증 전략. 코드 변경 없음.
- **Primary recommendation**: Anthropic strict clone이 아니라 `internal/managed/*` 기반의 Elnath-native local managed runtime

---

## 0. Executive Summary

Elnath는 이미 강한 로컬 실행기이지만, 현재 중심 구조는 `message array -> LLM -> local tools -> repeat` 모델에 가깝다. 이 구조는 빠르고 단순하지만, 다음 능력을 1급 개념으로 다루기 어렵다.

- 명시적 session lifecycle
- environment / sandbox 경계
- pause / resume / interrupt
- tool approval lifecycle
- artifact / deliverable 관리
- outcome-based completion
- cross-session memory
- thread-based multi-agent runtime

이 spec의 제안은 Anthropic Managed Agents의 hosted semantics를 그대로 복제하는 것이 아니라, 그 핵심 개념을 Elnath의 로컬/독립/멀티프로바이더 특성에 맞게 재구성한 **self-hosted managed runtime**을 도입하는 것이다.

핵심 결정은 다음 세 가지다.

1. **`internal/llm/*`는 계속 모델 호출 계층으로 유지한다.**
2. **새 실행 런타임은 `internal/managed/*` subtree로 분리한다.**
3. **`team`, `ralph`, `autopilot`은 실행 엔진이 아니라 managed runtime 위에서 동작하는 policy/workflow layer로 재배치한다.**

---

## 1. Problem Statement

현재 Elnath는 다음 장점을 가진다.

- provider-agnostic LLM abstraction
- local tool execution
- session persistence
- daemon queue / background execution
- wiki-backed knowledge base
- workflow-level orchestration (`single`, `team`, `autopilot`, `ralph`, `research`)

하지만 아래와 같은 실행 semantics는 런타임의 1급 개념이 아니다.

- approval-required tool이 세션을 멈추고 재개하는 흐름
- long-running task의 재현 가능한 상태 모델
- artifact를 기준으로 완료를 판정하는 흐름
- multi-agent를 thread isolation으로 다루는 substrate
- memory store를 세션 외부 durable resource로 관리하는 흐름

결과적으로 Elnath는 강한 "로컬 executor" 이지만, 장기적으로는 더 높은 수준의 **managed execution plane**이 필요하다.

---

## 2. Goals

### 2.1 Core goals

1. Elnath에 **managed execution backend**를 도입한다.
2. local 실행을 **session state machine + event log** 기반으로 승격한다.
3. tool 실행을 **runtime-managed approval / resume** semantics로 바꾼다.
4. 결과물을 **artifact**로 다루고 완료를 **outcome** 기준으로 판정할 수 있게 한다.
5. multi-agent를 goroutine fanout이 아니라 **thread runtime**으로 재정의한다.

### 2.2 Product goals

- Anthropic-style managed semantics를 **로컬 / self-hosted** 환경에서 재현한다.
- provider-agnostic 구조를 유지한다.
- 장기적으로 `local_managed`와 `anthropic_managed`를 같은 상위 orchestration plane에 수용할 수 있게 한다.

---

## 3. Non-goals

초기 범위에서 의도적으로 하지 않을 것:

- Anthropic Managed Agents API/리소스 모델의 완전한 1:1 복제
- Anthropic-hosted vault / OAuth refresh / infra reliability의 동일한 재현
- 첫 단계부터 Firecracker 급 최고 수준 sandbox 강제
- `llm.Provider`에 managed runtime semantics를 주입하는 설계
- `wiki == memory store`로 동일시하는 설계
- 기존 `internal/orchestrator/team.go`를 그대로 multi-agent core로 재사용하는 설계

---

## 4. Considered Approaches

### Option A — Anthropic strict clone
Anthropic Managed Agents의 개념/용어/API를 가능한 한 그대로 로컬에 복제한다.

- **장점**: 문서 대응이 쉽고 hosted backend와 비교가 쉽다.
- **단점**: Elnath 고유 구조와 충돌이 많고 provider-agnostic 철학을 약화시킨다.
- **판정**: 가능하지만 비추천.

### Option B — Hybrid backend first
초기부터 `local_managed`와 `anthropic_managed`를 동등한 우선순위로 설계한다.

- **장점**: 장기 호환성을 빨리 고려할 수 있다.
- **단점**: 현재 단계에서 abstraction surface가 너무 커져 설계 복잡도가 급격히 올라간다.
- **판정**: 중기 확장 방향으로는 유효하지만 baseline으로는 과하다.

### Option C — Elnath-native local managed runtime (**chosen**)
Anthropic Managed Agents의 핵심 메커니즘은 배우되, Elnath에 맞는 self-hosted runtime으로 재설계한다.

- **장점**: provider-agnostic, local-first, self-hosted, existing daemon/wiki/orchestrator와의 정합성이 가장 높다.
- **단점**: 초기에 execution plane과 state model을 정교하게 잡아야 한다.
- **판정**: 채택.

---

## 5. Architecture Principles

### 5.1 Separate model plane from execution plane
- `internal/llm/*`는 계속 **모델 호출 전용**이다.
- 새 `internal/managed/*`는 **실행 런타임 전용**이다.

### 5.2 Event log + session state are the system source of truth
- 시스템 전체의 진실원천은 `message array`가 아니라 **event log + session state machine**이다.
- message array는 각 worker/thread 내부 맥락으로만 사용한다.

### 5.3 Session is an execution unit, not just a chat log
- 세션은 단순 JSONL history가 아니라 **명시적 lifecycle을 가진 실행 단위**여야 한다.

### 5.4 Tools are runtime actions, not direct helper calls
- tool은 `request -> permission -> execute -> result -> resume` 흐름을 탄다.
- approval-required tool은 세션 lifecycle을 변경한다.

### 5.5 Orchestrators are policy layers, not runtime cores
- `team`, `ralph`, `autopilot`은 managed substrate 위에서 동작해야지, substrate 자체를 대체하면 안 된다.

---

## 6. Recommended Package Boundaries

```text
internal/
  managed/
    backend/
    session/
    events/
    environment/
    sandbox/
    tools/
    permissions/
    artifacts/
    outcomes/
    memory/
    threads/
    mcp/
```

### 6.1 `internal/managed/backend/`
**역할:** orchestrator-facing thin backend adapter

주요 책임:
- session create / get / archive / terminate 요청을 expose
- event append / stream / replay entrypoint 제공
- interrupt / resume entrypoint 제공
- environment/artifact/memory/thread 서비스에 대한 얇은 façade 제공

중요한 제약:
- backend는 business logic를 소유하지 않는다.
- session state transition, lease, event ordering, approval semantics는 `managed/session`과 그 하위 서비스가 소유한다.
- backend는 orchestrator/CLI/daemon이 managed runtime을 호출하기 위한 **boundary layer**여야지, session/event/environment/artifact를 모두 흡수한 god-layer가 되면 안 된다.

권장 dependency direction:

```text
orchestrator / cli / daemon
  -> managed/backend
    -> managed/session
      -> managed/events
      -> managed/environment
      -> managed/permissions
      -> managed/artifacts
      -> managed/outcomes
      -> managed/memory
      -> managed/threads
```

이 계층 덕분에 장기적으로는 `local_managed`와 `anthropic_managed`를 같은 orchestration plane에 수용할 수 있다.

### 6.2 `internal/managed/session/`
**역할:** session state machine + persistence

세션 상태는 아래 집합으로 고정한다.

| State | 의미 | Terminal? |
|---|---|---|
| `created` | 세션 메타데이터만 생성됨. 아직 runner lease 없음 | no |
| `idle` | work-in-flight 없음. 새 user event를 받을 준비가 됨 | no |
| `running` | active runner가 이벤트를 소비하며 실행 중 | no |
| `waiting_tool_confirmation` | approval-required tool 승인을 기다림 | no |
| `waiting_custom_tool_result` | external/custom tool result를 기다림 | no |
| `evaluating_outcome` | grader가 artifact snapshot을 평가 중 | no |
| `interrupted` | operator stop으로 runner가 분리됨. 명시적 resume 필요 | no |
| `completed` | bounded task 또는 outcome이 성공적으로 종료됨 | yes |
| `failed` | unrecoverable failure로 종료됨 | yes |
| `archived` | history 보존 상태로 닫힘. 새 event 불가 | yes |

권장 상태 전이:
- `created -> idle`
- `idle -> running`
- `running -> waiting_tool_confirmation`
- `running -> waiting_custom_tool_result`
- `running -> evaluating_outcome`
- `running -> interrupted`
- `running -> completed`
- `running -> failed`
- `waiting_tool_confirmation -> running`
- `waiting_custom_tool_result -> running`
- `evaluating_outcome -> running | completed | failed`
- `interrupted -> running | archived`
- `completed | failed -> archived`

현재 `internal/agent/session.go`는 conversation persistence에 가깝다. 새 계층은 이를 **execution lifecycle**로 승격한다.

### 6.3 `internal/managed/events/`
**역할:** canonical event schema

예시 이벤트:
- `user.message`
- `user.interrupt`
- `user.tool_confirmation`
- `user.custom_tool_result`
- `session.status_changed`
- `agent.message.delta`
- `agent.tool_requested`
- `agent.tool_completed`
- `thread.spawned`
- `thread.completed`
- `outcome.evaluation_started`
- `outcome.evaluation_result`

CLI, daemon, 향후 API/TUI/telemetry는 모두 같은 event spine을 공유해야 한다.

### 6.4 `internal/managed/environment/`
**역할:** reusable environment templates

포함 요소:
- base runtime profile
- mounted repositories/resources
- writable paths
- output directory policy
- allowed network policy
- permitted MCP endpoints
- package/tool profile

### 6.5 `internal/managed/sandbox/`
**역할:** actual isolated executor

초기 추천 backend:
1. 제한된 local process execution
2. Docker
3. 이후 stronger isolation (예: Firecracker/gVisor/nsjail 계열)

원칙은 **managed semantics 먼저, stronger isolation은 점진 강화**다.

### 6.6 `internal/managed/tools/`
**역할:** built-in tools + custom tool execution protocol

도구는 더 이상 단순 direct function call이 아니라:
- tool request 생성
- permission 검사
- 실행
- 결과 event 기록
- artifact 반영
- session resume

을 따르는 runtime-managed action이 된다.

### 6.7 `internal/managed/permissions/`
**역할:** approval lifecycle

현재 `internal/agent/permission.go`는 pre-check engine이다. managed runtime에서는 이를 pause/resume 메커니즘으로 승격한다.

- allow -> 즉시 실행
- ask -> `waiting_tool_confirmation` 전환 후 대기
- deny -> tool failure를 agent에 반환하고 계속 진행 가능

### 6.8 `internal/managed/artifacts/`
**역할:** outputs, logs, deliverables

관리 대상:
- output files
- tool logs
- verification reports
- grader reports
- thread outputs

이 계층은 특히 daemon 작업, 결과물 비교, benchmark gate에 중요하다.

### 6.9 `internal/managed/outcomes/`
**역할:** rubric-driven grader loop

구성 요소:
- outcome spec
- rubric
- separate grader context
- gap report
- retry budget / stop condition

이 계층은 `ralph`를 “감으로 끝나는 verify loop”에서 “정의된 완료 조건을 충족하는 loop”로 진화시킨다.

### 6.10 `internal/managed/memory/`
**역할:** durable memory stores

권장 원칙:
- wiki는 첫 번째 backend/adapter 후보일 뿐, memory store와 동일시하지 않는다.
- memory store는 path-addressed entries, versioning, audit trail, access mode를 가진다.
- session은 memory store를 `read_only` 또는 `read_write`로 attach 한다.

### 6.11 `internal/managed/threads/`
**역할:** thread-based multi-agent substrate

구성 요소:
- coordinator thread
- child thread
- isolated conversation context
- shared environment/workspace policy
- delegation / re-entry lifecycle
- thread events

이 substrate가 생겨야 `team` workflow는 더 이상 ad-hoc fanout이 아니라 runtime capability 위의 policy가 된다.

### 6.12 `internal/managed/mcp/`
**역할:** session-bound MCP policy + transport adapter

필요 support:
- stdio MCP
- remote MCP
- environment/network policy integration
- credential source abstraction
- read/write capability classification

이 패키지를 `managed` 하위에 두는 이유는, MCP가 단순 transport가 아니라 **세션별 네트워크 허용 범위, credential source, approval policy, attach된 environment**와 결합되기 때문이다. 즉 런타임은 “이 세션에서 어떤 MCP가 어떤 권한으로 호출 가능한가”를 알아야 한다.

다만 이 패키지는 커져서는 안 된다. 원칙은:
- transport/client 재사용은 기존 `internal/mcp/*`를 최대 활용
- `managed/mcp`는 session policy binding과 attach logic만 담당
- tool-specific execution 세부가 커지면 추후 `managed/tools/mcp` 또는 adapter layer로 하향 이동 가능

---

## 7. Relationship With Existing Elnath Components

### 7.1 Keep largely as-is
- `internal/llm/provider.go` -> model plane 유지
- `internal/orchestrator/router.go` -> intent routing 유지
- `internal/daemon/*` -> background managed session runner로 재사용 가능
- `internal/tools/*` -> 초기 built-in implementation 재사용 가능
- `internal/wiki/*` -> memory backend 후보

### 7.2 Reinterpret / demote
- `internal/agent/agent.go` -> top-level runtime이 아니라 single-thread worker engine
- `internal/agent/session.go` -> legacy conversation/session persistence
- `internal/orchestrator/team.go` -> thread runtime 위 orchestration policy로 재배치

### 7.3 Explicitly avoid
- `llm.Provider`에 session/event semantics 주입
- `wiki == memory store` 동일시
- goroutine fanout을 multi-agent core로 유지
- tool approval을 단순 if문 기반 pre-check에 고정

---

## 8. Runtime Model

### 8.1 Session lifecycle
1. agent definition 선택
2. environment template 선택
3. session record 생성 (`created`)
4. resources mount / workspace 준비 후 `idle`
5. user event append
6. active runner lease 획득 후 `running`
7. approval 또는 custom tool 필요 시 `waiting_*`
8. 관련 user/system event 후 `running` 재개
9. outcome 평가가 필요하면 `evaluating_outcome`
10. 추가 turn이 가능한 session이면 `idle`, bounded run이 끝났으면 `completed`, unrecoverable error면 `failed`
11. 보존-only 종료 시 `archived`

핵심 의미 구분:
- `idle` = 세션이 살아 있고 새 입력을 받을 수 있는 재대기 상태
- `completed` = 이번 session의 bounded work contract가 끝난 terminal success 상태
- `archived` = history 보존만 남고 더 이상 새 event를 받지 않는 closed 상태

### 8.2 Source-of-truth model
- system source of truth = **event log + session state**
- worker source of truth = **message array / local conversation context**
- artifact truth = **explicit artifact index + immutable references to generated outputs**

### 8.3 Session ownership / concurrency model
- 세션당 **active runner는 하나만** 허용한다.
- runner는 lease/heartbeat를 갱신하며 ownership를 유지한다.
- `resume`는 active lease가 없거나 lease가 만료된 경우에만 새 runner를 획득한다.
- 같은 resume token 또는 같은 expected revision으로 들어온 중복 resume 요청은 **idempotent no-op** 이어야 한다.
- 이벤트는 세션별 monotonic sequence number를 가지며, append는 `expected_revision` 기반으로 ordering을 보장한다.
- CLI와 daemon은 모두 같은 ownership 규칙을 따르며, 둘 중 하나가 lease를 잡고 있으면 다른 쪽은 observer 또는 reject path만 가진다.

### 8.4 Resume model
프로세스 재시작 후에도 event log와 persisted session state를 읽어 동일 상태로 재개 가능해야 한다. 이것이 장기 실행과 daemon reliability의 핵심이다.

---

## 9. Execution Boundary: Environment + Sandbox + Tool Approval

### 9.1 Environment templates
세션은 단순 현재 working directory에서 도는 것이 아니라, environment template에서 시작한다.

template 구성 예시:
- 기본 작업 디렉터리
- read-only mounts
- writable outputs
- network policy
- MCP access scope
- package/tool profile

### 9.2 Sandbox execution
environment가 정책과 구성이라면, sandbox는 실제 실행기다. MVP는 stronger isolation을 지향하되, 처음부터 최고급 격리만을 목표로 하지는 않는다.

### 9.3 Tool execution flow
1. agent/tool runtime이 tool request 생성
2. permission policy 검사
3. ask 정책이면 세션을 `waiting_tool_confirmation`으로 전환
4. 승인 event 수신 후 sandbox에서 실행
5. 결과를 event + artifact로 기록
6. agent execution resume

### 9.4 Permission semantics
- **allow**: 즉시 실행
- **ask**: pause + 대기
- **deny**: failure result를 agent에 돌려주고 계속 진행 가능

이 semantics가 성립해야 Elnath는 “direct local helper executor”에서 “managed runtime”으로 진화한다.

---

## 10. Outcomes, Memory, Threads

### 10.1 Outcomes = explicit completion criteria
Outcome은 `ralph`의 상위 개념으로, “무엇이 완료인지”를 명시적 rubric으로 정의하고 separate grader context에서 판정한다.

역할:
- rubric 기반 평가
- gap report 생성
- retry budget 안에서 반복
- criteria 충족 시 completion

grader contract는 최소 아래를 포함해야 한다.
- **입력**: rubric, artifact snapshot, bounded execution summary, optional explicit metadata
- **artifact read scope**: output dir와 명시적으로 publish된 artifacts만 read 가능. live mutable workspace 전체를 기본 read scope로 주지 않는다.
- **memory access**: 기본값은 off. 필요 시 attach된 memory store 중 허용된 subset만 read 가능하게 opt-in 한다.
- **determinism expectation**: 동일 rubric + 동일 artifact snapshot에 대해 criterion pass/fail 집합과 missing-reference 종류는 안정적이어야 한다. 서술 문구의 byte-identical 일치까지 요구하지는 않는다.
- **stable gap report의 의미**: 반복 평가 시 동일 artifact snapshot에서 criterion-level gap structure가 보존되고, 누락 artifact/path reference가 흔들리지 않는다.

### 10.2 Memory = durability across sessions
Memory store는 세션 외부 durable resource다.

포함 능력:
- read_only / read_write attach
- versioning / audit trail
- lessons learned, user preferences, project conventions 유지

### 10.3 Threads = delegation / parallelism substrate
thread는 단순 goroutine fanout이 아니라:
- coordinator + child structure
- isolated context
- shared environment/workspace policy
- lifecycle events

을 가진 런타임 substrate다.

권장 workspace 정책:
- 기본값은 **shared base workspace + per-thread scratch overlay** 다.
- child thread는 기본적으로 scratch overlay에만 write 한다.
- coordinator는 child 결과를 보고 merge/commit 여부를 결정한다.
- 동일 shared workspace에 대한 direct write는 opt-in 이며, 그 경우 scheduler가 serialize 하거나 conflict 시 fail-fast 해야 한다.
- merge conflict가 발생하면 child는 merge-request artifact를 남기고 coordinator/policy layer가 최종 해결 책임을 진다.

### 10.4 Conceptual separation
- **Outcome** = 이 작업이 끝났는가?
- **Memory** = 다음 작업에서도 무엇을 기억할 것인가?
- **Thread** = 이 작업을 몇 개의 맥락으로 나누어 수행할 것인가?

---

## 11. Migration Strategy

### 11.1 Dual-path migration
big-bang 교체가 아니라, 당분간 아래 두 경로를 공존시킨다.

- legacy local execution
- managed local execution

### 11.2 Managed path routing rules
초기 managed path 대상은 **pause/resume, artifact, verification 가치가 높은 작업**으로 제한한다.

| Request / workflow class | 초기 경로 | 이유 |
|---|---|---|
| Plain Q&A / short chat `single` | legacy | managed semantics 가치가 낮음 |
| approval-heavy bounded `single` | managed opt-in | pause/resume contract 검증 가치가 높음 |
| `ralph` verification loop | managed 우선 | outcomes/artifacts와 직접 결합 |
| daemon/background artifact-heavy task | managed 우선 | recovery/replay/artifact 가치가 큼 |
| pre-thread `team` | legacy | thread substrate 전에는 구조 mismatch |
| partial `autopilot` branch | capability-based | outcomes/thread readiness에 따라 점진 이전 |

원칙은 “managed semantics가 실제로 가치를 주는 작업부터 옮기고, 단순 즉시응답은 legacy를 유지”다.

### 11.3 Feature gate / operator control
전환 초기에는 managed path를 명시적 gate 뒤에 둔다. 권장 제어면:

- **config**: `execution.backend: legacy | managed | auto`
- **CLI switch**: `--backend managed` 또는 동등한 opt-in flag
- **workflow-level policy**: `auto` 모드에서는 `ralph`/daemon class를 managed로 보내고, 나머지는 capability readiness에 따라 분기
- **rollback switch**: emergency 시 `execution.backend=legacy`로 신규 세션을 강제 전환

이렇게 하면 CLI, daemon, benchmark harness가 동일한 gate를 공유할 수 있다.

### 11.4 In-flight session rule and rollback triggers
- 세션은 **생성 시점에 backend lane이 고정**된다. in-flight session을 mid-run에 legacy ↔ managed로 이관하지 않는다.
- rollback은 **신규 세션 routing** 에만 즉시 적용하고, 기존 managed session은 finish/interrupt/archive policy에 따라 정리한다.
- 권장 rollback trigger:
  1. event replay corruption 또는 revision ordering bug
  2. approval/resume failure가 임계치 이상 반복
  3. artifact loss / index corruption
  4. benchmark regression이 사전 정의 임계치를 초과

### 11.5 Recommended adoption order
1. `single`의 read-heavy / approval-heavy 작업 일부
2. `ralph` verification loop
3. artifact가 중요한 daemon/background 작업
4. 마지막에 `team`

이유:
- `ralph`는 outcomes와 즉시 결합 가치가 높다.
- `team`은 thread substrate가 생기기 전에는 성급히 옮기면 구조가 오히려 더 꼬인다.

### 11.6 Legacy exit criteria
legacy path를 기본 해제하거나 제거하려면 최소 아래 조건을 충족해야 한다.

1. managed path가 `single`(bounded), `ralph`, daemon 작업에서 benchmark parity 또는 개선을 보인다.
2. session replay / interrupt / approval resume / artifact retention 회귀가 연속 릴리스 기준으로 안정적이다.
3. `team`이 thread substrate 위로 재구성되어 legacy-only workflow가 사실상 남지 않는다.

그 전까지는 legacy path를 “fallback이 아니라 supported compatibility lane”으로 유지한다.

### 11.7 Orchestrator transition rule
초기에는 `router`, `autopilot`, `ralph`, `team`을 유지하고, 내부 실행 substrate만 managed runtime으로 점진 교체한다.

---

## 12. Verification Strategy

이 프로젝트에서 중요한 것은 단순 output correctness가 아니라 **runtime semantics correctness**다.

핵심 검증 축:
- approval-required tool이 정확히 pause 되는가
- approval event 후 같은 세션에서 resume 되는가
- interrupt 후 상태가 올바르게 보존되는가
- artifact가 누락 없이 남는가
- outcome grader가 stable gap report를 만드는가
- child thread가 context leakage 없이 분리되는가
- daemon crash/restart 후 session recovery가 가능한가
- session lease/ownership 충돌 시 ordering이 깨지지 않는가

권장 검증 묶음:
1. **State transition matrix tests** — 허용된 상태 전이만 가능한지 검증
2. **Lease/ownership tests** — single active runner, duplicate resume idempotency, expired lease take-over 검증
3. **Event ordering tests** — monotonic sequence, expected revision mismatch handling 검증
4. **Crash/replay tests** — pause 중 crash 후 recovery / resume / artifact 보존 검증
5. **Artifact contract tests** — tool log, grader report, merge-request artifact enumeration 검증
6. **Thread isolation fixtures** — transcript leakage, scratch overlay separation, merge conflict artifact 검증
7. **Outcome determinism fixtures** — 동일 snapshot에 대한 criterion-level result stability 검증
8. **Dual-path routing tests** — legacy/managed routing gate, rollback trigger, in-flight session 보호 규칙 검증

즉 단순 기능 테스트보다 **lifecycle / recovery / replay / isolation / ownership 검증**이 앞선다.

---

## 13. Acceptance Criteria

### A. Session / Event Runtime
- 세션 상태 집합은 정확히 `created`, `idle`, `running`, `waiting_tool_confirmation`, `waiting_custom_tool_result`, `evaluating_outcome`, `interrupted`, `completed`, `failed`, `archived` 로 고정된다.
- 모든 중요한 실행 전환은 event로 기록된다.
- 세션은 replay / resume 가능하다.
- 세션당 active runner는 하나만 허용되며 lease/heartbeat 규칙을 따른다.
- CLI와 daemon이 같은 session/event model과 ownership 규칙을 공유한다.
- **검증 시나리오 1**: approval 대기 상태의 세션을 persisted 뒤 프로세스를 재시작해도 동일 `session_id`와 `waiting_tool_confirmation` 상태로 복원되고, 승인 event 후 `running`으로 전환된다.
- **검증 시나리오 2**: CLI가 active lease를 가진 세션에 대해 daemon이 resume를 시도하면 duplicate runner가 생기지 않고, idempotent reject 또는 observer-only 경로로 처리된다.

### B. Environment / Sandbox
- session은 reusable environment template에서 시작할 수 있다.
- tool execution은 정의된 sandbox boundary 안에서만 일어난다.
- writable mounts / output dir / network policy를 분리할 수 있다.
- **검증 시나리오**: read-only mount에 대한 write 시도가 sandbox에서 거부되고, writable output 경로에는 artifact가 정상 생성된다.

### C. Tool Approval
- approval-required tool은 즉시 실행되지 않고 session pause 상태로 전환된다.
- approval event 후 같은 execution context에서 resume 된다.
- deny 시 agent는 failure result를 관찰하고 계속 진행 가능하다.
- **검증 시나리오**: 동일 tool request에 대해 allow/deny 각각을 주입했을 때, allow는 실행/기록 후 재개되고 deny는 error result block을 남긴 채 세션이 계속 진행된다.

### D. Artifacts
- 세션은 생성 파일/로그/리포트를 artifact로 조회 가능하다.
- verification / outcome 결과도 artifact로 보존된다.
- **검증 시나리오**: artifact index 조회 시 tool log와 grader report가 모두 반환되고, 각 artifact는 path/type/producer metadata를 가진다.

### E. Outcomes
- session에 outcome/rubric를 attach 할 수 있다.
- grader는 main execution context와 분리된 평가 맥락에서 동작한다.
- grader는 기본적으로 output dir와 explicit artifacts만 읽고, live mutable workspace 전체를 기본 read scope로 갖지 않는다.
- fail 시 gap report를 만들고 retry budget 안에서 반복한다.
- retry budget 소진 시 세션은 `failed` 또는 policy-defined terminal failure state로 종료된다.
- **검증 시나리오 1**: 일부 criterion을 고의로 누락한 deliverable에 대해 grader가 fail + criterion-level gap report를 반환하고, retry 후 충족 시 `completed`로 승격된다.
- **검증 시나리오 2**: 동일 artifact snapshot으로 grader를 두 번 실행했을 때 criterion pass/fail 집합과 gap category가 유지된다.

### F. Memory
- memory store는 세션 간 유지된다.
- read_only / read_write attach가 가능하다.
- 버전 이력 또는 최소 audit trail이 남는다.
- **검증 시나리오**: 세션 A가 memory entry를 수정하면 새 version이 생성되고, 세션 B는 attach mode에 따라 동일 변경을 읽거나 write가 차단된다.

### G. Threads / Multi-agent
- coordinator가 child thread를 생성할 수 있다.
- child는 독립 대화 맥락을 가진다.
- workspace 정책의 기본값은 `shared base workspace + per-thread scratch overlay` 다.
- direct shared write는 opt-in 이며 serialize 또는 fail-fast 정책을 따른다.
- merge/commit 결정 책임은 coordinator 또는 상위 policy layer가 가진다.
- thread lifecycle은 event로 관찰 가능하다.
- **검증 시나리오 1**: child thread A의 transcript 일부가 child thread B의 context dump에 나타나지 않는다.
- **검증 시나리오 2**: child A와 B가 같은 파일을 scratch overlay에서 수정하면 conflict artifact가 생성되고, direct shared write race는 serialize 또는 fail-fast로 처리된다.

---

## 14. Phased Workstreams

### WS0 — Control-plane foundations
**목표:** 개념 분리

산출물:
- execution backend interface
- managed session state enum
- canonical event schema
- legacy/local path vs managed path boundary

### WS1 — Session runtime + event spine
**목표:** event-driven execution

산출물:
- managed session persistence
- event append/replay/stream
- interrupt/resume flow

### WS2 — Environment templates + sandbox backend
**목표:** local managed execution boundary

산출물:
- environment template schema
- sandbox runner
- mount/output/network policy

### WS3 — Tool runtime + permissions
**목표:** tools를 runtime actions로 승격

산출물:
- tool request/result events
- approval wait/resume
- built-in tool managed wrapper

### WS4 — Artifacts
**목표:** outputs 1급화

산출물:
- session output dir
- artifact index
- tool / grader / verification report capture

### WS5 — Outcomes
**목표:** `ralph` 상위 completion model 구축

산출물:
- rubric schema
- grader worker
- retry/gap loop

### WS6 — Memory stores
**목표:** session 간 durable memory

산출물:
- memory store abstraction
- versioning / audit
- wiki adapter

### WS7 — Thread runtime
**목표:** 진짜 multi-agent substrate

산출물:
- coordinator/child threads
- context isolation
- delegation lifecycle

### WS8 — Optional Anthropic adapter
**목표:** 향후 hosted backend 수용

산출물:
- `anthropic_managed` backend
- session/event bridge
- tool confirmation/custom tool mapping

---

## 15. Recommended Implementation Order

권장 순서:
1. WS0
2. WS1
3. WS3
4. WS2
5. WS4
6. WS5
7. WS6
8. WS7
9. WS8 (optional)

이유:
- 먼저 개념과 event spine을 고정해야 한다.
- **WS3를 WS2보다 앞에 두는 이유는**, 현재 Elnath의 local tool path를 기준으로 request/permission/resume lifecycle을 먼저 표준화해야 sandbox backend가 “무엇을 감싸는지”가 명확해지기 때문이다.
- 그 직후 WS2를 수행해 environment/sandbox 경계를 early tranche에서 고정한다. 즉 순서는 `WS3 -> WS2`로 고정하되, 둘은 같은 초기 구현 묶음으로 취급한다.
- artifacts/outcomes/memory/threads는 그 위에 올라갈 때 가장 덜 꼬인다.

---

## 16. Risks and Mitigations

### Risk 1 — event schema가 약하면 전체 구조가 흔들림
- **Mitigation**: WS0/WS1에서 event schema를 우선 고정한다.

### Risk 2 — sandbox 경계를 너무 늦게 잡으면 managed semantics가 흐려짐
- **Mitigation**: WS3에서 tool/action lifecycle을 먼저 표준화한 뒤, **바로 다음 순서로** WS2를 수행해 environment/sandbox 경계를 고정한다. WS2는 WS4 이후로 미루지 않는다.

### Risk 3 — tool approval이 여전히 pre-check if문에 머무를 위험
- **Mitigation**: approval은 session state transition으로만 모델링한다.

### Risk 4 — wiki와 memory를 섞어 장기 구조가 꼬일 위험
- **Mitigation**: memory abstraction을 먼저 만들고 wiki는 adapter로 둔다.

### Risk 5 — `team`을 기존 fanout 모델로 유지하려는 유혹
- **Mitigation**: thread substrate를 별도 패키지로 만든다.

### Risk 6 — legacy path와 managed path 공존 기간 중 복잡도 증가
- **Mitigation**: adoption order를 `ralph` / daemon friendly path부터로 제한하고 feature flag / execution mode gate를 둔다.

---

## 17. Final Recommendation

Elnath는 Anthropic Managed Agents를 **복제**하기보다, 그 핵심 개념을 바탕으로 **provider-agnostic, self-hosted, local-first managed runtime**을 만드는 편이 전략적으로 더 강하다.

따라서 채택할 baseline은 다음과 같다.

- Anthropic strict clone **아님**
- `llm.Provider` 확장 **아님**
- `internal/managed/*` 기반의 **Elnath-native local managed runtime**
- event-first execution model
- `team` / `ralph` / `autopilot`은 runtime 위 policy layer로 재배치
- legacy path와 managed path의 점진적 dual-path migration

이 baseline이 성립하면, 이후에는:
- 더 강한 local runtime
- better long-running tasks
- durable artifacts / outcomes / memory
- eventually `anthropic_managed` backend 수용

까지 무리 없이 확장 가능하다.
