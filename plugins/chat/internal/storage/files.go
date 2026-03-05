package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStore manages file storage on disk.
type FileStore struct {
	uploadsDir   string
	responsesDir string
}

func NewFileStore(dataPath string) (*FileStore, error) {
	uploadsDir := filepath.Join(dataPath, "uploads")
	responsesDir := filepath.Join(dataPath, "responses")
	for _, dir := range []string{uploadsDir, responsesDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating dir %s: %w", dir, err)
		}
	}
	return &FileStore{uploadsDir: uploadsDir, responsesDir: responsesDir}, nil
}

func (fs *FileStore) SaveUpload(fileID, ext string, r io.Reader) (string, error) {
	filename := fileID + ext
	path := filepath.Join(fs.uploadsDir, filename)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return filename, nil
}

func (fs *FileStore) SaveResponse(fileID, ext string, r io.Reader) (string, error) {
	filename := fileID + ext
	path := filepath.Join(fs.responsesDir, filename)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return filename, nil
}

// LoadFile looks in both uploads and responses dirs. Returns the full path.
func (fs *FileStore) LoadFile(fileID string) (string, error) {
	// Check uploads first.
	matches, _ := filepath.Glob(filepath.Join(fs.uploadsDir, fileID+".*"))
	if len(matches) > 0 {
		return matches[0], nil
	}
	// Check responses.
	matches, _ = filepath.Glob(filepath.Join(fs.responsesDir, fileID+".*"))
	if len(matches) > 0 {
		return matches[0], nil
	}
	return "", fmt.Errorf("file not found: %s", fileID)
}

// DeleteConversationFiles removes all files associated with given file IDs.
func (fs *FileStore) DeleteFiles(fileIDs []string) {
	for _, id := range fileIDs {
		for _, dir := range []string{fs.uploadsDir, fs.responsesDir} {
			matches, _ := filepath.Glob(filepath.Join(dir, id+".*"))
			for _, m := range matches {
				os.Remove(m)
			}
		}
	}
}
