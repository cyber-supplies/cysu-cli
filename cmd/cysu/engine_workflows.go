package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const workflowSettingsFile = "settings.yaml"

const workflowExampleYAML = `# Edit these settings, then use save-workflow with this folder's name.
# Objectives are supplied when starting a run, not stored in workflows.
id: "" # Assigned by the engine; keep this ID when saving edits.
name: ""
request:
  globalProfile: "" # Optional global profile installed for every stage.
  model: "" # Empty uses the engine's default model.
  reasoningEffort: medium
  continueOnFailure: true
  stages: [] # Add your profile IDs here, in execution order.
  # Example: replace stages: [] with:
  # stages:
  #   - profile: recon
  #     tag: "1"
  #   - profile: attack
  #     tag: "2"
  # Optional fallback model and follow-up prompts:
  # fallbackModel: your-fallback-model
  # reprompts:
  #   - stage: "2"
  #     prompt: Review the results and try another approach.
`

// engineWorkflowSettings is the editable part of a workspace. An absent ID
// creates a new workflow; an existing ID pins saves to that exact workspace.
// Request stays open-ended to preserve advanced and future engine settings.
type engineWorkflowSettings struct {
	ID      string         `json:"id,omitempty" yaml:"id,omitempty"`
	Name    string         `json:"name" yaml:"name"`
	Request map[string]any `json:"request" yaml:"request"`
}

