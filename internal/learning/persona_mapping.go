package learning

import "strings"

func PersonaDeltaFromHint(direction, magnitude string) float64 {
	var base float64
	switch strings.ToLower(strings.TrimSpace(magnitude)) {
	case "small":
		base = 0.01
	case "medium":
		base = 0.03
	case "large":
		base = 0.06
	default:
		return 0
	}

	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "increase":
		return base
	case "decrease":
		return -base
	case "neutral":
		return 0
	default:
		return 0
	}
}
