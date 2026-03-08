package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: build-catalog <plugins-dir> <output-file>\n")
		fmt.Fprintf(os.Stderr, "  scans <plugins-dir>/*/plugin.yaml and writes a combined catalog\n")
		os.Exit(1)
	}

	pluginsDir := os.Args[1]
	outputFile := os.Args[2]

	matches, err := filepath.Glob(filepath.Join(pluginsDir, "*", "plugin.yaml"))
	if err != nil {
		log.Fatalf("glob failed: %v", err)
	}

	sort.Strings(matches)

	var entries []map[string]interface{}
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			log.Printf("skip %s: %v", f, err)
			continue
		}

		var entry map[string]interface{}
		if err := yaml.Unmarshal(data, &entry); err != nil {
			log.Printf("skip %s: parse error: %v", f, err)
			continue
		}

		if _, ok := entry["id"]; !ok {
			log.Printf("skip %s: missing id field", f)
			continue
		}

		entries = append(entries, entry)
	}

	out, err := yaml.Marshal(entries)
	if err != nil {
		log.Fatalf("marshal failed: %v", err)
	}

	if err := os.WriteFile(outputFile, out, 0o644); err != nil {
		log.Fatalf("write failed: %v", err)
	}

	fmt.Printf("catalog: wrote %d plugin(s) to %s\n", len(entries), outputFile)
}
