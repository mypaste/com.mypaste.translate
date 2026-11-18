package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

const (
	bilibiliEndpoint      = "https://index-translate.bilibili.com/api/doc/translate_stream"
	bilibiliMaxChunkRunes = 900
	bilibiliMinTokens     = 512
	bilibiliMaxTokens     = 1536
)

// bilibiliTranslate sends one paragraph to Index-Translate. The service has
// no automatic direction, so the runtime names both ends of the pair from the
// text itself.
func bilibiliTranslate(ctx context.Context, caller Caller, text, target, model string) (Result, error) {
	source := detectSource(text)
	resolved := target
	if resolved == AutoTarget {
		if source == "zh" {
			resolved = autoTargetOther
		} else {
			resolved = autoTargetChinese
		}
	}
	sourceLang, targetLang, err := bilibiliPair(source, resolved)
	if err != nil {
		return Result{}, err
	}
	chunks := splitChunks(text, bilibiliMaxChunkRunes)
	parts := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		part, err := bilibiliAsk(ctx, caller, chunk, sourceLang, targetLang, model)
		if err != nil {
			if len(chunks) > 1 {
				return Result{}, fmt.Errorf("第 %d 段翻译失败：%w", index+1, err)
			}
			return Result{}, err
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return Result{}, errors.New("翻译服务没有返回译文")
	}
	return Result{
		Translation: joinParts(resolved, parts),
		Source:      source,
		Target:      resolved,
	}, nil
}

// bilibiliLookup translates a single word. Index-Translate has no dictionary,
// so the card carries the term and its translation and nothing else.
func bilibiliLookup(ctx context.Context, caller Caller, word, target, model string) (Result, error) {
	result, err := bilibiliTranslate(ctx, caller, word, target, model)
	if err != nil {
		return Result{}, err
	}
	card := &Word{Term: word, Translation: result.Translation}
	return Result{
		Translation: result.Translation,
		Source:      result.Source,
		Target:      result.Target,
		Word:        card,
	}, nil
}

// bilibiliPair maps a detected source and a page target onto the two codes
// the service declares. Only zh/en and zh/ja exist, so an English-to-Japanese
// request is refused here rather than by the service.
func bilibiliPair(source, target string) (string, string, error) {
	switch {
	case strings.HasPrefix(target, "zh"):
		switch source {
		case "en":
			return "en", "zh", nil
		case "ja":
			return "ja", "zh", nil
		default:
			return "", "", errors.New("原文已经是中文，请把目标语言改为英文或日文")
		}
	case target == "en":
		if source != "zh" {
			return "", "", errors.New("Index-Translate 只支持中文译成英文，请换一个翻译服务")
		}
		return "zh", "en", nil
	case target == "ja":
		if source != "zh" {
			return "", "", errors.New("Index-Translate 只支持中文译成日文，请换一个翻译服务")
		}
		return "zh", "ja", nil
	default:
		return "", "", errors.New("Index-Translate 不支持这个目标语言")
	}
}

// bilibiliAsk posts one chunk and joins the server-sent events it streams
// back. max_tokens bounds a model that starts repeating itself; a response
// that still ends at the limit is reported instead of half drawn.
func bilibiliAsk(ctx context.Context, caller Caller, text, source, target, model string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"text":        text,
		"source_lang": source,
		"target_lang": target,
		"model":       model,
		"max_tokens":  bilibiliTokens(text),
	})
	if err != nil {
		return "", errors.New("无法生成翻译请求")
	}
	response, err := caller.Network(ctx, host.NetworkRequest{
		URL:    bilibiliEndpoint,
		Method: "POST",
		Headers: map[string]string{
			"content-type": "application/json",
			"accept":       "text/event-stream",
		},
		Body: string(payload),
	})
	if err != nil {
		return "", fmt.Errorf("翻译服务不可用：%w", err)
	}
	if response.Status != 200 {
		return "", bilibiliFailure(response.Status, response.Body)
	}
	translation, err := readBilibiliStream(response.Body)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(translation) == "" {
		return "", errors.New("翻译服务没有返回译文")
	}
	return strings.TrimSpace(translation), nil
}

func bilibiliTokens(text string) int {
	tokens := len([]rune(text)) + 128
	if tokens < bilibiliMinTokens {
		return bilibiliMinTokens
	}
	if tokens > bilibiliMaxTokens {
		return bilibiliMaxTokens
	}
	return tokens
}

// readBilibiliStream reads the service's OpenAI-shaped SSE frames: each
// `data:` line carries one delta, and the stream ends with `[DONE]`.
func readBilibiliStream(body string) (string, error) {
	var joined strings.Builder
	truncated := false
	seen := false
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			// A keep-alive or an unknown frame is not an answer; the stream
			// carries its real signal in the frames that do parse.
			continue
		}
		seen = true
		if event.Error != nil {
			message := strings.TrimSpace(event.Error.Message)
			if message == "" {
				message = "翻译服务返回了错误"
			}
			return "", fmt.Errorf("翻译服务：%s", message)
		}
		if len(event.Choices) == 0 {
			continue
		}
		choice := event.Choices[0]
		joined.WriteString(choice.Delta.Content)
		if choice.FinishReason == "length" {
			truncated = true
		}
	}
	if err := scanner.Err(); err != nil {
		return "", errors.New("无法读取翻译结果")
	}
	if !seen {
		return "", errors.New("无法解析翻译结果")
	}
	if truncated {
		return "", errors.New("译文过长被服务截断，请重试或换用其他翻译服务")
	}
	return joined.String(), nil
}

// bilibiliFailure turns a refused request into the service's own message
// when it sent one.
func bilibiliFailure(status int, body string) error {
	switch {
	case status == 429:
		return errors.New("翻译服务请求过于频繁，请稍后再试")
	case status >= 500:
		return errors.New("翻译服务暂时不可用，请稍后再试")
	}
	var payload struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		message := ""
		if payload.Error != nil {
			message = strings.TrimSpace(payload.Error.Message)
		}
		if message == "" {
			message = strings.TrimSpace(payload.Detail)
		}
		if message != "" {
			return fmt.Errorf("翻译服务：%s", message)
		}
	}
	return fmt.Errorf("翻译服务拒绝了这次请求（HTTP %d）", status)
}
