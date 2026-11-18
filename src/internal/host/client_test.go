package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type callLine struct {
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	Service string         `json:"service"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

func TestNetworkCallWritesTheRequestAndReadsTheAnswer(t *testing.T) {
	answers := strings.Join([]string{
		`{"type":"hostCallResult","id":"t1","ok":true,"result":{"status":200,"body":"{\"ok\":true}"}}`,
	}, "\n") + "\n"
	output := &bytes.Buffer{}
	client := NewClient(strings.NewReader(answers), output)

	response, err := client.Network(context.Background(), NetworkRequest{
		URL:     "https://translate.googleapis.com/translate_a/single?q=x",
		Method:  "POST",
		Headers: map[string]string{"content-type": "application/x-www-form-urlencoded"},
		Body:    "q=hi",
	})
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	if response.Status != 200 || response.Body != `{"ok":true}` {
		t.Fatalf("response = %+v", response)
	}
	var call callLine
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &call); err != nil {
		t.Fatalf("output line: %v (%q)", err, output.String())
	}
	if call.Type != "hostCall" || call.Service != "network.outbound" || call.ID != "t1" {
		t.Fatalf("call = %+v", call)
	}
	if call.Params["url"] != "https://translate.googleapis.com/translate_a/single?q=x" {
		t.Fatalf("url = %v", call.Params["url"])
	}
	if call.Params["method"] != "POST" {
		t.Fatalf("method = %v", call.Params["method"])
	}
}

func TestStorageReadAndMissingKey(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"target":"en"}`))
	answers := fmt.Sprintf(
		`{"type":"hostCallResult","id":"t1","ok":true,"result":{"key":"settings.json","data":"%s","size":16}}`+"\n"+
			`{"type":"hostCallResult","id":"t2","ok":false,"error":"存储项不存在"}`+"\n",
		encoded,
	)
	client := NewClient(strings.NewReader(answers), &bytes.Buffer{})

	data, err := client.StorageRead(context.Background(), "settings.json")
	if err != nil {
		t.Fatalf("StorageRead: %v", err)
	}
	if string(data) != `{"target":"en"}` {
		t.Fatalf("data = %q", data)
	}
	if _, err := client.StorageRead(context.Background(), "settings.json"); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("missing key error = %v", err)
	}
}

func TestStorageWriteEncodesBase64(t *testing.T) {
	answers := `{"type":"hostCallResult","id":"t1","ok":true,"result":{"key":"settings.json","size":5}}` + "\n"
	output := &bytes.Buffer{}
	client := NewClient(strings.NewReader(answers), output)

	if err := client.StorageWrite(context.Background(), "settings.json", []byte("hello")); err != nil {
		t.Fatalf("StorageWrite: %v", err)
	}
	var call callLine
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &call); err != nil {
		t.Fatalf("output line: %v", err)
	}
	if call.Method != "write" || call.Params["key"] != "settings.json" {
		t.Fatalf("call = %+v", call)
	}
	if call.Params["data"] != "aGVsbG8=" {
		t.Fatalf("data = %v", call.Params["data"])
	}
}

func TestCallLimitIsActionable(t *testing.T) {
	line := `{"type":"hostCallResult","id":"t1","ok":true,"result":{}}` + "\n"
	client := NewClient(strings.NewReader(strings.Repeat(line, maxCalls)), &bytes.Buffer{})
	ctx := context.Background()
	for index := 0; index < maxCalls; index += 1 {
		if _, err := client.call(ctx, "network.outbound", "", nil); err != nil {
			t.Fatalf("call %d: %v", index, err)
		}
	}
	if _, err := client.call(ctx, "network.outbound", "", nil); err == nil ||
		!strings.Contains(err.Error(), "最多请求宿主") {
		t.Fatalf("limit error = %v", err)
	}
}
