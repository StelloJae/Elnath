# Phase F-6 LB6 — OpenCode Prompt (Auth/Credential Portability)

## 1. Context

Elnath 는 Go 로 작성한 자율 AI 비서 daemon (`/Users/stello/elnath/`, 브랜치 `feat/telegram-redesign`). Phase F-5 Provider Patch (`a18d026`) 완료 상태에서 이 작업을 시작한다. F-6 LB6 는 Elnath 설정·데이터를 한 머신에서 다른 머신으로 안전하게 **export → 전송 → import** 하는 portability 기능을 추가한다.

구현 진실 원천: `docs/specs/PHASE-F6-LB6-AUTH-PORTABILITY.md` (변경 금지). 설계 결정 Q1-Q6 는 전부 locked — `PHASE-F6-DECISIONS.md` 에 기록됨. 이 prompt 는 spec 을 구현 단계별 지시로 번역한 것이며, spec 과 충돌 시 **spec 이 우선**.

LB6 는 다른 LB 와 병렬 진행되나 LB6 내부는 Phase 1 (scaffolding + export/import happy path) → Phase 2 (verify/list + RefreshableProvider + dry-run + error UX) 순서로 순차 실행.

---

## 2. Prerequisites — 반드시 먼저 실행

`golang.org/x/crypto` (scrypt) 와 `golang.org/x/term` (TTY passphrase input) 은 현재 `go.mod` 에 없다. **이 단계를 건너뛰면 빌드가 즉시 실패한다.**

```bash
cd /Users/stello/elnath
go get golang.org/x/crypto@latest
go get golang.org/x/term@latest
go mod tidy
```

완료 확인:

```bash
grep -E "golang.org/x/(crypto|term)" go.mod
# 두 줄 모두 출력되어야 함
```

---

## 3. Scope

### 신규 파일 (11)

| 파일 | 대략 LOC |
|------|---------|
| `internal/portability/errors.go` | ~10 |
| `internal/portability/bundle.go` | ~60 |
| `internal/portability/crypto.go` | ~80 |
| `internal/portability/export.go` | ~90 |
| `internal/portability/import.go` | ~90 |
| `internal/portability/inventory.go` | ~50 |
| `internal/portability/redactor.go` | ~30 |
| `cmd/elnath/cmd_portability.go` | ~100 |
| `internal/llm/refreshable.go` | ~30 |
| Tests (7개 `_test.go`) | ~250 |

### 수정 파일 (3)

| 파일 | 변경 내용 |
|------|---------|
| `cmd/elnath/commands.go` | `"portability": runPortability` 추가 (+3 줄) |
| `internal/llm/codex_oauth.go` | `refresh()` → `Refresh(ctx context.Context) error` public 승격 (+5 줄) |
| `go.mod` / `go.sum` | x/crypto, x/term 추가 |

---

## 4. Task — 파일별 구현 지시

### 4.0 `internal/portability/errors.go` (NEW)

전체 패키지에서 공유하는 sentinel error 5개. **이름을 임의로 바꾸지 말 것** — 테스트와 CLI 에서 `errors.Is` 로 직접 참조한다.

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

**중요**: `ErrBadPassphrase` 는 wrong passphrase 와 tampered bundle 을 구분하지 않는다. 이는 AES-256-GCM 의 특성 (auth tag 실패 = 동일 오류)이며 의도된 동작이다. 두 케이스를 별도 오류로 분기하려 하지 말 것.

### 4.1 `internal/portability/bundle.go` (NEW)

Manifest 구조 + bundle 상수 정의.

```go
package portability

import "time"

const (
    BundleVersion = 1
    ManifestName  = "bundle.json"
    MagicHeader   = "ELNBUNDLE" // .eln 파일 첫 9 bytes (암호화 이전, plain)
)

type Manifest struct {
    Version    int            `json:"version"`
    CreatedAt  time.Time      `json:"created_at"`
    SourceHost string         `json:"source_host"`    // optional — privacy 이유로 빈값 허용
    ElnathVer  string         `json:"elnath_version"`
    HasSecrets bool           `json:"has_secrets"`    // config.yaml 에 api_key 비어있지 않으면 true
    Files      []ManifestFile `json:"files"`
    Scope      BundleScope    `json:"scope"`
}

type ManifestFile struct {
    RelPath string `json:"rel_path"` // ~/.elnath 기준 상대 경로
    Size    int64  `json:"size"`
    SHA256  string `json:"sha256"`   // hex string
}

type BundleScope struct {
    Config   bool `json:"config"`
    DB       bool `json:"db"`
    Wiki     bool `json:"wiki"`
    Lessons  bool `json:"lessons"`
    Sessions bool `json:"sessions"`
}
```

