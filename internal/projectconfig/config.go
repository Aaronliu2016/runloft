package projectconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

const FileName = ".runloft.yaml"

var portPlaceholder = regexp.MustCompile(`\$\{([^}]+)\}`)

type Config struct {
	SchemaVersion    int      `yaml:"schemaVersion" json:"schemaVersion"`
	Project          Project  `yaml:"project" json:"project"`
	WorkingDirectory string   `yaml:"workingDirectory" json:"workingDirectory"`
	Runtime          Runtime  `yaml:"runtime" json:"runtime"`
	Commands         Commands `yaml:"commands" json:"commands"`
	Server           Server   `yaml:"server" json:"server"`
}

type Project struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
}

type Runtime struct {
	Checks []RuntimeCheck `yaml:"checks" json:"checks,omitempty"`
}

type RuntimeCheck struct {
	Name       string   `yaml:"name" json:"name"`
	Command    string   `yaml:"command" json:"command"`
	Args       []string `yaml:"args" json:"args"`
	Constraint string   `yaml:"version" json:"version"`
}

type Commands struct {
	Prepare Prepare `yaml:"prepare" json:"prepare"`
	Start   Command `yaml:"start" json:"start"`
}

type Prepare struct {
	Steps []Command `yaml:"steps" json:"steps,omitempty"`
}

type Command struct {
	Argv []string `yaml:"argv" json:"argv"`
}

type Server struct {
	Host                string `yaml:"host" json:"host"`
	Port                int    `yaml:"port" json:"port"`
	HealthPath          string `yaml:"healthPath" json:"healthPath"`
	ReadyTimeoutSeconds int    `yaml:"readyTimeoutSeconds" json:"readyTimeoutSeconds"`
	EntryURLRegex       string `yaml:"entryURLRegex" json:"entryURLRegex,omitempty"`
}

func Load(root string) (Config, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve repository path: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return Config{}, fmt.Errorf("resolve repository path: %w", err)
	}
	path := filepath.Join(rootReal, FileName)
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("inspect %s: %w", FileName, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("%s must be a regular file, not a symlink or special file", FileName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", FileName, err)
	}
	cfg, err := Decode(root, data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", FileName, err)
	}
	return cfg, nil
}

func Decode(root string, data []byte) (Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse YAML: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("only one YAML document is supported")
		}
		return Config{}, fmt.Errorf("parse YAML: %w", err)
	}
	if err := cfg.Validate(root); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate(root string) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion must be 1, got %d", c.SchemaVersion)
	}
	c.Project.Name = strings.TrimSpace(c.Project.Name)
	if c.Project.Name == "" {
		return errors.New("project.name is required")
	}
	if strings.ContainsAny(c.Project.Name, "\r\n\x00") {
		return errors.New("project.name cannot contain control characters")
	}
	if c.WorkingDirectory == "" {
		c.WorkingDirectory = "."
	}
	if err := validateWorkingDirectory(root, c.WorkingDirectory); err != nil {
		return err
	}
	if len(c.Commands.Start.Argv) == 0 || strings.TrimSpace(c.Commands.Start.Argv[0]) == "" {
		return errors.New("commands.start.argv must name an executable")
	}
	if err := validateCommand(c.Commands.Start); err != nil {
		return fmt.Errorf("commands.start: %w", err)
	}
	for i, command := range c.Commands.Prepare.Steps {
		if len(command.Argv) == 0 || strings.TrimSpace(command.Argv[0]) == "" {
			return fmt.Errorf("commands.prepare.steps[%d].argv must name an executable", i)
		}
		if err := validateCommand(command); err != nil {
			return fmt.Errorf("commands.prepare.steps[%d]: %w", i, err)
		}
	}
	for i, check := range c.Runtime.Checks {
		if strings.TrimSpace(check.Name) == "" || strings.TrimSpace(check.Command) == "" {
			return fmt.Errorf("runtime.checks[%d] requires name and command", i)
		}
		allowedArgs, ok := runtimeProbeArgs[check.Command]
		if !ok {
			return fmt.Errorf("runtime.checks[%d].command must name a supported built-in runtime tool", i)
		}
		if len(check.Args) == 0 {
			c.Runtime.Checks[i].Args = append([]string(nil), allowedArgs...)
		} else if !sameStrings(check.Args, allowedArgs) {
			return fmt.Errorf("runtime.checks[%d].args must use the built-in version probe for %s", i, check.Command)
		}
		if check.Constraint != "" {
			if _, err := semver.NewConstraint(check.Constraint); err != nil {
				return fmt.Errorf("runtime.checks[%d].version: %w", i, err)
			}
		}
	}
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	ip := net.ParseIP(c.Server.Host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("server.host must be a loopback IP address")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}
	if c.Server.HealthPath == "" {
		c.Server.HealthPath = "/"
	}
	if err := validateHealthPath(c.Server.HealthPath); err != nil {
		return err
	}
	if c.Server.ReadyTimeoutSeconds == 0 {
		c.Server.ReadyTimeoutSeconds = 60
	}
	if c.Server.ReadyTimeoutSeconds < 1 || c.Server.ReadyTimeoutSeconds > 600 {
		return errors.New("server.readyTimeoutSeconds must be between 1 and 600")
	}
	if c.Server.EntryURLRegex != "" {
		re, err := regexp.Compile(c.Server.EntryURLRegex)
		if err != nil {
			return fmt.Errorf("server.entryURLRegex: %w", err)
		}
		if re.NumSubexp() != 1 {
			return errors.New("server.entryURLRegex must contain exactly one capturing group for the entry URL")
		}
	}
	return nil
}

