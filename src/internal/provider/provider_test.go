package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

// fakeCaller answers like the host proxy, without a process or a socket.
type fakeCaller struct {
	responses []host.NetworkResponse
	errs      []error
	requests  []host.NetworkRequest
}

func (f *fakeCaller) Network(_ context.Context, request host.NetworkRequest) (host.NetworkResponse, error) {
	f.requests = append(f.requests, request)
	index := len(f.requests) - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return host.NetworkResponse{}, f.errs[index]
	}
	if index >= len(f.responses) {
		return host.NetworkResponse{}, errors.New("没有预设的回答")
	}
	return f.responses[index], nil
}

func jsonResponse(body string) host.NetworkResponse {
	return host.NetworkResponse{Status: 200, Body: body}
}

func bodyText(t *testing.T, request host.NetworkRequest) string {
	t.Helper()
	values, err := url.ParseQuery(request.Body)
	if err != nil {
		t.Fatalf("request body is not form encoded: %v", err)
	}
	return values.Get("q")
}

func googleRequest(text, target string) Request {
	return Request{Text: text, Provider: "google", Target: target}
}

func bilibiliRequest(text, target, model string) Request {
	return Request{Text: text, Provider: "bilibili", Target: target, Model: model}
}

func TestTranslateUsesChineseForNonChineseSource(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(`{"sentences":[{"trans":"你好，世界。","orig":"Hello, world."}],"src":"en"}`),
	}}
	result, err := Translate(context.Background(), caller, googleRequest("Hello, world.", AutoTarget))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Translation != "你好，世界。" {
		t.Fatalf("translation = %q", result.Translation)
	}
	if result.Source != "en" || result.Target != "zh-CN" {
		t.Fatalf("source/target = %q/%q", result.Source, result.Target)
	}
	if len(caller.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(caller.requests))
	}
	if !strings.Contains(caller.requests[0].URL, "tl=zh-CN") {
		t.Fatalf("url = %s", caller.requests[0].URL)
	}
	if caller.requests[0].Headers["content-type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("headers = %v", caller.requests[0].Headers)
	}
	if bodyText(t, caller.requests[0]) != "Hello, world." {
		t.Fatalf("body q = %q", bodyText(t, caller.requests[0]))
	}
}

func TestTranslateSwitchesToEnglishForChineseSource(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(`{"sentences":[{"trans":"你好，世界。","orig":"你好，世界。"}],"src":"zh-CN"}`),
		jsonResponse(`{"sentences":[{"trans":"Hello, world.","orig":"你好，世界。"}],"src":"zh-CN"}`),
	}}
	result, err := Translate(context.Background(), caller, googleRequest("你好，世界。", AutoTarget))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Translation != "Hello, world." || result.Target != "en" {
		t.Fatalf("got %q into %q", result.Translation, result.Target)
	}
	if len(caller.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(caller.requests))
	}
	if !strings.Contains(caller.requests[1].URL, "tl=en") {
		t.Fatalf("second url = %s", caller.requests[1].URL)
	}
}

func TestTranslateChunksLongText(t *testing.T) {
	// Echo each chunk back so the join is observable; the text is long enough
	// to force several chunks.
	var text strings.Builder
	for index := 0; index < 8; index++ {
		text.WriteString(strings.Repeat("word ", 120))
		text.WriteString("end. ")
	}
	source := strings.TrimSpace(text.String())
	echo := &echoCaller{}
	result, err := Translate(context.Background(), echo, googleRequest(source, "en"))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if echo.calls < 2 {
		t.Fatalf("chunks = %d, want at least 2", echo.calls)
	}
	if !strings.Contains(result.Translation, "end.") {
		t.Fatalf("joined translation lost content: %q", result.Translation)
	}
	for _, chunk := range echo.texts {
		if len([]rune(chunk)) > googleMaxChunkRunes {
			t.Fatalf("chunk is %d runes, over the limit", len([]rune(chunk)))
		}
	}
}

type echoCaller struct {
	calls int
	texts []string
}

