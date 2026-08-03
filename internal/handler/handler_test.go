package handler

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/qtopie/blackhole/internal/album"
	"github.com/qtopie/blackhole/internal/config"
	"github.com/qtopie/blackhole/internal/downloader"
	"github.com/qtopie/blackhole/internal/store"
)

func setupTestRouter(t *testing.T) (*gin.Engine, string, *album.Manager) {
	tempDir, err := os.MkdirTemp("", "blackhole_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := config.LoadConfig()
	cfg.ShareDir = tempDir
	cfg.AlbumDir = filepath.Join(tempDir, "photos")

	dl := downloader.NewManager(tempDir)
	st := store.NewStore(cfg)
	al := album.NewManager(cfg.AlbumDir, st)
	h := NewHandler(tempDir, dl, al)

	gin.SetMode(gin.TestMode)
	r := gin.New()

	api := r.Group("/api")
	{
		api.GET("/sysinfo", h.GetSysInfo)
		api.POST("/upload", h.Upload)
		api.POST("/upload/check", h.CheckUploadInstant)
		api.POST("/mkdir", h.Mkdir)
		api.POST("/rename", h.Rename)
		api.POST("/delete", h.Delete)
		api.DELETE("/delete", h.Delete)

		api.GET("/album/config", h.GetAlbumConfig)
		api.POST("/album/config", h.UpdateAlbumConfig)
		api.GET("/album/photos", h.ListAlbumPhotos)
		api.POST("/album/scan", h.ScanAlbumPhotos)
		api.POST("/album/photos/:id/favorite", h.TogglePhotoFavorite)
		api.DELETE("/album/photos/:id", h.DeleteAlbumPhoto)
		api.POST("/album/photos/batch-delete", h.BatchDeleteAlbumPhotos)
		api.GET("/album/photos/:id/file", h.GetPhotoFile)
		api.GET("/album/photos/:id/thumbnail", h.GetPhotoThumbnail)
	}

	ui := r.Group("/ui")
	{
		ui.GET("/*path", h.RenderWebUI)
	}

	return r, tempDir, al
}

func TestMkdirAndRenameAndDelete(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	body, _ := json.Marshal(map[string]string{"path": "test_folder/sub_folder"})
	req := httptest.NewRequest("POST", "/api/mkdir", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for mkdir, got %d: %s", w.Code, w.Body.String())
	}

	createdPath := filepath.Join(tempDir, "test_folder", "sub_folder")
	if fi, err := os.Stat(createdPath); err != nil || !fi.IsDir() {
		t.Fatalf("expected directory at %s, got error: %v", createdPath, err)
	}

	renameBody, _ := json.Marshal(map[string]string{
		"old_path": "test_folder/sub_folder",
		"new_path": "test_folder/renamed_folder",
	})
	req = httptest.NewRequest("POST", "/api/rename", bytes.NewBuffer(renameBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for rename, got %d: %s", w.Code, w.Body.String())
	}

	deleteBody, _ := json.Marshal(map[string]string{"path": "test_folder"})
	req = httptest.NewRequest("POST", "/api/delete", bytes.NewBuffer(deleteBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for delete, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFileUploadAndReupload(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	part.Write([]byte("initial content"))
	writer.Close()

	req := httptest.NewRequest("POST", "/api/upload?path=hello.txt", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for upload, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRenderWebUI(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	os.WriteFile(filepath.Join(tempDir, "sample.txt"), []byte("sample"), 0644)
	os.Mkdir(filepath.Join(tempDir, "sample_dir"), 0755)

	req := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Web UI, got %d", w.Code)
	}

	html := w.Body.String()
	if !strings.Contains(html, "Blackhole NAS") {
		t.Errorf("expected title in Web UI HTML")
	}
	if !strings.Contains(html, "tabAlbum") {
		t.Errorf("expected album tab in Web UI HTML")
	}
}

func TestAlbumAPIEndpoints(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	// 1. Get Album Config
	req := httptest.NewRequest("GET", "/api/album/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "album_dir") {
		t.Fatalf("expected 200 OK for album config, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Scan Album
	req = httptest.NewRequest("POST", "/api/album/scan", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for album scan, got %d: %s", w.Code, w.Body.String())
	}

	// 3. List Photos
	req = httptest.NewRequest("GET", "/api/album/photos", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "photos") {
		t.Fatalf("expected 200 OK for list photos, got %d: %s", w.Code, w.Body.String())
	}

	// 4. List Photos with Pagination
	req = httptest.NewRequest("GET", "/api/album/photos?page=1&limit=10", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "total_pages") {
		t.Fatalf("expected 200 OK for paged photos, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadCheckInstant(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	// Create existing file in shareDir
	existingPath := filepath.Join(tempDir, "existing.jpg")
	_ = os.WriteFile(existingPath, []byte("hello instant upload"), 0644)

	// Check pre-upload for existing file
	body, _ := json.Marshal(map[string]interface{}{
		"filename": "existing.jpg",
		"size":     20,
		"hash":     "test_hash",
		"path":     "existing.jpg",
	})
	req := httptest.NewRequest("POST", "/api/upload/check", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "hit") {
		t.Fatalf("expected instant upload hit, got %d: %s", w.Code, w.Body.String())
	}
}

func createTestJPEG(filePath string, width, height int, isDark bool) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var c color.Color = color.RGBA{R: 240, G: 240, B: 240, A: 255}
	if isDark {
		c = color.RGBA{R: 2, G: 2, B: 2, A: 255}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
}

func TestPhotoViewerAndPaginationUI(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	photosDir := filepath.Join(tempDir, "photos", "xs20")
	_ = os.MkdirAll(photosDir, 0755)
	_ = createTestJPEG(filepath.Join(photosDir, "DSCF0001.JPG"), 100, 100, false)
	_ = createTestJPEG(filepath.Join(photosDir, "DSCF0002.JPG"), 100, 100, false)

	// Scan album
	scanReq := httptest.NewRequest("POST", "/api/album/scan", nil)
	scanW := httptest.NewRecorder()
	r.ServeHTTP(scanW, scanReq)
	if scanW.Code != http.StatusOK {
		t.Fatalf("expected 200 for scan, got %d", scanW.Code)
	}

	// Test GET /ui/photos/
	req := httptest.NewRequest("GET", "/ui/photos/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "xs20") {
		t.Fatalf("expected 200 and xs20 folder in HTML, got %d", w.Code)
	}

	// Test GET /ui/photos/xs20/
	req = httptest.NewRequest("GET", "/ui/photos/xs20/", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "DSCF0001.JPG") {
		t.Fatalf("expected 200 and DSCF0001.JPG in HTML, got %d", w.Code)
	}

	// Verify pagination bar elements in HTML
	html := w.Body.String()
	if !strings.Contains(html, "filePaginationBar") || !strings.Contains(html, "searchInput") {
		t.Fatalf("expected filePaginationBar and searchInput in HTML")
	}
}

func TestDarkPhotoDetectionAndBatchDelete(t *testing.T) {
	r, tempDir, al := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	photosDir := filepath.Join(tempDir, "photos", "xs20")
	_ = os.MkdirAll(photosDir, 0755)
	darkPhotoPath := filepath.Join(photosDir, "DARK0001.JPG")
	_ = createTestJPEG(darkPhotoPath, 100, 100, true)

	// Scan album
	scanReq := httptest.NewRequest("POST", "/api/album/scan", nil)
	scanW := httptest.NewRecorder()
	r.ServeHTTP(scanW, scanReq)

	// Generate thumbnail and compute luminance
	photos, _ := al.ListPhotos(false)
	for _, p := range photos {
		_, _ = al.EnsureThumbnail(p.Path, p.ID)
	}

	// List photos with dark filter
	req := httptest.NewRequest("GET", "/api/album/photos?dark=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for dark photo list, got %d", w.Code)
	}

	var resp struct {
		Photos []struct {
			ID     string `json:"id"`
			IsDark bool   `json:"is_dark"`
		} `json:"photos"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Photos) == 0 {
		t.Fatalf("expected at least 1 dark photo, got 0")
	}

	darkPhotoID := resp.Photos[0].ID

	// Batch delete photo
	deleteBody, _ := json.Marshal(map[string]interface{}{
		"photo_ids": []string{darkPhotoID},
	})
	req = httptest.NewRequest("POST", "/api/album/photos/batch-delete", bytes.NewBuffer(deleteBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "success") {
		t.Fatalf("expected 200 OK for batch delete, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(darkPhotoPath); err == nil {
		t.Fatalf("expected dark photo file to be deleted from disk")
	}
}
