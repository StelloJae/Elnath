package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

type approvedToolCall struct {
	call  llm.ToolUseBlock
	index int
}

type scheduledToolCall struct {
	approvedToolCall
	safe        bool
	scope       tools.ToolScope
	cancelOnErr bool
}

func (a *Agent) executeApprovedToolCalls(ctx context.Context, approved []approvedToolCall, results []toolExecResult) error {
	for _, batch := range partitionToolCalls(a.tools, approved) {
		if err := a.executeToolBatch(ctx, batch, results); err != nil {
			return err
		}
	}
	return nil
}

func partitionToolCalls(reg *tools.Registry, approved []approvedToolCall) [][]scheduledToolCall {
	if len(approved) == 0 {
		return nil
	}

	batches := make([][]scheduledToolCall, 0, len(approved))
	current := make([]scheduledToolCall, 0, len(approved))
	for _, ap := range approved {
		call := scheduledToolCall{approvedToolCall: ap, scope: tools.ConservativeScope()}
		if tool, ok := reg.Get(ap.call.Name); ok {
			call.safe = tool.IsConcurrencySafe(ap.call.Input)
			call.scope = tool.Scope(ap.call.Input)
			call.cancelOnErr = tool.ShouldCancelSiblingsOnError()
		}
		if len(current) == 0 || canJoinBatch(call, current) {
			current = append(current, call)
			continue
		}
		batches = append(batches, current)
		current = []scheduledToolCall{call}
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

func canJoinBatch(candidate scheduledToolCall, current []scheduledToolCall) bool {
	for _, member := range current {
		if !canRunTogether(candidate, member) {
			return false
		}
	}
	return true
}

func canRunTogether(a, b scheduledToolCall) bool {
	if a.safe && b.safe {
		return true
	}
	return scopeDisjoint(a.scope, b.scope)
}

func scopeDisjoint(a, b tools.ToolScope) bool {
	if isConservativeScope(a) || isConservativeScope(b) {
		return false
	}
	return !anyPairIntersects(a.WritePaths, b.WritePaths) &&
		!anyPairIntersects(a.WritePaths, b.ReadPaths) &&
		!anyPairIntersects(a.ReadPaths, b.WritePaths)
}

func isConservativeScope(scope tools.ToolScope) bool {
	return len(scope.ReadPaths) == 0 && len(scope.WritePaths) == 0 && scope.Network && scope.Persistent
}

func anyPairIntersects(pathsA, pathsB []string) bool {
	for _, pathA := range pathsA {
		for _, pathB := range pathsB {
			if pathsIntersect(pathA, pathB) {
				return true
			}
		}
	}
	return false
}

func pathsIntersect(pathA, pathB string) bool {
	if pathA == "" || pathB == "" {
		return true
	}
	pathA = filepath.Clean(pathA)
	pathB = filepath.Clean(pathB)
	if !filepath.IsAbs(pathA) || !filepath.IsAbs(pathB) {
		return true
	}
	if pathA == pathB {
		return true
	}
	return hasPathPrefix(pathA, pathB) || hasPathPrefix(pathB, pathA)
}

func hasPathPrefix(path, prefix string) bool {
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (a *Agent) executeToolBatch(ctx context.Context, batch []scheduledToolCall, results []toolExecResult) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		fatalErr error
		once     sync.Once
		wg       sync.WaitGroup
	)

	for _, call := range batch {
		call := call
		wg.Add(1)
		go func() {
			defer wg.Done()

			result, err := a.tools.Execute(childCtx, call.call.Name, call.call.Input)
			if a.readTracker != nil {
				a.readTracker.NotifyTool(call.call.Name)
			}
			if err != nil {
				if call.cancelOnErr {
					once.Do(func() {
						fatalErr = fmt.Errorf("%w: %s: %w", core.ErrToolExecution, call.call.Name, err)
						cancel()
					})
					return
				}
				result = tools.ErrorResult(err.Error())
			}
			if result == nil {
				result = tools.ErrorResult("tool returned nil result")
			}
			if result.IsError && call.cancelOnErr {
				once.Do(func() {
					msg := strings.TrimSpace(result.Output)
					if msg == "" {
						msg = "tool returned error"
					}
					fatalErr = fmt.Errorf("%w: %s: %s", core.ErrToolExecution, call.call.Name, msg)
					cancel()
				})
				return
			}

			results[call.index] = toolExecResult{id: call.call.ID, output: result.Output, isError: result.IsError}
			if a.hooks != nil {
				if hookErr := a.hooks.RunPostToolUse(childCtx, call.call.Name, call.call.Input, result); hookErr != nil {
					a.logger.Warn("post-tool hook error", "tool", call.call.Name, "error", hookErr)
				}
			}
		}()
	}

	wg.Wait()
	return fatalErr
}
