# Phase F-6 LB6 — Auth/Credential Portability

**Predecessor:** Phase F-5 Provider Patch DONE (`a18d026`)
**Status:** SPEC (decisions Q1-Q6 locked — `PHASE-F6-DECISIONS.md`)
**Scope:** ~450 LOC (portability package + crypto + CLI + RefreshableProvider interface)
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

Elnath 설정·데이터를 한 머신에서 다른 머신으로 안전하게 **export → 전송 → import** 할 수 있게 한다. 이번 세션에서 plist `EnvironmentVariables` 를 손이전 해야 했던 경험이 직접적 동기.

**Why now**: F-5 Provider Patch 가 "메인 provider 재사용" 패턴을 도입하면서 Elnath runtime 은 Codex OAuth 에 강결합됨. Codex auth.json 은 이번 spec 범위 외 (Q1=A, 사용자 손이전). 대신 Elnath 자체 자료 (DB, config, lessons, cursors, session JSONL, wiki.db) 의 portability 를 한 명령으로 해결하여 재현성·백업·머신 이전 세 가지 운영 시나리오 모두 커버.

**Why portable bundle first, rotation later**: `PHASE-F6-DECISIONS.md` Q6=B — 이번 phase 는 `RefreshableProvider` interface 만 정의하고 Codex 기존 refresh 패턴을 해당 interface 에 맞춰 재노출. Anthropic OAuth refresh 실 구현은 defer.

---

## 1. Decisions (F-6 Q1-Q6 확정)

| ID | Question | Answer | Rationale |
|----|----------|--------|-----------|
| Q1 | Export 자료 범위 | **A** — Elnath 자료만 | `~/.codex/auth.json` 은 손이전. layering 깔끔. 외부 도구 자료 흡수 회피. |
| Q2 | 비밀 보호 | **A** — Passphrase AES-256-GCM (scrypt KDF) | 표준. lost passphrase = 복구 불가, 사용자 책임. |
| Q3 | Import 충돌 | **A** — abort + `--force` + `--dry-run` | 안전 기본값. 사용자 명시적 동의. |
| Q4 | CLI 표면 | **B** — `elnath portability {export,import,list,verify}` | 서브커맨드. 향후 확장 여지 (list/verify). |
| Q5 | plist/systemd | **B** — 별도 `elnath service install` | portability 는 자료 집중. plist 재생성은 별도 명령 (이미 `cmd_daemon_install.go` 계열 존재 가능). |
| Q6 | Refresh 표준화 | **B** — Interface-only | `RefreshableProvider` 정의 + Codex 가 implement. Anthropic 구현 defer. |

---

## 2. Architecture

```
elnath portability export --out bundle.eln [--passphrase-file file]
│
├─ portability.Exporter
│   ├─ Collector        ~/.elnath/{config.yaml,data/,wiki.db,lessons.jsonl,
│   │                                lesson_cursors.jsonl,sessions/}
│   ├─ Redactor         Secret-aware: config API keys 평문은 manifest metadata 로만 기록
│   ├─ Manifest         bundle.json (version, created_at, file list, sha256 per file)
│   ├─ TarGzipWriter    *.tar.gz 스트리밍 압축
│   └─ crypto.Seal      AES-256-GCM (passphrase → scrypt → key), encrypted tarball → `.eln`
│
└─ 결과: bundle.eln (단일 파일)

elnath portability import bundle.eln [--passphrase-file file] [--dry-run] [--force]
│
├─ portability.Importer
│   ├─ crypto.Open      passphrase → scrypt → key, AEAD decrypt
│   ├─ TarGzipReader    streaming 해제
│   ├─ Manifest.Verify  sha256 per file, bundle version compat
│   ├─ ConflictCheck    대상 경로에 기존 파일 존재 → --force 없으면 abort
│   ├─ DryRunReporter   --dry-run 시 변경 계획만 stdout 출력, FS touch 0
│   └─ Applier          atomic rename (temp dir → target)
│
└─ 결과: 대상 머신에 ~/.elnath/ 복원

elnath portability list        -> 과거 export 이력 조회 (로컬 인벤토리)
elnath portability verify PATH -> bundle 무결성 + 현재 머신 호환성 체크 (복원 전 확인)
```

**RefreshableProvider path** (Q6-B):

