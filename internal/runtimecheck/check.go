package runtimecheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/aaronliu2016/runloft/internal/projectconfig"
)

type State string

const (
	Available   State = "available"
	Missing     State = "missing"
	Unparseable State = "unparseable"
	OutOfRange  State = "out-of-range"
	Failed      State = "failed"
)

type Result struct {
	Name       string
	Command    string
	State      State
	Version    string
	Constraint string
	Detail     string
}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)`)

// Check runs each declared version probe with a short deadline. It diagnoses
// dependencies only: it never downloads or changes a runtime.
func Check(ctx context.Context, checks []projectconfig.RuntimeCheck) []Result {
	results := make([]Result, 0, len(checks))
	for _, check := range checks {
		results = append(results, checkOne(ctx, check))
	}
	return results
}

func checkOne(parent context.Context, check projectconfig.RuntimeCheck) Result {
	result := Result{Name: check.Name, Command: check.Command, Constraint: check.Constraint}
	args, ok := projectconfig.RuntimeProbeArgs(check.Command)
	if !ok || (len(check.Args) != 0 && !sameArgs(check.Args, args)) {
		result.State = Failed
		result.Detail = "runtime probe is not a supported built-in version check"
		return result
	}
	path, err := exec.LookPath(check.Command)
	if err != nil {
		result.State = Missing
		result.Detail = fmt.Sprintf("%s was not found in PATH", check.Command)
		return result
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = minimalEnvironment()
	output, err := cmd.CombinedOutput()
	if err != nil {
		result.State = Failed
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Detail = "version check timed out after 8 seconds"
		} else {
			result.Detail = "version check failed"
		}
		return result
	}
	version, ok := ParseVersion(string(output))
	if !ok {
		result.State = Unparseable
		result.Detail = "could not read a semantic version from the command output"
		return result
	}
	result.Version = version.String()
	if strings.TrimSpace(check.Constraint) != "" {
		constraint, err := semver.NewConstraint(check.Constraint)
		if err != nil {
			result.State = Failed
			result.Detail = "invalid configured version constraint"
			return result
		}
		if !constraint.Check(version) {
			result.State = OutOfRange
			result.Detail = fmt.Sprintf("installed version %s does not satisfy %s", version, check.Constraint)
			return result
		}
	}
	result.State = Available
	return result
}

func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func minimalEnvironment() []string {
	allowed := []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TEMP", "TMP"}
	var result []string
	seen := make(map[string]bool)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		for _, allowedKey := range allowed {
			if strings.EqualFold(key, allowedKey) && !seen[strings.ToUpper(key)] {
				result = append(result, entry)
				seen[strings.ToUpper(key)] = true
				break
			}
		}
	}
	return result
}

func ParseVersion(output string) (*semver.Version, bool) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if len(match) != 2 {
		return nil, false
	}
	version, err := semver.NewVersion(match[1])
	return version, err == nil
}
