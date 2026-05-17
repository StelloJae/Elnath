package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/userfacingerr"
)

// wrapSessionParse tags JSONL parse and scan failures with the ELN-070
// user-facing code so callers can distinguish "session file corrupted"
// from "session not found" (an os.Open error, which is left unwrapped).
func wrapSessionParse(err error, op string) error {
	return userfacingerr.Wrap(userfacingerr.ELN070, err, op)
}

// SessionPersister is an optional secondary persistence backend for sessions.
// Implementations (e.g., SQLite history store) mirror the canonical JSONL
// transcript for indexing/search and must not redefine resume semantics.
type SessionPersister interface {
	PersistSession(sessionID string, messages []llm.Message) error
}

// Session is a persisted conversation stored as a JSONL file.
// Format: first line is a sessionHeader, subsequent lines are llm.Message.
type Session struct {
	ID            string
	path          string
	Principal     identity.Principal // immutable after construction.
	Messages      []llm.Message
	Retirement    *SessionRetirementStatus
	appliedHashes map[string]struct{}
	mu            sync.Mutex
	persister     SessionPersister // optional secondary persistence
	logger        func(msg string, args ...any)
}

// SessionHeader is the JSONL header stored on the first line of each session.
type SessionHeader struct {
	ID        string
	CreatedAt time.Time
	Version   int
	Principal identity.Principal
}

// SessionResumeEvent records a surface transition for a persisted session.
type SessionResumeEvent struct {
	Type      string             `json:"type"`
	Surface   string             `json:"surface"`
	Principal identity.Principal `json:"principal"`
	At        time.Time          `json:"at"`
}

// SessionRetirementEvent records a terminal runtime hint that the session
// should not be silently reused by automatic resume paths.
type SessionRetirementEvent struct {
	Type         string    `json:"type"`
	FailureClass string    `json:"failure_class,omitempty"`
	Reason       string    `json:"reason"`
	NextAction   string    `json:"next_action,omitempty"`
	At           time.Time `json:"at"`
}

// SessionHandoffEvent records an explicit operator handoff state transition.
type SessionHandoffEvent struct {
	Type      string             `json:"type"`
	State     string             `json:"state"`
	Surface   string             `json:"surface,omitempty"`
	Principal identity.Principal `json:"principal,omitempty"`
	Reason    string             `json:"reason,omitempty"`
	At        time.Time          `json:"at"`
}

// SessionHandoffStatus is the latest explicit handoff state for a session.
type SessionHandoffStatus struct {
	State     string
	Surface   string
	Principal identity.Principal
	Reason    string
	At        time.Time
}

// SessionRetirementStatus is the latest retirement marker for a session.
type SessionRetirementStatus struct {
	FailureClass string
	Reason       string
	NextAction   string
	At           time.Time
}

// WithPersister sets an optional secondary persistence backend.
func (s *Session) WithPersister(p SessionPersister) {
	s.persister = p
}

// WithLogger sets a logger for persistence warnings.
func (s *Session) WithSessionLogger(fn func(msg string, args ...any)) {
	s.logger = fn
}

type sessionHeader struct {
	ID        string              `json:"id"`
	CreatedAt time.Time           `json:"created_at"`
	Version   int                 `json:"version"`
	Principal *identity.Principal `json:"principal,omitempty"`
}

// SessionFileInfo describes the file-backed metadata for a persisted session.
// It is intentionally derived from the canonical JSONL transcript plus
// filesystem state so callers can reconcile it with any secondary history store.
type SessionFileInfo struct {
	ID           string
	Path         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Principal    identity.Principal
	MessageCount int
}

// NewSession creates a new session with a random ID.
// The session is not persisted until Save or AppendMessage is called.
func NewSession(dataDir string, principals ...identity.Principal) (*Session, error) {
	id := uuid.New().String()
	path := sessionPath(dataDir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("session: create dir: %w", err)
	}
	principal := identity.LegacyPrincipal()
	if len(principals) > 0 && !principals[0].IsZero() {
		principal = principals[0]
	}
	s := &Session{ID: id, path: path, Principal: principal, appliedHashes: make(map[string]struct{})}
	if err := s.writeHeader(); err != nil {
		return nil, err
	}
	return s, nil
}