func (e *echoCaller) Network(_ context.Context, request host.NetworkRequest) (host.NetworkResponse, error) {
	e.calls++
	values, _ := url.ParseQuery(request.Body)
	text := values.Get("q")
	e.texts = append(e.texts, text)
	// Quote the text into the response so the parser sees a real answer.
	quoted := strings.ReplaceAll(text, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	return jsonResponse(`{"sentences":[{"trans":"` + quoted + `"}],"src":"en"}`), nil
}

func TestLookupParsesDictionary(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(`{
			"sentences":[
				{"trans":"跑步","orig":"run"},
				{"translit":"Pǎobù","src_translit":"rən"}
			],
			"dict":[{
				"pos":"verb",
				"terms":["运行","执行","跑"],
				"entry":[
					{"word":"运行","reverse_translation":["run","move","be in motion","extra"]},
					{"word":"执行","reverse_translation":["execute"]},
					{"word":"跑","reverse_translation":["run"]},
					{"word":"经营","reverse_translation":["operate"]},
					{"word":"管理","reverse_translation":["manage"]}
				]
			},{
				"pos":"noun",
				"terms":["跑步"],
				"entry":[{"word":"跑步","reverse_translation":["run"]}]
			}],
			"src":"en"
		}`),
	}}
	result, err := Lookup(context.Background(), caller, googleRequest("run", "zh-CN"))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.Word == nil {
		t.Fatal("word card missing")
	}
	if result.Word.Phonetic != "rən" || result.Word.Translit != "Pǎobù" {
		t.Fatalf("phonetic/translit = %q/%q", result.Word.Phonetic, result.Word.Translit)
	}
	if len(result.Word.Entries) != 2 {
		t.Fatalf("entries = %d", len(result.Word.Entries))
	}
	first := result.Word.Entries[0]
	if first.Part != "verb" || len(first.Terms) != 3 {
		t.Fatalf("first entry = %+v", first)
	}
	if len(first.Senses) != 3 {
		t.Fatalf("senses = %d, want 3", len(first.Senses))
	}
	if len(first.Senses[0].Reverse) != 3 {
		t.Fatalf("reverse = %v, want the first three", first.Senses[0].Reverse)
	}
}

func TestLookupWithoutDictionaryKeepsTranslation(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(`{"sentences":[{"trans":"这","orig":"the"}],"src":"en"}`),
	}}
	result, err := Lookup(context.Background(), caller, googleRequest("the", "zh-CN"))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.Word == nil || result.Word.Translation != "这" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Word.Entries) != 0 {
		t.Fatalf("entries = %d", len(result.Word.Entries))
	}
}

func TestAskReportsServiceStates(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{429, "过于频繁"},
		{503, "暂时不可用"},
		{400, "拒绝了这次请求"},
	}
	for _, test := range cases {
		caller := &fakeCaller{responses: []host.NetworkResponse{{Status: test.status}}}
		_, err := Translate(context.Background(), caller, googleRequest("hello", "zh-CN"))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("status %d: error = %v", test.status, err)
		}
	}
}

func TestNormalizeSettingsUsesClosedLists(t *testing.T) {
	clean, err := NormalizeSettings(Settings{})
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if clean.Provider != "bilibili" || clean.Target != AutoTarget || clean.Model != "index-nailong-9b" {
		t.Fatalf("defaults = %+v", clean)
	}
	clean, err = NormalizeSettings(Settings{Provider: "bilibili", Target: "ja"})
	if err != nil {
		t.Fatalf("bilibili ja: %v", err)
	}
	if clean.Model != "index-nailong-9b" {
		t.Fatalf("default model = %q", clean.Model)
	}
	clean, err = NormalizeSettings(Settings{Provider: "google", Target: "fr"})
	if err != nil {
		t.Fatalf("google fr: %v", err)
	}
	if clean.Model != "" {
		t.Fatalf("google model = %q, want none", clean.Model)
	}
	for _, stored := range []Settings{
		{Provider: "youdao"},
		{Provider: "google", Target: "klingon"},
		{Provider: "bilibili", Target: "fr"},
		{Provider: "bilibili", Model: "other"},
	} {
		if _, err := NormalizeSettings(stored); err == nil {
			t.Fatalf("%+v should be refused", stored)
		}
	}
}

