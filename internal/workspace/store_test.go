package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectLifecyclePersistsAndNeverDeletesWorkspace(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state", "workspace.json")
	store, err := OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.AddProject(Project{Name: "Demo", Path: projectPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(project.ID, "digest-a"); err != nil {
		t.Fatal(err)
	}
	if !store.IsApproved(project.ID, "digest-a") || store.IsApproved(project.ID, "digest-b") {
		t.Fatal("approval digest lookup is incorrect")
	}
	store, err = OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.List(); len(got) != 1 || got[0].ID != project.ID {
		t.Fatalf("project did not persist: %+v", got)
	}
	if err := store.RemoveProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(projectPath); err != nil {
		t.Fatalf("removing registration deleted or changed project: %v", err)
	}
}

func TestOpenStorePreservesMissingProjectForRemoval(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "deleted-project")
	data := diskState{Version: stateVersion, Projects: []Project{{ID: "stale", Name: "Stale", Path: missing}}, ApprovedConfig: map[string]string{"stale": "digest"}}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "workspace.json")
	if err := os.WriteFile(statePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(statePath)
	if err != nil {
		t.Fatalf("opening stale project registration: %v", err)
	}
	if got := store.List(); len(got) != 1 || got[0].Path != missing {
		t.Fatalf("stale path not preserved: %+v", got)
	}
	if err := store.RemoveProject("stale"); err != nil {
		t.Fatalf("removing stale registration: %v", err)
	}
}

func TestRemoveProjectRestoresStateWhenSaveFails(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state")
	store, err := OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.AddProject(Project{Name: "Demo", Path: projectPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(project.ID, "digest-a"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveProject(project.ID); err == nil {
		t.Fatal("RemoveProject succeeded despite an invalid state destination")
	}
	if got := store.List(); len(got) != 1 || got[0].ID != project.ID {
		t.Fatalf("project state was not restored: %+v", got)
	}
	if !store.IsApproved(project.ID, "digest-a") {
		t.Fatal("approval state was not restored")
	}
}
