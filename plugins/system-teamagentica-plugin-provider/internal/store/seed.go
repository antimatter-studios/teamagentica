package store

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultManifestDir is where the image keeps the manifests it ships with. The
// provider is built from the same commit as the plugins it advertises, so these
// files describe exactly the images that commit produces.
const DefaultManifestDir = "/usr/local/share/teamagentica/manifests"

// SeedManifests loads every *.yaml in dir into the catalog.
//
// A fresh instance has to be able to install a plugin before anyone can run a
// submit command against it: the catalog was previously empty until something
// POSTed to /manifests, so `tacli core create` had nothing to install from and
// a new instance could not reach its own marketplace.
//
// Upsert is keyed on (plugin_id, version), so re-seeding on every boot is
// idempotent and a manifest already submitted by hand at the same version is
// simply overwritten with the shipped copy. Manifests submitted at a *newer*
// version than the image ships are left alone, because they sort above these.
//
// A missing directory is not an error: a provider image built without manifests
// still serves whatever is submitted to it.
func (s *Store) SeedManifests(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read manifest dir: %w", err)
	}

	var seeded int
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read %s: %w", e.Name(), err)
			}
			continue
		}

		// yaml.v3 decodes into map[string]interface{} directly, unlike yaml.v2,
		// so the manifest needs no conversion pass before being stored as JSON.
		var manifest map[string]interface{}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("parse %s: %w", e.Name(), err)
			}
			continue
		}

		pluginID, _ := manifest["id"].(string)
		version, _ := manifest["version"].(string)
		if pluginID == "" || version == "" {
			// Not a plugin manifest, or one the catalog cannot key on. Skipping
			// beats failing the boot of a provider whose other manifests are fine.
			continue
		}

		if err := s.Upsert(pluginID, version, manifest); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("store %s: %w", pluginID, err)
			}
			continue
		}
		seeded++
	}

	return seeded, firstErr
}
