package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qtopie/blackhole/internal/config"
)

func setupTestServer(t *testing.T) (*Server, string, string) {
	tempShareDir, err := os.MkdirTemp("", "blackhole_share_*")
	if err != nil {
		t.Fatalf("failed to create temp share dir: %v", err)
	}

	tempExtDir, err := os.MkdirTemp("", "blackhole_external_*")
	if err != nil {
		t.Fatalf("failed to create temp external dir: %v", err)
	}

	cfg := config.LoadConfig()
	cfg.ShareDir = tempShareDir
	cfg.PublicDir = filepath.Join(tempShareDir, "public")
	cfg.AlbumDir = filepath.Join(tempShareDir, "photos")
	cfg.BooksDir = filepath.Join(tempShareDir, "books")
	cfg.Username = "admin"
	cfg.Password = "blackhole"

	srv := NewServer(cfg)
	return srv, tempShareDir, tempExtDir
}

// SPEC-ROUTE-001: 匿名用户访问 /public 目录与下载普通文件
func TestPublicRoute_AnonymousAccess(t *testing.T) {
	srv, shareDir, extDir := setupTestServer(t)
	defer os.RemoveAll(shareDir)
	defer os.RemoveAll(extDir)

	publicDir := filepath.Join(shareDir, "public")
	_ = os.MkdirAll(publicDir, 0755)

	testFile := filepath.Join(publicDir, "hello.txt")
	_ = os.WriteFile(testFile, []byte("Hello Public World!"), 0644)

	// 1. GET /public/ - HTML 列表
	req := httptest.NewRequest("GET", "/public/", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /public/, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "hello.txt") {
		t.Fatalf("expected hello.txt in HTML body, got: %s", body)
	}
	// 确保未写入认证 Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "blackhole_auth" && c.Value == "blackhole" {
			t.Fatalf("anonymous request should NOT receive admin auth cookie")
		}
	}

	// 2. GET /public/hello.txt - 直接下载
	reqFile := httptest.NewRequest("GET", "/public/hello.txt", nil)
	wFile := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wFile, reqFile)

	if wFile.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for file download, got %d", wFile.Code)
	}
	if wFile.Body.String() != "Hello Public World!" {
		t.Fatalf("expected 'Hello Public World!', got '%s'", wFile.Body.String())
	}

	// 3. GET /public/hello.txt?download=1 - 带 Content-Disposition 附件下载
	reqDl := httptest.NewRequest("GET", "/public/hello.txt?download=1", nil)
	wDl := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wDl, reqDl)
	if wDl.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for ?download=1, got %d", wDl.Code)
	}
	cd := wDl.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "hello.txt") {
		t.Fatalf("expected attachment Content-Disposition header, got: %s", cd)
	}
}

// SPEC-ROUTE-002: 匿名用户下载 /public 跨目录符号链接文件及子目录
func TestPublicRoute_SymlinkAccess(t *testing.T) {
	srv, shareDir, extDir := setupTestServer(t)
	defer os.RemoveAll(shareDir)
	defer os.RemoveAll(extDir)

	publicDir := filepath.Join(shareDir, "public")
	_ = os.MkdirAll(publicDir, 0755)

	// 1. 在外部目录创建源文件并建立软链接
	realFilePath := filepath.Join(extDir, "source_document.pdf")
	realContent := "PDF_BINARY_MOCK_CONTENT_12345"
	_ = os.WriteFile(realFilePath, []byte(realContent), 0644)

	symlinkPath := filepath.Join(publicDir, "public_doc.pdf")
	if err := os.Symlink(realFilePath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// 匿名 GET 请求该软链接文件
	req := httptest.NewRequest("GET", "/public/public_doc.pdf", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for symlink file download, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != realContent {
		t.Fatalf("expected content '%s', got '%s'", realContent, w.Body.String())
	}

	// 2. 软链接外部目录
	realFolder := filepath.Join(extDir, "shared_library")
	_ = os.MkdirAll(realFolder, 0755)
	_ = os.WriteFile(filepath.Join(realFolder, "subfile.txt"), []byte("Subfile in Symlink Folder"), 0644)

	symlinkFolder := filepath.Join(publicDir, "shared_library")
	if err := os.Symlink(realFolder, symlinkFolder); err != nil {
		t.Fatalf("failed to create symlink dir: %v", err)
	}

	// 匿名 GET 目录列表
	reqDir := httptest.NewRequest("GET", "/public/shared_library/", nil)
	wDir := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wDir, reqDir)

	if wDir.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for symlink dir listing, got %d: %s", wDir.Code, wDir.Body.String())
	}
	if !strings.Contains(wDir.Body.String(), "subfile.txt") {
		t.Fatalf("expected subfile.txt in dir list HTML")
	}

	// 匿名 GET 软链接子文件
	reqSub := httptest.NewRequest("GET", "/public/shared_library/subfile.txt", nil)
	wSub := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wSub, reqSub)

	if wSub.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for subfile download, got %d", wSub.Code)
	}
	if wSub.Body.String() != "Subfile in Symlink Folder" {
		t.Fatalf("expected 'Subfile in Symlink Folder', got '%s'", wSub.Body.String())
	}
}

