package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
)

// IntentClassifier classifies the user's intent from a message.
type IntentClassifier interface {
	Classify(ctx context.Context, provider llm.Provider, message string, history []llm.Message) (Intent, error)
}

// ContextWindowManager manages token budget and message compression.
type ContextWindowManager interface {
	Fit(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error)
	// CompressMessages applies the full 3-stage pipeline with LLM-based summarization.
	CompressMessages(ctx context.Context, provider llm.Provider, messages []llm.Message, maxTokens int) ([]llm.Message, error)
}

// HistoryStore persists and retrieves conversation history.
type HistoryStore interface {
	Save(ctx context.Context, sessionID string, messages []llm.Message) error
	Load(ctx context.Context, sessionID string) ([]llm.Message, error)
	ListSessions(ctx context.Context) ([]SessionInfo, error)
}

// Manager wraps agent.Session and provides higher-level conversation management.
// It coordinates intent classification, context window management, and history persistence.
type Manager struct {
	db               *sql.DB
	dataDir          string
	logger           *slog.Logger
	provider         llm.Provider
	classifier       IntentClassifier
	context          ContextWindowManager
	history          HistoryStore
	maxContextTokens int
}

// NewManager creates a Manager with the given database and data directory.
// Dependencies (classifier, context window, history store) are optional and
// can be set via the With* methods after construction.
func NewManager(db *sql.DB, dataDir string) *Manager {
	return &Manager{
		db:      db,
		dataDir: dataDir,
		logger:  slog.Default(),
	}
}

// WithProvider sets the LLM provider used for intent classification.
func (m *Manager) WithProvider(p llm.Provider) *Manager {
	m.provider = p
	return m
}

// WithClassifier sets the intent classifier.
func (m *Manager) WithClassifier(c IntentClassifier) *Manager {
	m.classifier = c
	return m
}

// WithContextWindow sets the context window manager.
func (m *Manager) WithContextWindow(cw ContextWindowManager) *Manager {
	m.context = cw
	return m
}

// WithHistoryStore sets the history store.
func (m *Manager) WithHistoryStore(hs HistoryStore) *Manager {
	m.history = hs
	return m
}

// WithMaxContextTokens sets the maximum token budget for the context window.
// If not set, defaults to 100,000.
func (m *Manager) WithMaxContextTokens(n int) *Manager {
	m.maxContextTokens = n
	return m
}

// WithLogger sets a custom structured logger.
func (m *Manager) WithLogger(l *slog.Logger) *Manager {
	m.logger = l
	return m
}

// NewSession creates a new conversation session persisted as a JSONL file.
func (m *Manager) NewSession() (*agent.Session, error) {
	s, err := agent.NewSession(m.dataDir)
	if err != nil {
		return nil, fmt.Errorf("conversation: new session: %w", err)
	}
	m.prepareSession(s)
	m.logger.Info("created session", "session_id", s.ID)
	return s, nil
}

// LoadSession loads an existing session by ID.
func (m *Manager) LoadSession(sessionID string) (*agent.Session, error) {
	s, err := agent.LoadSession(m.dataDir, sessionID)
	if err != nil {
		return nil, fmt.Errorf("conversation: load session %s: %w", sessionID, err)
	}
	m.prepareSession(s)
	return s, nil
}

