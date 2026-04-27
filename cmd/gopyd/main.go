package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"satis/appconfig"
	"satis/bridge"
	"satis/invoke"
	"satis/sandbox"
	"satis/vfs"
	"satis/vfsremote"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	fs := flag.NewFlagSet("gopyd", flag.ExitOnError)
	configPath := fs.String("config", "vfs.config.json", "path to VFS config JSON (optional \"invoke\" object for OpenAI-compatible API)")
	invokeConfigPath := fs.String("invoke-config", "", "optional path to invoke-only JSON; overrides embedded \"invoke\" in --config")
	invokeMode := fs.String("invoke-mode", "error", "invoke mode: error|echo|prompt-echo|openai")
	fs.Parse(os.Args[1:])

	cfg, embeddedInvoke, systemPortDir, softwareRegistryDir, err := appconfig.LoadVFSAndInvoke(*configPath)
	if err != nil {
		fatalf("failed to load config: %v", err)
	}
	if err := appconfig.EnsureRuntimeDirs(cfg, systemPortDir, softwareRegistryDir); err != nil {
		fatalf("failed to ensure runtime directories: %v", err)
	}
	invSettings := embeddedInvoke
	resolvedInvokeConfigPath := invoke.ResolveConfigPath(*configPath, *invokeConfigPath)
	if *invokeConfigPath != "" {
		invSettings, err = invoke.LoadFile(*invokeConfigPath)
		if err != nil {
			fatalf("failed to load invoke config: %v", err)
		}
	} else if loaded, err := invoke.LoadFile(resolvedInvokeConfigPath); err == nil {
		invSettings = loaded
	}
	invoker, err := invoke.InvokerForMode(*invokeMode, invSettings)
	if err != nil {
		fatalf("invoke: %v", err)
	}
	loadPort, err := sandbox.NewSystemPort(systemPortDir)
	if err != nil {
		fatalf("failed to initialize system_port: %v", err)
	}
	vfsSvc, cleanup, err := buildVFS(context.Background(), cfg, *configPath)
	if err != nil {
		fatalf("failed to initialize VFS backend: %v", err)
	}
	defer func() { _ = cleanup() }()

	server := bridge.NewServer(cfg, vfsSvc, invoker)
	server.SetLoadPort(loadPort)
	if err := serveRPC(context.Background(), os.Stdin, os.Stdout, server); err != nil {
		fatalf("gopyd rpc loop failed: %v", err)
	}
}

func serveRPC(ctx context.Context, r io.Reader, w io.Writer, server *bridge.Server) error {
	decoder := json.NewDecoder(r)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)

	for {
		var req rpcRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		resp := handleRPC(ctx, req, server)
		if err := encoder.Encode(resp); err != nil {
			return err
		}
	}
}

func handleRPC(ctx context.Context, req rpcRequest, server *bridge.Server) rpcResponse {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      decodeID(req.ID),
	}
	if req.JSONRPC != "2.0" {
		resp.Error = &rpcError{Code: -32600, Message: "invalid jsonrpc version"}
		return resp
	}

	switch req.Method {
	case bridge.RPCSubmitChunkGraph:
		var plan bridge.ChunkGraphPlan
		if err := json.Unmarshal(req.Params, &plan); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		resp.Result = server.SubmitChunkGraph(&plan)
	case bridge.RPCStartRun:
		var params bridge.StartRunParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		result, err := server.StartRun(ctx, params)
		if err != nil {
			resp.Error = &rpcError{Code: -32000, Message: err.Error()}
			return resp
		}
		resp.Result = result
	case bridge.RPCPauseRun:
		resp.Result, resp.Error = withRunID(req.Params, server.PauseRun)
	case bridge.RPCResumeRun:
		resp.Result, resp.Error = withRunID(req.Params, server.ResumeRun)
	case bridge.RPCCancelRun:
		resp.Result, resp.Error = withRunID(req.Params, server.CancelRun)
	case bridge.RPCGetRun:
		resp.Result, resp.Error = withRunID(req.Params, server.GetRun)
	case bridge.RPCInspectRun:
		resp.Result, resp.Error = withRunIDInspect(req.Params, server.InspectRun)
	case bridge.RPCStreamRunEvents:
		resp.Result, resp.Error = withRunIDEvents(req.Params, server.StreamRunEvents)
	case bridge.RPCListArtifacts:
		resp.Result, resp.Error = withRunIDArtifacts(req.Params, server.ListArtifacts)
	case bridge.RPCInspectChunk:
		var params bridge.InspectChunkParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		result, err := server.InspectChunk(params)
		if err != nil {
			resp.Error = &rpcError{Code: -32000, Message: err.Error()}
			return resp
		}
		resp.Result = result
	case bridge.RPCInspectObject:
		var params bridge.InspectObjectParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		result, err := server.InspectObject(params)
		if err != nil {
			resp.Error = &rpcError{Code: -32000, Message: err.Error()}
			return resp
		}
		resp.Result = result
	default:
		resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
	return resp
}

func withRunID(raw json.RawMessage, fn func(bridge.RunIDParams) (bridge.RunStatus, error)) (interface{}, *rpcError) {
	var params bridge.RunIDParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	result, err := fn(params)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

func withRunIDInspect(raw json.RawMessage, fn func(bridge.RunIDParams) (bridge.InspectRunResult, error)) (interface{}, *rpcError) {
	var params bridge.RunIDParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	result, err := fn(params)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

func withRunIDEvents(raw json.RawMessage, fn func(bridge.RunIDParams) (bridge.StreamRunEventsResult, error)) (interface{}, *rpcError) {
	var params bridge.RunIDParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	result, err := fn(params)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

func withRunIDArtifacts(raw json.RawMessage, fn func(bridge.RunIDParams) ([]bridge.ArtifactSpec, error)) (interface{}, *rpcError) {
	var params bridge.RunIDParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	result, err := fn(params)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

func decodeID(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func buildVFS(ctx context.Context, cfg vfs.Config, configPath string) (vfs.Service, func() error, error) {
	svc, cleanup, _, err := vfsremote.LocalOrRemote(ctx, cfg, configPath)
	if err != nil {
		return nil, nil, err
	}
	if cleanup == nil {
		cleanup = func() error { return nil }
	}
	return svc, cleanup, nil
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
