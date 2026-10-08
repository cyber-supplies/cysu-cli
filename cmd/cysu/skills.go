package main

// skills.go lists and installs the skills in a selected profile.

// Skills are mutated only by saving their complete profile.
// skillDocument is the gateway representation assembled from one SKILL.md.
type skillDocument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Profile     string `json:"profile,omitempty"`
}

// skillMetadata is the part of a SKILL.md read from its YAML front matter.
// Other front-matter fields are intentionally ignored so standard skill files
// containing metadata, license or compatibility keys remain usable.
type skillMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}