```
internal/llm/
├─ provider.go          (기존) Provider interface: Name/Stream/Models
└─ refreshable.go (NEW) RefreshableProvider interface: Refresh(ctx) error
                        + helper func RefreshIfSupported(ctx, Provider) error

internal/llm/codex_oauth.go
   CodexOAuthProvider.Refresh(ctx) error  ← 기존 refresh 로직 재노출 (public)

internal/llm/anthropic.go
   // No Refresh impl. Anthropic API-key provider는 인터페이스 unimplemented.
   // 향후 AnthropicOAuthProvider 복원 시 implement.
```

---

## 3. Implementation

**Phase 1** (scaffolding + export/import happy path): ~280 LOC
**Phase 2** (verify/list + RefreshableProvider + dry-run + error UX): ~170 LOC

두 phase 를 단일 opencode prompt 에서 순차 실행 (LB6 는 다른 sub-feature 와 병렬, 내부는 순차).

### 3.0 Prerequisites (opencode must run first)

`golang.org/x/crypto` (scrypt) 와 `golang.org/x/term` (hidden TTY input) 은 현 `go.mod` 에 없음. 구현 시작 전 필수:

```bash
cd /Users/stello/elnath
go get golang.org/x/crypto@latest
go get golang.org/x/term@latest
go mod tidy
```

### 3.0.1 Sentinel errors (`internal/portability/errors.go`, NEW ~10 LOC)

전 파일에서 공유하는 error sentinel. `errors.Is` 로 type-assert 가능하도록 package-level var.

```go
package portability

import "errors"

var (
    ErrBadPassphrase = errors.New("portability: bad passphrase or tampered bundle")
    ErrConflict      = errors.New("portability: target path conflict (use --force)")
    ErrIntegrity     = errors.New("portability: manifest integrity check failed")
    ErrVersion       = errors.New("portability: bundle version unsupported")
    ErrCrossDevice   = errors.New("portability: temp dir and target on different partitions")
)
```

### 3.1 `internal/portability/bundle.go` (NEW, ~60 LOC)

Bundle manifest + format 정의. 버전드 스펙.

```go
package portability

const (
    BundleVersion = 1
    ManifestName  = "bundle.json"
    MagicHeader   = "ELNBUNDLE" // 앞 9 bytes of .eln file — AEAD 가 wrapping 이전
)

// Manifest describes the bundle contents for integrity verification and
// cross-machine compatibility checks.
type Manifest struct {
    Version     int              `json:"version"`
    CreatedAt   time.Time        `json:"created_at"`
    SourceHost  string           `json:"source_host"`     // hostname, optional (privacy — user can strip)
    ElnathVer   string           `json:"elnath_version"`  // binary version
    HasSecrets  bool             `json:"has_secrets"`     // true if any collected file (예: config.yaml) 의 provider api_key 가 non-empty
    Files       []ManifestFile   `json:"files"`
    Scope       BundleScope      `json:"scope"`
}

// ManifestFile records a single exported file's identity for integrity check.
type ManifestFile struct {
    RelPath string `json:"rel_path"` // relative to ~/.elnath
    Size    int64  `json:"size"`
    SHA256  string `json:"sha256"`   // hex
}

// BundleScope marks what categories are included (future extension point).
type BundleScope struct {
    Config   bool `json:"config"`
    DB       bool `json:"db"`
    Wiki     bool `json:"wiki"`
    Lessons  bool `json:"lessons"`
    Sessions bool `json:"sessions"`
}
```

버전드: Manifest.Version mismatch 시 import 는 경고 후 abort. `--force` 로 우회.

### 3.2 `internal/portability/crypto.go` (NEW, ~80 LOC)

AES-256-GCM + scrypt KDF. 표준 `crypto/aes` + `crypto/cipher` + `golang.org/x/crypto/scrypt` (§3.0 prerequisite 로 `go get` 필요).

```go
const (
    scryptN      = 1 << 15 // 32768 (cost factor)
    scryptR      = 8
    scryptP      = 1
    scryptKeyLen = 32 // AES-256
    saltLen      = 16
    nonceLen     = 12 // GCM standard
)

// Seal encrypts plaintext with a passphrase-derived key. Output layout:
//   MagicHeader (9) | salt (16) | nonce (12) | ciphertext
// Note: AEAD.Seal 은 16-byte GCM authentication tag 를 ciphertext 끝에 자동 append.
// 별도 tag 필드를 저장하지 말 것 — Seal/Open 의 대칭성이 깨짐.
func Seal(plaintext, passphrase []byte) ([]byte, error) { ... }

// Open decrypts a sealed bundle. Returns plaintext or ErrBadPassphrase on
// auth fail (wrong passphrase OR tampered ciphertext — 구분 불가능, GCM 특성).
func Open(sealed, passphrase []byte) ([]byte, error) { ... }

// deriveKey runs scrypt and returns the 32-byte AES-256 key.
func deriveKey(passphrase, salt []byte) ([]byte, error) { ... }
```

