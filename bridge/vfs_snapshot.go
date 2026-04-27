package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"satis/sandbox"
	"satis/vfs"
)

func (s *Server) snapshotRunStableState(record *runRecord) *runStableStateSnapshot {
	if record == nil {
		return nil
	}
	planData, err := json.Marshal(record.plan)
	if err != nil {
		return nil
	}
	var clonedPlan ChunkGraphPlan
	if err := json.Unmarshal(planData, &clonedPlan); err != nil {
		return nil
	}
	chunks := make(map[string]ChunkExecutionResult, len(record.chunks))
	for chunkID, result := range record.chunks {
		chunks[chunkID] = cloneChunkExecutionResult(result)
	}
	snapshot := &runStableStateSnapshot{
		plan:              &clonedPlan,
		status:            cloneRunStatus(record.status),
		chunks:            chunks,
		artifacts:         append([]ArtifactSpec(nil), record.artifacts...),
		graphRevision:     record.graphRevision,
		continuationCount: record.continuationCount,
		planningHistory:   append([]PlanningHistoryEntry(nil), record.planningHistory...),
		workflowRegistry:  cloneWorkflowRegistry(record.workflowRegistry),
	}
	if dir, err := snapshotVFSMount(s.cfg); err == nil {
		snapshot.vfsSnapshotDir = dir
	}
	return snapshot
}

func snapshotVFSMount(cfg vfs.Config) (string, error) {
	if cfg.Backend != "disk" {
		return "", nil
	}
	mountDir := cfg.MountDir
	if mountDir == "" {
		return "", nil
	}
	tmpDir, err := os.MkdirTemp("", "satis_vfs_snapshot_*")
	if err != nil {
		return "", err
	}
	dstRoot := filepath.Join(tmpDir, "mount")
	if err := copyDirTree(mountDir, dstRoot); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", err
	}
	return tmpDir, nil
}

func (s *Server) restoreVFSSnapshot(snapshotDir string) error {
	if snapshotDir == "" || s.cfg.Backend != "disk" || s.cfg.MountDir == "" {
		return nil
	}
	srcRoot := filepath.Join(snapshotDir, "mount")
	if err := os.RemoveAll(s.cfg.MountDir); err != nil {
		return err
	}
	if err := copyDirTree(srcRoot, s.cfg.MountDir); err != nil {
		return err
	}
	svc, err := sandbox.NewDiskService(s.cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.vfs = svc
	if s.executor == nil {
		s.executor = newBridgeExecutor(svc, s.invoker)
		s.executor.LoadPort = s.loadPort
	} else {
		s.executor.VFS = svc
	}
	s.mu.Unlock()
	return nil
}

func copyDirTree(srcDir string, dstDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("copy dir tree: %s is not a directory", srcDir)
	}
	if err := os.MkdirAll(dstDir, info.Mode().Perm()); err != nil {
		return err
	}
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dstDir, rel)
		entryInfo := info
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("copy dir tree: symlink not supported: %s", path)
		}
		if entryInfo.IsDir() {
			return os.MkdirAll(dstPath, entryInfo.Mode().Perm())
		}
		return copyFile(path, dstPath, entryInfo.Mode().Perm())
	})
}

func copyFile(src string, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