**`HasSecrets` 필드 설정**: Export 시 `config.yaml` 을 파싱하여 provider 섹션의 `api_key` 값이 non-empty 이면 `Manifest.HasSecrets = true` 로 설정. 이 필드를 누락하거나 항상 false 로 두지 말 것.

### 4.2 `internal/portability/crypto.go` (NEW)

AES-256-GCM + scrypt KDF. `.eln` 파일 binary 레이아웃:

```
offset  size  content
0       9     "ELNBUNDLE" (magic, plain)
9       16    salt (scrypt)
25      12    nonce (GCM)
37      N+16  ciphertext (tar.gz stream; GCM 16-byte auth tag 는 AEAD.Seal 이 자동 append)
```

**핵심 경고**: `AEAD.Seal` 은 16-byte GCM authentication tag 를 ciphertext 끝에 **자동으로** append 한다. 이 tag 를 별도 필드로 저장하거나 직접 잘라내지 말 것 — `Seal/Open` 대칭성이 깨진다.

```go
const (
    scryptN      = 1 << 15 // 32768
    scryptR      = 8
    scryptP      = 1
    scryptKeyLen = 32 // AES-256
    saltLen      = 16
    nonceLen     = 12 // GCM standard
)

const (
    minPassphraseLen  = 8
    weakPassphraseLen = 12
)

func Seal(plaintext, passphrase []byte) ([]byte, error)
func Open(sealed, passphrase []byte) ([]byte, error)
func deriveKey(passphrase, salt []byte) ([]byte, error)
func ValidatePassphrase(pass []byte) (warning error, fatal error)
```

**Passphrase zeroize**: `deriveKey` 호출 직후 `for i := range passphrase { passphrase[i] = 0 }` 로 즉시 zeroize. Go GC 는 slice header 의 copy 를 남길 수 있으나 대부분 경로를 차단한다.

**`ValidatePassphrase` 3단계**:
- `len(pass) < 8` → `fatal` 반환 (hard reject)
- `8 <= len(pass) < 12` → `warning` 반환, `fatal = nil` (경고 수준)
- `len(pass) >= 12` → 둘 다 nil (통과)

**Passphrase 입력 경로** (CLI 쪽에서 처리):
1. `--passphrase-file <path>` → 파일 첫 줄 trim
2. env `ELNATH_PORTABILITY_PASSPHRASE` → `slog.Warn` + `fmt.Fprintln(os.Stderr, ...)` 이중 경고
3. TTY interactive → `golang.org/x/term.ReadPassword`

### 4.3 `internal/portability/export.go` (NEW)

```go
type ExportOptions struct {
    DataDir    string       // ~/.elnath
    WikiDir    string       // DataDir 와 다를 때만 설정
    OutPath    string       // bundle.eln
    Passphrase []byte
    Scope      BundleScope  // zero value = all true
    Logger     *slog.Logger
}

func Export(ctx context.Context, opts ExportOptions) error
```

수집 대상 (Q1=A 확정):

| 경로 | 카테고리 | 필수 여부 |
|------|---------|---------|
| `~/.elnath/config.yaml` | config | yes |
| `~/.elnath/data/elnath.db` | db | if exists |
| `~/.elnath/data/wiki.db` | wiki | if exists |
| `~/.elnath/data/lessons.jsonl` | lessons | if exists |
| `~/.elnath/data/lesson_cursors.jsonl` | lessons | if exists |
| `~/.elnath/data/breaker.json` | lessons | if exists |
| `~/.elnath/data/audit.jsonl` | lessons | if exists |
| `~/.elnath/data/sessions/*.jsonl` | sessions | if exists |
| `<WikiDir>/**/*.md` | wiki pages | DataDir 밖일 때만 |

**제외 목록** (절대 포함하지 말 것):
- `~/.codex/auth.json` (Q1=A)
- `~/Library/LaunchAgents/*.plist` (Q5=B)
- `*.log`, `*.log.1` 등 로그 파일

