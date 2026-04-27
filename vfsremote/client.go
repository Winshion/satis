package vfsremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"satis/vfs"
)

type readWriteCloser interface {
	io.Reader
	io.Writer
	io.Closer
}

type Client struct {
	rw      readWriteCloser
	enc     *json.Encoder
	dec     *json.Decoder
	writeMu sync.Mutex
	readMu  sync.Mutex
	nextID  atomic.Uint64
}

type requestEnvelope struct {
	ID     uint64 `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

func NewClient(rw readWriteCloser) *Client {
	return &Client{
		rw:  rw,
		enc: json.NewEncoder(rw),
		dec: json.NewDecoder(rw),
	}
}

func (c *Client) Close() error {
	if c == nil || c.rw == nil {
		return nil
	}
	return c.rw.Close()
}

func (c *Client) BeginChunkTxn(ctx context.Context, chunkID vfs.ChunkID) (vfs.Txn, error) {
	var out vfs.Txn
	err := c.call(ctx, methodBegin, map[string]any{"chunk_id": chunkID}, &out)
	return out, err
}

func (c *Client) CommitChunkTxn(ctx context.Context, txn vfs.Txn) error {
	return c.call(ctx, methodCommit, map[string]any{"txn": txn}, nil)
}

func (c *Client) RollbackChunkTxn(ctx context.Context, txn vfs.Txn) error {
	return c.call(ctx, methodRollback, map[string]any{"txn": txn}, nil)
}

func (c *Client) Create(ctx context.Context, txn vfs.Txn, input vfs.CreateInput) (vfs.FileRef, error) {
	var out vfs.FileRef
	err := c.call(ctx, methodCreate, map[string]any{"txn": txn, "input": input}, &out)
	return out, err
}

func (c *Client) Resolve(ctx context.Context, input vfs.ResolveInput) (vfs.FileRef, error) {
	var out vfs.FileRef
	err := c.call(ctx, methodResolve, map[string]any{"input": input}, &out)
	return out, err
}

func (c *Client) Read(ctx context.Context, txn vfs.Txn, input vfs.ReadInput) (vfs.ReadResult, error) {
	var out vfs.ReadResult
	err := c.call(ctx, methodRead, map[string]any{"txn": txn, "input": input}, &out)
	return out, err
}

func (c *Client) ListDir(ctx context.Context, txn vfs.Txn, virtualPath string) ([]vfs.DirEntry, error) {
	var out []vfs.DirEntry
	err := c.call(ctx, methodListDir, map[string]any{"txn": txn, "virtual_path": virtualPath}, &out)
	return out, err
}

func (c *Client) Write(ctx context.Context, txn vfs.Txn, input vfs.WriteInput) (vfs.FileRef, error) {
	var out vfs.FileRef
	err := c.call(ctx, methodWrite, map[string]any{"txn": txn, "input": input}, &out)
	return out, err
}

func (c *Client) Delete(ctx context.Context, txn vfs.Txn, input vfs.DeleteInput) (vfs.FileRef, error) {
	var out vfs.FileRef
	err := c.call(ctx, methodDelete, map[string]any{"txn": txn, "input": input}, &out)
	return out, err
}

func (c *Client) Rename(ctx context.Context, txn vfs.Txn, input vfs.RenameInput) (vfs.FileRef, error) {
	var out vfs.FileRef
	err := c.call(ctx, methodRename, map[string]any{"txn": txn, "input": input}, &out)
	return out, err
}

func (c *Client) Glob(ctx context.Context, pattern string) ([]string, error) {
	var out []string
	err := c.call(ctx, methodGlob, map[string]any{"pattern": pattern}, &out)
	return out, err
}

func (c *Client) call(_ context.Context, method string, params any, out any) error {
	reqID := c.nextID.Add(1)

	c.writeMu.Lock()
	err := c.enc.Encode(requestEnvelope{ID: reqID, Method: method, Params: params})
	c.writeMu.Unlock()
	if err != nil {
		return err
	}

	c.readMu.Lock()
	defer c.readMu.Unlock()

	var resp response
	if err := c.dec.Decode(&resp); err != nil {
		return err
	}
	if resp.ID != reqID {
		return fmt.Errorf("vfsremote: response id mismatch: got %d want %d", resp.ID, reqID)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}
