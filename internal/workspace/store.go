package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const stateVersion = 1

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	RemoteURL string    `json:"remoteUrl,omitempty"`
	AddedAt   time.Time `json:"addedAt"`
}

type diskState struct {
	Version        int               `json:"version"`
	Projects       []Project         `json:"projects"`
	ApprovedConfig map[string]string `json:"approvedConfig"`
	ProjectConfigs map[string]string `json:"projectConfigs,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data diskState
}

func OpenStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("state file path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve state file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	s := &Store{path: abs, data: diskState{Version: stateVersion, Projects: []Project{}, ApprovedConfig: map[string]string{}, ProjectConfigs: map[string]string{}}}
	bytes, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace state: %w", err)
	}
	if len(bytes) == 0 {
		return nil, errors.New("workspace state file is empty")
	}
	if err := json.Unmarshal(bytes, &s.data); err != nil {
		return nil, fmt.Errorf("parse workspace state: %w", err)
	}
	if s.data.Version != stateVersion {
		return nil, fmt.Errorf("unsupported workspace state version %d", s.data.Version)
	}
	if s.data.Projects == nil {
		s.data.Projects = []Project{}
	}
	if s.data.ApprovedConfig == nil {
		s.data.ApprovedConfig = map[string]string{}
	}
	if s.data.ProjectConfigs == nil {
		s.data.ProjectConfigs = map[string]string{}
	}
	for i := range s.data.Projects {
		canonical, err := storedPath(s.data.Projects[i].Path)
		if err != nil {
			return nil, fmt.Errorf("resolve registered project path: %w", err)
		}
		s.data.Projects[i].Path = canonical
		if s.data.Projects[i].ID == "" {
			s.data.Projects[i].ID = projectID(canonical)
		}
	}
	return s, nil
}

func (s *Store) List() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Project(nil), s.data.Projects...)
}

func (s *Store) Project(id string) (Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, project := range s.data.Projects {
		if project.ID == id {
			return project, true
		}
	}
	return Project{}, false
}

func (s *Store) AddProject(project Project) (Project, error) {
	path, err := canonicalPath(project.Path)
	if err != nil {
		return Project{}, err
	}
	if strings.TrimSpace(project.Name) == "" {
		return Project{}, errors.New("project name is required")
	}
	project.Path = path
	project.ID = projectID(path)
	if project.AddedAt.IsZero() {
		project.AddedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.data.Projects {
		if existing.Path == path {
			return existing, errors.New("project path is already registered")
		}
	}
	s.data.Projects = append(s.data.Projects, project)
	if err := s.saveLocked(); err != nil {
		s.data.Projects = s.data.Projects[:len(s.data.Projects)-1]
		return Project{}, err
	}
	return project, nil
}

// RemoveProject forgets the project registration. It never removes project files.
func (s *Store) RemoveProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, project := range s.data.Projects {
		if project.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("project is not registered")
	}
	oldProjects := append([]Project(nil), s.data.Projects...)
	oldApprovals := make(map[string]string, len(s.data.ApprovedConfig))
	for key, value := range s.data.ApprovedConfig {
		oldApprovals[key] = value
	}
	oldConfigs := make(map[string]string, len(s.data.ProjectConfigs))
	for key, value := range s.data.ProjectConfigs {
		oldConfigs[key] = value
	}
	projects := append([]Project(nil), s.data.Projects...)
	s.data.Projects = append(projects[:index], projects[index+1:]...)
	delete(s.data.ApprovedConfig, id)
	delete(s.data.ProjectConfigs, id)
	if err := s.saveLocked(); err != nil {
		s.data.Projects = oldProjects
		s.data.ApprovedConfig = oldApprovals
		s.data.ProjectConfigs = oldConfigs
		return err
	}
	return nil
}

// SetProjectConfig stores the RunLoft-owned YAML without writing into the
// project workspace. Callers validate it against the registered project first.
func (s *Store) SetProjectConfig(id, name, config string) error {
	if strings.TrimSpace(config) == "" {
		return errors.New("project configuration is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("project name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i := range s.data.Projects {
		if s.data.Projects[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("project is not registered")
	}
	oldName := s.data.Projects[index].Name
	old, hadOld := s.data.ProjectConfigs[id]
	s.data.Projects[index].Name = name
	s.data.ProjectConfigs[id] = config
	if err := s.saveLocked(); err != nil {
		s.data.Projects[index].Name = oldName
		if hadOld {
			s.data.ProjectConfigs[id] = old
		} else {
			delete(s.data.ProjectConfigs, id)
		}
		return err
	}
	return nil
}

func (s *Store) ProjectConfig(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !containsProject(s.data.Projects, id) {
		return "", false
	}
	config, ok := s.data.ProjectConfigs[id]
	return config, ok
}

func (s *Store) Approve(id, digest string) error {
	if digest == "" {
		return errors.New("configuration digest is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !containsProject(s.data.Projects, id) {
		return errors.New("project is not registered")
	}
	old, hadOld := s.data.ApprovedConfig[id]
	s.data.ApprovedConfig[id] = digest
	if err := s.saveLocked(); err != nil {
		if hadOld {
			s.data.ApprovedConfig[id] = old
		} else {
			delete(s.data.ApprovedConfig, id)
		}
		return err
	}
	return nil
}

func (s *Store) IsApproved(id, digest string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return digest != "" && s.data.ApprovedConfig[id] == digest
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace state: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".runloft-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("set state file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write workspace state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync workspace state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close workspace state: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return fmt.Errorf("replace workspace state: %w", err)
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("project path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("inspect project path: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("project path must be a directory")
	}
	return real, nil
}

// storedPath preserves missing paths so the UI can show stale registrations and
// let the user forget them without touching the rest of the workspace state.
func storedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("project path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(abs), nil
	} else if err != nil {
		return "", fmt.Errorf("inspect project path: %w", err)
	}
	return canonicalPath(abs)
}

func projectID(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return hex.EncodeToString(sum[:12])
}

func containsProject(projects []Project, id string) bool {
	for _, project := range projects {
		if project.ID == id {
			return true
		}
	}
	return false
}
