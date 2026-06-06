package main

import "encoding/json"

// JSON-RPC message types for MCP protocol.

type jsonRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   json.RawMessage  `json:"error,omitempty"`
}

type toolCallParams struct {
	Name          string          `json:"name"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	ProgressToken json.RawMessage `json:"_meta,omitempty"`
}

type metaWithProgress struct {
	ProgressToken json.RawMessage `json:"progressToken,omitempty"`
}

type progressNotification struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  progressParams `json:"params"`
}

type progressParams struct {
	ProgressToken json.RawMessage `json:"progressToken"`
	Progress      int             `json:"progress"`
	Total         int             `json:"total,omitempty"`
	Message       string          `json:"message,omitempty"`
}

// makeProgressNotification creates a JSON-RPC progress notification.
func makeProgressNotification(token json.RawMessage, progress, total int, message string) ([]byte, error) {
	notif := progressNotification{
		JSONRPC: "2.0",
		Method:  "notifications/progress",
		Params: progressParams{
			ProgressToken: token,
			Progress:      progress,
			Total:         total,
			Message:       message,
		},
	}
	return json.Marshal(notif)
}
