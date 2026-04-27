package sandbox

import (
	"io"
	"os/exec"
)

type Session struct {
	Cmd          *exec.Cmd
	Stdin        io.WriteCloser
	Stdout       io.ReadCloser
	Verification VerificationResult
}

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	if s.Stdin != nil {
		_ = s.Stdin.Close()
	}
	if s.Stdout != nil {
		_ = s.Stdout.Close()
	}
	if s.Cmd == nil || s.Cmd.Process == nil {
		return nil
	}
	_ = s.Cmd.Process.Kill()
	_, _ = s.Cmd.Process.Wait()
	return nil
}
