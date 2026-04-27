package vfsremote

import "encoding/json"

const (
	methodBegin    = "begin"
	methodCommit   = "commit"
	methodRollback = "rollback"
	methodCreate   = "create"
	methodResolve  = "resolve"
	methodRead     = "read"
	methodListDir  = "list_dir"
	methodWrite    = "write"
	methodDelete   = "delete"
	methodRename   = "rename"
	methodGlob     = "glob"
)

type request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
