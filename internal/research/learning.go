package research

import (
	"context"
	"log/slog"

	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/self"
)

type LearningAppender func(context.Context, learning.Lesson) (bool, error)

func ApplyLearning(result *ResearchResult, store *learning.Store, selfState *self.SelfState, logger *slog.Logger) {
	if store == nil || result == nil {
		return
	}
	ApplyLearningWithAppender(context.Background(), result, func(_ context.Context, lesson learning.Lesson) (bool, error) {
		if err := store.Append(lesson); err != nil {
			return false, err
		}
		return true, nil
	}, selfState, logger)
}

func ApplyLearningWithAppender(ctx context.Context, result *ResearchResult, appendLesson LearningAppender, selfState *self.SelfState, logger *slog.Logger) {
	if appendLesson == nil || result == nil {
		return
	}
	lessons := learning.Extract(toResultInfo(result))
	if len(lessons) == 0 {
		return
	}

	if logger == nil {
		logger = slog.Default()
	}

	personaChanged := false
	for _, lesson := range lessons {
		added, err := appendLesson(ctx, lesson)
		if err != nil {
			logger.Warn("learning: append failed", "error", err)
			continue
		}
		if added && len(lesson.PersonaDelta) > 0 && selfState != nil {
			selfState.ApplyLessons(lesson.PersonaDelta)
			personaChanged = true
		}
	}

	if personaChanged {
		if err := selfState.Save(); err != nil {
			logger.Warn("learning: selfState save failed", "error", err)
		}
	}
}

func toResultInfo(result *ResearchResult) learning.ResultInfo {
	if result == nil {
		return learning.ResultInfo{}
	}

	rounds := make([]learning.RoundInfo, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		rounds = append(rounds, learning.RoundInfo{
			HypothesisID: round.Hypothesis.ID,
			Statement:    round.Hypothesis.Statement,
			Findings:     round.Result.Findings,
			Confidence:   round.Result.Confidence,
			Supported:    round.Result.Supported,
		})
	}

	return learning.ResultInfo{
		Topic:     result.Topic,
		Summary:   result.Summary,
		TotalCost: result.TotalCost,
		Rounds:    rounds,
	}
}
