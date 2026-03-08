package index

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/s3client"
)

// Index is an in-memory metadata cache for fast browsing.
type Index struct {
	mu      sync.RWMutex
	objects map[string]s3client.ObjectMeta
	client  *s3client.Client
}

// BrowseResult represents a filesystem-like directory listing.
type BrowseResult struct {
	Prefix  string              `json:"prefix"`
	Folders []string            `json:"folders"`
	Files   []s3client.ObjectMeta `json:"files"`
}

// New creates a new index backed by the given S3 client.
func New(client *s3client.Client) *Index {
	return &Index{
		objects: make(map[string]s3client.ObjectMeta),
		client:  client,
	}
}

// Warm performs a full ListObjectsV2 scan and populates the cache.
func (idx *Index) Warm(ctx context.Context) error {
	objects, err := idx.client.ListObjects(ctx, "")
	if err != nil {
		return err
	}

	idx.mu.Lock()
	idx.objects = make(map[string]s3client.ObjectMeta, len(objects))
	for _, obj := range objects {
		idx.objects[obj.Key] = obj
	}
	idx.mu.Unlock()

	log.Printf("[index] warmed with %d objects", len(objects))
	return nil
}

// Put adds or updates an object in the cache.
func (idx *Index) Put(key string, meta s3client.ObjectMeta) {
	idx.mu.Lock()
	idx.objects[key] = meta
	idx.mu.Unlock()
}

// Delete removes an object from the cache.
func (idx *Index) Delete(key string) {
	idx.mu.Lock()
	delete(idx.objects, key)
	idx.mu.Unlock()
}

// List returns all objects whose key starts with prefix.
func (idx *Index) List(prefix string) []s3client.ObjectMeta {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var result []s3client.ObjectMeta
	for key, meta := range idx.objects {
		if strings.HasPrefix(key, prefix) {
			result = append(result, meta)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Key < result[j].Key
	})
	return result
}

// Browse returns a filesystem-like view at the given prefix level.
// It returns immediate folders and files, not recursing deeper.
func (idx *Index) Browse(prefix string) BrowseResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	folderSet := make(map[string]struct{})
	var files []s3client.ObjectMeta

	for key, meta := range idx.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		// Get the part after the prefix
		rest := key[len(prefix):]
		if rest == "" {
			continue
		}

		slashIdx := strings.Index(rest, "/")
		if slashIdx == -1 {
			// It's a file at this level
			files = append(files, meta)
		} else {
			// It's inside a subfolder
			folder := prefix + rest[:slashIdx+1]
			folderSet[folder] = struct{}{}
		}
	}

	folders := make([]string, 0, len(folderSet))
	for f := range folderSet {
		folders = append(folders, f)
	}
	sort.Strings(folders)

	sort.Slice(files, func(i, j int) bool {
		return files[i].Key < files[j].Key
	})

	return BrowseResult{
		Prefix:  prefix,
		Folders: folders,
		Files:   files,
	}
}

// Count returns the total number of cached objects.
func (idx *Index) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.objects)
}
