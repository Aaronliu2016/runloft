package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronliu2016/runloft/internal/projectconfig"
)

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_RUNLOFT_HELPER") != "1" {
		return
	}
	port := os.Getenv("RUNLOFT_PORT")
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })}
	go server.Serve(listener)
	fmt.Printf("READY http://127.0.0.1:%s/?token=top-secret-value\n", port)
	fmt.Printf("configured secret: %s\n", os.Getenv("RUNLOFT_TEST_SECRET"))
	for {
		time.Sleep(time.Hour)
	}
}

func TestStartWaitsForHealthAndRedactsEntryToken(t *testing.T) {
	root := t.TempDir()
	port := freePort(t)
	config := testConfig(t, root, port)
	config.Commands.Start.Argv = []string{os.Args[0], "-test.run=^TestRunnerHelperProcess$"}
	config.Server.EntryURLRegex = `READY (https?://[^ ]+)`
	t.Setenv("GO_WANT_RUNLOFT_HELPER", "1")
	t.Setenv("RUNLOFT_TEST_SECRET", "secret-env-value")
	result, err := Start(context.Background(), root, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.EntryURL != fmt.Sprintf("http://127.0.0.1:%d/?token=top-secret-value", port) {
		t.Fatalf("entry URL = %q", result.EntryURL)
	}
	deadline := time.Now().Add(2 * time.Second)
	var logs []LogLine
	for time.Now().Before(deadline) {
		logs = result.Session.Logs()
		if len(logs) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(logs) < 2 || logs[0].Message != "READY http://127.0.0.1:"+fmt.Sprint(port)+"/?token=[redacted]" {
		t.Fatalf("entry token was not redacted in logs: %+v", logs)
	}
	if logs[1].Message != "configured secret: [redacted]" {
		t.Fatalf("sensitive environment value was not redacted: %+v", logs[1])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := result.Session.Stop(ctx); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if _, done := result.Session.ExitError(); !done {
		t.Fatal("process still running after Stop")
	}
}

func TestStartRejectsOccupiedPortBeforeRunningCommand(t *testing.T) {
	root := t.TempDir()
	port := freePort(t)
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config := testConfig(t, root, port)
	config.Commands.Start.Argv = []string{"not-started"}
	if _, err := Start(context.Background(), root, config, nil); err == nil || !contains(err.Error(), "unavailable") {
		t.Fatalf("Start() error = %v; want port conflict", err)
	}
}

func TestRunPrepareStopsAfterFailure(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "should-not-exist")
	config := testConfig(t, root, freePort(t))
	if os.PathSeparator == '/' {
		config.Commands.Prepare.Steps = []projectconfig.Command{
			{Argv: []string{"sh", "-c", "exit 7"}},
			{Argv: []string{"sh", "-c", "touch " + marker}},
		}
	} else {
		t.Skip("shell command test is only applicable on Unix")
	}
	if err := RunPrepare(context.Background(), root, config, nil); err == nil || !contains(err.Error(), "prepare step 1") {
		t.Fatalf("RunPrepare() error = %v; want first step failure", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("later prepare command ran after a failure: %v", err)
	}
}

func TestReadOutputLinesDrainsOversizedLine(t *testing.T) {
	input := strings.Repeat("x", maxOutputLineSize+64*1024) + "\nnext line\n"
	var lines []string
	var truncated []bool
	if err := readOutputLines(strings.NewReader(input), func(line string, wasTruncated bool) {
		lines = append(lines, line)
		truncated = append(truncated, wasTruncated)
	}); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("readOutputLines() returned %d lines, want 2", len(lines))
	}
	if !truncated[0] || len(lines[0]) != maxOutputLineSize || truncated[1] || lines[1] != "next line" {
		t.Fatalf("readOutputLines() first=%d/%t second=%q/%t", len(lines[0]), truncated[0], lines[1], truncated[1])
	}
}

func testConfig(t *testing.T, root string, port int) projectconfig.Config {
	t.Helper()
	config := projectconfig.Config{
		SchemaVersion:    1,
		Project:          projectconfig.Project{Name: "Runner Test"},
		WorkingDirectory: ".",
		Commands:         projectconfig.Commands{Start: projectconfig.Command{Argv: []string{"unused"}}},
		Server:           projectconfig.Server{Host: "127.0.0.1", Port: port, HealthPath: "/", ReadyTimeoutSeconds: 5},
	}
	if err := config.Validate(root); err != nil {
		t.Fatal(err)
	}
	return config
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func contains(value, substring string) bool {
	return len(value) >= len(substring) && strings.Contains(value, substring)
}