var runtimeProbeArgs = map[string][]string{
	"node":    {"--version"},
	"pnpm":    {"--version"},
	"npm":     {"--version"},
	"go":      {"version"},
	"python":  {"--version"},
	"python3": {"--version"},
	"git":     {"--version"},
}

func RuntimeProbeArgs(command string) ([]string, bool) {
	args, ok := runtimeProbeArgs[command]
	return append([]string(nil), args...), ok
}

func sameStrings(a, b []string) bool {
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

func validateCommand(command Command) error {
	for i, arg := range command.Argv {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argv[%d] contains a NUL byte", i)
		}
		for _, match := range portPlaceholder.FindAllStringSubmatch(arg, -1) {
			if match[1] != "RUNLOFT_PORT" {
				return fmt.Errorf("argv[%d] uses unsupported placeholder ${%s}", i, match[1])
			}
		}
	}
	return nil
}

func validateWorkingDirectory(root, configured string) error {
	if filepath.IsAbs(configured) {
		return errors.New("workingDirectory must be relative to the repository")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	workAbs, err := filepath.Abs(filepath.Join(rootReal, configured))
	if err != nil {
		return fmt.Errorf("resolve workingDirectory: %w", err)
	}
	workReal, err := filepath.EvalSymlinks(workAbs)
	if err != nil {
		return fmt.Errorf("resolve workingDirectory: %w", err)
	}
	rel, err := filepath.Rel(rootReal, workReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("workingDirectory must remain inside the repository after resolving symlinks")
	}
	info, err := os.Stat(workReal)
	if err != nil {
		return fmt.Errorf("workingDirectory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("workingDirectory must be a directory")
	}
	return nil
}

func validateHealthPath(path string) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return errors.New("server.healthPath must be an absolute path on the local service")
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.RawQuery != "" {
		return errors.New("server.healthPath must be a local URL path without a host, query, or fragment")
	}
	return nil
}

func (c Config) WorkingPath(root string) (string, error) {
	if err := validateWorkingDirectory(root, c.WorkingDirectory); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	workReal, err := filepath.EvalSymlinks(filepath.Join(rootReal, c.WorkingDirectory))
	if err != nil {
		return "", fmt.Errorf("resolve workingDirectory: %w", err)
	}
	return workReal, nil
}

func (c Config) ExpandArg(arg string) (string, error) {
	var unsupported string
	result := portPlaceholder.ReplaceAllStringFunc(arg, func(match string) string {
		parts := portPlaceholder.FindStringSubmatch(match)
		if len(parts) != 2 || parts[1] != "RUNLOFT_PORT" {
			unsupported = match
			return match
		}
		return fmt.Sprint(c.Server.Port)
	})
	if unsupported != "" {
		return "", fmt.Errorf("unsupported placeholder %s", unsupported)
	}
	return result, nil
}

func (c Config) ExpandCommand(command Command) ([]string, error) {
	argv := make([]string, len(command.Argv))
	for i, arg := range command.Argv {
		expanded, err := c.ExpandArg(arg)
		if err != nil {
			return nil, fmt.Errorf("argv[%d]: %w", i, err)
		}
		argv[i] = expanded
	}
	return argv, nil
}

func (c Config) BaseURL() string {
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(c.Server.Host, fmt.Sprint(c.Server.Port)), Path: c.Server.HealthPath}).String()
}

func (c Config) ParseEntryURL(line string) (string, bool, error) {
	if c.Server.EntryURLRegex == "" {
		return "", false, nil
	}
	re := regexp.MustCompile(c.Server.EntryURLRegex)
	match := re.FindStringSubmatch(line)
	if len(match) == 0 {
		return "", false, nil
	}
	entry := match[1]
	u, err := url.Parse(entry)
	if err != nil || u.User != nil || u.Hostname() != c.Server.Host || u.Port() != fmt.Sprint(c.Server.Port) || u.Scheme != "http" {
		return "", true, errors.New("entry URL must use the configured loopback host, port, and HTTP scheme")
	}
	return u.String(), true, nil
}

func (c Config) ApprovalDigest(repositoryIdentity, repositoryRevision string) (string, error) {
	data, err := json.Marshal(struct {
		Repository string `json:"repository"`
		Revision   string `json:"revision"`
		Config     Config `json:"config"`
	}{repositoryIdentity, repositoryRevision, c})
	if err != nil {
		return "", fmt.Errorf("encode configuration: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