// SPEC-ROUTE-003: 匿名用户在 /public 下的写操作被 401 拦截
func TestPublicRoute_AnonymousWriteForbidden(t *testing.T) {
	srv, shareDir, extDir := setupTestServer(t)
	defer os.RemoveAll(shareDir)
	defer os.RemoveAll(extDir)

	publicDir := filepath.Join(shareDir, "public")
	_ = os.MkdirAll(publicDir, 0755)

	// 1. 匿名尝试上传
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "hack.txt")
	_, _ = io.WriteString(fw, "malicious payload")
	_ = mw.Close()

	reqUpload := httptest.NewRequest("POST", "/api/upload?path=public/hack.txt", &b)
	reqUpload.Header.Set("Content-Type", mw.FormDataContentType())
	wUpload := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wUpload, reqUpload)

	if wUpload.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for anonymous upload, got %d", wUpload.Code)
	}

	// 验证文件未被创建
	if _, err := os.Stat(filepath.Join(publicDir, "hack.txt")); err == nil {
		t.Fatalf("unauthorized file was unexpectedly created!")
	}

	// 2. 匿名尝试新建文件夹
	mkdirBody, _ := json.Marshal(map[string]string{"path": "public/newdir"})
	reqMkdir := httptest.NewRequest("POST", "/api/mkdir", bytes.NewBuffer(mkdirBody))
	reqMkdir.Header.Set("Content-Type", "application/json")
	wMkdir := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wMkdir, reqMkdir)

	if wMkdir.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for anonymous mkdir, got %d", wMkdir.Code)
	}

	// 3. 匿名尝试删除
	deleteBody, _ := json.Marshal(map[string]string{"path": "public/hello.txt"})
	reqDelete := httptest.NewRequest("POST", "/api/delete", bytes.NewBuffer(deleteBody))
	reqDelete.Header.Set("Content-Type", "application/json")
	wDelete := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wDelete, reqDelete)

	if wDelete.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for anonymous delete, got %d", wDelete.Code)
	}
}