Passphrase 출처:
- `--passphrase-file <path>` — 파일 첫 줄 trim
- TTY interactive prompt (`golang.org/x/term.ReadPassword`, §3.0 prerequisite)
- env `ELNATH_PORTABILITY_PASSPHRASE` (CI 용, stderr 에 `slog.Warn` + `fmt.Fprintln(os.Stderr, ...)` 이중 출력)

**Passphrase strength gate** (S4 결정):

```go
const (
    minPassphraseLen    = 8   // hard reject below
    weakPassphraseLen   = 12  // warn + interactive confirm between min and weak
)

// ValidatePassphrase returns an error for too-short passphrases and a non-nil
// warning for weak-but-acceptable ones. Callers decide how to surface the
// warning (interactive confirm vs. stderr-only for non-tty paths).
func ValidatePassphrase(pass []byte) (warning error, fatal error) { ... }
```

CLI 분기:
- `fatal != nil` → 에러 반환, 재입력 요구 (TTY) 또는 즉시 exit (non-TTY).
- `warning != nil && isTTY && !forceWeak` → `Continue with weak passphrase? [y/N]` 기본 N.
- `warning != nil && !isTTY` → stderr 에 `⚠ weak passphrase (< 12 chars) — consider 1Password/Keychain for generation`, 흐름 지속.
- `--force-weak` → warning 무시.

Passphrase `[]byte` 는 scrypt 연산 직후 `for i := range pass { pass[i] = 0 }` 로 zeroize (best-effort — Go GC 는 copy 를 남길 수 있으나 대부분 경로 차단).

### 3.3 `internal/portability/export.go` (NEW, ~90 LOC)

```go
// ExportOptions controls collection and output.
type ExportOptions struct {
    DataDir    string        // ~/.elnath
    WikiDir    string        // overrideable
    OutPath    string        // bundle.eln
    Passphrase []byte
    Logger     *slog.Logger
}

// Export collects Elnath data, builds manifest, encrypts, writes bundle.
func Export(ctx context.Context, opts ExportOptions) error { ... }
```

수집 대상 (Q1=A 확정):

| Path | Category | Required |
|------|----------|----------|
| `~/.elnath/config.yaml` | config | yes |
| `~/.elnath/data/elnath.db` | db | if exists |
| `~/.elnath/data/wiki.db` | wiki | if exists |
| `~/.elnath/data/lessons.jsonl` | lessons | if exists |
| `~/.elnath/data/lesson_cursors.jsonl` | lessons | if exists |
| `~/.elnath/data/breaker.json` (또는 memory state) | lessons | if exists |
| `~/.elnath/data/audit.jsonl` | lessons | if exists |
| `~/.elnath/data/sessions/*.jsonl` | sessions | if exists |
| `<WikiDir>/**/*.md` | wiki pages | if separate from DataDir |

**제외**:
- `~/.codex/auth.json` (Q1=A)
- `~/Library/LaunchAgents/com.elnath.daemon.plist` (Q5=B)
- macOS Keychain entries
- Log rotations (`*.log.1` 등) — 실용 가치 낮고 bundle 팽창

**Redaction policy** (`internal/portability/redactor.go`, ~30 LOC, Phase 1 포함):

config.yaml 의 provider `api_key` 필드는 bundle 에 **포함** (Q2 암호화로 보호). 단, manifest.json 에 `has_secrets: true` 플래그만 기록. 복원 시 import 로그에 "Secret-bearing fields restored — review on new host" 경고.

### 3.4 `internal/portability/import.go` (NEW, ~90 LOC)

```go
type ImportOptions struct {
    BundlePath string
    TargetDir  string // ~/.elnath override
    Passphrase []byte
    DryRun     bool
    Force      bool
    Logger     *slog.Logger
}

// Import decrypts, verifies, and optionally applies a bundle.
func Import(ctx context.Context, opts ImportOptions) (*ImportReport, error) { ... }

type ImportReport struct {
    BundleVersion int
    FilesApplied  []string
    FilesSkipped  []string      // --dry-run 시 "would apply"
    Conflicts     []string      // 기존 파일 존재, --force 없으면 abort
    Warnings      []string
}
```

