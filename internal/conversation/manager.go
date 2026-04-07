package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

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
	db        *sql.DB
	dataDir   string
	logger    *slog.Logger
	provider  llm.Provider
	classifier IntentClassifier
	context    ContextWindowManager
	history    HistoryStore
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
	m.logger.Info("created session", "session_id", s.ID)
	return s, nil
}

// LoadSession loads an existing session by ID.
func (m *Manager) LoadSession(sessionID string) (*agent.Session, error) {
	s, err := agent.LoadSession(m.dataDir, sessionID)
	if err != nil {
		return nil, fmt.Errorf("conversation: load session %s: %w", sessionID, err)
	}
	return s, nil
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

	// Fit messages to context window if available.
	if m.context != nil {
		const defaultMaxTokens = 100_000
		messages, err = m.context.Fit(ctx, messages, defaultMaxTokens)
		if err != nil {
			m.logger.Warn("context window fit failed, using original messages",
				"session_id", sessionID,
				"error", err,
			)
			// Revert to original messages + user message on error.
			messages = append(s.Messages, userMessage)
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

// GetHistory returns the conversation history for a session.
// It prefers the HistoryStore if available, falling back to the JSONL session file.
func (m *Manager) GetHistory(ctx context.Context, sessionID string) ([]llm.Message, error) {
	if m.history != nil {
		msgs, err := m.history.Load(ctx, sessionID)
		if err == nil {
			return msgs, nil
		}
		m.logger.Warn("history load failed, falling back to session file",
			"session_id", sessionID,
			"error", err,
		)
	}

	s, err := m.LoadSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("conversation: get history %s: %w", sessionID, err)
	}
	return s.Messages, nil
}

// ListSessions returns metadata for all known sessions.
// It prefers the HistoryStore if available, returning an empty list otherwise.
func (m *Manager) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	if m.history != nil {
		return m.history.ListSessions(ctx)
	}
	return nil, nil
}