// LoadSession reads a session from disk by ID.
func LoadSession(dataDir, id string) (*Session, error) {
	path := sessionPath(dataDir, id)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open: %w", err)
	}
	defer f.Close()

	s := &Session{ID: id, path: path, appliedHashes: make(map[string]struct{})}
	scanner := bufio.NewScanner(f)

	// First line: header.
	if !scanner.Scan() {
		return nil, wrapSessionParse(fmt.Errorf("session: empty file"), "load empty file")
	}
	hdr, err := decodeSessionHeader(scanner.Bytes())
	if err != nil {
		return nil, wrapSessionParse(err, "load decode header")
	}
	s.ID = hdr.ID
	s.Principal = hdr.Principal

	// Remaining lines: messages.
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineType, err := sessionLineType(line)
		if err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: inspect line: %w", err), "load inspect line")
		}
		if lineType == "retire" {
			var event SessionRetirementEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return nil, wrapSessionParse(fmt.Errorf("session: parse retirement: %w", err), "load parse retirement")
			}
			s.Retirement = retirementStatusFromEvent(event)
			continue
		}
		if lineType != "" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: parse message: %w", err), "load parse message")
		}
		s.Messages = append(s.Messages, msg)
		if shouldDedupMessage(msg) {
			s.appliedHashes[messageHash(msg)] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, wrapSessionParse(fmt.Errorf("session: read: %w", err), "load scan")
	}

	return s, nil
}

// ReadSessionHeader reads only the JSONL header line for a session file.
func ReadSessionHeader(path string) (*SessionHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open header: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: read header: %w", err), "read header scan")
		}
		return nil, wrapSessionParse(fmt.Errorf("session: empty file"), "read header empty")
	}
	hdr, err := decodeSessionHeader(scanner.Bytes())
	if err != nil {
		return nil, wrapSessionParse(err, "read header decode")
	}
	return hdr, nil
}

// LoadSessionResumeEvents reads resume metadata lines from a persisted session.
func LoadSessionResumeEvents(dataDir, id string) ([]SessionResumeEvent, error) {
	return readSessionResumeEvents(sessionPath(dataDir, id))
}

// LoadSessionHandoffEvents reads handoff metadata lines from a persisted session.
func LoadSessionHandoffEvents(dataDir, id string) ([]SessionHandoffEvent, error) {
	return readSessionHandoffEvents(sessionPath(dataDir, id))
}

// LoadSessionHandoffStatus reads the latest handoff metadata line from a
// persisted session. A nil status means the session has no handoff marker.
func LoadSessionHandoffStatus(dataDir, id string) (*SessionHandoffStatus, error) {
	return readSessionHandoffStatus(sessionPath(dataDir, id))
}

// LoadSessionRetirementStatus reads the latest retirement metadata line from a
// persisted session. A nil status means the session has no retirement marker.
func LoadSessionRetirementStatus(dataDir, id string) (*SessionRetirementStatus, error) {
	return readSessionRetirementStatus(sessionPath(dataDir, id))
}

