// Package proc runs external programs with a memory limit.
package proc

import (
	"fmt"
	"os/exec"
)

// Run starts cmd and waits for it, like cmd.Run. If maxData is more than
// zero, the program can use maxData bytes of data memory (RLIMIT_DATA)
// at most. If the program tries to use more memory, the allocation fails
// and the program usually stops with an error.
//
// The limit applies immediately after the start of the program. The
// program can use some memory before that time. This is not a problem,
// because a program usually reads its input first.
func Run(cmd *exec.Cmd, maxData uint64) error {
	if maxData == 0 {
		return cmd.Run()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := setDataLimit(cmd.Process.Pid, maxData); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("set the memory limit: %w", err)
	}
	return cmd.Wait()
}