Conflict 전략 (Q3=A):
1. `Force == false` AND 기존 파일 존재 → `ImportReport.Conflicts` 에 수집, 에러 반환 (applied 0).
2. `Force == true` → 기존 파일은 `<path>.preimport.<unix_ts>` 로 rename (백업), 새 파일 배치.
3. `DryRun == true` → FS touch 0, report 만 반환.

원자성:
- 먼저 temp dir (`TargetDir/.import-<ts>/`) 에 모두 해제. temp 위치는 **target 파티션 내** — 목적은 `os.Rename` 이 single fs 여서 atomic.
- 각 파일 해시 재계산 + manifest 대조 → 전부 OK 이면 `os.Rename` 개별 (atomic within fs).
- **EXDEV 처리**: 사용자가 `--target` 으로 다른 파티션 지정 시 temp dir 을 여전히 target 파티션 내 `TargetDir/.import-<ts>/` 로 배치하여 EXDEV 회피. temp dir 생성이 target 파티션에 실패 (권한 등) → `ErrCrossDevice` wrap 후 사용자에게 "target dir 쓰기 권한 없음 또는 read-only fs" 메시지.
- `os.Rename` 이 만에 하나 EXDEV 반환 시 (race: 사용자가 mid-flight 로 mount 변경 등 edge case) → byte copy + `f.Sync()` + remove temp 패턴으로 fallback, 실패 시 `ErrCrossDevice` 반환.
- 임의 단계 실패 시 temp dir 삭제. 기존 파일은 건드리지 않음 (dry-run 동일 효과).

### 3.5 `internal/portability/inventory.go` (NEW, ~50 LOC)

```go
// List returns past export records. Exported bundles drop a manifest-only
// JSON copy under ~/.elnath/portability/history/<timestamp>.json for this.
func List(ctx context.Context, dataDir string) ([]ExportRecord, error) { ... }

type ExportRecord struct {
    Timestamp time.Time
    OutPath   string
    Scope     BundleScope
    ByteSize  int64
}

// Verify opens a bundle (requires passphrase), validates manifest + per-file
// sha256, and reports compatibility with the current host (Elnath version,
// available disk space).
func Verify(ctx context.Context, opts VerifyOptions) (*VerifyReport, error) { ... }

// VerifyOptions mirrors ImportOptions but does not apply changes.
type VerifyOptions struct {
    BundlePath string
    Passphrase []byte
    Logger     *slog.Logger
}

// VerifyReport summarizes per-file integrity + host compatibility.
type VerifyReport struct {
    BundleVersion int
    FileCount     int
    TotalBytes    int64
    SourceHost    string
    ElnathVer     string
    IntegrityOK   bool         // all per-file sha256 matched
    HostWarnings  []string     // e.g., elnath version mismatch, low disk
}
```

List 는 passphrase 없이 동작 (manifest-only 공용 history). Verify 는 passphrase 필수.

### 3.6 `cmd/elnath/cmd_portability.go` (NEW, ~100 LOC)

```go
func runPortability(rt *Runtime, args []string) error {
    if len(args) == 0 {
        return printPortabilityHelp(rt.Out)
    }
    switch args[0] {
    case "export":
        return runPortabilityExport(rt, args[1:])
    case "import":
        return runPortabilityImport(rt, args[1:])
    case "list":
        return runPortabilityList(rt, args[1:])
    case "verify":
        return runPortabilityVerify(rt, args[1:])
    case "help", "--help", "-h":
        return printPortabilityHelp(rt.Out)
    default:
        return fmt.Errorf("unknown subcommand %q (try: export, import, list, verify)", args[0])
    }
}
```

Flag 규약:
- Global: `--data-dir <path>` (default `$HOME/.elnath`)
- `export`: `--out <path>` (required), `--passphrase-file <path>`, `--scope config,db,wiki,lessons,sessions` (default: all)
- `import`: `<bundle.eln>` (positional), `--passphrase-file <path>`, `--dry-run`, `--force`, `--target <path>`
- `list`: no flags beyond global
- `verify`: `<bundle.eln>`, `--passphrase-file <path>`

Dispatcher 등록: `cmd/elnath/commands.go` 에 `"portability": runPortability` 추가. `cmd/elnath/main.go` help 문에 한 줄 추가.

### 3.7 `internal/llm/refreshable.go` (NEW, ~30 LOC)

