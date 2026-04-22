package prompt

import (
	"context"
	"strings"
)

// ChatSystemPromptNode renders chat-only identity / locale / detailed-answer
// guidance. Task paths (IsChat=false) skip this node so the 17-node pipeline
// stays chat-clean. Content mirrors the legacy
// internal/telegram.chatSystemPrompt constant (chat.go:31-33); L3.1 keeps
// the constant alive for dual-path safety, L3.2 removes it once the
// Builder path is mandatory.
type ChatSystemPromptNode struct {
	priority int
}

func NewChatSystemPromptNode(priority int) *ChatSystemPromptNode {
	return &ChatSystemPromptNode{priority: priority}
}

func (n *ChatSystemPromptNode) Name() string {
	return "chat_system"
}

func (n *ChatSystemPromptNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

const chatSystemBody = "너는 Elnath, 파트너의 개인 AI 어시스턴트야. 사용자 언어에 맞춰 답하되 한국어가 기본이야.\n" +
	"답변은 직접적·구체적·충분히 상세하게 — 배경·이유·대안까지 담아 설명해. 파트너는 짧고 밋밋한 답보다 근거 있는 상세한 답을 선호해.\n" +
	"실시간 정보·파일·외부 사실이 필요하면 추측하지 말고 허용된 도구를 호출해. 도구 결과는 한국어로 자연스럽게 정리해 전달해."

func (n *ChatSystemPromptNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || !state.IsChat {
		return "", nil
	}
	return strings.TrimSpace(chatSystemBody), nil
}
