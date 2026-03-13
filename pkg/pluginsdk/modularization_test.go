package pluginsdk_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test file to find the repository root (has go.work).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.work found)")
		}
		dir = parent
	}
}

// pluginDirs returns all directories under plugins/ that contain a go.mod.
func pluginDirs(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		modPath := filepath.Join(root, "plugins", e.Name(), "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			dirs = append(dirs, filepath.Join(root, "plugins", e.Name()))
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no plugin directories found")
	}
	return dirs
}

func TestNoReplaceDirectives(t *testing.T) {
	for _, dir := range pluginDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk") {
				t.Errorf("go.mod still contains a replace directive for pluginsdk")
			}
		})
	}
}

func TestSDKVersionIsV1(t *testing.T) {
	for _, dir := range pluginDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if !strings.Contains(content, "github.com/antimatter-studios/teamagentica/pkg/pluginsdk v1.") {
				t.Errorf("go.mod does not reference pluginsdk v1.x")
			}
		})
	}
}

func TestDockerfilesNoSDKCopy(t *testing.T) {
	for _, dir := range pluginDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
			if err != nil {
				if os.IsNotExist(err) {
					t.Skip("no Dockerfile")
				}
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "COPY pkg/pluginsdk/") {
				t.Errorf("Dockerfile still copies pkg/pluginsdk/ — should use go mod download")
			}
		})
	}
}

func TestDockerfilesUsePluginOnlyContext(t *testing.T) {
	for _, dir := range pluginDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
			if err != nil {
				if os.IsNotExist(err) {
					t.Skip("no Dockerfile")
				}
				t.Fatal(err)
			}
			content := string(data)
			// Should not reference plugins/{name}/ in COPY — context is the plugin dir itself.
			copyPattern := "COPY plugins/" + name + "/"
			if strings.Contains(content, copyPattern) {
				t.Errorf("Dockerfile still uses repo-root COPY pattern: %s", copyPattern)
			}
		})
	}
}

func TestTaskfilesUseLocalContext(t *testing.T) {
	for _, dir := range pluginDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "Taskfile.yml"))
			if err != nil {
				if os.IsNotExist(err) {
					t.Skip("no Taskfile")
				}
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "{{.ROOT_DIR}}") {
				t.Errorf("Taskfile still uses {{.ROOT_DIR}} as docker build context")
			}
		})
	}
}

func TestDevMountTargetsApp(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "kernel", "internal", "runtime", "docker.go"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// The dev mode mount should target /app, not /app/plugins/{name}.
	// Look for the bind mount of plugin source in dev mode section.
	if strings.Contains(content, `Target: filepath.Join("/app/plugins"`) {
		t.Errorf("kernel docker.go still mounts plugin source to /app/plugins/{name} — should mount to /app")
	}

	if !strings.Contains(content, `Target: "/app"`) {
		t.Errorf("kernel docker.go does not mount plugin source to /app")
	}

	// SDK should NOT be mounted separately — go mod download handles it.
	// Count occurrences of /app/pkg/pluginsdk mount target.
	if strings.Contains(content, `"/app/pkg/pluginsdk"`) {
		t.Errorf("kernel docker.go still mounts SDK separately — should rely on go mod cache")
	}
}
