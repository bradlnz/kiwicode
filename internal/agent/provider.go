package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct{ Endpoint, Model, APIKey string }

type HTTPProvider struct {
	config Config
	client *http.Client
}

// NewHTTPProvider uses the explicitly configured Chat Completions-compatible
// endpoint. There is intentionally no vendor, model or remote endpoint default.
func NewHTTPProvider(cfg Config) (*HTTPProvider, error) {
	if cfg.Endpoint == "" || cfg.Model == "" {
		return nil, errors.New("set KIWICODE_AGENT_ENDPOINT and KIWICODE_AGENT_MODEL; no provider is configured")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("agent endpoint must be a URL without embedded credentials, query or fragment")
	}
	local := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		local = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("agent endpoint requires HTTPS (HTTP is allowed only on loopback)")
	}
	if len(cfg.Endpoint) > 8192 || len(cfg.Model) > 256 || len(cfg.APIKey) > 8192 {
		return nil, errors.New("agent configuration exceeds limits")
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns: 4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2, IdleConnTimeout: 90 * time.Second,
	}
	return &HTTPProvider{cfg, &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("agent endpoint redirects are disabled") }}}, nil
}
func (p *HTTPProvider) Close() { p.client.CloseIdleConnections() }

func tool(name, description string, properties map[string]any, required ...string) any {
	return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}}
}

var tools = []any{
	tool("set_plan", "Publish a short actionable plan; update it as the task progresses.", map[string]any{"steps": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 32}}, "steps"),
	tool("list_files", "List permitted workspace filenames. Contents require read_file approval.", map[string]any{"prefix": map[string]any{"type": "string"}}, "prefix"),
	tool("read_file", "Ask permission to read and send one workspace file. Reads are pinned to a run snapshot; subsequent reads include staged changes. Read a missing path before proposing a new file.", map[string]any{"path": map[string]any{"type": "string"}}, "path"),
	tool("propose_edit", "Stage an exact, unique text replacement in a previously read file. Does not modify the working tree. For an empty/new file use empty old_text. Deletes are represented as empty contents, not file removal.", map[string]any{"path": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"}}, "path", "old_text", "new_text"),
	tool("run_check", "Ask permission to execute argv without implicit shell expansion on a temporary project snapshot containing staged changes. Requires explicit host-command opt-in. This executes project code, is not a security sandbox, and output is sent to the configured provider.", map[string]any{"command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 64}}, "command"),
}

func (p *HTTPProvider) Respond(ctx context.Context, messages []Message, onText func(string) error) (Message, error) {
	body, err := json.Marshal(map[string]any{"model": p.config.Model, "messages": messages, "tools": tools, "stream": true, "parallel_tool_calls": false, "max_completion_tokens": 4096})
	if err != nil {
		return Message{}, err
	}
	if len(body) > MaxContextBytes {
		return Message{}, errors.New("context limit reached; start a new task with less context")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Message{}, ctx.Err()
		}
		return Message{}, errors.New("model connection failed; check endpoint, TLS and network")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Message{}, fmt.Errorf("model endpoint returned HTTP %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return Message{}, errors.New("model endpoint must support streamed Chat Completions (text/event-stream)")
	}
	return readStream(resp.Body, onText)
}

type streamCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}
type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string       `json:"content"`
			ToolCalls []streamCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

func readStream(reader io.Reader, onText func(string) error) (Message, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, MaxResponseBytes+1))
	scanner.Buffer(make([]byte, 4096), MaxResponseBytes+1)
	var text strings.Builder
	calls := make(map[int]Call)
	total := 0
	finished := false
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > MaxResponseBytes {
			return Message{}, errors.New("model response exceeds limit")
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		if data == "" {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Message{}, errors.New("invalid model stream frame")
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return Message{}, errors.New("model stream returned an error")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if finished && (choice.Delta.Content != "" || len(choice.Delta.ToolCalls) > 0) {
				return Message{}, errors.New("model sent content after finishing")
			}
			if text.Len()+len(choice.Delta.Content) > 64<<10 {
				return Message{}, errors.New("assistant text exceeds limit")
			}
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				if err := onText(choice.Delta.Content); err != nil {
					return Message{}, err
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index < 0 || delta.Index >= 8 {
					return Message{}, errors.New("too many tool calls in one turn")
				}
				call := calls[delta.Index]
				call.ID += delta.ID
				call.Type += delta.Type
				call.Function.Name += delta.Function.Name
				call.Function.Arguments += delta.Function.Arguments
				if len(call.ID) > 256 || len(call.Type) > 32 || len(call.Function.Name) > 64 || len(call.Function.Arguments) > MaxFileBytes*2 {
					return Message{}, errors.New("tool call exceeds limit")
				}
				calls[delta.Index] = call
			}
			if choice.FinishReason != nil {
				if *choice.FinishReason != "stop" && *choice.FinishReason != "tool_calls" {
					return Message{}, fmt.Errorf("model stopped before completing: %s", *choice.FinishReason)
				}
				finished = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, err
	}
	if !done || !finished {
		return Message{}, io.ErrUnexpectedEOF
	}
	result := Message{Role: "assistant", Content: text.String()}
	ids := map[string]bool{}
	for i := 0; i < len(calls); i++ {
		call, ok := calls[i]
		if !ok || call.ID == "" || call.Type != "function" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) || ids[call.ID] {
			return Message{}, errors.New("incomplete or duplicate tool call")
		}
		ids[call.ID] = true
		result.ToolCalls = append(result.ToolCalls, call)
	}
	return result, nil
}
