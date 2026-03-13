package handlers

import (
	"archive/tar"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
)

type buildRequest struct {
	Volume     string `json:"volume" binding:"required"`
	Dockerfile string `json:"dockerfile"`
	Image      string `json:"image" binding:"required"`
	Tag        string `json:"tag"`
}

// Build handles POST /build — builds a Docker image from a volume's source.
// Streams NDJSON: {"stream":"..."} lines + final {"result":{...}} or {"error":"..."}.
func (h *Handler) Build(c *gin.Context) {
	var req buildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.mu.Lock()
	if h.building {
		h.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "a build is already in progress"})
		return
	}
	h.building = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.building = false
		h.mu.Unlock()
	}()

	if req.Dockerfile == "" {
		req.Dockerfile = "Dockerfile"
	}
	if req.Tag == "" {
		req.Tag = time.Now().Format("20060102-150405")
	}

	imageTag := req.Image + ":" + req.Tag

	// Resolve volume path — volumes are at /workspaces/volumes/{name}.
	volumePath := filepath.Join("/workspaces", "volumes", req.Volume)
	if _, err := os.Stat(volumePath); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "volume not found: " + req.Volume})
		return
	}

	dockerfilePath := filepath.Join(volumePath, req.Dockerfile)
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dockerfile not found: " + req.Dockerfile})
		return
	}

	buildID := generateBuildID()
	h.addBuild(BuildRecord{
		ID:         buildID,
		Image:      req.Image,
		Tag:        req.Tag,
		Volume:     req.Volume,
		Dockerfile: req.Dockerfile,
		Status:     "building",
		StartedAt:  time.Now().Format(time.RFC3339),
	})

	// Connect to Docker daemon.
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		h.updateBuild(buildID, func(b *BuildRecord) {
			b.Status = "failed"
			b.Error = "docker client: " + err.Error()
		})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to connect to Docker: " + err.Error()})
		return
	}
	defer cli.Close()

	// Create tar archive of the build context.
	contextReader, err := createTarContext(volumePath)
	if err != nil {
		h.updateBuild(buildID, func(b *BuildRecord) {
			b.Status = "failed"
			b.Error = "tar context: " + err.Error()
		})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create build context: " + err.Error()})
		return
	}

	start := time.Now()

	buildResp, err := cli.ImageBuild(c.Request.Context(), contextReader, types.ImageBuildOptions{
		Tags:        []string{imageTag},
		Dockerfile:  req.Dockerfile,
		Target:      "prod",
		Remove:      true,
		ForceRemove: true,
	})
	if err != nil {
		h.updateBuild(buildID, func(b *BuildRecord) {
			b.Status = "failed"
			b.Error = err.Error()
			b.DurationMs = time.Since(start).Milliseconds()
		})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "docker build failed: " + err.Error()})
		return
	}
	defer buildResp.Body.Close()

	// Stream NDJSON output.
	c.Header("Content-Type", "application/x-ndjson")
	c.Status(http.StatusOK)

	flusher, canFlush := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(buildResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var allLogs strings.Builder
	var buildErr string

	for scanner.Scan() {
		line := scanner.Text()
		allLogs.WriteString(line)
		allLogs.WriteByte('\n')

		// Parse Docker build output to detect errors.
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			if msg.Error != "" {
				buildErr = msg.Error
			}
		}

		fmt.Fprintln(c.Writer, line)
		if canFlush {
			flusher.Flush()
		}
	}

	duration := time.Since(start).Milliseconds()

	if buildErr != "" {
		h.updateBuild(buildID, func(b *BuildRecord) {
			b.Status = "failed"
			b.Error = buildErr
			b.DurationMs = duration
			b.Logs = allLogs.String()
		})
		result, _ := json.Marshal(gin.H{"error": buildErr, "build_id": buildID, "duration_ms": duration})
		fmt.Fprintln(c.Writer, string(result))
	} else {
		h.updateBuild(buildID, func(b *BuildRecord) {
			b.Status = "success"
			b.DurationMs = duration
			b.Logs = allLogs.String()
		})
		result, _ := json.Marshal(gin.H{
			"result": gin.H{
				"build_id":    buildID,
				"image":       imageTag,
				"duration_ms": duration,
				"status":      "success",
			},
		})
		fmt.Fprintln(c.Writer, string(result))
	}

	if canFlush {
		flusher.Flush()
	}

	log.Printf("build %s completed: image=%s status=%s duration=%dms", buildID, imageTag, func() string {
		if buildErr != "" {
			return "failed"
		}
		return "success"
	}(), duration)
}

// createTarContext creates a tar archive of the directory for Docker build context.
func createTarContext(dir string) (io.Reader, error) {
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip .git directory to reduce context size.
			if info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}

			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(tw, f)
			return err
		})

		tw.Close()
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()

	return pr, nil
}