// RecordResume appends a metadata-only resume event line to the session JSONL.
func (s *Session) RecordResume(principal identity.Principal) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if principal.IsZero() {
		principal = s.Principal
	}
	event := SessionResumeEvent{
		Type:      "resume",
		Surface:   strings.TrimSpace(principal.Surface),
		Principal: principal,
		At:        time.Now().UTC(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("session: marshal resume: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("session: open for resume append: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write resume: %w", err)
	}
	return nil
}

// RecordHandoff appends a metadata-only handoff state event to the session JSONL.
func (s *Session) RecordHandoff(state, surface string, principal identity.Principal, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state = strings.TrimSpace(state)
	if !validSessionHandoffState(state) {
		return fmt.Errorf("session: invalid handoff state %q", state)
	}
	if principal.IsZero() {
		principal = s.Principal
	}
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = strings.TrimSpace(principal.Surface)
	}
	event := SessionHandoffEvent{
		Type:      "handoff",
		State:     state,
		Surface:   surface,
		Principal: principal,
		Reason:    strings.TrimSpace(reason),
		At:        time.Now().UTC(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("session: marshal handoff: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("session: open for handoff append: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write handoff: %w", err)
	}
	return nil
}

func validSessionHandoffState(state string) bool {
	switch strings.TrimSpace(state) {
	case "requested", "claimed", "running", "completed", "failed":
		return true
	default:
		return false
	}
}

// RecordRetirement appends a metadata-only retirement event line to the session
// JSONL and updates the in-memory retirement status.
func (s *Session) RecordRetirement(failureClass, reason, nextAction string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	event := SessionRetirementEvent{
		Type:         "retire",
		FailureClass: strings.TrimSpace(failureClass),
		Reason:       strings.TrimSpace(reason),
		NextAction:   strings.TrimSpace(nextAction),
		At:           time.Now().UTC(),
	}
	if event.Reason == "" {
		event.Reason = "runtime_retired_session"
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("session: marshal retirement: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("session: open for retirement append: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write retirement: %w", err)
	}
	s.Retirement = retirementStatusFromEvent(event)
	return nil
}

// Retired reports whether the session carries a runtime retirement marker.
func (s *Session) Retired() bool {
	return s != nil && s.Retirement != nil
}

// AppendMessage appends a single message to the session file (O_APPEND) and
// updates the in-memory slice. JSONL lines are small enough that O_APPEND is
// atomic on POSIX filesystems for reasonable message sizes.
//
// Phase L1.4 stamps the universal-message-schema task default: when a
// caller funnels a message through this boundary without setting
// Source, we record it as SourceTask. Chat callers (L1.2) already set
// SourceChat on every message they persist via
// conversation.Manager.AppendChatTurn, so the default only catches
// task-origin writers (agent.Loop iterations, orchestrators, the
// SendMessage user-turn path) that otherwise would have landed as
// Source="". The load-side sanitiser (L1.3) relies on "non-chat Source
// ⇒ strip tool blocks", so covering the write-side at the single
// persistence boundary is both sufficient and minimal — every
// task/team write goes through here, and we avoid sprinkling Source
// assignments across ~15 orchestrator call sites.
func (s *Session) AppendMessage(msg llm.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if msg.Source == "" {
		msg.Source = llm.SourceTask
	}

	hash := ""
	if shouldDedupMessage(msg) {
		hash = messageHash(msg)
		if s.appliedHashes == nil {
			s.appliedHashes = make(map[string]struct{})
		}
		if _, ok := s.appliedHashes[hash]; ok {
			if s.logger != nil {
				s.logger("session: skipped duplicate append", "session_id", s.ID, "hash", hash)
			}
			return nil
		}
	}

	data, err := msg.MarshalPersist()
	if err != nil {
		return fmt.Errorf("session: marshal message: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("session: open for append: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write message: %w", err)
	}

	if hash != "" {
		s.appliedHashes[hash] = struct{}{}
	}
	s.Messages = append(s.Messages, msg)
	return nil
}

func messageHash(msg llm.Message) string {
	data, _ := json.Marshal(msg)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// shouldDedupMessage limits AppendMessage's content-hash dedup to user-role
// messages. Spec SF2 FD4 Out clause: assistant and tool_result messages have
// naturally fresh hashes on every retry (streaming deltas, tool call IDs,
// provider timestamps), so hashing them would either (a) never fire or
// (b) collapse legitimate repeated assistant turns if a provider emits
// byte-identical payloads. The user-role narrowing keeps dedup focused on the
// same user prompt being resubmitted.
func shouldDedupMessage(msg llm.Message) bool {
	return msg.Role == llm.RoleUser
}

// AppendMessages appends multiple messages in a single write for efficiency.
// Also syncs to secondary persister (e.g., SQLite) if configured.
func (s *Session) AppendMessages(msgs []llm.Message) error {
	for _, m := range msgs {
		if err := s.AppendMessage(m); err != nil {
			return err
		}
	}
	// Secondary persistence: best-effort, never blocks primary JSONL.
	if s.persister != nil {
		snapshot := s.SnapshotMessages()
		if err := s.persister.PersistSession(s.ID, snapshot); err != nil {
			if s.logger != nil {
				s.logger("secondary persist failed", "session_id", s.ID, "error", err)
			}
		}
	}
	return nil
}

// SnapshotMessages returns a stable copy of the current in-memory transcript.
func (s *Session) SnapshotMessages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := make([]llm.Message, len(s.Messages))
	copy(snapshot, s.Messages)
	return snapshot
}

// Fork creates a new session that starts with a copy of the current messages.
// The forked session has its own ID and file; the original is unchanged.
func (s *Session) Fork(dataDir string) (*Session, error) {
	s.mu.Lock()
	snapshot := make([]llm.Message, len(s.Messages))
	copy(snapshot, s.Messages)
	principal := s.Principal
	s.mu.Unlock()

	child, err := NewSession(dataDir, principal)
	if err != nil {
		return nil, fmt.Errorf("session: fork: %w", err)
	}
	if err := child.AppendMessages(snapshot); err != nil {
		return nil, fmt.Errorf("session: fork copy: %w", err)
	}
	return child, nil
}

// writeHeader writes the JSONL header line to a new file.
func (s *Session) writeHeader() error {
	principal := s.Principal
	hdr := sessionHeader{
		ID:        s.ID,
		CreatedAt: time.Now().UTC(),
		Version:   1,
		Principal: &principal,
	}
	data, err := json.Marshal(hdr)
	if err != nil {
		return fmt.Errorf("session: marshal header: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("session: create file: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write header: %w", err)
	}
	return nil
}

func sessionPath(dataDir, id string) string {
	return filepath.Join(dataDir, "sessions", id+".jsonl")
}

// ResolveSessionID maps user-supplied input (full UUID, prefix, or status-line
// truncation like "cac7a3cc-c799...") to the canonical session ID on disk.
// Exact-file matches short-circuit before any directory scan; otherwise all
// sessions are listed and filtered by prefix. Ambiguous or missing matches
// return errors that name the offending candidates so callers can surface them
// directly to the user instead of the silent load_session_failed outcome that
// FU-DaemonStatusFullID was filed against.
func ResolveSessionID(dataDir, input string) (string, error) {
	normalized := normalizeSessionInput(input)
	if normalized == "" {
		return "", fmt.Errorf("session id is empty")
	}

	if _, err := os.Stat(sessionPath(dataDir, normalized)); err == nil {
		return normalized, nil
	}

	infos, err := ListSessionFiles(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve session %q: %w", normalized, err)
	}

	var matches []string
	for _, info := range infos {
		if strings.HasPrefix(info.ID, normalized) {
			matches = append(matches, info.ID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("session not found for %q", normalized)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous session prefix %q: %d matches (%s)", normalized, len(matches), strings.Join(matches, ", "))
	}
}

// normalizeSessionInput strips whitespace and the trailing ellipsis markers
// ("...", "…") that `elnath daemon status` appends when column-truncating UUIDs,
// so the output can be copy-pasted back into --session without manual edits.
func normalizeSessionInput(input string) string {
	trimmed := strings.TrimSpace(input)
	for {
		switch {
		case strings.HasSuffix(trimmed, "..."):
			trimmed = strings.TrimSuffix(trimmed, "...")
		case strings.HasSuffix(trimmed, "…"):
			trimmed = strings.TrimSuffix(trimmed, "…")
		default:
			return strings.TrimSpace(trimmed)
		}
	}
}

// ListSessionFiles returns metadata for all JSONL-backed sessions known on disk.
// UpdatedAt is derived from file modification time; CreatedAt is read from the
// JSONL header when available. Files that cannot be parsed still participate via
// their filename and modtime so callers can skip them explicitly later.
func ListSessionFiles(dataDir string) ([]SessionFileInfo, error) {
	sessDir := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: list files: %w", err)
	}

	infos := make([]SessionFileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(sessDir, entry.Name())
		fileInfo, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("session: stat %s: %w", entry.Name(), err)
		}

		meta, err := readSessionFileInfo(path)
		if err != nil {
			meta = SessionFileInfo{
				ID:   strings.TrimSuffix(entry.Name(), ".jsonl"),
				Path: path,
			}
		}
		meta.Path = path
		meta.UpdatedAt = fileInfo.ModTime().UTC()
		if meta.ID == "" {
			meta.ID = strings.TrimSuffix(entry.Name(), ".jsonl")
		}
		infos = append(infos, meta)
	}

	sort.SliceStable(infos, func(i, j int) bool {
		if !infos[i].UpdatedAt.Equal(infos[j].UpdatedAt) {
			return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
		}
		if !infos[i].CreatedAt.Equal(infos[j].CreatedAt) {
			return infos[i].CreatedAt.After(infos[j].CreatedAt)
		}
		return infos[i].ID > infos[j].ID
	})

	return infos, nil
}

func readSessionFileInfo(path string) (SessionFileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionFileInfo{}, fmt.Errorf("session: open metadata: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return SessionFileInfo{}, fmt.Errorf("session: read metadata header: %w", err)
		}
		return SessionFileInfo{}, fmt.Errorf("session: empty file")
	}

	hdr, err := decodeSessionHeader(scanner.Bytes())
	if err != nil {
		return SessionFileInfo{}, err
	}

	info := SessionFileInfo{
		ID:        hdr.ID,
		CreatedAt: hdr.CreatedAt.UTC(),
		Principal: hdr.Principal,
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineType, err := sessionLineType(line)
		if err != nil {
			return SessionFileInfo{}, fmt.Errorf("session: inspect metadata line: %w", err)
		}
		if lineType != "" {
			continue
		}
		info.MessageCount++
	}
	if err := scanner.Err(); err != nil {
		return SessionFileInfo{}, fmt.Errorf("session: scan metadata: %w", err)
	}

	return info, nil
}

func decodeSessionHeader(data []byte) (*SessionHeader, error) {
	var hdr sessionHeader
	if err := json.Unmarshal(data, &hdr); err != nil {
		return nil, fmt.Errorf("session: parse header: %w", err)
	}
	principal := identity.LegacyPrincipal()
	if hdr.Principal != nil && !hdr.Principal.IsZero() {
		principal = *hdr.Principal
	}
	return &SessionHeader{
		ID:        hdr.ID,
		CreatedAt: hdr.CreatedAt.UTC(),
		Version:   hdr.Version,
		Principal: principal,
	}, nil
}

func readSessionResumeEvents(path string) ([]SessionResumeEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open resumes: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: read resumes header: %w", err), "resumes header scan")
		}
		return nil, wrapSessionParse(fmt.Errorf("session: empty file"), "resumes empty")
	}

	var resumes []SessionResumeEvent
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineType, err := sessionLineType(line)
		if err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: inspect resume line: %w", err), "resumes inspect line")
		}
		if lineType != "resume" {
			continue
		}
		var resume SessionResumeEvent
		if err := json.Unmarshal(line, &resume); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: parse resume: %w", err), "resumes parse")
		}
		resumes = append(resumes, resume)
	}
	if err := scanner.Err(); err != nil {
		return nil, wrapSessionParse(fmt.Errorf("session: scan resumes: %w", err), "resumes scan")
	}
	return resumes, nil
}

