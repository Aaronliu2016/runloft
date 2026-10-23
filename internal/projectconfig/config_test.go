package projectconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const validYAML = `schemaVersion: 1
project:
  name: Sample
commands:
  start:
    argv: [node, server.js, --port, "${RUNLOFT_PORT}"]
server:
  port: 3080
  healthPath: /
`

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDecodeValidConfigAppliesDefaultsAndExpandsPort(t *testing.T) {
	root := fixtureRoot(t)
	cfg, err := Decode(root, []byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkingDirectory != "." || cfg.Server.Host != "127.0.0.1" || cfg.Server.ReadyTimeoutSeconds != 60 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	argv, err := cfg.ExpandCommand(cfg.Commands.Start)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"node", "server.js", "--port", "3080"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expanded argv = %#v, want %#v", argv, want)
	}
	if got, want := cfg.BaseURL(), "http://127.0.0.1:3080/"; got != want {
		t.Fatalf("BaseURL() = %q, want %q", got, want)
	}
}

func TestDecodeRejectsUnknownFieldsAndExtraDocuments(t *testing.T) {
	root := fixtureRoot(t)
	for name, input := range map[string]string{
		"unknown":  validYAML + "unknownField: true\n",
		"document": validYAML + "---\nproject: {name: second}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(root, []byte(input)); err == nil {
				t.Fatal("Decode unexpectedly accepted invalid YAML")
			}
		})
	}
}

func TestLoadRejectsSymlinkedConfiguration(t *testing.T) {
	root := fixtureRoot(t)
	external := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(external, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, FileName)); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Load() error = %v; want symlink refusal", err)
	}
}

func TestValidateRejectsUnsafeOrInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"unsupported schema", func(c *Config) { c.SchemaVersion = 2 }, "schemaVersion"},
		{"missing executable", func(c *Config) { c.Commands.Start.Argv = nil }, "commands.start.argv"},
		{"non-loopback host", func(c *Config) { c.Server.Host = "0.0.0.0" }, "loopback"},
		{"invalid port", func(c *Config) { c.Server.Port = 65536 }, "server.port"},
		{"remote health URL", func(c *Config) { c.Server.HealthPath = "https://example.com/" }, "healthPath"},
		{"unsupported placeholder", func(c *Config) { c.Commands.Start.Argv[1] = "${HOME}/app" }, "placeholder"},
		{"invalid runtime constraint", func(c *Config) {
			c.Runtime.Checks = []RuntimeCheck{{Name: "Node", Command: "node", Constraint: ">=wat"}}
		}, "runtime.checks"},
		{"unsafe runtime command", func(c *Config) {
			c.Runtime.Checks = []RuntimeCheck{{Name: "Node", Command: "sh", Args: []string{"-c", "touch /tmp/unsafe"}}}
		}, "supported built-in"},
		{"unsafe runtime arguments", func(c *Config) {
			c.Runtime.Checks = []RuntimeCheck{{Name: "Node", Command: "node", Args: []string{"--require", "./untrusted.js"}}}
		}, "built-in version probe"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := fixtureRoot(t)
			cfg, err := Decode(root, []byte(validYAML))
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&cfg)
			if err := cfg.Validate(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsWorkingDirectoryEscapeAndSymlink(t *testing.T) {
	root := fixtureRoot(t)
	cfg, err := Decode(root, []byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorkingDirectory = "../"
	if err := cfg.Validate(root); err == nil {
		t.Fatal("accepted parent path")
	}

	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, "escape")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	cfg.WorkingDirectory = "escape"
	if err := cfg.Validate(root); err == nil || !strings.Contains(err.Error(), "inside the repository") {
		t.Fatalf("Validate() error = %v, want workingDirectory containment error", err)
	}
}

func TestParseEntryURLRequiresConfiguredOrigin(t *testing.T) {
	root := fixtureRoot(t)
	input := strings.Replace(validYAML, "healthPath: /", "healthPath: /\n  entryURLRegex: 'READY (https?://[^ ]+)'", 1)
	cfg, err := Decode(root, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	entry, found, err := cfg.ParseEntryURL("READY http://127.0.0.1:3080/?token=secret")
	if err != nil || !found || entry != "http://127.0.0.1:3080/?token=secret" {
		t.Fatalf("ParseEntryURL() = %q, %t, %v", entry, found, err)
	}
	for _, line := range []string{
		"READY https://127.0.0.1:3080/?token=x",
		"READY http://127.0.0.1:3081/?token=x",
		"READY http://user@127.0.0.1:3080/?token=x",
	} {
		if _, found, err := cfg.ParseEntryURL(line); !found || err == nil {
			t.Errorf("ParseEntryURL(%q) = found %t, err %v; want rejected match", line, found, err)
		}
	}
}

func TestApprovalDigestTracksRepositoryAndCommands(t *testing.T) {
	root := fixtureRoot(t)
	cfg, err := Decode(root, []byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	first, err := cfg.ApprovalDigest("https://github.com/acme/sample.git", "commit-1")
	if err != nil {
		t.Fatal(err)
	}
	otherRepo, _ := cfg.ApprovalDigest("https://github.com/acme/other.git", "commit-1")
	otherRevision, _ := cfg.ApprovalDigest("https://github.com/acme/sample.git", "commit-2")
	cfg.Commands.Start.Argv[1] = "changed.js"
	otherCommand, _ := cfg.ApprovalDigest("https://github.com/acme/sample.git", "commit-1")
	if first == otherRepo || first == otherRevision || first == otherCommand {
		t.Fatal("approval digest did not change when repository, revision, or command changed")
	}
}

func TestDeepSeekHarnessSampleConfig(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	path := filepath.Join(repoRoot, "docs", "samples", "deepseek-harness.runloft.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := Decode(filepath.Join(repoRoot, "docs", "samples"), data)
	if err != nil {
		t.Fatalf("sample configuration is invalid: %v", err)
	}
	if config.Server.Port != 3080 || config.Server.EntryURLRegex == "" || len(config.Commands.Prepare.Steps) != 2 {
		t.Fatalf("sample configuration is missing expected fields: %+v", config)
	}
}
