// Package provider turns the page's paragraphs and words into requests to one
// of the translation services the manifest declares, and turns each answer
// into the small JSON the page draws.
//
// Three services are offered and none needs an account or a key: Google's
// public web endpoint and MyMemory's free API, both of which cover a wide set
// of languages, and Bilibili's Index-Translate service, which translates
// between Chinese, English and Japanese. Two more carry a key the host keeps:
// DeepSeek's own endpoint, and any OpenAI- or Anthropic-compatible service
// whose address and model the reader names (D-1185). A word card is filled in
// from Youdao's free dictionary endpoint whichever service translated it. The
// host proxy performs every request, so the runtime never sees a socket or a
// certificate; only the hosts declared in mypaste.plugin.json, plus the ones
// the reader filled in, can be reached, and the text to translate is the only
// thing sent.
package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

const (
	// AutoTarget asks the runtime to choose the direction: into Chinese for a
	// source that is not Chinese, into English for a source that is.
	AutoTarget = "auto"

	autoTargetChinese = "zh-CN"
	autoTargetOther   = "en"

	maxWordRunes = 64
)

// ModelInfo is one model a provider offers, in the order the page lists them.
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProviderInfo is one translation service as the page draws it. The runtime
// owns this list; the page only looks up the words it is given, so a new
// service or language is added here and nowhere else.
type ProviderInfo struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Targets      []string    `json:"targets"`
	Models       []ModelInfo `json:"models,omitempty"`
	DefaultModel string      `json:"defaultModel,omitempty"`
	// Note is a sentence the page shows in place of its usual hint, for a
	// service the reader still has to set up. It is empty once nothing is
	// missing; the runtime decides that from the invocation's settings.
	Note string `json:"note,omitempty"`
}

// allTargets is every direction Google and MyMemory accept;
// bilibiliTargets is the subset Index-Translate supports.
var (
	allTargets = []string{
		AutoTarget, "zh-CN", "zh-TW", "en", "ja", "ko",
		"fr", "de", "es", "pt", "ru", "it", "ar", "nl", "th", "vi",
	}
	bilibiliTargets = []string{AutoTarget, "zh-CN", "en", "ja"}
	bilibiliModels  = []ModelInfo{
		{ID: "index-nailong-9b", Name: "Index-Nailong-9B"},
		{ID: "index-nailong-2b", Name: "Index-Nailong-2B"},
	}
	providerList = []ProviderInfo{
		{
			ID:           "bilibili",
			Name:         "Bilibili Index-Translate",
			Description:  "Bilibili 的 Index 翻译服务，中英日互译，无需密钥。",
			Targets:      bilibiliTargets,
			Models:       bilibiliModels,
			DefaultModel: bilibiliModels[0].ID,
		},
		{
			ID:          "google",
			Name:        "Google 翻译",
			Description: "免费的网页接口，支持的语言最多。",
			Targets:     allTargets,
		},
		{
			ID:          "mymemory",
			Name:        "MyMemory",
			Description: "MyMemory 的免费接口，无需密钥，每天有一定的免费额度。",
			Targets:     allTargets,
		},
		{
			// D-1176: the key lives in the host, not here. Declaring the model
			// list is all this runtime knows about the account.
			ID:          "deepseek",
			Name:        "DeepSeek",
			Description: "DeepSeek 的对话接口，译文更自然；需要先在宿主的扩展设置里保存 API 密钥。",
			Targets:     allTargets,
			Models: []ModelInfo{
				{ID: "deepseek-chat", Name: "DeepSeek Chat"},
				{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner"},
			},
			DefaultModel: "deepseek-chat",
		},
		{
			// D-1185: the address, the model and the key are the reader's, not
			// this list's. The page draws whichever compatible service the reader
			// configured; this entry only names the family.
			ID:          "openai",
			Name:        openAICompat.Label + "服务",
			Description: "任何兼容 OpenAI Chat Completions 的服务：地址、模型与密钥在 设置 › 扩展 › 翻译对照 里填写。",
			Targets:     allTargets,
		},
		{
			ID:          "claude",
			Name:        claudeCompat.Label + "服务",
			Description: "任何兼容 Anthropic Messages 的服务：地址、模型与密钥在 设置 › 扩展 › 翻译对照 里填写。",
			Targets:     allTargets,
		},
	}
)

// Providers returns the closed list of services the page may offer. The
// invocation's settings only decide whether a compatible service is ready to
// use; the list itself, and every language it accepts, is fixed here.
func Providers(settings map[string]any) []ProviderInfo {
	providers := make([]ProviderInfo, len(providerList))
	for index, info := range providerList {
		providers[index] = ProviderInfo{
			ID:           info.ID,
			Name:         info.Name,
			Description:  info.Description,
			Targets:      append([]string(nil), info.Targets...),
			Models:       append([]ModelInfo(nil), info.Models...),
			DefaultModel: info.DefaultModel,
		}
		if service, ok := compatServiceFor(info.ID); ok {
			if _, err := service.origin(settings); err != nil {
				providers[index].Note = "地址与模型在 设置 › 扩展 › 翻译对照 中填写后可用"
			}
		}
	}
	return providers
}

// Settings is the service, direction and model the reader chose. It holds no
// part of any entry that was translated.
type Settings struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Model    string `json:"model,omitempty"`
}