func readSessionHandoffEvents(path string) ([]SessionHandoffEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open handoffs: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: read handoffs header: %w", err), "handoffs header scan")
		}
		return nil, wrapSessionParse(fmt.Errorf("session: empty file"), "handoffs empty")
	}

	var handoffs []SessionHandoffEvent
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineType, err := sessionLineType(line)
		if err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: inspect handoff line: %w", err), "handoffs inspect line")
		}
		if lineType != "handoff" {
			continue
		}
		var handoff SessionHandoffEvent
		if err := json.Unmarshal(line, &handoff); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: parse handoff: %w", err), "handoffs parse")
		}
		handoffs = append(handoffs, handoff)
	}
	if err := scanner.Err(); err != nil {
		return nil, wrapSessionParse(fmt.Errorf("session: scan handoffs: %w", err), "handoffs scan")
	}
	return handoffs, nil
}

func readSessionHandoffStatus(path string) (*SessionHandoffStatus, error) {
	events, err := readSessionHandoffEvents(path)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	latest := events[len(events)-1]
	return &SessionHandoffStatus{
		State:     strings.TrimSpace(latest.State),
		Surface:   strings.TrimSpace(latest.Surface),
		Principal: latest.Principal,
		Reason:    strings.TrimSpace(latest.Reason),
		At:        latest.At.UTC(),
	}, nil
}

