package mcp

import (
	"context"
	"encoding/json"
	"errors"
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
	codeParseError    = -32700
	codeInvalidParams = -32602
	codeMethodNotFnd  = -32601
)

// Serve runs the MCP server over a JSON-RPC 2.0 stream (newline-delimited JSON
// on stdio). It reads requests from in, dispatches them against reg, and writes
// responses to out. Notifications (no id) get no response. It returns when in
// reaches EOF or ctx is cancelled.
//
// Only read-only tools are exposed (reg is built from query-only ports), so
// nothing an agent can send over this transport mutates user infrastructure.
func Serve(ctx context.Context, in io.Reader, out io.Writer, reg *Registry, info ServerInfo) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// A malformed message desyncs the stream; report and stop.
			_ = enc.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()},
			})
			return err
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
