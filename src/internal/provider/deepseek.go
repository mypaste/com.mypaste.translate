package provider

import "context"

const (
	deepseekEndpoint = "https://api.deepseek.com/chat/completions"
	deepseekLabel    = "DeepSeek"
)

// deepseekTranslate sends one paragraph through DeepSeek's chat completion
// endpoint. The host proxy adds the API key as an Authorization header
// (D-1176): this runtime never sees the key, holds none, and cannot set that
// header itself, so a missing key is refused by the host before any request
// leaves.
func deepseekTranslate(
	ctx context.Context, caller Caller, text, target, model string,
) (Result, error) {
	source, resolved := direction(text, target)
	translation, err := chatCompletionAsk(ctx, caller, deepseekLabel, deepseekEndpoint,
		text, model, paragraphPrompt(languageName(resolved)), 4096)
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
	source, resolved := direction(word, target)
	translation, err := chatCompletionAsk(ctx, caller, deepseekLabel, deepseekEndpoint,
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