func readSessionRetirementStatus(path string) (*SessionRetirementStatus, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open retirement status: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: read retirement header: %w", err), "retirement header scan")
		}
		return nil, wrapSessionParse(fmt.Errorf("session: empty file"), "retirement empty")
	}

	var latest *SessionRetirementStatus
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineType, err := sessionLineType(line)
		if err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: inspect retirement line: %w", err), "retirement inspect line")
		}
		if lineType != "retire" {
			continue
		}
		var event SessionRetirementEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, wrapSessionParse(fmt.Errorf("session: parse retirement: %w", err), "retirement parse")
		}
		latest = retirementStatusFromEvent(event)
	}
	if err := scanner.Err(); err != nil {
		return nil, wrapSessionParse(fmt.Errorf("session: scan retirement: %w", err), "retirement scan")
	}
	return latest, nil
}

func retirementStatusFromEvent(event SessionRetirementEvent) *SessionRetirementStatus {
	return &SessionRetirementStatus{
		FailureClass: strings.TrimSpace(event.FailureClass),
		Reason:       strings.TrimSpace(event.Reason),
		NextAction:   strings.TrimSpace(event.NextAction),
		At:           event.At.UTC(),
	}
}

func sessionLineType(line []byte) (string, error) {
	var meta struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &meta); err != nil {
		return "", err
	}
	return strings.TrimSpace(meta.Type), nil
}