// SPEC-ROUTE-004: 匿名用户访问根路径 / 及私有目录被 401 拦截
func TestPublicRoute_PrivateAccessBlocked(t *testing.T) {
	srv, shareDir, extDir := setupTestServer(t)
	defer os.RemoveAll(shareDir)
	defer os.RemoveAll(extDir)

	// 创建私有目录与文件
	privateDir := filepath.Join(shareDir, "private")
	_ = os.MkdirAll(privateDir, 0755)
	_ = os.WriteFile(filepath.Join(privateDir, "secret.txt"), []byte("TOP_SECRET"), 0644)

	// 1. 匿名 GET / - 私有根目录
	reqRoot := httptest.NewRequest("GET", "/", nil)
	wRoot := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wRoot, reqRoot)
	if wRoot.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous / access, got %d", wRoot.Code)
	}

	// 2. 匿名 GET /private/
	reqPriv := httptest.NewRequest("GET", "/private/", nil)
	wPriv := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wPriv, reqPriv)
	if wPriv.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous /private/ access, got %d", wPriv.Code)
	}

	// 3. 匿名 GET 路径穿越 /public/../private/secret.txt
	reqEscape := httptest.NewRequest("GET", "/public/../private/secret.txt", nil)
	wEscape := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wEscape, reqEscape)
	if wEscape.Code != http.StatusUnauthorized && wEscape.Code != http.StatusBadRequest {
		t.Fatalf("expected 401/400 for escaped path traversal, got %d", wEscape.Code)
	}

	// 4. 匿名 GET /api/sysinfo
	reqSys := httptest.NewRequest("GET", "/api/sysinfo", nil)
	wSys := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wSys, reqSys)
	if wSys.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous /api/sysinfo, got %d", wSys.Code)
	}
}

// SPEC-ROUTE-005: 认证用户直接访问根路径 / 具备全量管理功能
func TestPublicRoute_AuthenticatedAdminAccess(t *testing.T) {
	srv, shareDir, extDir := setupTestServer(t)
	defer os.RemoveAll(shareDir)
	defer os.RemoveAll(extDir)

	publicDir := filepath.Join(shareDir, "public")
	_ = os.MkdirAll(publicDir, 0755)

	// 1. 认证用户访问 GET /
	reqRoot := httptest.NewRequest("GET", "/", nil)
	reqRoot.SetBasicAuth("admin", "blackhole")
	wRoot := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wRoot, reqRoot)

	if wRoot.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for auth GET /, got %d: %s", wRoot.Code, wRoot.Body.String())
	}
	if !strings.Contains(wRoot.Body.String(), "filesTab") {
		t.Fatalf("expected full admin tabs in HTML for authenticated GET /")
	}

	// 2. 认证用户创建目录
	mkdirBody, _ := json.Marshal(map[string]string{"path": "public/admin_folder"})
	reqMkdir := httptest.NewRequest("POST", "/api/mkdir", bytes.NewBuffer(mkdirBody))
	reqMkdir.Header.Set("Content-Type", "application/json")
	reqMkdir.SetBasicAuth("admin", "blackhole")
	wMkdir := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wMkdir, reqMkdir)

	if wMkdir.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for auth mkdir, got %d: %s", wMkdir.Code, wMkdir.Body.String())
	}
	if fi, err := os.Stat(filepath.Join(publicDir, "admin_folder")); err != nil || !fi.IsDir() {
		t.Fatalf("expected admin_folder to exist")
	}

	// 3. 认证用户上传文件
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "authorized.txt")
	_, _ = io.WriteString(fw, "authorized content")
	_ = mw.Close()

	reqUpload := httptest.NewRequest("POST", "/api/upload?path=public/authorized.txt", &b)
	reqUpload.Header.Set("Content-Type", mw.FormDataContentType())
	reqUpload.SetBasicAuth("admin", "blackhole")
	wUpload := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wUpload, reqUpload)

	if wUpload.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for auth upload, got %d: %s", wUpload.Code, wUpload.Body.String())
	}

	if fi, err := os.Stat(filepath.Join(publicDir, "authorized.txt")); err != nil || fi.Size() == 0 {
		t.Fatalf("expected authorized.txt to be uploaded")
	}

	// 4. 认证用户删除文件
	delBody, _ := json.Marshal(map[string]string{"path": "public/authorized.txt"})
	reqDel := httptest.NewRequest("POST", "/api/delete", bytes.NewBuffer(delBody))
	reqDel.Header.Set("Content-Type", "application/json")
	reqDel.SetBasicAuth("admin", "blackhole")
	wDel := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(wDel, reqDel)

	if wDel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for auth delete, got %d", wDel.Code)
	}
	if _, err := os.Stat(filepath.Join(publicDir, "authorized.txt")); err == nil {
		t.Fatalf("expected authorized.txt to be deleted")
	}
}
