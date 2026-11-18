package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

const (
	googleEndpoint = "https://translate.googleapis.com/translate_a/single"

	// googleMaxChunkRunes keeps one request well below the endpoint's own
	// length ceiling. A paragraph is split on sentence or word boundaries
	// first.
	googleMaxChunkRunes = 1200
)

func googleTranslate(ctx context.Context, caller Caller, text, target string) (Result, error) {
	resolved := target
	if resolved == AutoTarget {
		resolved = autoTargetChinese
	}
	translation, source, err := googleChunks(ctx, caller, text, resolved)
	if err != nil {
		return Result{}, err
	}
	if target == AutoTarget && strings.HasPrefix(strings.ToLower(source), "zh") {
		resolved = autoTargetOther
		if translation, source, err = googleChunks(ctx, caller, text, resolved); err != nil {
			return Result{}, err
		}
	}
	return Result{Translation: translation, Source: source, Target: resolved}, nil
}

func googleLookup(ctx context.Context, caller Caller, word, target string) (Result, error) {
	requested := target
	if requested == AutoTarget {
		requested = autoTargetChinese
	}
	answer, err := googleAsk(ctx, caller, word, requested)
	if err != nil {
		return Result{}, err
	}
	if target == AutoTarget && strings.HasPrefix(strings.ToLower(answer.source), "zh") {
		requested = autoTargetOther
		if answer, err = googleAsk(ctx, caller, word, requested); err != nil {
			return Result{}, err
		}
	}
	card := &Word{Term: word, Translation: strings.TrimSpace(answer.translation)}
	if len(answer.translit) > 1 {
		card.Translit = answer.translit
	}
	if len(answer.phonetic) > 1 {
		card.Phonetics = []Phonetic{{Value: answer.phonetic}}
	}
	for _, group := range answer.dict {
		if len(card.Entries) >= 4 {
			break
		}
		part := strings.TrimSpace(group.Pos)
		if part == "" {
			continue
		}
		entry := Entry{Part: part}
		for _, term := range group.Terms {
			term = strings.TrimSpace(term)
			if term == "" {
				continue
			}
			entry.Terms = append(entry.Terms, term)
			if len(entry.Terms) >= 6 {
				break
			}
		}
		for _, sense := range group.Entry {
			meaning := strings.TrimSpace(sense.Word)
			if meaning == "" {
				continue
			}
			item := Sense{Word: meaning}
			for _, reverse := range sense.Reverse {
				reverse = strings.TrimSpace(reverse)
				if reverse == "" {
					continue
				}
				item.Reverse = append(item.Reverse, reverse)
				if len(item.Reverse) >= 3 {
					break
				}
			}
			entry.Senses = append(entry.Senses, item)
			if len(entry.Senses) >= 3 {
				break
			}
		}
		card.Entries = append(card.Entries, entry)
	}
	if card.Translation == "" && len(card.Entries) == 0 {
		return Result{}, errors.New("没有查到这个词的释义")
	}
	return Result{
		Translation: card.Translation,
		Source:      answer.source,
		Target:      requested,
		Word:        card,
	}, nil
}

func googleChunks(ctx context.Context, caller Caller, text, target string) (string, string, error) {
	chunks := splitChunks(text, googleMaxChunkRunes)
	parts := make([]string, 0, len(chunks))
	source := ""
	for index, chunk := range chunks {
		answer, err := googleAsk(ctx, caller, chunk, target)
		if err != nil {
			if len(chunks) > 1 {
				return "", "", fmt.Errorf("第 %d 段翻译失败：%w", index+1, err)
			}
			return "", "", err
		}
		if source == "" {
			source = answer.source
		}
		part := strings.TrimSpace(answer.translation)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "", "", errors.New("翻译服务没有返回译文")
	}
	return joinParts(target, parts), source, nil
}

type googleAnswer struct {
	translation string
	source      string
	phonetic    string
	translit    string
	dict        []struct {
		Pos   string   `json:"pos"`
		Terms []string `json:"terms"`
		Entry []struct {
			Word    string   `json:"word"`
			Reverse []string `json:"reverse_translation"`
		} `json:"entry"`
	}
}

// googleAsk performs one request and reads the endpoint's dj=1 JSON answer.
func googleAsk(ctx context.Context, caller Caller, text, target string) (googleAnswer, error) {
	query := url.Values{
		"client": {"gtx"},
		"sl":     {"auto"},
		"tl":     {target},
		"dt":     {"t", "bd", "rm"},
		"dj":     {"1"},
	}
	response, err := caller.Network(ctx, host.NetworkRequest{
		URL:    googleEndpoint + "?" + query.Encode(),
		Method: "POST",
		Headers: map[string]string{
			"content-type": "application/x-www-form-urlencoded",
		},
		Body: url.Values{"q": {text}}.Encode(),
	})
	if err != nil {
		return googleAnswer{}, fmt.Errorf("翻译服务不可用：%w", err)
	}
	switch {
	case response.Status == 200:
	case response.Status == 429:
		return googleAnswer{}, errors.New("翻译服务请求过于频繁，请稍后再试")
	case response.Status >= 500:
		return googleAnswer{}, errors.New("翻译服务暂时不可用，请稍后再试")
	default:
		return googleAnswer{}, fmt.Errorf("翻译服务拒绝了这次请求（HTTP %d）", response.Status)
	}
	var body struct {
		Sentences []struct {
			Trans       string `json:"trans"`
			Translit    string `json:"translit"`
			SrcTranslit string `json:"src_translit"`
		} `json:"sentences"`
		Dict []struct {
			Pos   string   `json:"pos"`
			Terms []string `json:"terms"`
			Entry []struct {
				Word    string   `json:"word"`
				Reverse []string `json:"reverse_translation"`
			} `json:"entry"`
		} `json:"dict"`
		Src string `json:"src"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		return googleAnswer{}, errors.New("无法解析翻译结果")
	}
	result := googleAnswer{source: strings.TrimSpace(body.Src)}
	var joined strings.Builder
	for _, sentence := range body.Sentences {
		if sentence.Trans != "" {
			joined.WriteString(sentence.Trans)
		}
		if result.translit == "" && len([]rune(sentence.Translit)) > 1 {
			result.translit = sentence.Translit
		}
		if result.phonetic == "" && len([]rune(sentence.SrcTranslit)) > 1 {
			result.phonetic = sentence.SrcTranslit
		}
	}
	result.translation = joined.String()
	result.dict = body.Dict
	return result, nil
}
