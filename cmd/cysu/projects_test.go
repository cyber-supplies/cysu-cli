package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunSelectProjectPersistsValidatedProject(t *testing.T) {
	const projectID = "cysu-dojo-ei2xb"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/projects/"+projectID {
			t.Errorf("path = %s, want /api/projects/%s", r.URL.Path, projectID)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q, want bearer token", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cysu-dojo-ei2xb"}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CYSU_API_URL", server.URL)
	setTestAPIToken(t)

	if err := runSelectProject(projectID); err != nil {
		t.Fatalf("runSelectProject() error = %v", err)
	}

	selected, err := currentProject()
	if err != nil {
		t.Fatalf("currentProject() error = %v", err)
	}
	if selected != projectID {
		t.Fatalf("currentProject() = %q, want %q", selected, projectID)
	}
}

func TestRunSelectProjectDoesNotReplaceSelectionWhenValidationFails(t *testing.T) {
	const existingProject = "existing-project-a1b2c"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Project not found"}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CYSU_API_URL", server.URL)
	setTestAPIToken(t)

	if _, err := selectProject(existingProject); err != nil {
		t.Fatalf("selectProject() error = %v", err)
	}
	if err := runSelectProject("missing-project-z9y8x"); err == nil {
		t.Fatal("runSelectProject() error = nil, want backend validation error")
	}

	selected, err := currentProject()
	if err != nil {
		t.Fatalf("currentProject() error = %v", err)
	}
	if selected != existingProject {
		t.Fatalf("currentProject() = %q, want unchanged %q", selected, existingProject)
	}
}

func setTestAPIToken(t *testing.T) {
	t.Helper()
	previous := apiToken
	apiToken = "test-token"
	t.Cleanup(func() { apiToken = previous })
}
