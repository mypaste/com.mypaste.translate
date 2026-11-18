package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

const deepseekEndpoint = "https://api.deepseek.com/chat/completions"

// deepseekTranslate sends one paragraph through DeepSeek's chat completion
// endpoint. The host proxy adds the API key as an Authorization header
// (D-1176): this runtime never sees the key, holds none, and cannot set that
// header itself, so a missing key is refused by the host before any request
// leaves.
func deepseekTranslate(
	ctx context.Context, caller Caller, text, target, model string,
) (Result, error) {
	source := detectSource(text)
	resolved := target
	if resolved == AutoTarget {
		if source == "zh" {
			resolved = autoTargetOther
		} else {
			resolved = autoTargetChinese
		}
	}
	translation, err := deepseekAsk(ctx, caller, text, model,
		deepseekParagraphPrompt(deepseekLanguage(resolved)), 4096)
	if err != nil {
		return Result{}, err
	}
	return Result{Translation: translation, Source: source, Target: resolved}, nil
}

// deepseekLookup translates one word or phrase. DeepSeek has no dictionary, so
// the card starts as the term and its translation; withDictionary fills in the
// phonetics and senses afterwards, exactly as for the other plain services.
func deepseekLookup(
	ctx context.Context, caller Caller, word, target, model string,
) (Result, error) {
	source := detectSource(word)
	resolved := target
	if resolved == AutoTarget {
		if source == "zh" {
			resolved = autoTargetOther
		} else {
			resolved = autoTargetChinese
		}
	}
	translation, err := deepseekAsk(ctx, caller, word, model,
		deepseekWordPrompt(deepseekLanguage(resolved)), 256)
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

// deepseekParagraphPrompt is the whole instruction. It names the language,
// keeps the answer to the translation itself, and asks for the paragraph
// rhythm the reader preserves.
func deepseekParagraphPrompt(language string) string {
	return "你是翻译引擎。把用户消息里的文本翻译成" + language + "。" +
		"只输出译文本身：不要解释、不要加引号、不要复述原文、不要添加任何前缀或后缀。" +
		"保留原文的段落结构与换行；原文里的 Markdown 标记原样保留。"
}

// deepseekWordPrompt asks for one line of senses, which is all the card's
// translation field needs before the dictionary fills in the rest.
func deepseekWordPrompt(language string) string {
	return "你是词典。把用户给出的单词或短语翻译成" + language + "。" +
		"只输出最常用的一到三个释义，用「；」分隔，不要解释、不要例句、不要音标。"
}

// deepseekLanguage names a direction for the prompt. The closed target list is
// the page's, so an unknown code is passed through rather than guessed.
func deepseekLanguage(code string) string {
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

// deepseekAsk performs one completion and returns its single answer. The
// request carries no credential: the host adds it for this declared host.
func deepseekAsk(
	ctx context.Context, caller Caller, text, model, prompt string, maxTokens int,
) (string, error) {
	body, err := json.Marshal(map[string]any{
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
		return "", errors.New("无法生成翻译请求")
	}
	response, err := caller.Network(ctx, host.NetworkRequest{
		URL:     deepseekEndpoint,
		Method:  "POST",
		Headers: map[string]string{"content-type": "application/json", "accept": "application/json"},
		Body:    string(body),
	})
	if err != nil {
		return "", fmt.Errorf("DeepSeek 请求失败：%w", err)
	}
	if response.Status != 200 {
		return "", deepseekFailure(response.Status, response.Body)
	}
	return deepseekAnswer(response.Body)
}

// deepseekAnswer reads the one message the endpoint returns.
func deepseekAnswer(body string) (string, error) {
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
		return "", errors.New("无法解析 DeepSeek 的译文")
	}
	if payload.Error != nil {
		message := strings.TrimSpace(payload.Error.Message)
		if message == "" {
			message = "翻译服务返回了错误"
		}
		return "", fmt.Errorf("DeepSeek：%s", message)
	}
	if len(payload.Choices) == 0 {
		return "", errors.New("DeepSeek 没有返回译文")
	}
	choice := payload.Choices[0]
	if choice.FinishReason == "length" {
		return "", errors.New("译文过长被服务截断，请缩短段落或换用其他翻译服务")
	}
	translation := strings.TrimSpace(choice.Message.Content)
	if translation == "" {
		return "", errors.New("DeepSeek 没有返回译文")
	}
	return translation, nil
}

// deepseekFailure turns a refused request into an actionable sentence. The key
// itself is never echoed; the message only tells the reader where to fix it.
func deepseekFailure(status int, body string) error {
	switch status {
	case 401, 403:
		return errors.New("DeepSeek 密钥无效或已过期：请在 设置 › 扩展 › 翻译对照 中重新填写")
	case 402:
		return errors.New("DeepSeek 账户余额不足，请充值后重试")
	case 429:
		return errors.New("DeepSeek 请求过于频繁，请稍后再试")
	}
	if status >= 500 {
		return errors.New("DeepSeek 暂时不可用，请稍后再试")
	}
	var payload struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err == nil &&
		payload.Error != nil {
		if message := strings.TrimSpace(payload.Error.Message); message != "" {
			return fmt.Errorf("DeepSeek：%s", message)
		}
	}
	return fmt.Errorf("DeepSeek 拒绝了这次请求（HTTP %d）", status)
}
