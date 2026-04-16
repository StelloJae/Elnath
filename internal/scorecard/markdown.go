package scorecard

import (
	"fmt"
	"strings"
)

// RenderMarkdown produces a human-readable report from a Report. All data is
// derived from the Report; no independent computation is performed.
func RenderMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Maturity Scorecard — %s\n\n", r.Timestamp.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "  Overall:                %s\n\n", r.Overall)
	axes := []struct {
		name   string
		report AxisReport
	}{
		{"routing_adaptation", r.Axes.RoutingAdaptation},
		{"outcome_recording", r.Axes.OutcomeRecording},
		{"lesson_extraction", r.Axes.LessonExtraction},
		{"synthesis_compounding", r.Axes.SynthesisCompounding},
	}
	for _, a := range axes {
		fmt.Fprintf(&b, "  %-24s%-10s%s\n", a.name, a.report.Score, a.report.Reason)
	}
	fmt.Fprintf(&b, "\n  Sources:\n")
	fmt.Fprintf(&b, "    outcomes:    %s\n", r.Sources.OutcomesPath)
	fmt.Fprintf(&b, "    lessons:     %s\n", r.Sources.LessonsPath)
	fmt.Fprintf(&b, "    synthesis:   %s\n", r.Sources.SynthesisDir)
	fmt.Fprintf(&b, "    state:       %s\n", r.Sources.StatePath)
	return b.String()
}
