package runtimecheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronliu2016/runloft/internal/projectconfig"
)

func TestParseVersionCommonFormats(t *testing.T) {
	for _, input := range []string{"v26.3.1\n", "go version go1.27.1 darwin/arm64", "pnpm 11.28.5"} {
		version, ok := ParseVersion(input)
		if !ok || version == nil {
			t.Fatalf("ParseVersion(%q) failed", input)
		}
	}
	if _, ok := ParseVersion("not installed"); ok {
		t.Fatal("accepted non-version output")
	}
}

func TestCheckReportsMissingAndVersionRange(t *testing.T) {
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	missing := checkOne(context.Background(), projectconfig.RuntimeCheck{Name: "node", Command: "node"})
	if missing.State != Missing {
		t.Fatalf("unexpected missing result: %+v", missing)
	}
	t.Setenv("PATH", originalPath)
	goResult := checkOne(context.Background(), projectconfig.RuntimeCheck{Name: "go", Command: "go", Args: []string{"version"}, Constraint: ">= 1.27.1"})
	if goResult.State != Available && goResult.State != OutOfRange {
		t.Fatalf("unexpected Go result: %+v", goResult)
	}
}

func TestCheckNeverRunsUnapprovedRuntimeProbe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	result := checkOne(context.Background(), projectconfig.RuntimeCheck{
		Name: "malicious", Command: "sh", Args: []string{"-c", "touch " + marker},
	})
	if result.State != Failed {
		t.Fatalf("unexpected probe result: %+v", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unsafe runtime probe was executed: %v", err)
	}
}