```go
package llm

// RefreshableProvider is implemented by providers that can refresh their
// credentials (OAuth token rotation, API-key re-exchange, etc.) without a
// user-interactive re-auth flow. Providers that require user interaction
// for refresh MUST NOT implement this interface — callers distinguish
// automatic refresh (this interface) from manual re-auth (not this).
type RefreshableProvider interface {
    Provider
    Refresh(ctx context.Context) error
}

// RefreshIfSupported calls Refresh if the provider implements
// RefreshableProvider; otherwise returns nil (no-op).
func RefreshIfSupported(ctx context.Context, p Provider) error {
    if r, ok := p.(RefreshableProvider); ok {
        return r.Refresh(ctx)
    }
    return nil
}
```

**CodexOAuthProvider 변경** (`codex_oauth.go`):
- 기존 private `refresh()` 메서드를 public `Refresh(ctx context.Context) error` 로 승격.
- 시그니처 통일: `func (p *CodexOAuthProvider) Refresh(ctx context.Context) error`.
- 내부 호출자 (`streamWithRefresh` 등) 는 새 이름 사용.

**AnthropicProvider 변경**: 없음 (API-key 기반, OAuth 아님). Interface assertion test 로만 "API key provider는 refreshable 아님" 명시.

### 3.8 Config 확장 — (없음)

Q6=B 결정으로 F-6 에선 config 필드 추가 안 함. 향후 Anthropic OAuth refresh 구현 시 `Anthropic.OAuthRefresh bool` 등이 추가될 수 있으나 spec 외.

---

## 4. Bundle format (정밀 스펙)

`.eln` 파일 구조:

**Bundle format v2** (streaming chunked AEAD — 단일 큰 AEAD.Seal 호출의 메모리 spike 회피):

```
offset  size   content
0       9      "ELNBUNDLE"     (magic header, plain)
9       1      format version  (0x02)
10      16     salt             (scrypt)
26      4      chunk_size       (uint32 BE, 예: 16 MiB = 0x01000000)
30      ...    frames until EOF

each frame:
  0    12     nonce         (per-chunk, random)
  12   4      ct_len        (uint32 BE, 실 ciphertext 바이트 수, tag 포함)
  16   ct_len ciphertext    (AES-256-GCM output; 16-byte tag 는 AEAD.Seal 이
                             ciphertext 끝에 자동 append — 별도 필드 저장 금지)
```

Export/Import 양쪽 모두 chunk 단위 streaming. 메모리 상한 = `chunk_size` (기본 16 MiB). 수백 MB wiki 도 OOM 없음. 단일 chunk 변조 시에도 해당 chunk 복호화 실패 → `ErrBadPassphrase` (GCM per-chunk authentication tag).

**API**:
- `SealWriter(dst io.Writer, pass) (io.WriteCloser, error)` — 스트리밍 암호화. tar+gzip writer 를 여기에 chain.
- `OpenReader(src io.Reader, pass) (io.Reader, error)` — 스트리밍 복호화. gzip+tar reader 가 여기서 읽음.
- `Seal(plaintext, pass) ([]byte, error)`, `Open(sealed, pass) ([]byte, error)` — 작은 데이터용 편의 함수 (내부적으로 streaming 사용). 테스트·manifest 등 소량에만 사용.

tar.gz 내부 레이아웃:

```
bundle.json            ← Manifest (생성 시 마지막에 먼저 쓰되 tar 내 첫 entry 되도록 보장)
config/config.yaml     ← ~/.elnath/config.yaml
data/elnath.db         ← SQLite
data/wiki.db
data/lessons.jsonl
data/lesson_cursors.jsonl
data/breaker.json
data/audit.jsonl
data/sessions/<session-id>.jsonl
...
wiki/<page>.md         ← WikiDir 가 DataDir 밖일 때만
```

Streaming: Exporter 는 tar entry 를 쓰면서 개별 파일의 SHA256 를 동시 계산해 manifest 에 누적. 메모리 사용 O(파일 크기 × 해시 state) ≪ O(전체 bundle).

### Manifest 예시

```json
{
  "version": 1,
  "created_at": "2026-04-14T18:25:00Z",
  "source_host": "stello-mbp",
  "elnath_version": "v0.4.0",
  "scope": {"config":true,"db":true,"wiki":true,"lessons":true,"sessions":true},
  "files": [
    {"rel_path":"config/config.yaml","size":1234,"sha256":"abc..."},
    {"rel_path":"data/elnath.db","size":5242880,"sha256":"def..."}
  ]
}
```

---

## 5. Tests

### 5.1 Unit tests

**`internal/portability/crypto_test.go`**:
- Seal → Open round-trip: plaintext 보존.
- Wrong passphrase: `ErrBadPassphrase` 반환 (decryption fail, not panic).
- Tampered ciphertext (1 byte flip): auth fail.
- Empty plaintext: round-trip 허용.
- Long plaintext (1 MB): 성능 sanity (<1s dev machine).