**streaming 구현**: tar entry 를 쓰면서 각 파일의 SHA256 를 동시 계산해 manifest 에 누적. 메모리 사용 O(파일 크기 × 해시 상태) 로 제한. export 성공 메시지에 반드시 포함: **"Store passphrase in password manager. Loss = unrecoverable."**

### 4.4 `internal/portability/import.go` (NEW)

```go
type ImportOptions struct {
    BundlePath string
    TargetDir  string // default ~/.elnath
    Passphrase []byte
    DryRun     bool
    Force      bool
    Logger     *slog.Logger
}

func Import(ctx context.Context, opts ImportOptions) (*ImportReport, error)

type ImportReport struct {
    BundleVersion int
    FilesApplied  []string
    FilesSkipped  []string  // --dry-run 시 "would apply"
    Conflicts     []string  // Force=false 시 abort
    Warnings      []string
}
```

**충돌 전략 (Q3=A)**:
1. `Force=false` AND 기존 파일 존재 → `ImportReport.Conflicts` 에 수집, `ErrConflict` 반환 (적용 0)
2. `Force=true` → 기존 파일을 `<path>.preimport.<unix_ts>` 로 rename 후 새 파일 배치
3. `DryRun=true` → FS touch 없음, report 만 반환

**원자성 구현 (EXDEV 회피 필수)**:
- temp dir 위치를 반드시 **target 파티션 내** `TargetDir/.import-<ts>/` 로 생성
- 모두 해제 후 파일별 SHA256 재계산 + manifest 대조 → 전부 OK 이면 `os.Rename` 수행
- **EXDEV fallback**: `os.Rename` 이 EXDEV 반환 시 (mid-flight mount 변경 edge case) → byte copy + `f.Sync()` + temp 파일 삭제 패턴으로 재시도. 재시도도 실패 시 `ErrCrossDevice` 반환
- temp dir 생성 실패 (권한, read-only fs) → `ErrCrossDevice` wrap + "target dir 쓰기 권한 없음 또는 read-only fs" 메시지
- 중단 발생 시 temp dir 삭제; 기존 파일은 변경 없음

**path traversal 방어 (CRITICAL)**: tar entry 마다 반드시:
```go
cleaned := filepath.Clean(filepath.Join(targetDir, entry.Name))
if !strings.HasPrefix(cleaned, targetDir+string(os.PathSeparator)) {
    return fmt.Errorf("path traversal detected: %s", entry.Name)
}
```
이 검증을 생략하면 `../../etc/passwd` 류 공격에 노출된다.

**has_secrets 경고**: import 완료 후 `ImportReport.Manifest.HasSecrets == true` 이면 Warnings 에 "Secret-bearing fields restored — review on new host" 추가.

### 4.5 `internal/portability/inventory.go` (NEW)

```go
func List(ctx context.Context, dataDir string) ([]ExportRecord, error)
func Verify(ctx context.Context, opts VerifyOptions) (*VerifyReport, error)

type ExportRecord struct {
    Timestamp time.Time
    OutPath   string
    Scope     BundleScope
    ByteSize  int64
}

type VerifyOptions struct {
    BundlePath string
    Passphrase []byte
    Logger     *slog.Logger
}

type VerifyReport struct {
    BundleVersion int
    FileCount     int
    TotalBytes    int64
    SourceHost    string
    ElnathVer     string
    IntegrityOK   bool
    HostWarnings  []string
}
```

export 이력: bundle 생성 시 manifest-only JSON copy 를 `~/.elnath/portability/history/<timestamp>.json` 에 저장. `List` 는 해당 디렉터리를 스캔, 최신순 정렬. `List` 는 passphrase 불필요. `Verify` 는 passphrase 필수 (bundle open 후 per-file SHA256 검증).

### 4.6 `internal/portability/redactor.go` (NEW)

config.yaml 에서 api_key 를 감지하여 `Manifest.HasSecrets` 판별에 사용하는 helper. config.yaml 내용 자체는 bundle 에 **평문 포함** (AEAD 암호화로 보호됨). redactor 는 manifest 플래그 설정 목적으로만 api_key 존재 여부를 체크한다.

