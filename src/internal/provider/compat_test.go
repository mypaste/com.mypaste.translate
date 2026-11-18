package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

func compatRequest(provider, text, target string, settings map[string]any) Request {
	return Request{Text: text, Provider: provider, Target: target, Settings: settings}
}

func TestOpenAICompatUsesTheReadersAddressAndModel(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		completion("你好，世界。"),
	}}
	result, err := Translate(context.Background(), caller, compatRequest(
		"openai", "Hello, world.", AutoTarget,
		map[string]any{"openaiurl": "https://relay.example.com", "openaimodel": "gpt-4o"}))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Translation != "你好，世界。" ||
		result.Source != "en" || result.Target != "zh-CN" {
		t.Fatalf("result = %#v", result)
	}
	request := caller.requests[0]
	if request.URL != "https://relay.example.com/v1/chat/completions" {
		t.Fatalf("url = %s", request.URL)
	}
	// D-1185: the key belongs to the host. A runtime that could set this header
	// would make the secret setting pointless.
	for name := range request.Headers {
		if strings.EqualFold(name, "authorization") || strings.EqualFold(name, "x-api-key") {
			t.Fatalf("runtime set its own credential header: %#v", request.Headers)
		}
	}
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(request.Body), &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	if payload.Model != "gpt-4o" {
		t.Fatalf("model = %q", payload.Model)
	}
	if len(payload.Messages) != 2 || payload.Messages[1].Content != "Hello, world." {
		t.Fatalf("messages = %#v", payload.Messages)
	}
	if !strings.Contains(payload.Messages[0].Content, "简体中文") {
		t.Fatalf("prompt = %q", payload.Messages[0].Content)
	}
}

func claudeAnswerBody(text string) host.NetworkResponse {
	body, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": text}},
		"stop_reason": "end_turn",
	})
	return jsonResponse(string(body))
}

func TestClaudeCompatSendsTheMessagesShape(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		claudeAnswerBody("你好，世界。"),
	}}
	result, err := Translate(context.Background(), caller, compatRequest(
		"claude", "Hello, world.", AutoTarget,
		map[string]any{"claudeurl": "https://relay.example.com", "claudemodel": "claude-sonnet-4-5"}))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Translation != "你好，世界。" || result.Target != "zh-CN" {
		t.Fatalf("result = %#v", result)
	}
	request := caller.requests[0]
	if request.URL != "https://relay.example.com/v1/messages" {
		t.Fatalf("url = %s", request.URL)
	}
	if request.Headers["anthropic-version"] != claudeVersion {
		t.Fatalf("headers = %#v", request.Headers)
	}
	for name := range request.Headers {
		if strings.EqualFold(name, "x-api-key") ||
			strings.EqualFold(name, "authorization") {
			t.Fatalf("runtime set its own credential header: %#v", request.Headers)
		}
	}
	var payload struct {
		Model     string `json:"model"`
		System    string `json:"system"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(request.Body), &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	if payload.Model != "claude-sonnet-4-5" || payload.MaxTokens == 0 {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.System == "" || len(payload.Messages) != 1 ||
		payload.Messages[0].Content != "Hello, world." {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestClaudeLookupFillsTheCardFromYoudao(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		claudeAnswerBody("你好；喂"),
		jsonResponse(`{"ec":[{"word":{"usphone":"həˈloʊ","trs":[{"pos":"int.","tran":"你好"}]}}]}`),
	}}
	result, err := Lookup(context.Background(), caller, compatRequest(
		"claude", "hello", AutoTarget,
		map[string]any{"claudeurl": "https://relay.example.com"}))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.Word == nil || result.Word.Translation != "你好；喂" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Word.Phonetics) == 0 || len(result.Word.Entries) == 0 {
		t.Fatalf("dictionary did not fill the card: %#v", result.Word)
	}
	// The configured model default stands in when the reader left it empty.
	if caller.requests[0].URL != "https://relay.example.com/v1/messages" {
		t.Fatalf("url = %s", caller.requests[0].URL)
	}
}

func TestCompatRefusesAnUnfilledOrMalformedAddress(t *testing.T) {
	cases := []struct {
		provider string
		settings map[string]any
		want     string
	}{
		{"openai", nil, "还没有填写服务地址"},
		{"openai", map[string]any{"openaiurl": "  "}, "还没有填写服务地址"},
		{"openai", map[string]any{"openaiurl": "http://relay.example.com"}, "必须是 https 地址"},
		{"openai", map[string]any{"openaiurl": "https://relay.example.com/v1"}, "只填主机名"},
		{"openai", map[string]any{"openaiurl": "https://relay.example.com:8443"}, "只填主机名"},
		{"claude", map[string]any{"claudeurl": "https://user:x@relay.example.com"}, "只填主机名"},
	}
	for _, test := range cases {
		caller := &fakeCaller{}
		_, err := Translate(context.Background(), caller, compatRequest(
			test.provider, "Hello.", AutoTarget, test.settings))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s %#v: err = %v", test.provider, test.settings, err)
		}
		if len(caller.requests) != 0 {
			t.Fatalf("%s %#v: a request left the runtime", test.provider, test.settings)
		}
	}
}

func TestClaudeRefusesATruncatedOrUselessAnswer(t *testing.T) {
	truncated, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": "半句"}},
		"stop_reason": "max_tokens",
	})
	thinking := `{"content":[{"type":"thinking","thinking":"hmm"}],"stop_reason":"end_turn"}`
	for _, response := range []host.NetworkResponse{
		jsonResponse(string(truncated)), jsonResponse(thinking),
	} {
		caller := &fakeCaller{responses: []host.NetworkResponse{response}}
		if _, err := Translate(context.Background(), caller, compatRequest(
			"claude", "Hello.", AutoTarget,
			map[string]any{"claudeurl": "https://relay.example.com"})); err == nil {
			t.Fatalf("answer accepted: %s", response.Body)
		}
	}
}

func TestProvidersMarkAnUnconfiguredCompatService(t *testing.T) {
	for _, info := range Providers(nil) {
		if info.ID == "openai" || info.ID == "claude" {
			if info.Note == "" {
				t.Fatalf("%s has no setup note", info.ID)
			}
		} else if info.Note != "" {
			t.Fatalf("%s carried a note: %q", info.ID, info.Note)
		}
	}
	for _, info := range Providers(map[string]any{
		"openaiurl": "https://relay.example.com",
		"claudeurl": "https://relay.example.com",
	}) {
		if info.Note != "" {
			t.Fatalf("%s note = %q", info.ID, info.Note)
		}
	}
}
