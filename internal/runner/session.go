package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aaronliu2016/runloft/internal/projectconfig"
)

const (
	logLimit          = 1000
	maxLogLineSize    = 4096
	maxOutputLineSize = 64 * 1024
	prepareTimeout    = 30 * time.Minute
)

var (
	secretQueryValue      = regexp.MustCompile(`(?i)([?&](?:token|key|secret|password|authorization|access_token|refresh_token|api_key|client_secret)=)[^&#\s]+`)
	secretAssignmentValue = regexp.MustCompile(`(?i)("?(?:token|key|secret|password|authorization|access_token|refresh_token|api_key|client_secret)"?\s*[:=]\s*"?)[^"&\s,}]+`)
	bearerValue           = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]+`)
	sensitiveEnvName      = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|API[_-]?KEY|AUTHORIZATION|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|SIGNING[_-]?KEY)`)
)

type LogLine struct {
	At      time.Time
	Stream  string
	Message string
}

type Session struct {
	cmd      *exec.Cmd
	logsMu   sync.Mutex
	logs     []LogLine
	entryMu  sync.RWMutex
	entry    string
	entryErr error
	done     chan struct{}
	waitErr  error
	stopMu   sync.Mutex
	onLog    func(LogLine)
	secrets  []string
}

type StartResult struct {
	Session  *Session
	EntryURL string
}

