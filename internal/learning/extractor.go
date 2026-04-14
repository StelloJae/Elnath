package learning

import (
	"fmt"
	"time"

	"github.com/stello/elnath/internal/self"
)

type ResultInfo struct {
	Topic     string
	Summary   string
	TotalCost float64
	Rounds    []RoundInfo
}

type RoundInfo struct {
	HypothesisID string
	Statement    string
	Findings     string
	Confidence   string
	Supported    bool
}

const maxLessonTextLen = 200
const costThresholdUSD = 2.0

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}

func Extract(result ResultInfo) []Lesson {
	var lessons []Lesson
	now := time.Now().UTC()

	for _, round := range result.Rounds {
		if round.Supported && round.Confidence == "high" {
			lessons = append(lessons, Lesson{
				Text:       truncate(fmt.Sprintf("On %s: %s", result.Topic, round.Findings), maxLessonTextLen),
				Topic:      result.Topic,
				Source:     result.Topic,
				Confidence: "high",
				PersonaDelta: []self.Lesson{{
					Param: "persistence",
					Delta: 0.02,
				}},
				Created: now,
			})
		}
	}

	lowCount := 0
	for _, round := range result.Rounds {
		if round.Confidence == "low" {
			lowCount++
		}
	}
	if len(result.Rounds) > 0 && lowCount*2 >= len(result.Rounds) {
		lessons = append(lessons, Lesson{
			Text:       truncate(fmt.Sprintf("Topic %s requires more evidence before conclusions.", result.Topic), maxLessonTextLen),
			Topic:      result.Topic,
			Source:     result.Topic,
			Confidence: "medium",
			PersonaDelta: []self.Lesson{
				{Param: "caution", Delta: 0.03},
				{Param: "curiosity", Delta: -0.01},
			},
			Created: now,
		})
	}

	if result.TotalCost > costThresholdUSD {
		lessons = append(lessons, Lesson{
			Text:       truncate(fmt.Sprintf("Research on %s exceeded budget ($%.2f); prefer focused experiments.", result.Topic, result.TotalCost), maxLessonTextLen),
			Topic:      result.Topic,
			Source:     result.Topic,
			Confidence: "high",
			PersonaDelta: []self.Lesson{{
				Param: "verbosity",
				Delta: -0.02,
			}},
			Created: now,
		})
	}

	return lessons
}
