// Command mypaste-translate is the translation surface's own runtime.
//
// Protocol v3 lets a UI package declare a runtime and placement-less internal
// commands (D-1084); only this package's page can reach them and they never
// appear in the command palette. The page hands over one paragraph or one
// word as JSON and gets one JSON answer back. Every network request goes
// through the host's network.outbound proxy to the single declared host, and
// the reading preference lives in the package's own private storage.
//
// The runtime speaks the plugin v2 line protocol (PLUGINS.md §4): one request
// line on stdin, one response line on stdout, host calls in between.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mypaste/com.mypaste.translate/src/internal/host"
	"github.com/mypaste/com.mypaste.translate/src/internal/provider"
)

const (
	maxRequestBytes = 1 << 20
	settingsKey     = "settings.json"
	defaultTimeout  = 9 * time.Second
	errorCode       = "translate-failed"
)

// invocation is the plugin v2 request line, reduced to the fields these
// commands read. Unknown fields are ignored by design.
type invocation struct {
	ID             string `json:"id"`
	Method         string `json:"method"`
	CommandID      string `json:"commandId"`
	DeadlineUnixMs int64  `json:"deadlineUnixMs"`
	Input          *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"input"`
}

// response is the one-line reply. Exactly one of result / error is set.
type response struct {
	ID     string       `json:"id"`
	OK     bool         `json:"ok"`
	Result *resultShape `json:"result,omitempty"`
	Error  *errorShape  `json:"error,omitempty"`
}

type resultShape struct {
	Output string `json:"output"`
}

type errorShape struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// paragraphRequest is the page's payload for translate.paragraph.
type paragraphRequest struct {
	Text     string `json:"text"`
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Model    string `json:"model"`
}

// wordRequest is the page's payload for translate.word.
type wordRequest struct {
	Word     string `json:"word"`
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Model    string `json:"model"`
}

// settingsRequest is the page's payload for translate.settings.
type settingsRequest struct {
	Op       string             `json:"op"`
	Settings *provider.Settings `json:"settings,omitempty"`
}

// settingsView is what the page receives: the stored choice plus the closed
// list of services, directions and models it may draw.
type settingsView struct {
	Settings  provider.Settings       `json:"settings"`
	Providers []provider.ProviderInfo `json:"providers"`
}

func main() {
	// One client owns the conversation: the request line and every host
	// answer share the same buffered reader, so no byte is read twice.
	client := host.NewClient(os.Stdin, os.Stdout)
	request, err := client.ReadRequestLine()
	if err != nil {
		respond(failure("", err.Error()))
		return
	}
	if len(request) > maxRequestBytes {
		respond(failure("", "请求过大"))
		return
	}
	var call invocation
	if err := json.Unmarshal(request, &call); err != nil {
		respond(failure("", "无法解析调用请求"))
		return
	}
	if call.ID == "" {
		// The host matches responses by ID; an unnamed request cannot be
		// answered usefully.
		respond(failure("", "调用请求缺少请求 ID"))
		return
	}
	if call.Method != "" && call.Method != "invoke" {
		respond(failure(call.ID, "不支持的调用方式"))
		return
	}

	ctx, cancel := invocationContext(call.DeadlineUnixMs)
	defer cancel()

	text := ""
	if call.Input != nil {
		text = call.Input.Text
	}
	var output any
	switch call.CommandID {
	case "translate.paragraph":
		output, err = translateParagraph(ctx, client, text)
	case "translate.word":
		output, err = lookupWord(ctx, client, text)
	case "translate.settings":
		output, err = readOrWriteSettings(ctx, client, text)
	default:
		err = errors.New("不支持的翻译操作")
	}
	if err != nil {
		respond(failure(call.ID, err.Error()))
		return
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		respond(failure(call.ID, "无法生成翻译结果"))
		return
	}
	respond(response{
		ID:     call.ID,
		OK:     true,
		Result: &resultShape{Output: string(encoded)},
	})
}

func translateParagraph(ctx context.Context, client *host.Client, text string) (any, error) {
	var request paragraphRequest
	if err := json.Unmarshal([]byte(text), &request); err != nil {
		return nil, errors.New("翻译参数无效")
	}
	return provider.Translate(ctx, client, provider.Request{
		Text:     request.Text,
		Provider: request.Provider,
		Target:   request.Target,
		Model:    request.Model,
	})
}

func lookupWord(ctx context.Context, client *host.Client, text string) (any, error) {
	var request wordRequest
	if err := json.Unmarshal([]byte(text), &request); err != nil {
		return nil, errors.New("查询参数无效")
	}
	return provider.Lookup(ctx, client, provider.Request{
		Text:     request.Word,
		Provider: request.Provider,
		Target:   request.Target,
		Model:    request.Model,
	})
}

func readOrWriteSettings(ctx context.Context, client *host.Client, text string) (any, error) {
	var request settingsRequest
	if err := json.Unmarshal([]byte(text), &request); err != nil {
		return nil, errors.New("偏好参数无效")
	}
	switch request.Op {
	case "read":
		current, err := loadSettings(ctx, client)
		if err != nil {
			return nil, err
		}
		return view(current), nil
	case "write":
		if request.Settings == nil {
			return nil, errors.New("偏好内容缺失")
		}
		clean, err := provider.NormalizeSettings(*request.Settings)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(clean)
		if err != nil {
			return nil, errors.New("无法保存偏好")
		}
		if err := client.StorageWrite(ctx, settingsKey, encoded); err != nil {
			return nil, fmt.Errorf("无法保存偏好：%w", err)
		}
		return view(clean), nil
	default:
		return nil, errors.New("不支持的偏好操作")
	}
}

func view(current provider.Settings) settingsView {
	return settingsView{Settings: current, Providers: provider.Providers()}
}

func loadSettings(ctx context.Context, client *host.Client) (provider.Settings, error) {
	current := provider.DefaultSettings()
	data, err := client.StorageRead(ctx, settingsKey)
	if err != nil {
		if errors.Is(err, host.ErrMissingKey) {
			return current, nil
		}
		return current, fmt.Errorf("无法读取偏好：%w", err)
	}
	var stored provider.Settings
	if err := json.Unmarshal(data, &stored); err != nil {
		return current, nil
	}
	clean, err := provider.NormalizeSettings(stored)
	if err != nil {
		return current, nil
	}
	return clean, nil
}

// invocationContext bounds one request by the host's deadline, leaving room
// to write the answer before the host gives up on the process.
func invocationContext(deadlineUnixMs int64) (context.Context, context.CancelFunc) {
	timeout := defaultTimeout
	if deadlineUnixMs > 0 {
		remaining := time.Until(time.UnixMilli(deadlineUnixMs))
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		if usable := remaining - 250*time.Millisecond; usable < timeout {
			if usable < 500*time.Millisecond {
				usable = 500 * time.Millisecond
			}
			timeout = usable
		}
	}
	return context.WithTimeout(context.Background(), timeout)
}

func failure(id string, message string) response {
	return response{ID: id, OK: false, Error: &errorShape{Code: errorCode, Message: message}}
}

func respond(value response) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = os.Stdout.Write(append(encoded, '\n'))
}
