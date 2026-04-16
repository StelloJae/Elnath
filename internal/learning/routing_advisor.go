package learning

import (
	"sort"

	routing "github.com/stello/elnath/internal/routing"
)

type RoutingAdvisor struct {
	store      *OutcomeStore
	windowSize int
	minSamples int
}

func NewRoutingAdvisor(store *OutcomeStore) *RoutingAdvisor {
	return &RoutingAdvisor{
		store:      store,
		windowSize: 30,
		minSamples: 5,
	}
}

// WorkflowStat holds success/total counts for one workflow.
type WorkflowStat struct {
	Total   int
	Success int
}

// ProjectStats returns per-intent, per-workflow counts for the last windowSize
// outcomes of the given project. The outer map key is intent, inner is workflow.
func (a *RoutingAdvisor) ProjectStats(projectID string, window int) (map[string]map[string]WorkflowStat, error) {
	records, err := a.store.ForProject(projectID, window)
	if err != nil {
		return nil, err
	}
	result := make(map[string]map[string]WorkflowStat)
	for _, r := range records {
		if result[r.Intent] == nil {
			result[r.Intent] = make(map[string]WorkflowStat)
		}
		s := result[r.Intent][r.Workflow]
		s.Total++
		if r.Success {
			s.Success++
		}
		result[r.Intent][r.Workflow] = s
	}
	return result, nil
}

func (a *RoutingAdvisor) Advise(projectID string) (*routing.WorkflowPreference, error) {
	records, err := a.store.ForProject(projectID, a.windowSize)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}

	// Group records by intent, then by workflow.
	type stats struct {
		total   int
		success int
	}
	// intentWorkflow[intent][workflow] = stats
	intentWorkflow := make(map[string]map[string]*stats)
	for _, r := range records {
		if intentWorkflow[r.Intent] == nil {
			intentWorkflow[r.Intent] = make(map[string]*stats)
		}
		wm := intentWorkflow[r.Intent]
		if wm[r.Workflow] == nil {
			wm[r.Workflow] = &stats{}
		}
		wm[r.Workflow].total++
		if r.Success {
			wm[r.Workflow].success++
		}
	}

	preferred := make(map[string]string)
	var avoided []string
	avoidSet := make(map[string]bool)

	for intent, workflows := range intentWorkflow {
		// Collect workflows that meet the minimum sample threshold.
		type wfRate struct {
			workflow string
			rate     float64
		}
		var eligible []wfRate
		for wf, s := range workflows {
			if s.total < a.minSamples {
				continue
			}
			rate := float64(s.success) / float64(s.total)
			eligible = append(eligible, wfRate{wf, rate})

			if rate < 0.30 && !avoidSet[wf] {
				avoidSet[wf] = true
				avoided = append(avoided, wf)
			}
		}

		if len(eligible) == 0 {
			continue
		}

		// Find best and second-best rates.
		best := eligible[0]
		for _, e := range eligible[1:] {
			if e.rate > best.rate {
				best = e
			}
		}

		if best.rate == 1.0 {
			preferred[intent] = best.workflow
			continue
		}

		var secondBest *wfRate
		for i := range eligible {
			if eligible[i].workflow == best.workflow {
				continue
			}
			if secondBest == nil || eligible[i].rate > secondBest.rate {
				e := eligible[i]
				secondBest = &e
			}
		}

		if secondBest == nil {
			// Only one eligible workflow — prefer it if gap is significant vs 0%.
			if best.rate-0.0 >= 0.20 {
				preferred[intent] = best.workflow
			}
			continue
		}

		if best.rate-secondBest.rate >= 0.20 {
			preferred[intent] = best.workflow
		}
	}

	if len(preferred) == 0 && len(avoided) == 0 {
		return nil, nil
	}

	pref := &routing.WorkflowPreference{}
	if len(preferred) > 0 {
		pref.PreferredWorkflows = preferred
	}
	if len(avoided) > 0 {
		sort.Strings(avoided)
		pref.AvoidWorkflows = avoided
	}
	return pref, nil
}
