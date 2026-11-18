package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

const (
	mymemoryEndpoint = "https://api.mymemory.translated.net/get"

	// mymemoryMaxChunkBytes stays below the free endpoint's 500-byte query
	// ceiling. A paragraph is split on sentence or word boundaries first.
	mymemoryMaxChunkBytes = 480
)

// mymemoryTranslate translates text through MyMemory's free API, which has no
// automatic direction: the source language is detected here, and the
// automatic target follows from it.
func mymemoryTranslate(ctx context.Context, caller Caller, text, target string) (Result, error) {
	source := detectSource(text)
	resolved := target
	if resolved == AutoTarget {
		if source == "zh" {
			resolved = autoTargetOther
		} else {
			resolved = autoTargetChinese
		}
	}
	translation, err := mymemoryChunks(ctx, caller, text, mymemoryLanguage(source), resolved)
	if err != nil {
		return Result{}, err
	}
	return Result{Translation: translation, Source: source, Target: resolved}, nil
}

// mymemoryLookup translates a single word. MyMemory has no dictionary; the
// card's phonetics and senses come from the dictionary lookup that follows.
func mymemoryLookup(ctx context.Context, caller Caller, word, target string) (Result, error) {
	result, err := mymemoryTranslate(ctx, caller, word, target)
	if err != nil {
		return Result{}, err
	}
	result.Word = &Word{Term: word, Translation: result.Translation}
	return result, nil
}

// mymemoryLanguage maps the detected source onto the code MyMemory expects.
// Anything the detector does not recognise is sent as English, which is what
// its own auto-detect would answer for Latin script.
func mymemoryLanguage(source string) string {
	switch source {
	case "zh":
		return "zh-CN"
	case "ja":
		return "ja"
	default:
		return "en"
	}
}

func mymemoryChunks(ctx context.Context, caller Caller, text, source, target string) (string, error) {
	chunks := splitChunksBytes(text, mymemoryMaxChunkBytes)
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		translated, err := mymemoryAsk(ctx, caller, chunk, source, target)
		if err != nil {
			return "", err
		}
		parts = append(parts, translated)
	}
	return joinParts(target, parts), nil
}

type mymemoryAnswer struct {
	ResponseData struct {
		TranslatedText string `json:"translatedText"`
	} `json:"responseData"`
	ResponseStatus  int    `json:"responseStatus"`
	ResponseDetails string `json:"responseDetails"`
	QuotaFinished   bool   `json:"quotaFinished"`
}

func mymemoryAsk(ctx context.Context, caller Caller, text, source, target string) (string, error) {
	query := url.Values{}
	query.Set("q", text)
	query.Set("langpair", source+"|"+target)
	response, err := caller.Network(ctx, host.NetworkRequest{
		URL:     mymemoryEndpoint + "?" + query.Encode(),
		Method:  "GET",
		Headers: map[string]string{"accept": "application/json"},
	})
	if err != nil {
		return "", err
	}
	if response.Status != 200 {
		return "", fmt.Errorf("MyMemory 返回了 HTTP %d", response.Status)
	}
	var answer mymemoryAnswer
	if err := json.Unmarshal([]byte(response.Body), &answer); err != nil {
		return "", errors.New("MyMemory 的应答无法解析")
	}
	if answer.QuotaFinished {
		return "", errors.New("MyMemory 今日的免费额度已用完，请换一个翻译服务")
	}
	if answer.ResponseStatus != 200 {
		details := strings.TrimSpace(answer.ResponseDetails)
		if details == "" {
			details = fmt.Sprintf("HTTP %d", answer.ResponseStatus)
		}
		return "", fmt.Errorf("MyMemory 无法翻译这段原文：%s", details)
	}
	translated := strings.TrimSpace(html.UnescapeString(answer.ResponseData.TranslatedText))
	if translated == "" ||
		strings.HasPrefix(strings.ToUpper(translated), "MYMEMORY WARNING") {
		return "", errors.New("MyMemory 没有返回译文，请换一个翻译服务")
	}
	return translated, nil
}