func TestResultUsesLowercaseKeys(t *testing.T) {
	encoded, err := json.Marshal(Result{Translation: "你好", Source: "en", Target: "zh-CN"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The page reads `target`; a missing tag would ship `Target` and silently
	// drop the direction from the reading hint.
	for _, want := range []string{`"translation":"你好"`, `"source":"en"`, `"target":"zh-CN"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("result %s lacks %s", encoded, want)
		}
	}
}

func TestDefaultSettingsNamesTheDefaultModel(t *testing.T) {
	settings := DefaultSettings()
	if settings.Provider != "bilibili" || settings.Target != AutoTarget ||
		settings.Model != "index-nailong-9b" {
		t.Fatalf("defaults = %+v", settings)
	}
}

func TestDetectSource(t *testing.T) {
	cases := map[string]string{
		"Hello, world.": "en",
		"你好，世界。":        "zh",
		"こんにちは世界。":      "ja",
		"東京は雨です。":       "ja",
		"123":           "en",
	}
	for text, want := range cases {
		if got := detectSource(text); got != want {
			t.Fatalf("detectSource(%q) = %q, want %q", text, got, want)
		}
	}
}

func sse(frames ...string) string {
	var body strings.Builder
	for _, frame := range frames {
		body.WriteString("data: ")
		body.WriteString(frame)
		body.WriteString("\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

func chunkFrame(content, finish string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"delta":         map[string]string{"content": content},
			"finish_reason": finish,
		}},
	})
	return string(payload)
}

func TestBilibiliTranslateStreamsAndNamesBothEnds(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(sse(
			chunkFrame("你好", ""),
			chunkFrame("，世界。", "stop"),
		)),
	}}
	result, err := Translate(context.Background(), caller, bilibiliRequest("Hello, world.", AutoTarget, "index-nailong-2b"))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Translation != "你好，世界。" || result.Source != "en" || result.Target != "zh-CN" {
		t.Fatalf("result = %+v", result)
	}
	if caller.requests[0].URL != bilibiliEndpoint {
		t.Fatalf("url = %s", caller.requests[0].URL)
	}
	var payload struct {
		Text       string `json:"text"`
		SourceLang string `json:"source_lang"`
		TargetLang string `json:"target_lang"`
		Model      string `json:"model"`
		MaxTokens  int    `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(caller.requests[0].Body), &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	if payload.SourceLang != "en" || payload.TargetLang != "zh" {
		t.Fatalf("pair = %q -> %q", payload.SourceLang, payload.TargetLang)
	}
	if payload.Model != "index-nailong-2b" || payload.MaxTokens < bilibiliMinTokens {
		t.Fatalf("model/tokens = %q/%d", payload.Model, payload.MaxTokens)
	}
}

func TestBilibiliAutoDirectionFromChinese(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(sse(chunkFrame("Hello, world.", "stop"))),
	}}
	result, err := Translate(context.Background(), caller, bilibiliRequest("你好，世界。", AutoTarget, ""))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.Target != "en" || result.Translation != "Hello, world." {
		t.Fatalf("result = %+v", result)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(caller.requests[0].Body), &payload)
	if payload["source_lang"] != "zh" || payload["target_lang"] != "en" {
		t.Fatalf("pair = %v -> %v", payload["source_lang"], payload["target_lang"])
	}
}

func TestBilibiliRefusesPairsItDoesNotServe(t *testing.T) {
	caller := &fakeCaller{}
	cases := []struct{ text, target, want string }{
		{"Hello.", "en", "中文译成英文"},
		{"Hello.", "ja", "中文译成日文"},
		{"你好。", "zh-CN", "已经是中文"},
	}
	for _, test := range cases {
		_, err := Translate(context.Background(), caller, bilibiliRequest(test.text, test.target, ""))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%q -> %q: error = %v", test.text, test.target, err)
		}
	}
	if len(caller.requests) != 0 {
		t.Fatalf("no request should be sent, got %d", len(caller.requests))
	}
}

func TestBilibiliReportsTruncatedStream(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(sse(chunkFrame("循环", "length"))),
	}}
	_, err := Translate(context.Background(), caller, bilibiliRequest("loop", "zh-CN", ""))
	if err == nil || !strings.Contains(err.Error(), "截断") {
		t.Fatalf("error = %v", err)
	}
}

func TestBilibiliLookupReturnsPlainWord(t *testing.T) {
	caller := &fakeCaller{responses: []host.NetworkResponse{
		jsonResponse(sse(chunkFrame("跑步", "stop"))),
	}}
	result, err := Lookup(context.Background(), caller, bilibiliRequest("run", "zh-CN", ""))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.Word == nil || result.Word.Term != "run" || result.Word.Translation != "跑步" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Word.Entries) != 0 || result.Word.Phonetic != "" {
		t.Fatalf("bilibili has no dictionary: %+v", result.Word)
	}
}

func TestSplitChunksKeepsEveryRune(t *testing.T) {
	text := strings.Repeat("alpha beta gamma. ", 200)
	chunks := splitChunks(text, 300)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk == "" {
			t.Fatal("empty chunk")
		}
		if len([]rune(chunk)) > 300 {
			t.Fatalf("chunk over limit: %d", len([]rune(chunk)))
		}
	}
}
