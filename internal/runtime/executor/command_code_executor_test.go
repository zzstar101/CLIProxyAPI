package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/commandcode"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type commandCodeTransport func(*http.Request) (*http.Response, error)

func (f commandCodeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCommandCodeClaudeStreamUsesMessageStopAsTerminal(t *testing.T) {
	const model = "claude-sonnet-5"
	auth := &coreauth.Auth{ID: "command-claude", Provider: commandcode.Provider, Metadata: map[string]any{"api_key": "test-key"}}
	commandcode.SetSnapshot(auth, commandcode.Snapshot{Account: commandcode.Account{ID: "a", Subscription: commandcode.Subscription{PlanID: "individual-pro-v1", Status: "active"}}, Models: []commandcode.Model{{ID: model}}})
	commandcode.SetOverrides(auth, map[string]bool{model: true})
	transport := commandCodeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/provider/v1/messages" {
			t.Fatalf("wrong endpoint: %s", r.URL.Path)
		}
		requestBody, _ := io.ReadAll(r.Body)
		if !gjson.GetBytes(requestBody, "stream").Bool() {
			t.Fatal("Claude cross-protocol aggregation requires upstream SSE")
		}
		content := gjson.GetBytes(requestBody, "messages.0.content")
		if content.String() != "hello" && content.Get("0.text").String() != "hello" {
			t.Fatalf("Responses string input was lost: %s", requestBody)
		}
		body := ""
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-5","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			body += "data: " + event + "\n\n"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	payload := []byte(`{"model":"claude-sonnet-5","input":"hello","stream":true}`)
	result, err := NewCommandCodeExecutor(nil).ExecuteStream(ctx, auth, coreexecutor.Request{Model: model, Payload: payload}, coreexecutor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: payload, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		out.Write(chunk.Payload)
	}
	if !strings.Contains(out.String(), "response.completed") || !strings.Contains(out.String(), "hello") {
		t.Fatalf("incomplete Responses stream: %s", out.String())
	}
	for _, input := range []struct {
		format translator.Format
		body   []byte
	}{
		{translator.FormatOpenAI, []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hello"}]}`)},
		{translator.FormatOpenAIResponse, []byte(`{"model":"claude-sonnet-5","input":"hello"}`)},
	} {
		response, err := NewCommandCodeExecutor(nil).Execute(ctx, auth, coreexecutor.Request{Model: model, Payload: input.body}, coreexecutor.Options{SourceFormat: input.format, OriginalRequest: input.body})
		if err != nil || !strings.Contains(string(response.Payload), "hello") {
			t.Fatalf("empty Claude nonstream translation (%s): %s, %v", input.format, response.Payload, err)
		}
	}
}

func TestCommandCodeExecutorProtocolAndAccountGate(t *testing.T) {
	const model = "deepseek/deepseek-v4-flash"
	auth := &coreauth.Auth{ID: "command-account", Provider: commandcode.Provider, Metadata: map[string]any{"api_key": "test-key"}}
	commandcode.SetSnapshot(auth, commandcode.Snapshot{Account: commandcode.Account{ID: "a", Subscription: commandcode.Subscription{PlanID: "individual-pro-v1", Status: "active"}}, Models: []commandcode.Model{{ID: model}}})
	executor := NewCommandCodeExecutor(nil)
	formats := []struct {
		format  translator.Format
		payload string
	}{
		{translator.FormatOpenAI, `{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`},
		{translator.FormatOpenAIResponse, `{"model":"deepseek/deepseek-v4-flash","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"lookup"}}`},
		{translator.FormatClaude, `{"model":"deepseek/deepseek-v4-flash","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"lookup"}}`},
	}
	for _, tc := range formats {
		for _, stream := range []bool{false, true} {
			name := tc.format.String()
			if stream {
				name += "/stream"
			}
			t.Run(name, func(t *testing.T) {
				calls := 0
				transport := commandCodeTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.String() != commandcode.ProviderBaseURL+"/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
						t.Fatal("incorrect provider request")
					}
					body, _ := io.ReadAll(r.Body)
					if gjson.GetBytes(body, "model").String() != model || gjson.GetBytes(body, "tools.0.function.name").String() != "lookup" {
						t.Fatalf("lost model/tools: %s", body)
					}
					if gjson.GetBytes(body, "tool_choice.function.name").String() != "lookup" {
						t.Fatalf("invalid upstream tool choice: %s", body)
					}
					response := `{"id":"test","object":"chat.completion","model":"deepseek/deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":1}}}`
					header := http.Header{"Content-Type": []string{"application/json"}}
					if stream {
						header.Set("Content-Type", "text/event-stream")
						response = "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"deepseek/deepseek-v4-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\ndata: [DONE]\n\n"
					}
					return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				req := coreexecutor.Request{Model: commandcode.PublicModelID(model), Payload: []byte(strings.ReplaceAll(tc.payload, model, commandcode.PublicModelID(model)))}
				if stream {
					req.Payload, _ = sjson.SetBytes(req.Payload, "stream", true)
				}
				opts := coreexecutor.Options{SourceFormat: tc.format, OriginalRequest: req.Payload, Stream: stream}
				if stream {
					result, err := executor.ExecuteStream(ctx, auth, req, opts)
					if err != nil {
						t.Fatal(err)
					}
					var out strings.Builder
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
						out.Write(chunk.Payload)
					}
					if !strings.Contains(out.String(), "hello") {
						t.Fatalf("empty translated stream: %s", out.String())
					}
				} else {
					result, err := executor.Execute(ctx, auth, req, opts)
					if err != nil {
						t.Fatal(err)
					}
					if tc.format == translator.FormatOpenAIResponse && (gjson.GetBytes(result.Payload, "tool_choice.name").String() != "lookup" || gjson.GetBytes(result.Payload, "tools.0.name").String() != "lookup") {
						t.Fatalf("response echoed upstream tool schema instead of client schema: %s", result.Payload)
					}
					if tc.format == translator.FormatOpenAIResponse && gjson.GetBytes(result.Payload, "usage.output_tokens_details.reasoning_tokens").Int() != 1 {
						t.Fatalf("lost upstream reasoning usage: %s", result.Payload)
					}
					if !strings.Contains(string(result.Payload), "hello") {
						t.Fatalf("invalid translated response: %s", result.Payload)
					}
				}
				blocked := auth.Clone()
				commandcode.SetOverrides(blocked, map[string]bool{model: false})
				if _, err := executor.Execute(ctx, blocked, req, opts); err == nil {
					t.Fatal("disabled model reached upstream")
				}
				if calls != 1 {
					t.Fatalf("unexpected upstream calls: %d", calls)
				}
			})
		}
	}
}
