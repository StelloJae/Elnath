package magicdocs

import (
	"context"
	"log/slog"
	"sync"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

const extractChSize = 16

type Config struct {
	Enabled   bool
	Store     *wiki.Store
	Provider  llm.Provider
	Model     string
	Logger    *slog.Logger
	SessionID string
}

type MagicDocs struct {
	observer  *AccumulatorObserver
	extractor *Extractor
	extractCh chan ExtractionRequest
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	enabled   bool
	logger    *slog.Logger
}

func New(cfg Config) *MagicDocs {
	if !cfg.Enabled {
		return &MagicDocs{enabled: false}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	ch := make(chan ExtractionRequest, extractChSize)
	writer := NewWikiWriter(cfg.Store, logger)

	return &MagicDocs{
		observer:  NewAccumulatorObserver(ch, cfg.SessionID, logger),
		extractor: NewExtractor(cfg.Provider, cfg.Model, writer, logger),
		extractCh: ch,
		enabled:   true,
		logger:    logger,
	}
}

func (m *MagicDocs) Observer() event.Observer {
	if !m.enabled {
		return event.ObserverFunc(func(event.Event) {})
	}
	return m.observer
}

func (m *MagicDocs) Start(ctx context.Context) {
	if !m.enabled {
		return
	}
	extractCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.extractor.Run(extractCtx, m.extractCh)
	}()
	m.logger.Info("magic-docs started", "session_id", m.observer.sessionID)
}

func (m *MagicDocs) Close(ctx context.Context) error {
	if !m.enabled {
		return nil
	}
	close(m.extractCh)

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("magic-docs stopped gracefully")
		return nil
	case <-ctx.Done():
		if m.cancel != nil {
			m.cancel()
		}
		return ctx.Err()
	}
}
