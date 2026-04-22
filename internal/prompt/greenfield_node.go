package prompt

import (
	"context"
	"strings"
)

type GreenfieldNode struct {
	priority int
}

func NewGreenfieldNode(priority int) *GreenfieldNode {
	return &GreenfieldNode{priority: priority}
}

func (n *GreenfieldNode) Name() string { return "greenfield" }

// CacheBoundary classifies greenfield context as stable: the anchor
// is a project-posture flag, not per-turn state.
func (n *GreenfieldNode) CacheBoundary() CacheBoundary { return CacheBoundaryStable }

func (n *GreenfieldNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *GreenfieldNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil {
		return "", nil
	}
	if state.BenchmarkMode {
		return "", nil
	}
	if state.ExistingCode {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("# New Project Guidance\n\n")
	b.WriteString("## Structure\n")
	b.WriteString("- Start with the smallest working version. One file > three files when starting out.\n")
	b.WriteString("- Organize by feature/domain, not by type (models/, controllers/, views/).\n")
	b.WriteString("- Add files only when a single file grows past ~200 lines.\n\n")
	b.WriteString("## Implementation\n")
	b.WriteString("- Write a failing test first, then implement.\n")
	b.WriteString("- Choose well-known libraries over hand-rolling. Check package registries before writing utilities.\n")
	b.WriteString("- Set up CI/linting from the first commit.\n\n")
	b.WriteString("## Architecture\n")
	b.WriteString("- Start with the simplest architecture that works. Monolith > microservices for v1.\n")
	b.WriteString("- Define clear interfaces between components so you can refactor later without rewriting.\n")
	b.WriteString("- Hard-code configuration initially, extract to config files when you have 3+ values.\n\n")
	b.WriteString("## Quality\n")
	b.WriteString("- Every public function gets a test before moving to the next function.\n")
	b.WriteString("- Handle errors explicitly at every level. Never silently swallow errors.\n")
	b.WriteString("- Validate all inputs at system boundaries.")

	switch state.TaskLanguage {
	case "go":
		b.WriteString("\n\nGo-specific:\n")
		b.WriteString("- Use `go mod init` with a meaningful module path.\n")
		b.WriteString("- Start with `main.go` + one package. Split when responsibilities diverge.\n")
		b.WriteString("- Use table-driven tests from the start.\n")
		b.WriteString("- Accept interfaces, return structs. Keep interfaces small (1-3 methods).\n")
		b.WriteString("- Use `log/slog` for structured logging.\n")
		b.WriteString("- Run `go vet` and `go test -race` before every commit.")
	case "typescript":
		b.WriteString("\n\nTypeScript-specific:\n")
		b.WriteString("- Use strict TypeScript (`strict: true` in tsconfig).\n")
		b.WriteString("- Prefer named exports over default exports.\n")
		b.WriteString("- Set up ESLint + Prettier from the first commit.\n")
		b.WriteString("- Use `vitest` or `jest` for testing. Write tests alongside source files.\n")
		b.WriteString("- Prefer `const` and immutable patterns. Avoid `any` type.")
	case "python":
		b.WriteString("\n\nPython-specific:\n")
		b.WriteString("- Use `pyproject.toml` for project configuration.\n")
		b.WriteString("- Set up `ruff` for linting and formatting from the start.\n")
		b.WriteString("- Use type hints consistently. Run `mypy` or `pyright`.\n")
		b.WriteString("- Use `pytest` for testing with fixtures and parametrize.\n")
		b.WriteString("- Prefer dataclasses or Pydantic models over raw dicts.")
	}

	return b.String(), nil
}
