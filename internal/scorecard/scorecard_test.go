package scorecard

import "testing"

func TestScoreStringValues(t *testing.T) {
	tests := []struct {
		score Score
		want  string
	}{
		{ScoreOK, "OK"},
		{ScoreNascent, "NASCENT"},
		{ScoreDegraded, "DEGRADED"},
		{ScoreUnknown, "UNKNOWN"},
	}
	for _, tc := range tests {
		if got := string(tc.score); got != tc.want {
			t.Errorf("Score %v: got %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestAggregateOverall(t *testing.T) {
	mk := func(s Score) AxisReport { return AxisReport{Score: s} }
	tests := []struct {
		name string
		axes AxesReport
		want Score
	}{
		{"all OK", AxesReport{mk(ScoreOK), mk(ScoreOK), mk(ScoreOK), mk(ScoreOK)}, ScoreOK},
		{"any DEGRADED wins", AxesReport{mk(ScoreOK), mk(ScoreDegraded), mk(ScoreOK), mk(ScoreOK)}, ScoreDegraded},
		{"DEGRADED beats UNKNOWN", AxesReport{mk(ScoreDegraded), mk(ScoreUnknown), mk(ScoreOK), mk(ScoreOK)}, ScoreDegraded},
		{"any UNKNOWN else", AxesReport{mk(ScoreOK), mk(ScoreUnknown), mk(ScoreOK), mk(ScoreOK)}, ScoreUnknown},
		{"mixed OK/NASCENT", AxesReport{mk(ScoreOK), mk(ScoreNascent), mk(ScoreOK), mk(ScoreNascent)}, ScoreNascent},
		{"all NASCENT", AxesReport{mk(ScoreNascent), mk(ScoreNascent), mk(ScoreNascent), mk(ScoreNascent)}, ScoreNascent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateOverall(tc.axes)
			if got != tc.want {
				t.Errorf("aggregateOverall: got %v, want %v", got, tc.want)
			}
		})
	}
}