```go
// HasSecretAPIKeys reports whether any provider section in yamlContent
// contains a non-empty api_key value. Used to set Manifest.HasSecrets.
func HasSecretAPIKeys(yamlContent []byte) bool
```

### 4.7 `cmd/elnath/cmd_portability.go` (NEW)

```go
func runPortability(rt *Runtime, args []string) error {
    if len(args) == 0 {
        return printPortabilityHelp(rt.Out)
    }
    switch args[0] {
    case "export":  return runPortabilityExport(rt, args[1:])
    case "import":  return runPortabilityImport(rt, args[1:])
    case "list":    return runPortabilityList(rt, args[1:])
    case "verify":  return runPortabilityVerify(rt, args[1:])
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
- `list`: global flags only
- `verify`: `<bundle.eln>`, `--passphrase-file <path>`

TTY 감지: `golang.org/x/term.IsTerminal(int(os.Stdin.Fd()))` 로 passphrase weak 경고 분기. TTY 이면 `Continue with weak passphrase? [y/N]` (기본 N), non-TTY 이면 stderr 에 경고 후 계속.

### 4.8 `cmd/elnath/commands.go` 수정

dispatcher map 에 한 줄 추가:

```go
"portability": runPortability,
```

`main.go` 또는 help 문에 `portability` 한 줄 추가.

### 4.9 `internal/llm/refreshable.go` (NEW)

```go
package llm

import "context"

// RefreshableProvider is implemented by providers that can refresh credentials
// automatically (OAuth token rotation). Providers requiring user interaction
// MUST NOT implement this interface.
type RefreshableProvider interface {
    Provider
    Refresh(ctx context.Context) error
}

