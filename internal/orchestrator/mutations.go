package orchestrator

import (
	"github.com/stello/elnath/internal/research"
	"github.com/stello/elnath/internal/tools"
)

func appendMutationReceipts(dst []*tools.FileMutation, src []*tools.FileMutation) []*tools.FileMutation {
	if len(src) == 0 {
		return dst
	}
	for _, mutation := range src {
		if mutation != nil {
			dst = append(dst, mutation)
		}
	}
	return dst
}

func researchMutationReceipts(result *research.ResearchResult) []*tools.FileMutation {
	if result == nil || len(result.Rounds) == 0 {
		return nil
	}
	var mutations []*tools.FileMutation
	for _, round := range result.Rounds {
		mutations = appendMutationReceipts(mutations, round.Result.Mutations)
	}
	return mutations
}
