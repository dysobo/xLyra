package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	routeengine "xlyra/server/internal/router"
)

func TestAggregateChatCompletionSSE(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"c1","model":"glm-5.2","created":1,"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think "}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"hard","content":"Hel"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		``,
		`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	body, err := aggregateChatCompletionSSE([]byte(stream))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	var decoded struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Object != "chat.completion" || decoded.Model != "glm-5.2" || len(decoded.Choices) != 1 {
		t.Fatalf("unexpected envelope: %s", body)
	}
	choice := decoded.Choices[0]
	if choice.Message.Content != "Hello" || choice.Message.ReasoningContent != "think hard" || choice.Message.Role != "assistant" || choice.FinishReason != "stop" {
		t.Fatalf("unexpected choice: %+v", choice)
	}
	if decoded.Usage.PromptTokens != 3 || decoded.Usage.CompletionTokens != 2 {
		t.Fatalf("unexpected usage: %+v", decoded.Usage)
	}
}

func TestAggregateChatCompletionSSEToolCalls(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"c2","model":"kimi-k2.7","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")
	body, err := aggregateChatCompletionSSE([]byte(stream))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if !strings.Contains(string(body), `"arguments":"{\"q\":\"x\"}"`) || !strings.Contains(string(body), `"finish_reason":"tool_calls"`) || !strings.Contains(string(body), `"content":null`) {
		t.Fatalf("unexpected tool call aggregation: %s", body)
	}
}

func TestAggregateChatCompletionSSEErrors(t *testing.T) {
	if _, err := aggregateChatCompletionSSE([]byte("data: [DONE]\n")); err == nil {
		t.Fatal("expected error for empty stream")
	}
	if _, err := aggregateChatCompletionSSE([]byte(`data: {"error":{"message":"boom"}}` + "\n")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected upstream error, got %v", err)
	}
}

func TestWorkBuddyChatAdapterBuffersStreamedResponse(t *testing.T) {
	candidate := routeengine.Candidate{}
	candidate.Site.SiteType = "workbuddy"
	candidate.Model.UpstreamName = "glm-5.2"
	request := gatewayRequest{DownstreamPath: gatewayEndpointChatCompletions, Payload: map[string]any{"model": "glm-5.2", "messages": []any{}}}
	protocol := newOpenAIChatProtocolAdapter(request, candidate)

	payload, err := protocol.BuildUpstreamPayload(request, candidate)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload["stream"] != true {
		t.Fatalf("workbuddy requests must be forced to stream, got %#v", payload["stream"])
	}
	if got := protocol.UpstreamPath(""); got != "https://copilot.tencent.com/v2/chat/completions" {
		t.Fatalf("unexpected upstream path %q", got)
	}
	if !protocolUsesRawBufferedResponse(protocol) {
		t.Fatal("workbuddy must bypass the Responses stream buffering")
	}

	headers := http.Header{"Content-Type": []string{"text/event-stream"}}
	sse := `data: {"id":"c1","model":"glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}` + "\n\ndata: [DONE]\n"
	buffered, err := protocol.TransformBufferedResponse(http.StatusOK, headers, []byte(sse))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if buffered.ContentType != "application/json" || !strings.Contains(string(buffered.Body), `"content":"ok"`) || buffered.Usage.PromptTokens != 1 {
		t.Fatalf("unexpected buffered response: %+v body=%s", buffered, buffered.Body)
	}

	req := httptest.NewRequest(http.MethodPost, "https://copilot.tencent.com/v2/chat/completions", nil)
	req.Header.Set("anthropic-beta", "claude-code-20250219")
	req.Header.Set("x-stainless-lang", "js")
	protocol.ApplyUpstreamHeaders(req, "token-1", "tenant.example", true)
	if req.Header.Get("Authorization") != "Bearer token-1" || req.Header.Get("X-Domain") != "tenant.example" || req.Header.Get("X-Product") != "SaaS" {
		t.Fatalf("unexpected headers: %v", req.Header)
	}
	if req.Header.Get("anthropic-beta") != "" || req.Header.Get("x-stainless-lang") != "" {
		t.Fatalf("foreign client headers must be removed, got %v", req.Header)
	}

	request.Payload["messages"] = []any{map[string]any{"role": "system", "content": "You are Claude Code, Anthropic's official CLI for Claude."}}
	rewritten, err := protocol.BuildUpstreamPayload(request, candidate)
	if err != nil {
		t.Fatalf("rewrite payload: %v", err)
	}
	system := rewritten["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(system, "official CLI for Claude") || !strings.Contains(system, "official CLI tool for Claude") {
		t.Fatalf("blocked phrase was not rewritten: %q", system)
	}
}

func TestWorkBuddyInternationalAccountsUseTheirOwnSite(t *testing.T) {
	candidate := routeengine.Candidate{}
	candidate.Site.SiteType = "workbuddy"
	candidate.Model.UpstreamName = "glm-5.2"
	request := gatewayRequest{DownstreamPath: gatewayEndpointChatCompletions, Payload: map[string]any{"model": "glm-5.2", "messages": []any{}}}
	protocol := newOpenAIChatProtocolAdapter(request, candidate)

	// The gateway passes the account's site base URL to UpstreamPath.
	if got := protocol.UpstreamPath("https://www.codebuddy.ai"); got != "https://www.codebuddy.ai/v2/chat/completions" {
		t.Fatalf("international upstream path = %q", got)
	}

	req := httptest.NewRequest(http.MethodPost, "https://www.codebuddy.ai/v2/chat/completions", nil)
	protocol.ApplyUpstreamHeaders(req, "token-1", "www.codebuddy.ai", true)
	if req.Header.Get("Origin") != "https://www.codebuddy.ai" || req.Header.Get("Referer") != "https://www.codebuddy.ai/" {
		t.Fatalf("international headers = %v", req.Header)
	}
}
