// Package host is this package's side of the plugin v2 line protocol
// (PLUGINS.md §4): one request line in, one answer line out, and the host's
// closed services in between. Only the two services the translation
// surface needs are implemented here: the host proxy for HTTPS requests and
// the package's private storage. The runtime never opens a socket or a file
// on its own.
package host

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// maxCalls mirrors the host's per-invocation ceiling. Checking it here turns
// a loop in this runtime into an actionable error instead of a host refusal
// halfway through a page of paragraphs.
const maxCalls = 16

// Client is one invocation's conversation with the host. Calls are serial:
// the protocol never has two host calls in flight.
type Client struct {
	input  *bufio.Reader
	output *bufio.Writer
	calls  int
}

// NewClient wraps the invocation's streams.
func NewClient(input io.Reader, output io.Writer) *Client {
	return &Client{
		input:  bufio.NewReaderSize(input, 1<<20),
		output: bufio.NewWriterSize(output, 1<<20),
	}
}

// ReadRequestLine reads the invocation's one request line. It must use this
// reader: the buffered bytes belong to the same host conversation.
func (c *Client) ReadRequestLine() ([]byte, error) {
	line, err := c.input.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, errors.New("没有收到宿主请求")
	}
	return line, nil
}

// WriteLine writes one line and flushes it.
func (c *Client) WriteLine(line []byte) error {
	if _, err := c.output.Write(append(line, '\n')); err != nil {
		return errors.New("无法写入宿主响应")
	}
	return c.output.Flush()
}

type hostCall struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Service string `json:"service"`
	Method  string `json:"method,omitempty"`
	Params  any    `json:"params,omitempty"`
}

type hostCallResult struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// call performs one host call and returns its raw result.
func (c *Client) call(ctx context.Context, service, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.New("调用已超时")
	}
	if c.calls >= maxCalls {
		return nil, fmt.Errorf("一次调用最多请求宿主 %d 次", maxCalls)
	}
	c.calls++
	line, err := json.Marshal(hostCall{
		Type:    "hostCall",
		ID:      "t" + strconv.Itoa(c.calls),
		Service: service,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, errors.New("无法编码宿主调用")
	}
	if err := c.WriteLine(line); err != nil {
		return nil, err
	}
	answer, err := c.input.ReadBytes('\n')
	if err != nil && len(answer) == 0 {
		return nil, errors.New("宿主没有回答调用")
	}
	var result hostCallResult
	if err := json.Unmarshal(answer, &result); err != nil {
		return nil, errors.New("无法解析宿主回答")
	}
	if result.Type != "hostCallResult" {
		return nil, errors.New("宿主回答了意外的消息")
	}
	if !result.OK {
		if result.Error == "" {
			result.Error = "宿主拒绝了这次调用"
		}
		return nil, errors.New(result.Error)
	}
	return result.Result, nil
}

// NetworkRequest is one host-proxied HTTPS request. Only the host's closed
// header whitelist is used; the provider sets content-type and nothing else.
type NetworkRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// NetworkResponse is the bounded answer the host proxy hands back.
type NetworkResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// Network performs one HTTPS request to a host the manifest declared.
func (c *Client) Network(ctx context.Context, request NetworkRequest) (NetworkResponse, error) {
	raw, err := c.call(ctx, "network.outbound", "", request)
	if err != nil {
		return NetworkResponse{}, err
	}
	var response NetworkResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return NetworkResponse{}, errors.New("无法解析网络回答")
	}
	return response, nil
}

// ErrMissingKey means the key has never been written, so the caller can fall
// back to defaults instead of failing the page.
var ErrMissingKey = errors.New("存储项不存在")

// StorageRead reads one key from the package's private directory.
func (c *Client) StorageRead(ctx context.Context, key string) ([]byte, error) {
	raw, err := c.call(ctx, "storage.plugin", "read", map[string]string{"key": key})
	if err != nil {
		if err.Error() == ErrMissingKey.Error() {
			return nil, ErrMissingKey
		}
		return nil, err
	}
	var file struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, errors.New("无法解析存储内容")
	}
	data, err := base64.StdEncoding.DecodeString(file.Data)
	if err != nil {
		return nil, errors.New("存储内容不是有效的 base64")
	}
	return data, nil
}

// StorageWrite replaces one key in the package's private directory.
func (c *Client) StorageWrite(ctx context.Context, key string, data []byte) error {
	_, err := c.call(ctx, "storage.plugin", "write", map[string]string{
		"key":  key,
		"data": base64.StdEncoding.EncodeToString(data),
	})
	return err
}