// workflowSettingsPath accepts a single portable folder name, never a path.
func workflowSettingsPath(name string) (string, error) {
	safe, err := safeSkillDirectoryName(name)
	if err != nil || safe != name {
		return "", failf("invalid workflow folder name %q", name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "cysu", "engine", "workflows", name, workflowSettingsFile)
	if err := workflowNoLinks(path); err != nil {
		return "", err
	}
	return path, nil
}

// workflowNoLinks keeps settings reads and replacements inside real folders.
func workflowNoLinks(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return failf("workflow settings do not follow symlinks: %s", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

// workflowFolderName preserves normal workflow labels, replacing only
// characters that cannot be used in a portable directory name.
func workflowFolderName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = "workflow"
	}
	if windowsReservedName(name) {
		name = "workflow-" + name
	}
	return name
}

// validateWorkflowSettings checks the file envelope and objective-free template
// shape. The engine remains responsible for planning drafts and profile access.
func validateWorkflowSettings(settings *engineWorkflowSettings) error {
	if settings.ID != "" {
		if !workflowIDPattern.MatchString(settings.ID) {
			return failf("settings.yaml: id must be a full workflow UUID; omit id to create a workflow")
		}
		settings.ID = strings.ToLower(settings.ID)
	}
	if strings.TrimSpace(settings.Name) == "" || settings.Request == nil {
		return failf("settings.yaml requires a name and a request object (use request: {stages: []} for a draft)")
	}
	for _, key := range []string{"prompt", "objective"} {
		if _, exists := settings.Request[key]; exists {
			return failf("settings.yaml: workflows have no objective; provide --objective when starting a run")
		}
	}
	_, picked := settings.Request["stages"]
	_, tagged := settings.Request["fromTags"]
	if picked && tagged {
		return failf("settings.yaml: send either stages or fromTags, not both")
	}
	if picked {
		stages, ok := settings.Request["stages"].([]any)
		if !ok || len(stages) > 20 {
			return failf("settings.yaml: stages must be a list of at most 20 stages")
		}
		for _, stage := range stages {
			fields, ok := stage.(map[string]any)
			if !ok {
				return failf("settings.yaml: each stage must be an object")
			}
			for _, key := range []string{"prompt", "objective"} {
				if _, exists := fields[key]; exists {
					return failf("settings.yaml: stage objectives belong to runs, not workflows")
				}
			}
		}
	}
	if _, err := json.Marshal(settings.Request); err != nil {
		return fmt.Errorf("settings.yaml contains values that cannot be sent as JSON: %w", err)
	}
	return nil
}

// readWorkflowSettings rejects duplicate keys, unknown envelope fields and
// multi-document YAML, retaining the syntax tree to preserve comments on save.
func readWorkflowSettings(path string) (engineWorkflowSettings, *yaml.Node, error) {
	var settings engineWorkflowSettings
	if err := workflowNoLinks(path); err != nil {
		return settings, nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return settings, nil, fmt.Errorf("reading workflow settings: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&settings); err != nil {
		return settings, nil, fmt.Errorf("reading settings.yaml: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return settings, nil, failf("settings.yaml must contain exactly one YAML document")
	}
	if err := validateWorkflowSettings(&settings); err != nil {
		return settings, nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return settings, nil, err
	}
	return settings, &document, nil
}

// validateWorkflowSave checks local settings before preflight touches the engine.
func validateWorkflowSave(v flagValues) error {
	path, err := workflowSettingsPath(v["name"])
	if err != nil {
		return err
	}
	_, _, err = readWorkflowSettings(path)
	return err
}

// validateWorkflowCreate refuses to overwrite a local folder before preflight.
func validateWorkflowCreate(v flagValues) error {
	path, err := workflowSettingsPath(v["name"])
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Dir(path)); err == nil {
		return failf("workflow folder already exists: %s", filepath.Dir(path))
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// createWorkflow creates an engine draft with editable example settings. The
// new folder is reserved and written before POST so local failures happen first.
func createWorkflow(v flagValues) error {
	path, err := workflowSettingsPath(v["name"])
	if err != nil {
		return err
	}
	root := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return fmt.Errorf("creating workflow folder: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(root)
		}
	}()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(workflowExampleYAML), &document); err != nil {
		return err
	}
	setWorkflowString(&document, "name", v["name"])
	var settings engineWorkflowSettings
	if err := document.Decode(&settings); err != nil {
		return err
	}
	if err := writeWorkflowSettings(path, &document); err != nil {
		return err
	}
	raw, err := engineSend(v, http.MethodPost, "/api/workspaces", map[string]any{
		"name": settings.Name, "request": settings.Request,
	})
	if err != nil {
		return err
	}
	// Once the engine accepts creation, retain the settings even if recording
	// its ID fails, and report the ID so the user can recover the saved workflow.
	keep = true
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || !workflowIDPattern.MatchString(saved.ID) {
		return failf("engine returned an invalid created workflow")
	}
	setWorkflowString(&document, "id", saved.ID)
	if err := writeWorkflowSettings(path, &document); err != nil {
		return failf("workflow created as %s, but updating local settings failed: %v", saved.ID, err)
	}
	return printWorkflowFileResult(saved.ID, path, "created")
}

// writeWorkflowSettings atomically replaces only settings.yaml, preserving any
// other files in the workflow folder. Syntax trees retain comments and ordering.
func writeWorkflowSettings(path string, document any) error {
	if err := workflowNoLinks(path); err != nil {
		return err
	}
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".settings-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(encoded.Bytes()); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// getWorkflow downloads a workspace's editable settings into its named folder.
// If portable names collide, refuse to overwrite a different local workflow.
func getWorkflow(v flagValues) error {
	raw, err := engineSend(v, http.MethodGet, workflowPath(v), nil)
	if err != nil {
		return err
	}
	var settings engineWorkflowSettings
	if err := json.Unmarshal(raw, &settings); err != nil || settings.ID != v["workflow"] {
		return failf("engine returned an invalid workflow")
	}
	if err := validateWorkflowSettings(&settings); err != nil {
		return err
	}
	path, err := workflowSettingsPath(workflowFolderName(settings.Name))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		previous, _, err := readWorkflowSettings(path)
		if err != nil {
			return err
		}
		if previous.ID != settings.ID {
			return failf("workflow folder already contains another or unsaved workflow: %s", filepath.Dir(path))
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeWorkflowSettings(path, settings); err != nil {
		return err
	}
	return printWorkflowFileResult(settings.ID, path, "downloaded")
}

// removeLocalWorkflow finds local folders by the settings ID, independently
// of folder names or editable request validity. Unidentified folders and
// symlinks are left alone; the workflows directory itself is retained.
func removeLocalWorkflow(id string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, "cysu", "engine", "workflows")
	if err := workflowNoLinks(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := filepath.Join(root, entry.Name())
		path := filepath.Join(folder, workflowSettingsFile)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var identity struct {
			ID string `yaml:"id"`
		}
		if yaml.Unmarshal(raw, &identity) != nil || !strings.EqualFold(identity.ID, id) {
			continue
		}
		if err := os.RemoveAll(folder); err != nil {
			return err
		}
	}
	return nil
}

// saveWorkflow creates or replaces a workspace from its local YAML. Record a
// newly assigned ID immediately so subsequent saves update rather than create.
func saveWorkflow(v flagValues) error {
	path, err := workflowSettingsPath(v["name"])
	if err != nil {
		return err
	}
	settings, document, err := readWorkflowSettings(path)
	if err != nil {
		return err
	}
	method, route, action := http.MethodPost, "/api/workspaces", "created"
	if settings.ID != "" {
		method, route, action = http.MethodPatch, "/api/workspaces/"+url.PathEscape(settings.ID), "updated"
	}
	body := map[string]any{"name": settings.Name, "request": settings.Request}
	raw, err := engineSend(v, method, route, body)
	if err != nil {
		return err
	}
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || !workflowIDPattern.MatchString(saved.ID) || (settings.ID != "" && saved.ID != settings.ID) {
		return failf("engine returned an invalid saved workflow")
	}
	setWorkflowString(document, "id", saved.ID)
	if err := writeWorkflowSettings(path, document); err != nil {
		return failf("workflow saved as %s, but updating local settings failed: %v", saved.ID, err)
	}
	return printWorkflowFileResult(saved.ID, path, action)
}

// setWorkflowString changes one envelope value while retaining YAML comments,
// ordering, and all other settings. Missing keys are appended to the mapping.
func setWorkflowString(document *yaml.Node, key, value string) {
	mapping := document.Content[0]
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			node := mapping.Content[i+1]
			node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!str", value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// printWorkflowFileResult gives callers the remote identity and editable path.
func printWorkflowFileResult(id, path, action string) error {
	return printJSON(map[string]any{
		"success": true, "workflow": id, "directory": filepath.Dir(path),
		"settings": path, "action": action,
	})
}
