package daemon

import (
	"fmt"
	"strings"
)

type DeliveryTargetKind string

const (
	DeliveryTargetOrigin   DeliveryTargetKind = "origin"
	DeliveryTargetLocal    DeliveryTargetKind = "local"
	DeliveryTargetPlatform DeliveryTargetKind = "platform"
)

// DeliveryTarget describes where task output or progress should be delivered.
// A platform target without an address means "that platform's configured home
// channel"; an explicit address means "this concrete chat/channel/thread".
type DeliveryTarget struct {
	Kind     DeliveryTargetKind
	Platform string
	Address  string
	ThreadID string
	Explicit bool
}

func ParseDeliveryTarget(raw string) (DeliveryTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DeliveryTarget{}, fmt.Errorf("delivery target is required")
	}
	lower := strings.ToLower(raw)
	switch lower {
	case string(DeliveryTargetOrigin):
		return DeliveryTarget{Kind: DeliveryTargetOrigin}, nil
	case string(DeliveryTargetLocal):
		return DeliveryTarget{Kind: DeliveryTargetLocal}, nil
	}

	parts := strings.SplitN(raw, ":", 3)
	platform := strings.ToLower(strings.TrimSpace(parts[0]))
	if platform == "" {
		return DeliveryTarget{}, fmt.Errorf("delivery target platform is required")
	}

	target := DeliveryTarget{
		Kind:     DeliveryTargetPlatform,
		Platform: platform,
	}
	if len(parts) > 1 {
		target.Address = strings.TrimSpace(parts[1])
		if target.Address == "" {
			return DeliveryTarget{}, fmt.Errorf("delivery target address is required when ':' is used")
		}
		target.Explicit = true
	}
	if len(parts) > 2 {
		target.ThreadID = strings.TrimSpace(parts[2])
	}
	return target, nil
}

func ParseDeliveryTargets(raw []string) ([]DeliveryTarget, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	targets := make([]DeliveryTarget, 0, len(raw))
	for _, item := range raw {
		target, err := ParseDeliveryTarget(item)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func normalizeDeliveryTargetStrings(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func deliveryTargetStrings(targets []DeliveryTarget) []string {
	if len(targets) == 0 {
		return nil
	}
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		if raw := strings.TrimSpace(target.String()); raw != "" {
			out = append(out, raw)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func DeliveryTargetStrings(targets []DeliveryTarget) []string {
	return deliveryTargetStrings(targets)
}

func parseDeliveryTargetsLenient(raw []string) []DeliveryTarget {
	targets, err := ParseDeliveryTargets(raw)
	if err != nil {
		return nil
	}
	return targets
}

func (t DeliveryTarget) IsHomeChannel() bool {
	return t.Kind == DeliveryTargetPlatform && t.Platform != "" && t.Address == "" && !t.Explicit
}

func (t DeliveryTarget) String() string {
	switch t.Kind {
	case DeliveryTargetOrigin:
		return string(DeliveryTargetOrigin)
	case DeliveryTargetLocal:
		return string(DeliveryTargetLocal)
	case DeliveryTargetPlatform:
		if t.Address != "" && t.ThreadID != "" {
			return t.Platform + ":" + t.Address + ":" + t.ThreadID
		}
		if t.Address != "" || t.Explicit {
			return t.Platform + ":" + t.Address
		}
		return t.Platform
	default:
		return ""
	}
}