// LoadLatestSession finds and loads the most recent resumable transcript.
// JSONL files are the canonical source of truth for resume because they are the
// append-only transcript the runtime replays; the secondary history store is
// only used for indexing/listing and must not change which session
// `--continue` resolves to.
func (m *Manager) LoadLatestSession() (*agent.Session, error) {
	sessions, err := agent.ListSessionFiles(m.dataDir)
	if err != nil {
		return nil, fmt.Errorf("conversation: list sessions: %w", err)
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("conversation: no sessions found")
	}

	var lastErr error
	for _, info := range sessions {
		s, err := m.LoadSession(info.ID)
		if err == nil {
			m.logger.Info("resuming latest session", "session_id", info.ID)
			return s, nil
		}
		lastErr = err
		if m.logger != nil {
			m.logger.Warn("failed to load candidate latest session; trying next candidate",
				"session_id", info.ID,
				"error", err,
			)
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("conversation: load latest session: %w", lastErr)
	}
	return nil, fmt.Errorf("conversation: no loadable sessions found")
}

// SendMessage processes a user message for the given session.
// It loads the session, classifies intent, fits messages to context window,
// appends the user message, and persists the updated history.
// The agent loop itself is handled by callers (CLI, daemon); this method
// prepares the message array ready for agent.Run().
func (m *Manager) SendMessage(ctx context.Context, sessionID, userMsg string) ([]llm.Message, Intent, error) {
	s, err := m.LoadSession(sessionID)
	if err != nil {
		return nil, IntentUnclear, fmt.Errorf("conversation: send message: %w", err)
	}

	messages := s.Messages

	// Classify intent if classifier is available.
	intent := IntentUnclear
	if m.classifier != nil && m.provider != nil {
		intent, err = m.classifier.Classify(ctx, m.provider, userMsg, messages)
		if err != nil {
			m.logger.Warn("intent classification failed, using 'unclear'",
				"session_id", sessionID,
				"error", err,
			)
			intent = IntentUnclear
		}
	}

	// Append the new user message.
	userMessage := llm.NewUserMessage(userMsg)
	messages = append(messages, userMessage)
	if err := s.AppendMessage(userMessage); err != nil {
		return nil, intent, fmt.Errorf("conversation: persist user message: %w", err)
	}

	// Compress messages to fit context window if available.
	if m.context != nil {
		maxTokens := m.maxContextTokens
		if maxTokens == 0 {
			maxTokens = 100_000
		}
		if m.provider != nil {
			messages, err = m.context.CompressMessages(ctx, m.provider, messages, maxTokens)
		} else {
			messages, err = m.context.Fit(ctx, messages, maxTokens)
		}
		if err != nil {
			m.logger.Warn("context compression failed, using original messages",
				"session_id", sessionID,
				"error", err,
			)
			messages = append([]llm.Message(nil), s.Messages...)
		}
	}

	// Persist to history store if available.
	if m.history != nil {
		if err := m.history.Save(ctx, sessionID, messages); err != nil {
			m.logger.Warn("history save failed",
				"session_id", sessionID,
				"error", err,
			)
		}
	}

	m.logger.Debug("prepared messages for agent",
		"session_id", sessionID,
		"intent", intent,
		"message_count", len(messages),
	)

	return messages, intent, nil
}

func (m *Manager) prepareSession(s *agent.Session) {
	if s == nil {
		return
	}
	if m.history != nil {
		s.WithPersister(sessionPersisterAdapter{history: m.history})
	}
	if m.logger != nil {
		s.WithSessionLogger(func(msg string, args ...any) {
			m.logger.Warn(msg, args...)
		})
	}
}

type sessionPersisterAdapter struct {
	history HistoryStore
}

func (a sessionPersisterAdapter) PersistSession(sessionID string, messages []llm.Message) error {
	if a.history == nil {
		return nil
	}
	return a.history.Save(context.Background(), sessionID, messages)
}

// GetHistory returns the conversation history for a session.
// It prefers the JSONL transcript because resume semantics are defined by the
// append-only session file. The HistoryStore is a fallback index for cases
// where the transcript is temporarily unavailable.
func (m *Manager) GetHistory(ctx context.Context, sessionID string) ([]llm.Message, error) {
	s, err := m.LoadSession(sessionID)
	if err == nil {
		return s.Messages, nil
	}
	fileErr := err

	if m.history != nil {
		if sessionKnownToHistory(ctx, m.history, sessionID) {
			msgs, err := m.history.Load(ctx, sessionID)
			if err == nil {
				m.logger.Warn("session transcript unavailable, falling back to history store",
					"session_id", sessionID,
					"error", fileErr,
				)
				return msgs, nil
			}
			m.logger.Warn("history fallback failed after transcript load failure",
				"session_id", sessionID,
				"transcript_error", fileErr,
				"history_error", err,
			)
		} else {
			m.logger.Warn("session transcript unavailable and history store has no matching session",
				"session_id", sessionID,
				"error", fileErr,
			)
		}
	}

	return nil, fmt.Errorf("conversation: get history %s: %w", sessionID, fileErr)
}

// ListSessions returns metadata for all known sessions.
// JSONL metadata remains authoritative for resumable sessions; the HistoryStore
// only supplements sessions that are not presently backed by a transcript file.
func (m *Manager) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	fileInfos, err := agent.ListSessionFiles(m.dataDir)
	if err != nil {
		return nil, fmt.Errorf("conversation: list session files: %w", err)
	}

	merged := make(map[string]SessionInfo, len(fileInfos))
	for _, info := range fileInfos {
		merged[info.ID] = SessionInfo{
			ID:           info.ID,
			CreatedAt:    info.CreatedAt,
			UpdatedAt:    info.UpdatedAt,
			MessageCount: info.MessageCount,
		}
	}

	if m.history != nil {
		storeInfos, err := m.history.ListSessions(ctx)
		if err != nil {
			return nil, err
		}
		for _, info := range storeInfos {
			existing, ok := merged[info.ID]
			if !ok {
				merged[info.ID] = info
				continue
			}
			if existing.CreatedAt.IsZero() && !info.CreatedAt.IsZero() {
				existing.CreatedAt = info.CreatedAt
			}
			if existing.UpdatedAt.IsZero() && !info.UpdatedAt.IsZero() {
				existing.UpdatedAt = info.UpdatedAt
			}
			if existing.MessageCount == 0 && info.MessageCount > 0 {
				existing.MessageCount = info.MessageCount
			}
			merged[info.ID] = existing
		}
	}

	if len(merged) == 0 {
		return nil, nil
	}

	sessions := make([]SessionInfo, 0, len(merged))
	for _, info := range merged {
		sessions = append(sessions, info)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		if !sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
		}
		if sessions[i].MessageCount != sessions[j].MessageCount {
			return sessions[i].MessageCount > sessions[j].MessageCount
		}
		return sessions[i].ID > sessions[j].ID
	})

	return sessions, nil
}

func sessionKnownToHistory(ctx context.Context, store HistoryStore, sessionID string) bool {
	infos, err := store.ListSessions(ctx)
	if err != nil {
		return false
	}
	for _, info := range infos {
		if info.ID == sessionID {
			return true
		}
	}
	return false
}
