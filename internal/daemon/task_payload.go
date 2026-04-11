package daemon

import (
	"encoding/json"
	"strings"

	"github.com/stello/elnath/internal/identity"
)

// TaskPayload is the shared queue payload contract for daemon work.
// Plain string payloads remain supported for backward compatibility.
type TaskPayload struct {
	Prompt    string             `json:"prompt"`
	SessionID string             `json:"session_id,omitempty"`
	Surface   string             `json:"surface,omitempty"`
	Principal identity.Principal `json:"principal,omitempty"`
}

func ParseTaskPayload(raw string) TaskPayload {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TaskPayload{}
	}

	var payload TaskPayload
	if strings.HasPrefix(raw, "{") && json.Unmarshal([]byte(raw), &payload) == nil {
		payload = normalizeTaskPayload(payload)
		if payload.Prompt != "" {
			return payload
		}
	}

	return TaskPayload{Prompt: raw}
}

func EncodeTaskPayload(payload TaskPayload) string {
	payload = normalizeTaskPayload(payload)
	if payload.SessionID == "" && payload.Surface == "" && payload.Principal.IsZero() {
		return payload.Prompt
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return payload.Prompt
	}
	return string(data)
}

func normalizeTaskPayload(payload TaskPayload) TaskPayload {
	payload.Prompt = strings.TrimSpace(payload.Prompt)
	payload.SessionID = strings.TrimSpace(payload.SessionID)
	payload.Surface = strings.TrimSpace(payload.Surface)
	payload.Principal = identity.NewPrincipal(identity.PrincipalSource{
		UserID:    payload.Principal.UserID,
		ProjectID: payload.Principal.ProjectID,
		Surface:   payload.Principal.Surface,
	})
	if payload.Principal.Surface == "" && payload.Surface != "" {
		payload.Principal.Surface = payload.Surface
	}
	if payload.Surface == "" && payload.Principal.Surface != "" {
		payload.Surface = payload.Principal.Surface
	}
	return payload
}
