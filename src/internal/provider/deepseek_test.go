package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

func deepseekRequest(text, target, model string) Request {
	return Request{Text: text, Provider: "deepseek", Target: target, Model: model}
}

func completion(content string) host.NetworkResponse {
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"message":       map[string]string{"content": content},
			"finish_reason": "stop",
		}},
	})
	return jsonResponse(string(body))
}

func TestDeepSeekTranslatesOneParagraphWithoutTheKey(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		completion("你好，世界。"),
	}}
	result, err := Translate(context.Background(), caller,
		deepseekRequest("Hello, world.", AutoTarget, "deepseek-chat"))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Translation != "你好，世界。" ||
		result.Source != "en" || result.Target != "zh-CN" {
		t.Fatalf("result = %#v", result)
	}
	if len(caller.requests) != 1 {
		t.Fatalf("requests = %d", len(caller.requests))
	}
	request := caller.requests[0]
	if request.URL != deepseekEndpoint || request.Method != "POST" {
		t.Fatalf("request = %#v", request)
	}
	// D-1176: the key is the host's. A runtime that could set this header would
	// not need the secret setting at all.
	for name := range request.Headers {
		if strings.EqualFold(name, "authorization") {
			t.Fatalf("runtime set its own authorization header: %#v", request.Headers)
		}
	}
	if request.Headers["content-type"] != "application/json" {
		t.Fatalf("headers = %#v", request.Headers)
	}
	var payload struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(request.Body), &payload); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if payload.Model != "deepseek-chat" || payload.Stream {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Messages) != 2 ||
		payload.Messages[0].Role != "system" ||
		payload.Messages[1].Role != "user" ||
		payload.Messages[1].Content != "Hello, world." {
		t.Fatalf("messages = %#v", payload.Messages)
	}
	prompt := payload.Messages[0].Content
	for _, want := range []string{"简体中文", "只输出译文", "段落"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q does not say %q", prompt, want)
		}
	}
}

func TestDeepSeekAutoDirectionFromChinese(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		completion("Hello, world."),
	}}
	result, err := Translate(context.Background(), caller,
		deepseekRequest("你好，世界。", AutoTarget, "deepseek-chat"))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Target != "en" {
		t.Fatalf("target = %q", result.Target)
	}
	if !strings.Contains(caller.requests[0].Body, "英语") {
		t.Fatalf("body = %s", caller.requests[0].Body)
	}
}

func TestDeepSeekPassesTheChosenModelThrough(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		completion("译文"),
	}}
	if _, err := Translate(context.Background(), caller,
		deepseekRequest("text", "zh-CN", "deepseek-reasoner")); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if !strings.Contains(caller.requests[0].Body, `"deepseek-reasoner"`) {
		t.Fatalf("body = %s", caller.requests[0].Body)
	}
}

func TestDeepSeekPassesTheHostRefusalThrough(t *testing.T) {
	// The host refuses before any request leaves when no key is saved; the
	// runtime adds no words of its own that would hide where to fix it.
	refusal := errors.New("尚未保存「DeepSeek API 密钥」：请在设置 › 扩展 › 翻译对照 中填写后重试")
	caller := &fakeCaller{errs: []error{refusal}}
	_, err := Translate(context.Background(), caller,
		deepseekRequest("Hello.", "zh-CN", "deepseek-chat"))
	if err == nil || !strings.Contains(err.Error(), "尚未保存") ||
		!strings.Contains(err.Error(), "设置 › 扩展 › 翻译对照") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeepSeekFailureWords(t *testing.T) {
	for _, test := range []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"error":{"message":"Authentication Fails"}}`, "密钥无效或已过期"},
		{403, `{}`, "密钥无效或已过期"},
		{402, `{"error":{"message":"Insufficient Balance"}}`, "余额不足"},
		{429, `{}`, "请求过于频繁"},
		{503, `{}`, "暂时不可用"},
		{400, `{"error":{"message":"Model Not Exist"}}`, "Model Not Exist"},
	} {
		caller := &fakeCaller{responses: []host.NetworkResponse{
			{Status: test.status, Body: test.body},
		}}
		_, err := Translate(context.Background(), caller,
			deepseekRequest("Hello.", "zh-CN", "deepseek-chat"))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("status %d error = %v, want %q", test.status, err, test.want)
		}
	}
}

func TestDeepSeekRefusesATruncatedOrEmptyAnswer(t *testing.T) {
	truncated := jsonResponse(`{"choices":[{"message":{"content":"half"},"finish_reason":"length"}]}`)
	caller := &fakeCaller{responses: []host.NetworkResponse{truncated}}
	_, err := Translate(context.Background(), caller,
		deepseekRequest("Hello.", "zh-CN", "deepseek-chat"))
	if err == nil || !strings.Contains(err.Error(), "截断") {
		t.Fatalf("truncated error = %v", err)
	}
	caller = &fakeCaller{responses: []host.NetworkResponse{jsonResponse(`{"choices":[]}`)}}
	_, err = Translate(context.Background(), caller,
		deepseekRequest("Hello.", "zh-CN", "deepseek-chat"))
	if err == nil || !strings.Contains(err.Error(), "没有返回译文") {
		t.Fatalf("empty error = %v", err)
	}
}

func TestDeepSeekLookupFillsCardFromYoudao(t *testing.T) {
	dictionary := jsonResponse(`{"ec":[{"word":{"ukphone":"həˈləʊ",
		"usphone":"həˈloʊ","trs":[{"pos":"int.","tran":"你好"}]}}]}`)
	caller := &fakeCaller{responses: []host.NetworkResponse{
		completion("你好"),
		dictionary,
	}}
	result, err := Lookup(context.Background(), caller,
		deepseekRequest("hello", AutoTarget, "deepseek-chat"))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.Word == nil || result.Word.Term != "hello" ||
		result.Word.Translation != "你好" {
		t.Fatalf("card = %#v", result.Word)
	}
	if len(result.Word.Phonetics) == 0 || len(result.Word.Entries) == 0 {
		t.Fatalf("dictionary did not fill the card: %#v", result.Word)
	}
	// The word prompt asks for senses, not a paragraph of explanation.
	if !strings.Contains(caller.requests[0].Body, "词典") {
		t.Fatalf("word prompt = %s", caller.requests[0].Body)
	}
}

func TestDeepSeekIsOfferedWithItsTwoModels(t *testing.T) {
	info, ok := findProvider("deepseek")
	if !ok {
		t.Fatal("deepseek is not offered")
	}
	if len(info.Models) != 2 || info.DefaultModel != "deepseek-chat" {
		t.Fatalf("models = %#v", info)
	}
	if !contains(info.Targets, AutoTarget) || !contains(info.Targets, "ja") {
		t.Fatalf("targets = %#v", info.Targets)
	}
	if _, err := NormalizeSettings(Settings{
		Provider: "deepseek", Target: "zh-CN", Model: "gpt-4",
	}); err == nil {
		t.Fatal("an undeclared model was accepted")
	}
	settings, err := NormalizeSettings(Settings{Provider: "deepseek"})
	if err != nil || settings.Model != "deepseek-chat" ||
		settings.Target != AutoTarget {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
}
