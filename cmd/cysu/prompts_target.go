package main

// prompts_target.go merges a profile's prompts into a marked section of the
// provider instruction file. Other instructions remain outside CYSU's section.

import (
	"bytes"
	"os"
	"path/filepath"
)

// profilePromptPath locates the provider instruction file for a scope root.
func profilePromptPath(provider, root string) string {
	name := "AGENTS.md"
	if provider == skillProviderClaude {
		name = "CLAUDE.md"
	}
	return filepath.Join(root, name)
}

// promptMarkers delimit this provider and scope's managed instructions.
func promptMarkers(provider, scope string) ([]byte, []byte) {
	label := provider + " " + scope
	return []byte("<!-- BEGIN CYSU PROMPTS " + label + " -->"),
		[]byte("<!-- END CYSU PROMPTS " + label + " -->")
}

// promptSectionRange locates one complete section and rejects damaged markers.
func promptSectionRange(raw []byte, provider, scope string) (int, int, bool, error) {
	begin, end := promptMarkers(provider, scope)
	starts, ends := bytes.Count(raw, begin), bytes.Count(raw, end)
	if starts == 0 && ends == 0 {
		return 0, 0, false, nil
	}
	if starts != 1 || ends != 1 {
		return 0, 0, false, failf("invalid CYSU prompt section: expected one matching marker pair")
	}
	start := bytes.Index(raw, begin)
	endAt := bytes.Index(raw, end)
	if endAt < start+len(begin) {
		return 0, 0, false, failf("invalid CYSU prompt section: markers are out of order")
	}
	endAt += len(end)
	if endAt < len(raw) && raw[endAt] == '\n' {
		endAt++
	}
	return start, endAt, true, nil
}

// promptSection renders the concatenated prompt text inside section markers.
func promptSection(provider, scope string, content []byte) ([]byte, error) {
	begin, end := promptMarkers(provider, scope)
	if bytes.Contains(content, begin) || bytes.Contains(content, end) {
		return nil, failf("prompt content contains a CYSU section marker")
	}
	var result bytes.Buffer
	result.Write(begin)
	result.WriteByte('\n')
	result.Write(content)
	result.Write(end)
	result.WriteByte('\n')
	return result.Bytes(), nil
}

// readPromptInstruction reads a regular file without following a leaf symlink.
func readPromptInstruction(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, 0o600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, failf("instruction file %s must be a regular file", path)
	}
	raw, err := os.ReadFile(path)
	return raw, info.Mode().Perm(), err
}

// mergePromptSection replaces or removes CYSU's section in existing content.
func mergePromptSection(raw []byte, provider, scope string, prompts []storedPrompt) ([]byte, error) {
	start, end, exists, err := promptSectionRange(raw, provider, scope)
	if err != nil {
		return nil, err
	}
	if len(prompts) == 0 {
		if !exists {
			return bytes.Clone(raw), nil
		}
		if start >= 2 && bytes.Equal(raw[start-2:start], []byte("\n\n")) {
			start -= 2
		}
		return append(bytes.Clone(raw[:start]), raw[end:]...), nil
	}
	section, err := promptSection(provider, scope, concatenatePrompts(prompts))
	if err != nil {
		return nil, err
	}
	if exists {
		return append(append(bytes.Clone(raw[:start]), section...), raw[end:]...), nil
	}
	output := bytes.Clone(raw)
	if len(output) != 0 {
		output = append(output, '\n', '\n')
	}
	return append(output, section...), nil
}

// restorePromptSection restores the prior managed section while preserving
// instructions added outside it after profile installation.
func restorePromptSection(current, original []byte, provider, scope string) ([]byte, error) {
	start, end, exists, err := promptSectionRange(current, provider, scope)
	if err != nil {
		return nil, err
	}
	oldStart, oldEnd, hadOld, err := promptSectionRange(original, provider, scope)
	if err != nil {
		return nil, err
	}
	if !exists {
		if hadOld {
			output := bytes.Clone(current)
			if len(output) != 0 {
				output = append(output, '\n', '\n')
			}
			return append(output, original[oldStart:oldEnd]...), nil
		}
		return bytes.Clone(current), nil
	}
	if hadOld {
		return append(append(bytes.Clone(current[:start]), original[oldStart:oldEnd]...), current[end:]...), nil
	}
	if start >= 2 && bytes.Equal(current[start-2:start], []byte("\n\n")) {
		start -= 2
	}
	return append(bytes.Clone(current[:start]), current[end:]...), nil
}
