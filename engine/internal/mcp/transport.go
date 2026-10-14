package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// protocolVersion is the MCP protocol revision this server implements and
// advertises in the initialize response.
const protocolVersion = "2024-11-05"

// ServerInfo identifies this server to clients in the initialize handshake.
type ServerInfo struct {
	Name    string
	Version string
}

// JSON-RPC 2.0 error codes used here.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeInvalidParams  = -32602
	codeMethodNotFnd   = -32601
)

// maxFrame caps one message. A request is a method, a query and a few
// arguments; a megabyte is far beyond any real one, and the cap keeps a
// client from growing the server's memory with one endless line.
const maxFrame = 1 << 20

// Serve runs the MCP server over a JSON-RPC 2.0 stream: one JSON message per
// line, on stdio. It reads requests from in, dispatches them against reg, and
// writes responses to out. Notifications (no id) get no response. It returns
// when in reaches EOF, when a line is longer than maxFrame, or when ctx is
// cancelled, even while waiting for the next line.
//
// Requests are handled one at a time, so notifications/cancelled has nothing
// to cancel by the time it is read; it is accepted and ignored. A tool call
// is bounded by its backend client's timeout instead.
//
// Only read-only tools are exposed (reg is built from query-only ports), so
// nothing an agent can send over this transport mutates user infrastructure.
func Serve(ctx context.Context, in io.Reader, out io.Writer, reg *Registry, info ServerInfo) error {
	frames, readErr := readFrames(ctx, in)
	enc := json.NewEncoder(out)
	for {
		var line []byte
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = enc.Encode(errorResponse(json.RawMessage("null"), codeParseError, "unreadable message: "+err.Error()))
			return err
		case line = <-frames:
		}

		req, rerr := parseRequest(line)
		if rerr != nil {
			// The line is lost, but the next one starts clean: with one
			// message per line, a bad message cannot desynchronize the stream.
			if err := enc.Encode(errorResponse(req.ID, rerr.Code, rerr.Message)); err != nil {
				return err
			}
			continue
		}

		result, rerr := dispatch(ctx, reg, info, req.Method, req.Params)

		// Notifications have no id and never get a response, even on error.
		if isNotification(req.ID) {
			continue
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		if err := enc.Encode(&resp); err != nil {
			return err
		}
	}
}

// readFrames reads in line by line on its own goroutine, so Serve can stop
// on ctx while a read is blocked. Blank lines are skipped. The goroutine ends
// at EOF, at a read error (sent on the error channel), or when ctx is done;
// one blocked in a read of stdin ends with the process.
func readFrames(ctx context.Context, in io.Reader) (<-chan []byte, <-chan error) {
	frames := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64<<10), maxFrame)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			select {
			case frames <- bytes.Clone(line): // the scanner reuses its buffer
			case <-ctx.Done():
				return
			}
		}
		if err := sc.Err(); err != nil {
			readErr <- err // bufio.ErrTooLong past maxFrame
			return
		}
		readErr <- io.EOF
	}()
	return frames, readErr
}

// parseRequest decodes one line as a JSON-RPC 2.0 request: a single JSON
// object, nothing after it, with "jsonrpc": "2.0". The returned request
// carries the id when it could be read, so the error can be answered.
func parseRequest(line []byte) (rpcRequest, *rpcError) {
	var req rpcRequest
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&req); err != nil {
		return rpcRequest{ID: json.RawMessage("null")}, &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}
	}
	if dec.More() {
		return req, &rpcError{Code: codeParseError, Message: "parse error: more than one JSON value on the line"}
	}
	if req.JSONRPC != "2.0" {
		return req, &rpcError{Code: codeInvalidRequest, Message: fmt.Sprintf(`invalid request: jsonrpc is %q, want "2.0"`, req.JSONRPC)}
	}
	return req, nil
}

func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func isNotification(id json.RawMessage) bool {
	return len(id) == 0 || string(id) == "null"
}

func dispatch(ctx context.Context, reg *Registry, info ServerInfo, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": info.Name, "version": info.Version},
		}, nil

	case "notifications/initialized", "notifications/cancelled":
		return nil, nil // notifications; ignored

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		return map[string]any{"tools": descriptors(reg)}, nil

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
		}
		res, err := reg.Call(ctx, p.Name, p.Arguments)
		if err != nil {
			// Tool execution errors are reported in the result (isError), not as a
			// protocol error — the agent should see and reason about them.
			return toolCallResult{
				Content: []toolContent{{Type: "text", Text: err.Error()}},
				IsError: true,
			}, nil
		}
		return toolCallResult{Content: []toolContent{{Type: "text", Text: string(res)}}}, nil

	default:
		return nil, &rpcError{Code: codeMethodNotFnd, Message: "method not found: " + method}
	}
}

// descriptors builds the tools/list payload: schema plus the read-only hint.
func descriptors(reg *Registry) []toolDescriptor {
	tools := reg.List()
	out := make([]toolDescriptor, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Annotations: toolAnnotations{ReadOnlyHint: t.ReadOnly},
		})
	}
	return out
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations toolAnnotations `json:"annotations"`
}

type toolAnnotations struct {
	ReadOnlyHint bool `json:"readOnlyHint"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
