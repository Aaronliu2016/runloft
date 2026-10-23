//go:build windows

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createSuspended                = 0x00000004
	processSetQuota                = 0x0100
	processTerminate               = 0x0001
	processQueryLimitedInformation = 0x1000
	threadSuspendResume            = 0x0002
	jobObjectExtendedInfo          = 9
	jobLimitKillOnJobClose         = 0x2000
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectProc    = kernel32.NewProc("CreateJobObjectW")
	setInformationJobProc  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobProc = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObjectProc = kernel32.NewProc("TerminateJobObject")
	thread32FirstProc      = kernel32.NewProc("Thread32First")
	thread32NextProc       = kernel32.NewProc("Thread32Next")
	openThreadProc         = kernel32.NewProc("OpenThread")
	resumeThreadProc       = kernel32.NewProc("ResumeThread")
	processJobs            = struct {
		sync.Mutex
		byPID map[int]syscall.Handle
	}{byPID: make(map[int]syscall.Handle)}
)

type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IOInfo                jobIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePriority   int32
	DeltaPriority  int32
	Flags          uint32
}

func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createSuspended}
	return nil
}

func startProcess(cmd *exec.Cmd) error {
	if err := configureProcess(cmd); err != nil {
		return err
	}
	job, err := createProcessJob()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = syscall.CloseHandle(job)
		return err
	}
	if err := assignProcess(job, cmd.Process.Pid); err != nil {
		_ = terminateJob(job)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = syscall.CloseHandle(job)
		return fmt.Errorf("assign process to Windows job object: %w", err)
	}
	if err := resumeInitialThread(uint32(cmd.Process.Pid)); err != nil {
		_ = terminateJob(job)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = syscall.CloseHandle(job)
		return fmt.Errorf("resume project process: %w", err)
	}
	processJobs.Lock()
	processJobs.byPID[cmd.Process.Pid] = job
	processJobs.Unlock()
	return nil
}

func createProcessJob() (syscall.Handle, error) {
	result, _, callErr := createJobObjectProc.Call(0, 0)
	if result == 0 {
		return 0, windowsCallError("CreateJobObjectW", callErr)
	}
	job := syscall.Handle(result)
	limits := jobExtendedLimitInformation{
		BasicLimitInformation: jobBasicLimitInformation{LimitFlags: jobLimitKillOnJobClose},
	}
	result, _, callErr = setInformationJobProc.Call(
		uintptr(job), jobObjectExtendedInfo, uintptr(unsafe.Pointer(&limits)), uintptr(unsafe.Sizeof(limits)),
	)
	if result == 0 {
		_ = syscall.CloseHandle(job)
		return 0, windowsCallError("SetInformationJobObject", callErr)
	}
	return job, nil
}

func assignProcess(job syscall.Handle, pid int) error {
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	result, _, callErr := assignProcessToJobProc.Call(uintptr(job), uintptr(process))
	if result == 0 {
		return windowsCallError("AssignProcessToJobObject", callErr)
	}
	return nil
}

func resumeInitialThread(pid uint32) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)

	entry := threadEntry32{Size: uint32(unsafe.Sizeof(threadEntry32{}))}
	result, _, callErr := thread32FirstProc.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	if result == 0 {
		return windowsCallError("Thread32First", callErr)
	}
	for result != 0 {
		if entry.OwnerProcessID == pid {
			thread, _, openErr := openThreadProc.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
			if thread == 0 {
				return windowsCallError("OpenThread", openErr)
			}
			defer syscall.CloseHandle(syscall.Handle(thread))
			resumeCount, _, resumeErr := resumeThreadProc.Call(thread)
			if resumeCount == ^uintptr(0) {
				return windowsCallError("ResumeThread", resumeErr)
			}
			return nil
		}
		entry.Size = uint32(unsafe.Sizeof(threadEntry32{}))
		result, _, callErr = thread32NextProc.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	}
	return fmt.Errorf("find initial thread for process %d: %w", pid, syscall.ERROR_NOT_FOUND)
}

func signalProcessTree(cmd *exec.Cmd) error {
	return runTaskkill(cmd, false)
}

func forceKillProcessTree(cmd *exec.Cmd) error {
	if job := processJob(cmd); job != 0 {
		return terminateJob(job)
	}
	return runTaskkill(cmd, true)
}

func waitProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return cmd.Wait()
	}
	job := processJob(cmd)
	defer closeProcessJob(cmd)
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE|processQueryLimitedInformation, false, uint32(cmd.Process.Pid))
	if err != nil {
		if job != 0 {
			_ = terminateJob(job)
		}
		return cmd.Wait()
	}
	defer syscall.CloseHandle(handle)
	if _, err := syscall.WaitForSingleObject(handle, syscall.INFINITE); err != nil {
		if job != 0 {
			_ = terminateJob(job)
		}
		return cmd.Wait()
	}
	if job != 0 {
		// The job retains descendants even after the wrapper exits. Terminate
		// any remaining members before closing pipes and reporting completion.
		_ = terminateJob(job)
	}
	return cmd.Wait()
}

func runTaskkill(cmd *exec.Cmd, force bool) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE|processQueryLimitedInformation, false, uint32(cmd.Process.Pid))
	if err != nil {
		return os.ErrProcessDone
	}
	defer syscall.CloseHandle(handle)
	state, err := syscall.WaitForSingleObject(handle, 0)
	if err != nil {
		return err
	}
	if state == syscall.WAIT_OBJECT_0 {
		return os.ErrProcessDone
	}
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	if err := exec.Command("taskkill", args...).Run(); err != nil {
		return fmt.Errorf("taskkill process tree: %w", err)
	}
	return nil
}

func processJob(cmd *exec.Cmd) syscall.Handle {
	if cmd.Process == nil {
		return 0
	}
	processJobs.Lock()
	defer processJobs.Unlock()
	return processJobs.byPID[cmd.Process.Pid]
}

func closeProcessJob(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	processJobs.Lock()
	job := processJobs.byPID[cmd.Process.Pid]
	delete(processJobs.byPID, cmd.Process.Pid)
	processJobs.Unlock()
	if job != 0 {
		_ = syscall.CloseHandle(job)
	}
}

func terminateJob(job syscall.Handle) error {
	result, _, callErr := terminateJobObjectProc.Call(uintptr(job), 1)
	if result == 0 {
		return windowsCallError("TerminateJobObject", callErr)
	}
	return nil
}

func windowsCallError(name string, callErr error) error {
	if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", name)
	}
	return fmt.Errorf("%s: %w", name, callErr)
}
