package proc

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestRunLimit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("memory limits are available only on Linux")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no shell")
	}
	// The shell prints its own limit in KiB. Wait a short time first, so
	// that the limit is set when the shell reads it.
	cmd := exec.Command(sh, "-c", "sleep 0.2; ulimit -d")
	var out strings.Builder
	cmd.Stdout = &out
	if err := Run(cmd, 64<<20); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "65536" {
		t.Errorf("limit = %q, want 65536", got)
	}
}
