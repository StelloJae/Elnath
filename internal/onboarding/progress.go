package onboarding

import (
	"fmt"
	"strings"
)

// stepInfo maps each Step to its display position (1-based) and total count.
// Quick and Full paths have different step counts.
func stepProgress(step Step, quick bool) (current, total int) {
	if quick {
		switch step {
		case StepWelcome:
			return 1, 5
		case StepLanguage:
			return 2, 5
		case StepAPIKey:
			return 3, 5
		case StepSummary:
			return 4, 5
		case StepSmokeTest:
			return 5, 5
		default:
			return 0, 0
		}
	}

	switch step {
	case StepWelcome:
		return 1, 8
	case StepLanguage:
		return 2, 8
	case StepAPIKey:
		return 3, 8
	case StepPermission:
		return 4, 8
	case StepMCP:
		return 5, 8
	case StepDirectory:
		return 6, 8
	case StepSummary:
		return 7, 8
	case StepSmokeTest:
		return 8, 8
	default:
		return 0, 0
	}
}

// RenderProgress renders a progress indicator bar for the current step.
func RenderProgress(locale Locale, step Step, quick bool) string {
	current, total := stepProgress(step, quick)
	if current == 0 || total == 0 {
		return ""
	}

	var b strings.Builder

	// Render filled and empty segments
	for i := 1; i <= total; i++ {
		if i <= current {
			b.WriteString(progressFilledStyle.Render("━"))
		} else {
			b.WriteString(progressEmptyStyle.Render("━"))
		}
	}

	b.WriteString("  ")
	b.WriteString(subtitleStyle.Render(fmt.Sprintf(T(locale, "progress.step"), current, total)))

	return b.String()
}
