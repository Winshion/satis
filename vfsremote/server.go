package vfsremote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"satis/vfs"
)

type Server struct {
	service vfs.Service
}

type responseEnvelope struct {
	ID     uint64 `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func NewServer(service vfs.Service) *Server {
	return &Server{service: service}
}

func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	dec := json.NewDecoder(r)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		resp := s.handle(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, req request) responseEnvelope {
	resp := responseEnvelope{ID: req.ID}
	result, err := s.dispatch(ctx, req.Method, req.Params)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	if result == nil {
		return resp
	}
	resp.Result = result
	return resp
}

func (s *Server) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case methodBegin:
		var params struct {
			ChunkID vfs.ChunkID `json:"chunk_id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.BeginChunkTxn(ctx, params.ChunkID)
	case methodCommit:
		var params struct {
			Txn vfs.Txn `json:"txn"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return nil, s.service.CommitChunkTxn(ctx, params.Txn)
	case methodRollback:
		var params struct {
			Txn vfs.Txn `json:"txn"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return nil, s.service.RollbackChunkTxn(ctx, params.Txn)
	case methodCreate:
		var params struct {
			Txn   vfs.Txn        `json:"txn"`
			Input vfs.CreateInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Create(ctx, params.Txn, params.Input)
	case methodResolve:
		var params struct {
			Input vfs.ResolveInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Resolve(ctx, params.Input)
	case methodRead:
		var params struct {
			Txn   vfs.Txn       `json:"txn"`
			Input vfs.ReadInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Read(ctx, params.Txn, params.Input)
	case methodListDir:
		var params struct {
			Txn         vfs.Txn `json:"txn"`
			VirtualPath string  `json:"virtual_path"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.ListDir(ctx, params.Txn, params.VirtualPath)
	case methodWrite:
		var params struct {
			Txn   vfs.Txn        `json:"txn"`
			Input vfs.WriteInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Write(ctx, params.Txn, params.Input)
	case methodDelete:
		var params struct {
			Txn   vfs.Txn         `json:"txn"`
			Input vfs.DeleteInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Delete(ctx, params.Txn, params.Input)
	case methodRename:
		var params struct {
			Txn   vfs.Txn         `json:"txn"`
			Input vfs.RenameInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Rename(ctx, params.Txn, params.Input)
	case methodGlob:
		var params struct {
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.service.Glob(ctx, params.Pattern)
	default:
		return nil, fmt.Errorf("unknown vfs method %q", method)
	}
}
