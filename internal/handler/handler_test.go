package handler

import (
	"archive/zip"
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
	"github.com/qtopie/blackhole/internal/book"
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
	cfg.BooksDir = filepath.Join(tempDir, "books")

	dl := downloader.NewManager(tempDir)
	st := store.NewStore(cfg)
	al := album.NewManager(cfg.AlbumDir, st)
	bk := book.NewManager(cfg.BooksDir, st)
	h := NewHandler(tempDir, dl, al, bk)

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

		api.GET("/books/config", h.GetBooksConfig)
		api.POST("/books/config", h.UpdateBooksConfig)
		api.GET("/books/list", h.ListBooks)
		api.POST("/books/scan", h.ScanBooks)
		api.GET("/books/:id/cover", h.GetBookCover)
		api.GET("/books/:id/file", h.GetBookFile)
		api.DELETE("/books/:id", h.DeleteBook)
	}

	r.GET("/", func(c *gin.Context) {
		c.Set("authenticated", true)
		h.RenderWebUI(c)
	})
	r.NoRoute(func(c *gin.Context) {
		c.Set("authenticated", true)
		h.RenderWebUI(c)
	})

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

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Web UI, got %d", w.Code)
	}

	html := w.Body.String()
	if !strings.Contains(html, "Domour Drive") {
		t.Errorf("expected title in Web UI HTML")
	}
	if !strings.Contains(html, "tabAlbum") {
		t.Errorf("expected album tab in Web UI HTML")
	}
	if !strings.Contains(html, "tabBooks") {
		t.Errorf("expected books tab in Web UI HTML")
	}
}

func TestBookAPIEndpoints(t *testing.T) {
	r, tempDir, _ := setupTestRouter(t)
	defer os.RemoveAll(tempDir)

	// 1. Get Books Config
	req := httptest.NewRequest("GET", "/api/books/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "books_dir") {
		t.Fatalf("expected 200 OK for books config, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Write a test epub into the books dir and scan
	booksDir := filepath.Join(tempDir, "books")
	if err := os.MkdirAll(booksDir, 0755); err != nil {
		t.Fatalf("failed to mkdir books dir: %v", err)
	}
	createTestEpubFile(t, filepath.Join(booksDir, "test-book.epub"), "Test Book Title", "Test Author", true)

	req = httptest.NewRequest("POST", "/api/books/scan", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for books scan, got %d: %s", w.Code, w.Body.String())
	}

	// 3. List books with pagination + search
	req = httptest.NewRequest("GET", "/api/books/list?page=1&limit=25", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "total_pages") {
		t.Fatalf("expected 200 OK for books list, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Test Book Title") || !strings.Contains(w.Body.String(), "Test Author") {
		t.Fatalf("expected title/author in books list: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "\"has_cover\":true") {
		t.Fatalf("expected has_cover true in books list: %s", w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/books/list?search=Test+Author", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Test Book Title") {
		t.Fatalf("expected search to match book, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/books/list?search=zzz-none", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Test Book Title") {
		t.Fatalf("expected search with no matches to be empty, got %d: %s", w.Code, w.Body.String())
	}

	// Extract book id from list response
	var listResp struct {
		Books []struct {
			ID string `json:"id"`
		} `json:"books"`
	}
	req = httptest.NewRequest("GET", "/api/books/list", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil || len(listResp.Books) != 1 {
		t.Fatalf("failed to parse books list: %v, body=%s", err, w.Body.String())
	}
	bookID := listResp.Books[0].ID

	// 4. Get book cover (200 with image content-type)
	req = httptest.NewRequest("GET", "/api/books/"+bookID+"/cover", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for book cover, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Fatalf("expected image content-type for cover, got %q", ct)
	}

	// 5. Get book file
	req = httptest.NewRequest("GET", "/api/books/"+bookID+"/file", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for book file, got %d: %s", w.Code, w.Body.String())
	}

	// 6. Delete book, then confirm cascade 404
	req = httptest.NewRequest("DELETE", "/api/books/"+bookID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "success") {
		t.Fatalf("expected 200 OK for book delete, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("DELETE", "/api/books/"+bookID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for second delete, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/books/"+bookID+"/cover", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cover of deleted book, got %d", w.Code)
	}
}

// createTestEpubFile writes a minimal valid epub zip to path.
func createTestEpubFile(t *testing.T, path, title, author string, withCover bool) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	addZip := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("failed to write zip entry %s: %v", name, err)
		}
	}

	addZip("META-INF/container.xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	manifest := `<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>`
	coverMeta := ""
	if withCover {
		manifest = `<item id="cover-image" href="cover.jpg" media-type="image/jpeg"/>` + manifest
		coverMeta = `<meta name="cover" content="cover-image"/>`
	}
	addZip("OEBPS/content.opf", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="BookId">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>`+title+`</dc:title>
    <dc:creator opf:role="aut">`+author+`</dc:creator>
    `+coverMeta+`
  </metadata>
  <manifest>
    `+manifest+`
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`))
	addZip("OEBPS/chapter.xhtml", []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>hi</p></body></html>`))
	if withCover {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for i := 0; i < 32; i++ {
			for j := 0; j < 32; j++ {
				img.Set(i, j, color.RGBA{R: 200, G: 30, B: 30, A: 255})
			}
		}
		var jpegBuf bytes.Buffer
		if err := jpeg.Encode(&jpegBuf, img, nil); err != nil {
			t.Fatalf("failed to encode cover: %v", err)
		}
		addZip("OEBPS/cover.jpg", jpegBuf.Bytes())
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write epub: %v", err)
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

	// Test GET /photos/
	req := httptest.NewRequest("GET", "/photos/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "xs20") {
		t.Fatalf("expected 200 and xs20 folder in HTML, got %d", w.Code)
	}

	// Test GET /photos/xs20/
	req = httptest.NewRequest("GET", "/photos/xs20/", nil)
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