// RefreshIfSupported calls Refresh if the provider implements RefreshableProvider;
// otherwise returns nil (no-op).
func RefreshIfSupported(ctx context.Context, p Provider) error {
    if r, ok := p.(RefreshableProvider); ok {
        return r.Refresh(ctx)
    }
    return nil
}
```

### 4.10 `internal/llm/codex_oauth.go` 수정

기존 private `refresh()` 메서드를 public 으로 승격:

```go
// Refresh implements RefreshableProvider.
func (p *CodexOAuthProvider) Refresh(ctx context.Context) error {
    // 기존 private refresh() 로직 그대로 이동. 내부 호출자 (streamWithRefresh 등)
    // 도 새 이름으로 갱신.
}
```

`AnthropicProvider` 는 변경 없음. API-key 기반이므로 `RefreshableProvider` 를 구현하지 않는 것이 의도된 동작.

---

## 5. Behavior Invariants — 구현 중 위반 금지

다음 규칙은 구현 도중 어기면 안 된다.

1. **ErrBadPassphrase 단일화**: wrong passphrase 와 tampered bundle 을 구별하지 않는다. GCM auth tag 실패는 하나의 에러로 처리. 두 케이스를 나누는 코드 추가 금지.

2. **AEAD.Seal tag 자동 처리**: GCM 16-byte authentication tag 를 별도 필드로 저장하거나 수동으로 잘라내지 않는다. `Seal/Open` 이 대칭성을 보장한다.

3. **os.Rename EXDEV 회피**: temp dir 은 반드시 target 파티션 내에 생성. `os.Rename` 이 cross-device 로 실패하는 것은 설계 오류다.

4. **Passphrase zeroize**: scrypt 연산 직후 passphrase `[]byte` 를 for-range 로 0 fill. 이후 코드에서 재사용하지 않는다.

5. **path traversal 방어 필수**: tar entry 마다 `filepath.Clean` + target prefix 검증. 실패 시 abort (skip 이 아닌 abort).

6. **config api_key 는 bundle 에 포함**: AEAD 암호화가 보호함. redaction 해서 제거하지 말 것. 단, manifest `has_secrets: true` 로 표기 필수.

7. **CodexOAuthProvider 만 Refresh 구현**: AnthropicProvider 는 구현하지 않는다. 테스트에서 type-assert 실패를 명시적으로 확인.

8. **scope=all 이 default**: `ExportOptions.Scope` zero value 는 모든 카테고리 포함으로 처리.

---

## 6. Tests Required

### 6.1 Unit tests

**`internal/portability/crypto_test.go`**:
- `TestCryptoSealOpenRoundTrip` — 임의 plaintext Seal → Open, bitwise 일치
- `TestCryptoWrongPassphrase` — Open 에 다른 passphrase → `ErrBadPassphrase`
- `TestCryptoTamperedCiphertext` — ciphertext 1 byte flip → auth fail (`ErrBadPassphrase`)
- `TestCryptoEmptyPlaintext` — empty plaintext 허용
- `TestCryptoLargePlaintext` — 1 MB, 1s 이내 완료
- `TestValidatePassphrase` — 7자: fatal, 9자: warning+nil, 12자: nil+nil

**`internal/portability/bundle_test.go`**:
- `TestManifestJSONRoundTrip` — JSON marshal/unmarshal, 필드 보존
- `TestManifestVersionMismatch` — version 불일치 감지

**`internal/portability/export_test.go`**:
- `TestExportHappyPath` — 임시 DataDir (config.yaml + dummy db + lessons) → bundle 존재, size > 0
- `TestExportScopeFilter` — scope=config 만 → db/lessons 제외 확인
- `TestExportEmptyDataDir` — 빈 DataDir → 성공, manifest.files >= 1 (config 필수)

**`internal/portability/import_test.go`**:
- `TestImportRoundTrip` — Export → 다른 TargetDir 로 Import → config.yaml bitwise 일치
- `TestImportConflictNoForce` — 기존 파일 존재 + Force=false → `ErrConflict`, FilesApplied=[]
- `TestImportConflictWithForce` — Force=true → 기존 파일 `.preimport.*` 로 rename 확인
- `TestImportDryRun` — DryRun=true → FS 변경 0, FilesSkipped expected
- `TestImportIntegrityFail` — manifest sha256 mismatch → `ErrIntegrity`
- `TestImportPathTraversal` — `../etc/passwd` entry → abort

**`internal/portability/inventory_test.go`**:
- `TestListEmpty` — history 없음 → 빈 slice
- `TestListAfterTwoExports` — Export 2회 → List 2 records, 최신순
- `TestVerifyValidBundle` — valid bundle → IntegrityOK=true
- `TestVerifyVersionMismatch` — version 불일치 → HostWarnings 포함

**`internal/llm/refreshable_test.go`**:
- `TestRefreshIfSupported_NonRefreshable` — mock non-refreshable provider → nil 반환, Refresh 호출 안 됨
- `TestRefreshIfSupported_Refreshable` — mock refreshable provider → Refresh 1회 호출
- `TestCodexOAuthProvider_IsRefreshable` — CodexOAuthProvider type-assert to RefreshableProvider 성공
- `TestAnthropicProvider_NotRefreshable` — AnthropicProvider type-assert 실패 (의도된 동작)

### 6.2 CLI/Integration tests

**`cmd/elnath/cmd_portability_test.go`**:
- `TestPortabilityHelp` — `runPortability([]string{})` → help 출력
- `TestPortabilityUnknownSubcommand` — `runPortability([]string{"unknown"})` → 에러
- `TestPortabilityExportIntegration` — 임시 DataDir + `--passphrase-file` → 성공 종료
- `TestPortabilityImportDryRun` — export 후 `--dry-run` → 변경 0, stdout "would apply N files"

---

## 7. Verification Gates

구현 완료 후 아래 명령을 **순서대로** 실행하고 **모두 exit 0** 이어야 한다.

```bash
cd /Users/stello/elnath

# Prerequisites 확인
go mod tidy
grep -E "golang.org/x/(crypto|term)" go.mod
# 기대: 두 줄 모두 출력

# Static analysis
go vet ./internal/portability/... ./internal/llm/... ./cmd/elnath/...

# Tests (race detector 필수)
go test -race ./internal/portability/... ./internal/llm/... ./cmd/elnath/...

# Build
make build
```

기존 패키지 regression 도 함께 확인:

```bash
go test -race ./internal/learning/... ./internal/orchestrator/... ./internal/conversation/...
```

---

## 8. Smoke Test

passphrase 12자 이상을 사용해야 weak-warn 인터랙션을 건너뛸 수 있다.

```bash
# 임시 sandbox
export ELNATH_PORTABILITY_PASSPHRASE="test-passphrase-for-ci"
TMP_A=$(mktemp -d)
TMP_B=$(mktemp -d)

