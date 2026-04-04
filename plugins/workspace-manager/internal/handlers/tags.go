package handlers

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VolumeTagInfo describes what's inside a volume directory.
type VolumeTagInfo struct {
	Tags       []string `json:"tags"`
	GitRemote  string   `json:"git_remote,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

// DetectVolumeTags scans a directory for project type indicators.
func DetectVolumeTags(dir string) VolumeTagInfo {
	info := VolumeTagInfo{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return info
	}

	files := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() {
			files[e.Name()] = true
		} else {
			files[e.Name()+"/"] = true
		}
	}

	// Git remote detection.
	if files[".git/"] {
		if remote := gitRemote(dir); remote != "" {
			info.GitRemote = remote
		}
		info.Tags = append(info.Tags, "git")
	}

	// Go
	if files["go.mod"] {
		info.Tags = append(info.Tags, "go")
	}

	// Node / JS / TS ecosystem
	if files["package.json"] {
		info.Tags = append(info.Tags, "node")
		info.Tags = append(info.Tags, detectNodeFrameworks(dir)...)
	} else if files["tsconfig.json"] {
		info.Tags = append(info.Tags, "typescript")
	}

	// Python
	if files["requirements.txt"] || files["pyproject.toml"] || files["setup.py"] || files["Pipfile"] {
		info.Tags = append(info.Tags, "python")
	}

	// Rust
	if files["Cargo.toml"] {
		info.Tags = append(info.Tags, "rust")
	}

	// Ruby
	if files["Gemfile"] {
		info.Tags = append(info.Tags, "ruby")
	}

	// Java / Kotlin
	if files["pom.xml"] || files["build.gradle"] || files["build.gradle.kts"] {
		info.Tags = append(info.Tags, "java")
	}

	// PHP
	if files["composer.json"] {
		info.Tags = append(info.Tags, "php")
	}

	// Docker
	if files["Dockerfile"] || files["docker-compose.yml"] || files["docker-compose.yaml"] {
		info.Tags = append(info.Tags, "docker")
	}

	// Static HTML
	if files["index.html"] && !files["package.json"] {
		info.Tags = append(info.Tags, "html")
	}

	// Detect installed code-server extensions.
	info.Extensions = detectExtensions(dir)

	return info
}

// detectExtensions scans .code-server/extensions/ for installed extensions.
// Each extension is a directory named like "publisher.name-version".
func detectExtensions(dir string) []string {
	extDir := filepath.Join(dir, ".code-server", "extensions")
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return nil
	}

	var exts []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Strip version suffix: "ms-python.python-2024.1.0" → "ms-python.python"
		if idx := strings.LastIndex(name, "-"); idx > 0 {
			// Verify the suffix looks like a version (starts with digit).
			suffix := name[idx+1:]
			if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
				name = name[:idx]
			}
		}
		exts = append(exts, name)
	}
	return exts
}

// detectNodeFrameworks peeks into package.json dependencies to identify frameworks.
func detectNodeFrameworks(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	// Merge deps for lookup.
	all := make(map[string]bool)
	for k := range pkg.Dependencies {
		all[k] = true
	}
	for k := range pkg.DevDependencies {
		all[k] = true
	}

	var tags []string

	// TypeScript
	if all["typescript"] {
		tags = append(tags, "typescript")
	}

	// React
	if all["react"] {
		tags = append(tags, "react")
		if all["next"] || all["next/core"] {
			tags = append(tags, "next")
		}
	}

	// Vue
	if all["vue"] {
		tags = append(tags, "vue")
		if all["nuxt"] || all["nuxt3"] {
			tags = append(tags, "nuxt")
		}
	}

	// Svelte
	if all["svelte"] {
		tags = append(tags, "svelte")
	}

	// Angular
	if all["@angular/core"] {
		tags = append(tags, "angular")
	}

	// Vite
	if all["vite"] {
		tags = append(tags, "vite")
	}

	return tags
}

// gitRemote extracts the origin remote URL from a git repo.
func gitRemote(dir string) string {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