**`internal/portability/bundle_test.go`**:
- Manifest JSON round-trip.
- Version mismatch detection.

**`internal/portability/export_test.go`**:
- 임시 `DataDir` 구성 (config.yaml + dummy elnath.db + lessons.jsonl) → Export → 결과 bundle 존재, size > 0.
- Scope filter: `scope=config` 만 활성 → DB/lessons 제외.
- 빈 DataDir: bundle 생성 성공, manifest.files 최소 1개 (config 가 필수).

**`internal/portability/import_test.go`**:
- Export → Import (다른 TargetDir) round-trip: 파일 내용 bitwise 일치 (config.yaml, elnath.db).
- Conflict without Force: `ErrConflict`, FilesApplied=[].
- Conflict with Force: 기존 파일 `*.preimport.*` 로 rename 확인.
- DryRun: FS 변경 0, report 의 FilesSkipped 는 expected.
- Manifest sha256 mismatch: `ErrIntegrity` (tampered bundle 시뮬레이션).

**`internal/portability/inventory_test.go`**:
- List on empty history → `[]`.
- Export 2회 → List 2 records, 최신순.
- Verify valid bundle → report PASS.
- Verify mismatched version → WARN 포함.

**`internal/llm/refreshable_test.go`**:
- `RefreshIfSupported(ctx, mockNonRefreshable)` → nil, Refresh 호출 안 됨.
- `RefreshIfSupported(ctx, mockRefreshable)` → Refresh 1회 호출.
- CodexOAuthProvider → RefreshableProvider 로 type-assert 가능.
- AnthropicProvider → type-assert 실패 (현 API-key 패턴 유지 확인).

### 5.2 CLI / integration

**`cmd/elnath/cmd_portability_test.go`**:
- `runPortability([]string{})` → help 출력.
- `runPortability([]{"unknown"})` → 에러.
- 임시 DataDir + `export --out /tmp/x.eln --passphrase-file ...` → 성공 종료 코드.
- 동일 bundle 을 `import --dry-run` → 변경 0, stdout 에 "would apply N files".

**End-to-end smoke** (`scripts/portability_smoke.sh`, 선택):
- `elnath portability export --out /tmp/x.eln` (passphrase env) → `rm -rf ~/.elnath` → `elnath portability import /tmp/x.eln --passphrase-file ...` → `elnath lessons stats` 가 원래 수치 출력.

Smoke 는 CI 아닌 dog-food 검증용. CI 는 unit + integration 만.

---

## 6. Scope boundaries

**In scope** (이 spec):
- `internal/portability` 신규 패키지 (bundle/crypto/export/import/inventory/redactor)
- `cmd/elnath/cmd_portability.go` + dispatcher 등록
- `internal/llm/refreshable.go` + `CodexOAuthProvider.Refresh` public 승격
- Unit + integration tests
- `cmd_portability` help 문 (Q16 man-page 친화 스타일 적용 예시 용으로도 사용)
- `PHASE-F6-LB6-OPENCODE-PROMPT.md`

**Out of scope** — defer:

1. **Codex auth.json 포함** (Q1=A 결정). 사용자가 수동 복사. 필요성 증가 시 별도 phase.
2. **Anthropic OAuth Refresh 구현** (Q6=B). Interface 만. 구현은 Anthropic OAuth 복원 시.
3. **plist/systemd 이전** (Q5=B). `elnath service install` 별도 명령. 현재 파일 `cmd/elnath/cmd_daemon*.go` 에 이미 install 기능 있는지 확인 필요. 없으면 LB6 와 별개 미니 feature.
4. **Credential rotation** (사고 대응). 별도 보안 phase.
5. **Incremental export** (diff bundle). 현재는 full snapshot.
6. **Multi-host sync**. portability 는 1→1 이전만. sync 는 외부 도구 (rsync 등) 활용 가정.
7. **Bundle signing** (PGP 등). 암호화 하나로 integrity + confidentiality 충족. 전자서명은 공개 배포 시 필요.
8. **Keychain 연동** (Q2=A 로 passphrase 선택). 향후 `--keychain` 플래그로 passphrase 저장 자동화 가능.

---

## 7. Verification gates

### 7.1 Build/Test

```bash
cd /Users/stello/elnath
# §3.0 prerequisites: x/crypto, x/term 이 go.mod 에 기록되어 있는지 확인
go mod tidy
grep -E "golang.org/x/(crypto|term)" go.mod   # expected: 두 항목 모두 존재

go vet ./internal/portability/... ./internal/llm/... ./cmd/elnath/...
go test -race ./internal/portability/... ./internal/llm/... ./cmd/elnath/...
make build
```

