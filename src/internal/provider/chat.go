package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

// The chat completion exchange is one shape, shared by DeepSeek and every
// OpenAI-compatible service the reader names: one POST carrying a model, a
// system instruction and one user message, answered by
// choices[0].message.content. This file owns that shape, the prompts and the
// failure wording; each service supplies only its endpoint and its label.
//
// No request built here carries a credential. The host adds the declared key
// for the destination this request goes to (D-1176, D-1185), so the runtime
// never holds one and cannot set that header itself.

// direction resolves the automatic target against the text's own script: into
// Chinese for anything that is not Chinese, into English for anything that is.
func direction(text, target string) (source, resolved string) {
	source = detectSource(text)
	resolved = target
	if resolved == AutoTarget {
		if source == "zh" {
			resolved = autoTargetOther
		} else {
			resolved = autoTargetChinese
		}
	}
	return source, resolved
}

// paragraphPrompt is the whole instruction for one paragraph. It names the
// language, keeps the answer to the translation itself, and asks for the
// paragraph rhythm the reader preserves.
func paragraphPrompt(language string) string {
	return "你是翻译引擎。把用户消息里的文本翻译成" + language + "。" +
		"只输出译文本身：不要解释、不要加引号、不要复述原文、不要添加任何前缀或后缀。" +
		"保留原文的段落结构与换行；原文里的 Markdown 标记原样保留。"
}

// wordPrompt asks for one line of senses, which is all the card's translation
// field needs before the dictionary fills in the rest.
func wordPrompt(language string) string {
	return "你是词典。把用户给出的单词或短语翻译成" + language + "。" +
		"只输出最常用的一到三个释义，用「；」分隔，不要解释、不要例句、不要音标。"
}

// languageName names a direction for the prompt. The closed target list is the
// page's, so an unknown code is passed through rather than guessed.
func languageName(code string) string {
	names := map[string]string{
		"zh-CN": "简体中文",
		"zh-TW": "繁體中文",
		"en":    "英语",
		"ja":    "日语",
		"ko":    "韩语",
		"fr":    "法语",
		"de":    "德语",
		"es":    "西班牙语",
		"pt":    "葡萄牙语",
		"ru":    "俄语",
		"it":    "意大利语",
		"ar":    "阿拉伯语",
		"nl":    "荷兰语",
		"th":    "泰语",
		"vi":    "越南语",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return code
}

// postJSON sends one JSON body through the host proxy and returns the raw
// answer. The host owns the destination check and the credential; this
// function only adds the two headers every JSON endpoint expects.
func postJSON(
	ctx context.Context, caller Caller, label, endpoint string,
	headers map[string]string, body any,
) (host.NetworkResponse, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return host.NetworkResponse{}, errors.New("无法生成翻译请求")
	}
	requestHeaders := make(map[string]string, len(headers)+2)
	for name, value := range headers {
		requestHeaders[name] = value
	}
	requestHeaders["content-type"] = "application/json"
	requestHeaders["accept"] = "application/json"
	response, err := caller.Network(ctx, host.NetworkRequest{
		URL:     endpoint,
		Method:  "POST",
		Headers: requestHeaders,
		Body:    string(encoded),
	})
	if err != nil {
		return host.NetworkResponse{}, fmt.Errorf("%s请求失败：%w", label, err)
	}
	return response, nil
}

// chatCompletionAsk performs one chat completion and returns its single
// answer. label names the service in every error and is the same word the
// page lists it under.
func chatCompletionAsk(
	ctx context.Context, caller Caller, label, endpoint, text, model, prompt string,
	maxTokens int,
) (string, error) {
	response, err := postJSON(ctx, caller, label, endpoint, nil, map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": prompt},
			{"role": "user", "content": text},
		},
		"temperature": 0,
		"stream":      false,
		"max_tokens":  maxTokens,
	})
	if err != nil {
		return "", err
	}
	if response.Status != 200 {
		return "", chatFailure(label, response.Status, response.Body)
	}
	return chatAnswer(label, response.Body)
}

// chatAnswer reads the one message the endpoint returns.
func chatAnswer(label, body string) (string, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", fmt.Errorf("无法解析%s的译文", label)
	}
	if payload.Error != nil {
		message := strings.TrimSpace(payload.Error.Message)
		if message == "" {
			message = "翻译服务返回了错误"
		}
		return "", fmt.Errorf("%s：%s", label, message)
	}
	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("%s没有返回译文", label)
	}
	choice := payload.Choices[0]
	if choice.FinishReason == "length" {
		return "", errors.New("译文过长被服务截断，请缩短段落或换用其他翻译服务")
	}
	translation := strings.TrimSpace(choice.Message.Content)
	if translation == "" {
		return "", fmt.Errorf("%s没有返回译文", label)
	}
	return translation, nil
}

// chatFailure turns a refused request into an actionable sentence. The key
// itself is never echoed; the message only tells the reader where to fix it.
func chatFailure(label string, status int, body string) error {
	switch status {
	case 401, 403:
		return fmt.Errorf("%s密钥无效或已过期：请在 设置 › 扩展 › 翻译对照 中重新填写", label)
	case 402:
		return fmt.Errorf("%s账户余额不足，请充值后重试", label)
	case 429:
		return fmt.Errorf("%s请求过于频繁，请稍后再试", label)
	}
	if status >= 500 {
		return fmt.Errorf("%s暂时不可用，请稍后再试", label)
	}
	var payload struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err == nil &&
		payload.Error != nil {
		if message := strings.TrimSpace(payload.Error.Message); message != "" {
			return fmt.Errorf("%s：%s", label, message)
		}
	}
	return fmt.Errorf("%s拒绝了这次请求（HTTP %d）", label, status)
}
