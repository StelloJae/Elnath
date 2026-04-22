package prompt

import (
	"context"
	"strings"
)

// ChatToolGuideNode renders the structured tool-use instruction block
// (triggers, tool catalog, execution rules 1-6, fact-fence alternate
// sources) that the chat path emits when its tool loop is active.
//
// Double-gated: IsChat (task path never sees this) and AvailableTools
// non-empty (chat legacy stream with no executor skips the guide since
// the model's tool_use blocks would be silently dropped, and pretending
// otherwise would mislead the model).
//
// Content mirrors the legacy internal/telegram.chatToolGuideHeader
// (chat_tools.go:162-195). L3.1 keeps the legacy function alive for
// dual-path safety; L3.3 removes it once the Builder path is the sole
// source of chat tool guidance.
type ChatToolGuideNode struct {
	priority int
}

func NewChatToolGuideNode(priority int) *ChatToolGuideNode {
	return &ChatToolGuideNode{priority: priority}
}

func (n *ChatToolGuideNode) Name() string {
	return "chat_tool_guide"
}

// CacheBoundary classifies the chat tool guide as volatile:
// AvailableTools varies per tool-loop turn.
func (n *ChatToolGuideNode) CacheBoundary() CacheBoundary { return CacheBoundaryVolatile }

func (n *ChatToolGuideNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

const chatToolGuideBody = `## 도구 사용 지침

아래 상황에서는 반드시 도구를 호출하세요 (추측·지식 cutoff 답변 금지):
- "지금/오늘/최근/최신" 등 현재 시점 정보 (시세·뉴스·릴리즈·트렌드)
- 특정 URL의 내용 확인이 필요한 질문
- 로컬 파일·코드 내용 확인이 필요한 질문
- 외부 사실 검증이 필요한 주장

사용 가능한 도구:
- web_search: 최신 정보 검색 (뉴스·가격·트렌드)
- web_fetch: 주어진 URL 내용 가져오기
- read_file: 프로젝트 파일 내용 읽기
- glob: 파일 경로 패턴 매칭
- grep: 파일 내용에서 문자열 검색

실행 규칙:
1. 위 상황에 해당하면 먼저 도구를 호출한 뒤 답한다.
2. 서로 독립적인 조회 여러 개는 한 번에 병렬 tool_use 블록으로 발행한다.
3. 도구 결과를 받으면 한국어로 자연스럽게 요약·정리해 답한다.
4. 일반 지식·간단한 대화처럼 도구 없이 답할 수 있으면 그대로 답한다.
5. tool_result 가 요청한 대상의 **구체 수치 rows** (종목명·가격·거래량·건수 등) 를 반환하지 못했다면, 사전지식으로 이름·수치를 지어내지 말 것. 대신 (a) 무엇이 추출됐고 무엇이 비어있는지 명시, (b) 아래 대안 소스 시도 또는 파트너에게 재질문.
6. web_search 나 web_fetch 로 외부 출처를 참고한 답변에는 **반드시 답변 맨 끝에 "Sources:" 섹션** 을 포함하고, 참고한 URL 을 markdown hyperlink (` + "`- [Title](URL)`" + `) 형식으로 나열한다. 예시:

   Sources:
   - [Yahoo Finance most-active](https://finance.yahoo.com/most-active)
   - [Naver 거래상위](https://finance.naver.com/sise/sise_quant.naver)

대안 소스 (primary scrape 가 sparse 일 때 순차 시도):
- US 거래량 상위: https://finance.yahoo.com/most-active → https://query1.finance.yahoo.com/v1/finance/trending/US (JSON) → https://finviz.com/screener.ashx?v=111&s=ta_mostactive
- 한국 시장: https://finance.naver.com/sise/sise_quant.naver (코스피) · https://finance.naver.com/sise/sise_quant.naver?sosok=1 (코스닥)`

func (n *ChatToolGuideNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || !state.IsChat || len(state.AvailableTools) == 0 {
		return "", nil
	}
	return strings.TrimSpace(chatToolGuideBody), nil
}