func Start(ctx context.Context, root string, config projectconfig.Config, onLog func(LogLine)) (StartResult, error) {
	if err := config.Validate(root); err != nil {
		return StartResult{}, fmt.Errorf("validate project configuration: %w", err)
	}
	workingPath, err := config.WorkingPath(root)
	if err != nil {
		return StartResult{}, err
	}
	argv, err := config.ExpandCommand(config.Commands.Start)
	if err != nil {
		return StartResult{}, err
	}
	if err := ensurePortAvailable(config.Server.Host, config.Server.Port); err != nil {
		return StartResult{}, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workingPath
	cmd.Env = setEnvironment(os.Environ(), "RUNLOFT_PORT", fmt.Sprint(config.Server.Port))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return StartResult{}, fmt.Errorf("capture stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return StartResult{}, fmt.Errorf("capture stderr: %w", err)
	}
	if err := startProcess(cmd); err != nil {
		return StartResult{}, fmt.Errorf("start %q: %w", argv[0], err)
	}
	s := &Session{cmd: cmd, done: make(chan struct{}), onLog: onLog, secrets: secretValues(cmd.Env)}
	go s.readLines(stdout, "stdout", config)
	go s.readLines(stderr, "stderr", config)
	go s.waitProcess()

	baseURL := config.BaseURL()
	deadline := time.NewTimer(time.Duration(config.Server.ReadyTimeoutSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	for {
		if entryErr := s.EntryError(); entryErr != nil {
			_ = s.Stop(context.Background())
			return StartResult{}, fmt.Errorf("reject project entry URL: %w", entryErr)
		}
		select {
		case <-s.done:
			return StartResult{}, fmt.Errorf("start command exited before the local service became ready: %s", formatExit(s.waitErr))
		default:
		}
		entry := s.EntryURL()
		if entry == "" && config.Server.EntryURLRegex == "" {
			entry = baseURL
		}
		if entry != "" && localHTTPReady(ctx, client, baseURL) {
			return StartResult{Session: s, EntryURL: entry}, nil
		}
		select {
		case <-ctx.Done():
			_ = s.Stop(context.Background())
			return StartResult{}, ctx.Err()
		case <-deadline.C:
			_ = s.Stop(context.Background())
			return StartResult{}, fmt.Errorf("service did not become ready at %s within %d seconds", baseURL, config.Server.ReadyTimeoutSeconds)
		case <-ticker.C:
		}
	}
}

func (s *Session) readLines(reader io.Reader, stream string, config projectconfig.Config) {
	err := readOutputLines(reader, func(line string, truncated bool) {
		if !truncated {
			if entry, found, err := config.ParseEntryURL(line); found {
				s.entryMu.Lock()
				if err != nil {
					s.entryErr = err
				} else {
					s.entry = entry
				}
				s.entryMu.Unlock()
			}
		}
		s.recordLogLine(stream, line, truncated)
	})
	if err != nil {
		s.recordLogLine(stream, "output reader failed; stopping the project process", false)
		_ = forceKillProcessTree(s.cmd)
	}
}

func (s *Session) recordLogLine(stream, line string, truncated bool) {
	message := logMessage(line, truncated, s.secrets)
	record := LogLine{At: time.Now().UTC(), Stream: stream, Message: message}
	s.logsMu.Lock()
	if len(s.logs) == logLimit {
		copy(s.logs, s.logs[1:])
		s.logs[len(s.logs)-1] = record
	} else {
		s.logs = append(s.logs, record)
	}
	s.logsMu.Unlock()
	if s.onLog != nil {
		s.onLog(record)
	}
}

func (s *Session) waitProcess() {
	s.waitErr = waitProcessTree(s.cmd)
	close(s.done)
}

func (s *Session) EntryURL() string {
	s.entryMu.RLock()
	defer s.entryMu.RUnlock()
	return s.entry
}

func (s *Session) EntryError() error {
	s.entryMu.RLock()
	defer s.entryMu.RUnlock()
	return s.entryErr
}

func (s *Session) Logs() []LogLine {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	return append([]LogLine(nil), s.logs...)
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) ExitError() (error, bool) {
	select {
	case <-s.done:
		return s.waitErr, true
	default:
		return nil, false
	}
}

func (s *Session) Stop(ctx context.Context) error {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	select {
	case <-s.done:
		return nil
	default:
	}
	if err := signalProcessTree(s.cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("request process shutdown: %w", err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-s.done:
		return nil
	case <-timer.C:
	}
	if err := forceKillProcessTree(s.cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("force stop project process: %w", err)
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for project process to stop: %w", ctx.Err())
	case <-time.After(3 * time.Second):
		return errors.New("project process did not stop after force kill")
	}
}

func RunPrepare(ctx context.Context, root string, config projectconfig.Config, onLog func(LogLine)) error {
	if err := config.Validate(root); err != nil {
		return fmt.Errorf("validate project configuration: %w", err)
	}
	workingPath, err := config.WorkingPath(root)
	if err != nil {
		return err
	}
	for index, command := range config.Commands.Prepare.Steps {
		argv, err := config.ExpandCommand(command)
		if err != nil {
			return fmt.Errorf("prepare step %d: %w", index+1, err)
		}
		stepCtx, cancel := context.WithTimeout(ctx, prepareTimeout)
		err = runCommand(stepCtx, workingPath, argv, config.Server.Port, onLog)
		cancel()
		if err != nil {
			return fmt.Errorf("prepare step %d (%s) failed: %w", index+1, argv[0], err)
		}
	}
	return nil
}

func runCommand(ctx context.Context, dir string, argv []string, port int, onLog func(LogLine)) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = setEnvironment(os.Environ(), "RUNLOFT_PORT", fmt.Sprint(port))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := startProcess(cmd); err != nil {
		return err
	}
	secrets := secretValues(cmd.Env)
	var readers sync.WaitGroup
	readErrors := make(chan error, 2)
	readers.Add(2)
	read := func(r io.Reader, stream string) {
		defer readers.Done()
		if err := readOutputLines(r, func(line string, truncated bool) {
			if onLog != nil {
				onLog(LogLine{At: time.Now().UTC(), Stream: stream, Message: logMessage(line, truncated, secrets)})
			}
		}); err != nil {
			readErrors <- err
			_ = forceKillProcessTree(cmd)
		}
	}
	go read(stdout, "stdout")
	go read(stderr, "stderr")
	wait := make(chan error, 1)
	go func() { wait <- waitProcessTree(cmd) }()
	readersDone := make(chan struct{})
	go func() { readers.Wait(); close(readersDone) }()
	select {
	case err := <-wait:
		if scanErr := finishReaders(readersDone, cmd); scanErr != nil {
			return scanErr
		}
		if readErr := firstReadError(readErrors); readErr != nil {
			return fmt.Errorf("read process output: %w", readErr)
		}
		return err
	case <-ctx.Done():
		_ = signalProcessTree(cmd)
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
			_ = forceKillProcessTree(cmd)
			select {
			case <-wait:
			case <-time.After(3 * time.Second):
				return ctx.Err()
			}
		}
		if scanErr := finishReaders(readersDone, cmd); scanErr != nil {
			return scanErr
		}
		if readErr := firstReadError(readErrors); readErr != nil {
			return fmt.Errorf("read process output: %w", readErr)
		}
		return ctx.Err()
	}
}

func readOutputLines(source io.Reader, consume func(string, bool)) error {
	reader := bufio.NewReaderSize(source, 32*1024)
	for {
		line := make([]byte, 0, maxLogLineSize)
		truncated, readAny := false, false
		for {
			fragment, err := reader.ReadSlice('\n')
			if len(fragment) > 0 {
				readAny = true
				remaining := maxOutputLineSize - len(line)
				if remaining > 0 {
					if len(fragment) > remaining {
						line = append(line, fragment[:remaining]...)
						truncated = true
					} else {
						line = append(line, fragment...)
					}
				} else {
					truncated = true
				}
			}

			switch err {
			case bufio.ErrBufferFull:
				continue
			case nil:
				line = trimOutputLineEnding(line)
				consume(string(line), truncated)
			case io.EOF:
				if readAny {
					line = trimOutputLineEnding(line)
					consume(string(line), truncated)
				}
				return nil
			default:
				if readAny {
					line = trimOutputLineEnding(line)
					consume(string(line), truncated)
				}
				return err
			}
			break
		}
	}
}

func trimOutputLineEnding(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}

func logMessage(line string, truncated bool, secrets []string) string {
	if truncated {
		return "[output line exceeded 64 KiB and was omitted]"
	}
	message := redact(line, secrets)
	if len(message) > maxLogLineSize {
		message = message[:maxLogLineSize] + "…"
	}
	return strings.ToValidUTF8(message, "�")
}

func firstReadError(readErrors <-chan error) error {
	select {
	case err := <-readErrors:
		return err
	default:
		return nil
	}
}

func ensurePortAvailable(host string, port int) error {
	address := net.JoinHostPort(host, fmt.Sprint(port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("port %d is unavailable on %s: %w", port, host, err)
	}
	return listener.Close()
}

func localHTTPReady(ctx context.Context, client *http.Client, rawURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.Request == nil || response.Request.URL == nil {
		return false
	}
	final, err := url.Parse(response.Request.URL.String())
	if err != nil || final.Scheme != "http" || final.Host != req.URL.Host {
		return false
	}
	return response.StatusCode >= 200 && response.StatusCode < 300 || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden
}

func formatExit(err error) string {
	if err == nil {
		return "without an error"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("exit code %d", exit.ExitCode())
	}
	return "with an error"
}

func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	value = secretQueryValue.ReplaceAllString(value, "${1}[redacted]")
	value = secretAssignmentValue.ReplaceAllString(value, "${1}[redacted]")
	return bearerValue.ReplaceAllString(value, "${1}[redacted]")
}

func setEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func finishReaders(done <-chan struct{}, cmd *exec.Cmd) error {
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
	}
	_ = forceKillProcessTree(cmd)
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("command output streams remained open after process cleanup")
	}
}

func secretValues(environment []string) []string {
	var values []string
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && sensitiveEnvName.MatchString(key) && len(value) >= 4 {
			values = append(values, value)
		}
	}
	return values
}