// DefaultSettings is the first visit: the default service into whatever the
// source is not, with that service's default model.
func DefaultSettings() Settings {
	return Settings{
		Provider: providerList[0].ID,
		Target:   AutoTarget,
		Model:    providerList[0].DefaultModel,
	}
}

// NormalizeSettings trims the stored values and rejects a service, direction
// or model outside the closed lists above.
func NormalizeSettings(stored Settings) (Settings, error) {
	id := strings.TrimSpace(stored.Provider)
	if id == "" {
		// A settings file written before services were selectable carried a
		// direction only; it keeps that direction under the default service.
		id = providerList[0].ID
	}
	info, ok := findProvider(id)
	if !ok {
		return Settings{}, errors.New("翻译服务无效")
	}
	settings := Settings{Provider: info.ID, Target: strings.TrimSpace(stored.Target)}
	if settings.Target == "" {
		settings.Target = AutoTarget
	}
	if !contains(info.Targets, settings.Target) {
		return Settings{}, fmt.Errorf("%s 不支持这个目标语言", info.Name)
	}
	if len(info.Models) > 0 {
		settings.Model = strings.TrimSpace(stored.Model)
		if settings.Model == "" {
			settings.Model = info.DefaultModel
		}
		if !hasModel(info, settings.Model) {
			return Settings{}, fmt.Errorf("%s 没有这个模型", info.Name)
		}
	}
	return settings, nil
}

// Request is one paragraph or word the page asked about. Settings carries the
// declared non-secret settings the host attached to this invocation; only a
// compatible service reads them, for the address and model the reader filled
// in. A secret is never part of this object (D-1177).
type Request struct {
	Text     string
	Provider string
	Target   string
	Model    string
	Settings map[string]any
}

// Result is one translated paragraph or one dictionary card.
type Result struct {
	Translation string `json:"translation"`
	Source      string `json:"source,omitempty"`
	Target      string `json:"target,omitempty"`
	Word        *Word  `json:"word,omitempty"`
}

// Word is the dictionary card for one word or phrase.
type Word struct {
	Term        string     `json:"term"`
	Phonetics   []Phonetic `json:"phonetics,omitempty"`
	Translit    string     `json:"translit,omitempty"`
	Translation string     `json:"translation,omitempty"`
	Entries     []Entry    `json:"entries,omitempty"`
	Dictionary  string     `json:"dictionary,omitempty"`
}

// Phonetic is one pronunciation written out, labelled by the accent it
// belongs to when the dictionary says which one it is.
type Phonetic struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
}

// Entry groups senses by part of speech, in the order the service ranks them.
type Entry struct {
	Part   string   `json:"part"`
	Terms  []string `json:"terms,omitempty"`
	Senses []Sense  `json:"senses,omitempty"`
}

// Sense is one sense with a few reverse translations for disambiguation.
type Sense struct {
	Word    string   `json:"word"`
	Reverse []string `json:"reverse,omitempty"`
}

// Caller is the host proxy this runtime needs. It is an interface so a
// provider can be tested without a process or a network.
type Caller interface {
	Network(ctx context.Context, request host.NetworkRequest) (host.NetworkResponse, error)
}

// Translate returns the translation of request.Text into request.Target.
func Translate(ctx context.Context, caller Caller, request Request) (Result, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return Result{}, errors.New("没有可翻译的原文")
	}
	settings, err := NormalizeSettings(Settings{
		Provider: request.Provider,
		Target:   request.Target,
		Model:    request.Model,
	})
	if err != nil {
		return Result{}, err
	}
	switch settings.Provider {
	case "google":
		return googleTranslate(ctx, caller, text, settings.Target)
	case "bilibili":
		return bilibiliTranslate(ctx, caller, text, settings.Target, settings.Model)
	case "mymemory":
		return mymemoryTranslate(ctx, caller, text, settings.Target)
	case "deepseek":
		return deepseekTranslate(ctx, caller, text, settings.Target, settings.Model)
	case "openai":
		return openaiTranslate(ctx, caller, text, settings.Target, request.Settings)
	case "claude":
		return claudeTranslate(ctx, caller, text, settings.Target, request.Settings)
	default:
		return Result{}, errors.New("翻译服务无效")
	}
}

