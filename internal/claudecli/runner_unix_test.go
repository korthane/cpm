//go:build unix

package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRealRunnerTimeoutKillsGrandchildren(t *testing.T) {
	// WaitDelay alone unblocks Run but would leave the grandchild running
	// detached for the rest of the session; the process-group kill must take
	// it down together with claude.
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	ready := filepath.Join(dir, "ready")
	stub := writeScript(t, "#!/bin/sh\nsleep 30 &\necho $! > "+pidFile+"\ntouch "+ready+"\nwait\n")
	r := &realRunner{binary: stub, waitDelay: 100 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(ready); err == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	if _, err := r.Run(ctx, "", "anything"); err == nil {
		t.Fatal("expected error from a killed run, got nil")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse grandchild pid %q: %v", raw, err)
	}
	// Signal 0 probes existence; poll briefly because init may not have
	// reaped the killed grandchild the instant Run returns.
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still alive after the timeout kill", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
