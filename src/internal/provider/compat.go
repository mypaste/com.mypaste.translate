package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// A compatible service is one the reader names: the manifest declares the
// address as a configuredHosts setting, so the host adds exactly the hostname
// the reader typed to this invocation's destinations and sends the matching
// key there (D-1031, D-1185). Nothing here knows a vendor's hostname, a
// vendor's model list, or a key: the address, the model and the credential
// all come from the reader, through the host.
//
// The address setting holds an origin only. The path below is the service
// family's own, so a reader pasting an OpenAI-style base URL does not have to
// guess which suffix the extension appends.
type compatService struct {
	Label        string
	URLKey       string
	ModelKey     string
	DefaultModel string
	Path         string
}

var (
	openAICompat = compatService{
		Label:        "OpenAI 兼容",
		URLKey:       "openaiurl",
		ModelKey:     "openaimodel",
		DefaultModel: "gpt-4o-mini",
		Path:         "/v1/chat/completions",
	}
	claudeCompat = compatService{
		Label:        "Claude 兼容",
		URLKey:       "claudeurl",
		ModelKey:     "claudemodel",
		DefaultModel: "claude-sonnet-4-5",
		Path:         "/v1/messages",
	}
)

// compatServiceFor names the compatible service behind a provider ID, if any.
func compatServiceFor(id string) (compatService, bool) {
	switch id {
	case "openai":
		return openAICompat, true
	case "claude":
		return claudeCompat, true
	}
	return compatService{}, false
}

// endpoint resolves the address and model the reader filled in. A missing or
// malformed address is an actionable sentence rather than a host refusal: the
// reader has to fix it in the extension's settings before any request can be
// made anyway.
func (service compatService) endpoint(settings map[string]any) (string, string, error) {
	origin, err := service.origin(settings)
	if err != nil {
		return "", "", err
	}
	model := strings.TrimSpace(settingText(settings, service.ModelKey))
	if model == "" {
		model = service.DefaultModel
	}
	return origin + service.Path, model, nil
}

// origin validates the one address the reader typed. The host applies the
// same rule when the setting is saved; checking here as well turns a stale or
// hand-edited value into a sentence the page can show. Plain HTTP is accepted
// on purpose: a self-hosted gateway usually runs it on a high port, and the
// host keeps the whole origin (scheme, host and port) as the one destination
// the key may travel to (D-1187).
func (service compatService) origin(settings map[string]any) (string, error) {
	raw := strings.TrimSpace(settingText(settings, service.URLKey))
	if raw == "" {
		return "", fmt.Errorf(
			"%s还没有填写服务地址：请在 设置 › 扩展 › 翻译对照 中填写 https 或 http 地址", service.Label)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s的服务地址无效，例如 https://api.example.com 或 http://192.168.1.10:11434",
			service.Label)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "https" && scheme != "http") || parsed.Hostname() == "" {
		return "", fmt.Errorf("%s的服务地址必须是 https 或 http 地址，例如 https://api.example.com 或 http://192.168.1.10:11434",
			service.Label)
	}
	if parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("%s的服务地址只填协议、主机与端口，不要带用户名、路径或查询", service.Label)
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := scheme + "://" + host
	port := parsed.Port()
	if port != "" {
		number, convErr := strconv.Atoi(port)
		if convErr != nil || number <= 0 || number > 65535 {
			return "", fmt.Errorf("%s的服务地址端口无效", service.Label)
		}
	}
	if port != "" && port != defaultPort(scheme) {
		origin += ":" + port
	}
	return origin, nil
}

func defaultPort(scheme string) string {
	if scheme == "http" {
		return "80"
	}
	return "443"
}

func settingText(settings map[string]any, key string) string {
	value, _ := settings[key].(string)
	return value
}

// openaiTranslate sends one paragraph to an OpenAI-compatible chat completion
// endpoint. The key, if saved, is added by the host for the reader's own host.
func openaiTranslate(
	ctx context.Context, caller Caller, text, target string, settings map[string]any,
) (Result, error) {
	endpoint, model, err := openAICompat.endpoint(settings)
	if err != nil {
		return Result{}, err
	}
	source, resolved := direction(text, target)
	translation, err := chatCompletionAsk(ctx, caller, openAICompat.Label, endpoint,
		text, model, paragraphPrompt(languageName(resolved)), 4096)
	if err != nil {
		return Result{}, err
	}
	return Result{Translation: translation, Source: source, Target: resolved}, nil
}