// Lookup returns the card for one word or phrase. A service without a
// dictionary still returns the word's plain translation.
func Lookup(ctx context.Context, caller Caller, request Request) (Result, error) {
	word := strings.TrimSpace(request.Text)
	if word == "" {
		return Result{}, errors.New("没有可查询的单词")
	}
	if len([]rune(word)) > maxWordRunes {
		return Result{}, errors.New("一次只能查询一个单词或短语")
	}
	settings, err := NormalizeSettings(Settings{
		Provider: request.Provider,
		Target:   request.Target,
		Model:    request.Model,
	})
	if err != nil {
		return Result{}, err
	}
	switch settings.Provider {
	case "google":
		// Google answers with its own dictionary, phonetics included.
		return googleLookup(ctx, caller, word, settings.Target)
	case "bilibili":
		result, err := bilibiliLookup(ctx, caller, word, settings.Target, settings.Model)
		return withDictionary(ctx, caller, result, err)
	case "mymemory":
		result, err := mymemoryLookup(ctx, caller, word, settings.Target)
		return withDictionary(ctx, caller, result, err)
	case "deepseek":
		result, err := deepseekLookup(ctx, caller, word, settings.Target, settings.Model)
		return withDictionary(ctx, caller, result, err)
	case "openai":
		result, err := openaiLookup(ctx, caller, word, settings.Target, request.Settings)
		return withDictionary(ctx, caller, result, err)
	case "claude":
		result, err := claudeLookup(ctx, caller, word, settings.Target, request.Settings)
		return withDictionary(ctx, caller, result, err)
	default:
		return Result{}, errors.New("翻译服务无效")
	}
}

// withDictionary fills a service's plain word card in from Youdao's free
// dictionary. The lookup is best effort: a card without phonetics or senses is
// still worth drawing, so a failure leaves the translation alone.
func withDictionary(ctx context.Context, caller Caller, result Result, err error) (Result, error) {
	if err != nil || result.Word == nil {
		return result, err
	}
	entry, lookupErr := youdaoLookup(ctx, caller, result.Word.Term)
	if lookupErr != nil || entry == nil {
		return result, nil
	}
	card := result.Word
	if len(card.Phonetics) == 0 {
		card.Phonetics = entry.Phonetics
	}
	if len(card.Entries) == 0 {
		card.Entries = entry.Entries
	}
	if card.Translation == "" {
		card.Translation = entry.Translation
	}
	if len(entry.Phonetics) > 0 || len(entry.Entries) > 0 {
		card.Dictionary = entry.Dictionary
	}
	return result, nil
}

func findProvider(id string) (ProviderInfo, bool) {
	for _, info := range providerList {
		if info.ID == id {
			return info, true
		}
	}
	return ProviderInfo{}, false
}

func hasModel(info ProviderInfo, id string) bool {
	for _, model := range info.Models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// detectSource names the source language the runtime has to declare to a
// service that has no automatic direction. Japanese wins over Chinese when
// kana are present; anything without Han or kana is treated as English.
func detectSource(text string) string {
	han, kana := false, false
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana = true
		case unicode.Is(unicode.Han, r):
			han = true
		}
	}
	switch {
	case kana:
		return "ja"
	case han:
		return "zh"
	default:
		return "en"
	}
}

// joinParts keeps Latin targets readable and CJK targets tight: each service
// already returns the punctuation every chunk needs.
func joinParts(target string, parts []string) string {
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "zh") || strings.HasPrefix(lower, "ja") ||
		strings.HasPrefix(lower, "ko") {
		return strings.Join(parts, "")
	}
	return strings.Join(parts, " ")
}

// splitChunks cuts text into at most limit runes each, preferring sentence
// ends and then whitespace, so a long paragraph keeps its own punctuation.
func splitChunks(text string, limit int) []string {
	return splitChunksBy(text, limit, func(chunk string) int { return len([]rune(chunk)) })
}

// splitChunksBytes is the same cut measured in bytes, for a service that
// limits its query that way.
func splitChunksBytes(text string, limit int) []string {
	return splitChunksBy(text, limit, func(chunk string) int { return len(chunk) })
}

// splitChunksBy cuts text into chunks weighing at most limit each, preferring
// sentence ends and then whitespace, so a long paragraph keeps its own
// punctuation. It never returns an empty chunk.
func splitChunksBy(text string, limit int, weight func(string) int) []string {
	runes := []rune(text)
	if weight(text) <= limit {
		return []string{strings.TrimSpace(text)}
	}
	var chunks []string
	for start := 0; start < len(runes); {
		if weight(string(runes[start:])) <= limit {
			chunk := strings.TrimSpace(string(runes[start:]))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}
		// end is one past the last rune whose chunk still fits.
		end := start + 1
		for end < len(runes) && weight(string(runes[start:end+1])) <= limit {
			end++
		}
		lowest := start + (end-start)/2
		cut := -1
		for index := end; index > lowest; index-- {
			if isSentenceEnd(runes[index-1]) &&
				(index == len(runes) || unicode.IsSpace(runes[index])) {
				cut = index
				break
			}
		}
		if cut < 0 {
			for index := end; index > lowest; index-- {
				if unicode.IsSpace(runes[index-1]) {
					cut = index
					break
				}
			}
		}
		if cut < 0 {
			cut = end
		}
		chunk := strings.TrimSpace(string(runes[start:cut]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = cut
	}
	return chunks
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '…', '.', '!', '?', ';':
		return true
	}
	return false
}