# source: testdata 주입
mkdir -p "$TMP_A/data/sessions"
echo "test: ok" > "$TMP_A/config.yaml"
echo '{"id":"1"}' > "$TMP_A/data/lessons.jsonl"

# export
./elnath --data-dir "$TMP_A" portability export --out /tmp/bundle.eln

# verify (passphrase 필요)
./elnath --data-dir "$TMP_B" portability verify /tmp/bundle.eln
# 기대: PASS, files=2

# import dry-run
./elnath --data-dir "$TMP_B" portability import /tmp/bundle.eln --dry-run
# 기대: 0 FS 변경, "would apply 2 files"

# import real
./elnath --data-dir "$TMP_B" portability import /tmp/bundle.eln
# 기대: files applied

# bitwise 일치 확인
diff "$TMP_A/config.yaml" "$TMP_B/config.yaml"
# 기대: 출력 없음 (동일)
```

---

## 9. Commit Message Template

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

커밋은 하지 말 것. stello 가 직접 실행.

---

## 10. Self-Review Checklist

구현 완료 후 반드시 확인:

- [ ] `go.mod` 에 `golang.org/x/crypto` 와 `golang.org/x/term` 이 모두 존재
- [ ] Sentinel error 5개 정의: `ErrBadPassphrase`, `ErrConflict`, `ErrIntegrity`, `ErrVersion`, `ErrCrossDevice` (이름 정확히 일치)
- [ ] `Manifest.HasSecrets` 필드 정의 + export 시 api_key 감지 후 설정
- [ ] `AEAD.Seal` tag 를 별도 필드로 저장하지 않음 — binary 레이아웃 `MagicHeader|salt|nonce|ciphertext(+tag)` 확인
- [ ] temp dir 을 `TargetDir/.import-<ts>/` 로 생성 (EXDEV 방지)
- [ ] EXDEV fallback 구현: byte copy + `f.Sync()` + temp 삭제
- [ ] tar entry 마다 `filepath.Clean` + target prefix 검증 (path traversal 방어)
- [ ] Passphrase `[]byte` 를 scrypt 직후 for-range zeroize
- [ ] `RefreshIfSupported` helper 구현 — non-refreshable provider 에서 nil 반환
- [ ] `CodexOAuthProvider.Refresh(ctx context.Context) error` public 메서드 존재
- [ ] `AnthropicProvider` 는 `RefreshableProvider` 를 구현하지 않음 (테스트로 검증)
- [ ] `ValidatePassphrase` 3단계 로직: <8 fatal, 8-11 warning, ≥12 통과
- [ ] Smoke test 통과 (`diff` 출력 없음)
- [ ] 커밋 메시지 template 과 일치
- [ ] 기존 기능 regression 없음: `go test -race ./...` 전체 pass

---

## 11. Scope Boundaries — 이번 구현에서 제외

다음 항목은 이번 작업 범위 밖이다. 구현 중 언급하거나 추가하지 말 것:

1. **`~/.codex/auth.json` 포함** — Q1=A 결정. 사용자 수동 이전.
2. **Anthropic OAuth Refresh 실 구현** — Q6=B. interface 정의만. 실 구현은 별도 phase.
3. **plist/systemd 이전** — Q5=B. `elnath service install` 별도 명령.
4. **Credential rotation** — 별도 보안 phase.
5. **Incremental export (diff bundle)** — 현재는 full snapshot.
6. **Multi-host sync** — 1→1 이전만.
7. **Bundle signing (PGP 등)** — AEAD 로 충분.
8. **Keychain 연동** — 향후 `--keychain` 플래그.
9. **Config 필드 추가** — Q6=B 로 F-6 에선 config.go 변경 없음.
10. **scrypt 파라미터 tuning** — N=32768, r=8, p=1 고정. 변경 금지.

---

## 12. Branch / Commit Hygiene

- 브랜치: `feat/telegram-redesign` 위에서 작업 (checkout 이미 되어 있어야 함)
- 부분 커밋 금지 — 전체 구현 완성 후 단일 `feat:` 커밋
- Phase 1 (export/import happy path) 과 Phase 2 (verify/list + RefreshableProvider) 를 같은 커밋에 포함
- 커밋 전 `go test -race ./...` 과 `make build` 모두 pass 필수
- `go.mod` / `go.sum` 도 커밋에 포함 (x/crypto, x/term 의존성 변경)