// openaiLookup translates one word or phrase; the dictionary fills the card.
func openaiLookup(
	ctx context.Context, caller Caller, word, target string, settings map[string]any,
) (Result, error) {
	endpoint, model, err := openAICompat.endpoint(settings)
	if err != nil {
		return Result{}, err
	}
	source, resolved := direction(word, target)
	translation, err := chatCompletionAsk(ctx, caller, openAICompat.Label, endpoint,
		word, model, wordPrompt(languageName(resolved)), 256)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Translation: translation,
		Source:      source,
		Target:      resolved,
		Word:        &Word{Term: word, Translation: translation},
	}, nil
}

// claudeVersion is the Messages API version every Anthropic-compatible
// endpoint requires. The host's header whitelist admits exactly this name
// (D-1185); the key itself is still the host's to add.
const claudeVersion = "2023-06-01"

// claudeTranslate sends one paragraph to an Anthropic-compatible Messages
// endpoint.
func claudeTranslate(
	ctx context.Context, caller Caller, text, target string, settings map[string]any,
) (Result, error) {
	endpoint, model, err := claudeCompat.endpoint(settings)
	if err != nil {
		return Result{}, err
	}
	source, resolved := direction(text, target)
	translation, err := claudeAsk(ctx, caller, endpoint, text, model,
		paragraphPrompt(languageName(resolved)), 4096)
	if err != nil {
		return Result{}, err
	}
	return Result{Translation: translation, Source: source, Target: resolved}, nil
}

// claudeLookup translates one word or phrase; the dictionary fills the card.
func claudeLookup(
	ctx context.Context, caller Caller, word, target string, settings map[string]any,
) (Result, error) {
	endpoint, model, err := claudeCompat.endpoint(settings)
	if err != nil {
		return Result{}, err
	}
	source, resolved := direction(word, target)
	translation, err := claudeAsk(ctx, caller, endpoint, word, model,
		wordPrompt(languageName(resolved)), 256)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Translation: translation,
		Source:      source,
		Target:      resolved,
		Word:        &Word{Term: word, Translation: translation},
	}, nil
}

// claudeAsk performs one Messages request. The system instruction is a
// top-level field in this protocol, and the answer is a list of content
// blocks rather than one message object.
func claudeAsk(
	ctx context.Context, caller Caller, endpoint, text, model, prompt string, maxTokens int,
) (string, error) {
	response, err := postJSON(ctx, caller, claudeCompat.Label, endpoint,
		map[string]string{"anthropic-version": claudeVersion},
		map[string]any{
			"model":       model,
			"max_tokens":  maxTokens,
			"temperature": 0,
			"system":      prompt,
			"messages": []map[string]string{
				{"role": "user", "content": text},
			},
		})
	if err != nil {
		return "", err
	}
	if response.Status != 200 {
		return "", chatFailure(claudeCompat.Label, response.Status, response.Body)
	}
	return claudeAnswer(response.Body)
}

// claudeAnswer joins the text blocks of the one answer. A tool block or a
// thinking block is not part of a translation, so only text blocks count.
func claudeAnswer(body string) (string, error) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Error      *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", fmt.Errorf("无法解析%s的译文", claudeCompat.Label)
	}
	if payload.Error != nil {
		message := strings.TrimSpace(payload.Error.Message)
		if message == "" {
			message = "翻译服务返回了错误"
		}
		return "", fmt.Errorf("%s：%s", claudeCompat.Label, message)
	}
	if payload.StopReason == "max_tokens" {
		return "", errors.New("译文过长被服务截断，请缩短段落或换用其他翻译服务")
	}
	parts := make([]string, 0, len(payload.Content))
	for _, block := range payload.Content {
		if block.Type != "" && block.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%s没有返回译文", claudeCompat.Label)
	}
	return strings.Join(parts, "\n\n"), nil
}
