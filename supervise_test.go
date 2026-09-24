package main

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// The whole of the supervision is this question, asked every couple of seconds,
// and a wrong answer either way is a server that stops while its console is
// open or one that outlives it. Worth pinning down against two pids that really
// are what they claim to be.
func TestAGoneSupervisorIsNoticedAndALiveOneIsNot(t *testing.T) {
	if processGone(os.Getpid()) {
		t.Fatal("this very process was reported gone")
	}

	if runtime.GOOS == "windows" {
		t.Skip("no signal to probe with; FindProcess is the whole answer there")
	}

	// A pid that certainly existed and certainly does not now. Invented numbers
	// are not the same test: a high one that happens to be in use passes for
	// the wrong reason.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("nothing to start: %v", err)
	}
	pid := cmd.Process.Pid
	// Reaped, so the pid is released rather than left as a zombie this process
	// is still the parent of — a zombie answers signal 0 and is not gone.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the child did not exit cleanly: %v", err)
	}

	if !processGone(pid) {
		t.Fatalf("pid %d exited and was reported alive", pid)
	}
}