### 7.2 Smoke

```bash
# 임시 sandbox (passphrase 12+ chars to avoid weak-warn interactive prompt)
export ELNATH_PORTABILITY_PASSPHRASE="test-passphrase-for-ci"
TMP_A=$(mktemp -d)
TMP_B=$(mktemp -d)

# source 머신: elnath 에 testdata 주입
mkdir -p "$TMP_A/data/sessions"
echo "test: ok" > "$TMP_A/config.yaml"
echo '{"id":"1"}' > "$TMP_A/data/lessons.jsonl"

./elnath --data-dir "$TMP_A" portability export --out /tmp/bundle.eln

./elnath --data-dir "$TMP_B" portability verify /tmp/bundle.eln
# expected: PASS, files=2

./elnath --data-dir "$TMP_B" portability import /tmp/bundle.eln --dry-run
# expected: 0 FS change, report lists 2 files

./elnath --data-dir "$TMP_B" portability import /tmp/bundle.eln
# expected: files applied, $TMP_B/config.yaml == $TMP_A/config.yaml (bitwise)
diff "$TMP_A/config.yaml" "$TMP_B/config.yaml"
```

### 7.3 Code hygiene

```bash
# 하위호환 확인: 기존 ~/.elnath/ 는 건드리지 않음
grep -r "os.Remove\|os.RemoveAll" internal/portability/ | grep -v _test.go
# expected: 오직 temp-dir cleanup 만

# RefreshableProvider 인터페이스 assertion
grep -r "llm.RefreshableProvider" internal/ cmd/
# expected: refreshable.go 정의 + codex_oauth.go implement + test assertion
```

---

## 8. Commit message template

```
feat: phase F-6 LB6 auth/credential portability

Elnath 자료 (config, DB, wiki, lessons, sessions) 를 AES-256-GCM
암호화 bundle (.eln) 로 export/import. Codex auth.json 은 사용자
수동 이전 (scope 외, layering 유지).

- internal/portability: Manifest, Exporter, Importer, crypto (scrypt+GCM),
  redactor, inventory (list/verify)
- cmd elnath portability {export,import,list,verify}
  - import: --dry-run, --force, conflict → *.preimport.<ts> 백업
  - verify: passphrase 요구 + manifest sha256 per-file 검증
- internal/llm/refreshable.go: RefreshableProvider interface +
  RefreshIfSupported helper. CodexOAuthProvider.Refresh public 승격.
  AnthropicProvider 는 implement 안 함 (API-key, not OAuth)
- Unit + integration tests. End-to-end smoke script optional.

Deferred (F-6 후):
- ~/.codex/auth.json 포함 (Q1=A 결정)
- Anthropic OAuth refresh 실 구현 (Q6=B 인터페이스만)
- plist/systemd 이전 (별도 elnath service install)
- Credential rotation (보안 phase)
```

---

## 9. OpenCode prompt

`docs/specs/PHASE-F6-LB6-OPENCODE-PROMPT.md` (별도 작성).

내부 구조 (집계):
- §1 Context (메모리 + 본 spec + decisions 요약)
- §2 파일 생성/수정 목록 (절대 경로)
- §3 상세 구현 지시 (파일별)
- §4 테스트 요구
- §5 Verification gate 명령
- §6 Commit message 템플릿
- §7 자가 리뷰 체크리스트

---

## 10. Risks & mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| scrypt 파라미터가 약하면 passphrase brute-force 가능 | HIGH | N=32768, r=8, p=1 (OWASP 최소 권장). Phase 2 에서 파라미터 upgrade 가능성. |
| Lost passphrase = 데이터 복구 불가 | HIGH | Help 문과 export 성공 메시지에 명시: "Store passphrase in password manager. Loss = unrecoverable." |
| Manifest.Version 미래 호환 | MED | version=1 fixed. 2+ 도입 시 import 는 호환성 매트릭스 체크 + 사용자 명시 동의. |
| Import 중단 시 부분 적용 | HIGH | Atomic rename 패턴 (temp dir → target). 중단 시 기존 파일 무손상. |
| Bundle 내 config API key 평문 노출 | HIGH | AEAD 암호화로 1차 보호. 추가로 manifest 에 `has_secrets: true` 표기 + import 로그 경고. |
| tar 내 path traversal (`../../etc/passwd`) | CRITICAL | Importer 는 각 entry 를 `filepath.Clean` + DataDir prefix 강제. `strings.HasPrefix(cleaned, targetDir+"/")` 검증 실패 시 abort. |
| Large wiki (~수백 MB) 시 메모리 폭주 | MED | Export/Import 전부 streaming tar + streaming GCM chunks. 메모리 O(chunk) <= 64 KB. |
| Codex `Refresh` public 승격이 기존 caller 의 receiver signature 충돌 | LOW | 기존 private 메서드는 이미 `*CodexOAuthProvider` 에 붙어 있음. 이름만 변경. 호출처 1-2 곳. |

