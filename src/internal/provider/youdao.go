package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
)

const (
	youdaoEndpoint = "https://dict.youdao.com/jsonapi_s"

	youdaoName = "有道词典"

	maxDictionaryEntries = 4
	maxDictionarySenses  = 4
)

// youdaoLookup reads the dictionary entry Youdao's own site draws for a word.
// This endpoint needs no cookie, signature or AES key — unlike Youdao's web
// translator — so it is only used to fill in phonetics and senses, never to
// translate a paragraph.
func youdaoLookup(ctx context.Context, caller Caller, word string) (*Word, error) {
	if !isEnglishHeadword(word) {
		return nil, errors.New("有道词典只收录英文单词")
	}
	query := url.Values{}
	query.Set("doctype", "json")
	query.Set("jsonversion", "4")
	query.Set("q", word)
	response, err := caller.Network(ctx, host.NetworkRequest{
		URL:     youdaoEndpoint + "?" + query.Encode(),
		Method:  "GET",
		Headers: map[string]string{"accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	if response.Status != 200 {
		return nil, errors.New("有道词典没有应答")
	}
	var answer youdaoAnswer
	if err := json.Unmarshal([]byte(response.Body), &answer); err != nil {
		return nil, errors.New("有道词典的应答无法解析")
	}
	card := youdaoCard(answer)
	if card == nil {
		return nil, errors.New("有道词典没有收录这个单词")
	}
	card.Term = word
	card.Dictionary = youdaoName
	return card, nil
}

type youdaoAnswer struct {
	Simple struct {
		Word []youdaoWord `json:"word"`
	} `json:"simple"`
	// EC is an object for most words and an array for words several entries
	// share, so it is decoded in youdaoWords rather than typed here.
	EC json.RawMessage `json:"ec"`
}

type youdaoWord struct {
	USPhone string `json:"usphone"`
	UKPhone string `json:"ukphone"`
	Trs     []struct {
		Pos  string `json:"pos"`
		Tran string `json:"tran"`
	} `json:"trs"`
}

// youdaoCard takes the first entry that carries a pronunciation or a sense.
func youdaoCard(answer youdaoAnswer) *Word {
	for _, word := range append(youdaoWords(answer.EC), answer.Simple.Word...) {
		card := youdaoCardFrom(word)
		if card != nil {
			return card
		}
	}
	return nil
}

func youdaoWords(raw json.RawMessage) []youdaoWord {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var single struct {
		Word youdaoWord `json:"word"`
	}
	if err := json.Unmarshal(raw, &single); err == nil {
		return []youdaoWord{single.Word}
	}
	var many []struct {
		Word youdaoWord `json:"word"`
	}
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil
	}
	words := make([]youdaoWord, 0, len(many))
	for _, item := range many {
		words = append(words, item.Word)
	}
	return words
}

func youdaoCardFrom(word youdaoWord) *Word {
	card := &Word{}
	uk := strings.TrimSpace(word.UKPhone)
	us := strings.TrimSpace(word.USPhone)
	if uk != "" {
		card.Phonetics = append(card.Phonetics, Phonetic{Label: "英", Value: uk})
	}
	if us != "" && !strings.EqualFold(us, uk) {
		card.Phonetics = append(card.Phonetics, Phonetic{Label: "美", Value: us})
	}
	for _, tr := range word.Trs {
		if len(card.Entries) >= maxDictionaryEntries {
			break
		}
		part := strings.TrimSpace(tr.Pos)
		terms := youdaoSenses(tr.Tran)
		if part == "" || len(terms) == 0 {
			continue
		}
		card.Entries = append(card.Entries, Entry{Part: part, Terms: terms})
	}
	if len(card.Phonetics) == 0 && len(card.Entries) == 0 {
		return nil
	}
	return card
}

// youdaoSenses splits "使高兴；使满意" into its senses, keeping the order the
// dictionary ranks them in.
func youdaoSenses(tran string) []string {
	var senses []string
	for _, sense := range strings.FieldsFunc(tran, func(r rune) bool {
		return r == '；' || r == ';'
	}) {
		sense = strings.TrimSpace(sense)
		if sense == "" {
			continue
		}
		senses = append(senses, sense)
		if len(senses) >= maxDictionarySenses {
			break
		}
	}
	return senses
}

// isEnglishHeadword reports whether the query is made of Latin letters only,
// which is the only thing this dictionary answers for.
func isEnglishHeadword(word string) bool {
	letters := 0
	for _, r := range word {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letters++
		case r == ' ', r == '-', r == '\'', r == '.':
		default:
			return false
		}
	}
	return letters > 0
}
