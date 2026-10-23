//go:build !windows

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWaitProcessTreeKillsDescendantsAfterLeaderExits(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is required to create a background descendant")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is required to keep the descendant alive")
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is required to inspect descendant state")
	}

	root := t.TempDir()
	childPIDFile := filepath.Join(root, "child.pid")
	unrelated := exec.Command("sleep", "60")
	if err := startProcess(unrelated); err != nil {
		t.Fatalf("start unrelated process: %v", err)
	}
	unrelatedStopped := false
	t.Cleanup(func() {
		if !unrelatedStopped {
			_ = unrelated.Process.Kill()
			_ = unrelated.Wait()
		}
	})

	command := exec.Command("sh", "-c", `sleep 60 & echo $! > "$1"; wait`, "runloft-test", childPIDFile)
	if err := startProcess(command); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- waitProcessTree(command) }()
	childPID := 0
	treeWaited := false
	t.Cleanup(func() {
		if !treeWaited {
			select {
			case <-wait:
				treeWaited = true
			default:
				_ = forceKillProcessTree(command)
				_ = command.Process.Kill()
				select {
				case <-wait:
					treeWaited = true
				case <-time.After(3 * time.Second):
				}
			}
		}
		killTestDescendant(childPID, command.Process.Pid)
	})

	childPID = waitForChildPID(t, childPIDFile)
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill leader: %v", err)
	}
	select {
	case err := <-wait:
		treeWaited = true
		if err == nil {
			t.Fatal("leader kill unexpectedly returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitProcessTree did not return after the leader exited")
	}

	if err := waitForProcessStopped(childPID, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	alive, err := processIsAlive(unrelated.Process.Pid)
	if err != nil || !alive {
		t.Fatalf("process-group cleanup affected unrelated process %d (alive %t, error %v)", unrelated.Process.Pid, alive, err)
	}
	if err := unrelated.Process.Kill(); err != nil {
		t.Fatalf("stop unrelated test process: %v", err)
	}
	if err := unrelated.Wait(); err == nil {
		t.Fatal("unrelated process kill unexpectedly returned no error")
	}
	unrelatedStopped = true
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr != nil || pid <= 0 {
				t.Fatalf("invalid child PID in %s: %q (%v)", path, contents, parseErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child PID was not written to %s", path)
	return 0
}

func waitForProcessStopped(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := processIsAlive(pid)
		if err != nil {
			return fmt.Errorf("inspect child process %d: %w", pid, err)
		}
		if !alive {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("child process %d remained active after process-group cleanup", pid)
}

func processIsAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false, err
	}
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}
	processState := strings.TrimSpace(string(state))
	if processState == "" || processState[0] == 'Z' {
		return false, nil
	}
	return true, nil
}

func killTestDescendant(pid, leaderPID int) {
	if pid <= 0 {
		return
	}
	groupID, err := syscall.Getpgid(pid)
	if err != nil || groupID != leaderPID {
		return
	}
	_ = syscall.Kill(-groupID, syscall.SIGKILL)
}