---

## 11. Estimated LOC breakdown

| File | NEW/MODIFY | Est LOC |
|------|-----------|---------|
| `internal/portability/errors.go` | NEW | 10 |
| `internal/portability/bundle.go` | NEW | 60 |
| `internal/portability/crypto.go` | NEW | 80 |
| `internal/portability/export.go` | NEW | 90 |
| `internal/portability/import.go` | NEW | 90 |
| `internal/portability/inventory.go` | NEW | 50 |
| `internal/portability/redactor.go` | NEW | 30 |
| `cmd/elnath/cmd_portability.go` | NEW | 100 |
| `cmd/elnath/commands.go` | MODIFY | +3 |
| `internal/llm/refreshable.go` | NEW | 30 |
| `internal/llm/codex_oauth.go` | MODIFY | +5 (Refresh public 승격) |
| `go.mod` / `go.sum` | MODIFY | +N (x/crypto, x/term 추가) |
| Tests (7 files, _test.go) | NEW | ~250 |

**Production 소계**: ~540 LOC (spec §0 추정 450 보다 많음 — import/export 의 streaming + conflict 처리가 기존 추정보다 무거움).
**Test 소계**: ~250 LOC.
**Total**: ~790 LOC.

추정 차이 원인: bundle format 의 streaming tar + integrity verification 경로가 초기 추정의 단순 "파일 복사" 모델보다 복잡. 필요 시 Phase 2 의 `inventory.go` / `redactor.go` 를 최소화하여 300 LOC 대로 압축 가능하지만 verify/list 가치 훼손.

---

## 12. Next after this spec

1. 사용자 리뷰 → 수정 반영
2. LB7 / F7 / F8 spec 3 개 병렬 작성 (executor subagents)
3. opencode prompt 4 개 작성 (LB6/LB7/F7/F8)
4. opencode 4 세션 병렬 위임

---

## 13. Spec-stage decisions (사용자 확정 2026-04-14)

| ID | Question | Decision | Rationale |
|----|----------|----------|-----------|
| S1 | BundleScope 기본값 | **all** (config + db + wiki + lessons + sessions) | 무플래그 export = 완전 복원 보장. `--scope` 플래그는 미래 부분 백업 옵션으로 보존. |
| S2 | Sessions 포함 기본값 | **on** | Sessions = 과거 대화 JSONL 파일. 토큰 비용 0 (순수 파일). 연속성 복원 위해 필수. 용량 부담 시 `--scope config,db,wiki,lessons` 로 명시 제외. |
| S3 | Bundle 확장자 | **`.eln`** | 짧고 elnath-specific. 타 포맷과 충돌 없음. |
| S4 | Passphrase 강도 게이트 | **길이 기반 3단계** | (a) 8자 미만 거부 (GPU brute force 즉시 crack 가능), (b) 8-12자 경고 + `Continue? [y/N]` 대화형 confirm (기본 N), (c) 12+ 통과. Env/`--passphrase-file` 경로에선 경고만 stderr, 흐름 막지 않음 (CI 우호). `--force-weak` 플래그로 confirm 우회 가능. zxcvbn 등 외부 라이브러리 미사용 (의존성 증가 회피, false positive 회피). |

### S4 의 이유 (Elnath 목표 관점)

- Passphrase 는 **연 1-2회** 입력하는 이삿짐 자물쇠. 일상 Elnath 사용 (`elnath run`, daemon, Telegram) 과 무관.
- Bundle 은 USB/클라우드/메신저로 이동할 수 있음 → passphrase 가 유일한 2차 방어선.
- Elnath 는 단일 사용자 (stello) CLI 도구 → 과도한 paternalism 불필요.
- 1Password/Keychain 같은 매니저에 저장 권장 (README / help 문에 명시).
- 생존 프로젝트 관점 (API key / Codex OAuth 토큰 유출 = 비용 폭탄) 에서 최소 방어선 강제가 합리적.
