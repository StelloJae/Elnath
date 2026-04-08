package tools

import "fmt"

const toolMaxOutputBytes = 64 * 1024

func truncateOutput(output string, limit int) string {
	if limit <= 0 || len(output) <= limit {
		return output
	}

	suffix := fmt.Sprintf("\n... [output truncated to %d bytes]\n", limit)
	keep := limit - len(suffix)
	if keep < 0 {
		keep = 0
	}
	return output[:keep] + suffix
}
