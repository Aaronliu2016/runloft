//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func startProcess(cmd *exec.Cmd) error {
	if err := configureProcess(cmd); err != nil {
		return err
	}
	return cmd.Start()
}

func signalProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}

func forceKillProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}

func waitProcessTree(cmd *exec.Cmd) error {
	err := cmd.Wait()
	// Commands can exit while leaving background descendants in their group.
	// Once the leader exits those descendants no longer belong to the session.
	_ = forceKillProcessTree(cmd)
	return err
}
