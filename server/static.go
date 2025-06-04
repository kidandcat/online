package server

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type StaticFileManager struct {
	stores map[string]*StaticStore
	mu     sync.RWMutex
}

type StaticStore struct {
	ID      string
	Path    string
	files   map[string][]byte
	created time.Time
	mu      sync.RWMutex
}

func NewStaticFileManager() *StaticFileManager {
	sfm := &StaticFileManager{
		stores: make(map[string]*StaticStore),
	}

	// Clean up expired stores periodically
	go sfm.cleanupExpiredStores()

	return sfm
}

func (sfm *StaticFileManager) cleanupExpiredStores() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sfm.mu.Lock()
		for id, store := range sfm.stores {
			if time.Since(store.created) > 24*time.Hour {
				delete(sfm.stores, id)
			}
		}
		sfm.mu.Unlock()
	}
}

func (sfm *StaticFileManager) CreateStore() *StaticStore {
	sfm.mu.Lock()
	defer sfm.mu.Unlock()

	id := generateStoreID()
	store := &StaticStore{
		ID:      id,
		Path:    "/" + id,
		files:   make(map[string][]byte),
		created: time.Now(),
	}

	sfm.stores[id] = store
	return store
}

func (sfm *StaticFileManager) GetStore(id string) (*StaticStore, bool) {
	sfm.mu.RLock()
	defer sfm.mu.RUnlock()

	store, exists := sfm.stores[id]
	return store, exists
}

func (sfm *StaticFileManager) DeleteStore(id string) {
	sfm.mu.Lock()
	defer sfm.mu.Unlock()

	delete(sfm.stores, id)
}

func (s *StaticStore) AddFile(filename string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Normalize the filename
	filename = strings.TrimPrefix(filename, "/")
	s.files[filename] = content
}

func (s *StaticStore) GetFile(filename string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Normalize the filename
	filename = strings.TrimPrefix(filename, "/")

	// Try exact match first
	content, exists := s.files[filename]
	if exists {
		return content, true
	}

	// Try index.html for directories
	if !strings.Contains(filename, ".") {
		indexPath := filepath.Join(filename, "index.html")
		content, exists = s.files[indexPath]
		if exists {
			return content, true
		}
	}

	return nil, false
}

func (s *StaticStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract the path after the store ID
	path := strings.TrimPrefix(r.URL.Path, s.Path)
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		path = "index.html"
	}

	content, exists := s.GetFile(path)
	if !exists {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Set content type based on file extension
	contentType := getContentType(path)
	w.Header().Set("Content-Type", contentType)

	// Set cache headers
	w.Header().Set("Cache-Control", "public, max-age=3600")

	w.Write(content)
}

func (sfm *StaticFileManager) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(100 << 20) // 100 MB max
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Create new store
	store := sfm.CreateStore()

	// Process all uploaded files
	for _, fileHeaders := range r.MultipartForm.File {
		for _, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				continue
			}
			defer file.Close()

			// Read file content
			content, err := io.ReadAll(file)
			if err != nil {
				continue
			}

			// Add file to store
			store.AddFile(fileHeader.Filename, content)
		}
	}

	// Return the store URL
	response := map[string]string{
		"id":  store.ID,
		"url": fmt.Sprintf("https://%s%s", r.Host, store.Path),
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"%s","url":"%s"}`, response["id"], response["url"])
}

func getContentType(filename string) string {
	ext := filepath.Ext(filename)
	switch ext {
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

func generateStoreID() string {
	return uuid.New().String()[:8]
}
