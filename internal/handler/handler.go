package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/qtopie/blackhole/internal/album"
	"github.com/qtopie/blackhole/internal/downloader"
	"github.com/qtopie/blackhole/internal/store"
	"github.com/qtopie/blackhole/internal/vault"
	"github.com/shirou/gopsutil/v3/disk"
	"golang.org/x/net/webdav"
)

type SysInfoResponse struct {
	ShareDir    string `json:"share_dir"`
	TotalGB     uint64 `json:"total_gb"`
	FreeGB      uint64 `json:"free_gb"`
	UsedPercent string `json:"used_percent"`
}

type PathRequest struct {
	Path string `json:"path" form:"path"`
}

type RenameRequest struct {
	OldPath string `json:"old_path" form:"old_path"`
	NewPath string `json:"new_path" form:"new_path"`
	Path    string `json:"path" form:"path"`
	NewName string `json:"new_name" form:"new_name"`
}

type UpdateAlbumConfigReq struct {
	AlbumDir string `json:"album_dir" form:"album_dir"`
}

type Handler struct {
	shareDir      string
	downloadMgr   *downloader.Manager
	webdavHandler *webdav.Handler
	albumMgr      *album.Manager
}

func NewHandler(shareDir string, downloadMgr *downloader.Manager, albumMgr *album.Manager) *Handler {
	return &Handler{
		shareDir:    shareDir,
		downloadMgr: downloadMgr,
		albumMgr:    albumMgr,
		webdavHandler: &webdav.Handler{
			Prefix:     "/shared",
			FileSystem: webdav.Dir(shareDir),
			LockSystem: webdav.NewMemLS(),
		},
	}
}

func (h *Handler) getSafePath(relPath string) (string, error) {
	cleanRel := filepath.Clean("/" + relPath)
	fullPath := filepath.Join(h.shareDir, cleanRel)

	rel, err := filepath.Rel(h.shareDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("非法或超出访问权限范围的路径")
	}
	return fullPath, nil
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func getFileIcon(name string, isDir bool) string {
	if isDir {
		if strings.HasPrefix(name, ".") {
			return "🔒"
		}
		return "📁"
	}
	if strings.HasPrefix(name, ".") {
		return "🔒"
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico":
		return "🖼️"
	case ".mp4", ".mkv", ".webm", ".avi", ".mov":
		return "🎥"
	case ".mp3", ".wav", ".flac", ".aac", ".ogg":
		return "🎵"
	case ".zip", ".tar", ".gz", ".7z", ".rar":
		return "📦"
	case ".pdf":
		return "📕"
	case ".txt", ".md", ".json", ".xml", ".yaml", ".yml", ".go", ".py", ".sh", ".html", ".css", ".js":
		return "📄"
	case ".enc":
		return "🔒"
	default:
		return "📄"
	}
}

func (h *Handler) GetSysInfo(c *gin.Context) {
	usage, err := disk.UsageWithContext(c.Request.Context(), h.shareDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取磁盘容量失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, SysInfoResponse{
		ShareDir:    h.shareDir,
		TotalGB:     usage.Total / (1024 * 1024 * 1024),
		FreeGB:      usage.Free / (1024 * 1024 * 1024),
		UsedPercent: fmt.Sprintf("%.2f%%", usage.UsedPercent),
	})
}

func (h *Handler) GetP2PProgress(c *gin.Context) {
	progressPath := filepath.Join(h.shareDir, ".p2p_progress.json")
	fi, err := os.Stat(progressPath)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"active":  false,
			"message": "当前没有在运行的 P2P 同步任务",
		})
		return
	}

	if time.Since(fi.ModTime()) > 30*time.Second {
		c.JSON(http.StatusOK, gin.H{
			"active":  false,
			"status":  "completed",
			"message": "P2P 同步任务已完成",
		})
		return
	}

	data, err := os.ReadFile(progressPath)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	var res map[string]interface{}
	if err := json.Unmarshal(data, &res); err != nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) VaultUpload(c *gin.Context) {
	vaultPass := c.GetHeader("X-Vault-Password")
	if vaultPass == "" {
		vaultPass = c.PostForm("vault_password")
	}
	if vaultPass == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要提供独立安全文件夹密码 (X-Vault-Password)"})
		return
	}

	relPath := c.Query("path")
	fileHeader, err := c.FormFile("file")
	var rawBytes []byte

	if err == nil {
		if relPath == "" {
			relPath = fileHeader.Filename
		}
		f, err := fileHeader.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer f.Close()
		rawBytes, err = io.ReadAll(f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		rawBytes, err = io.ReadAll(c.Request.Body)
		if err != nil || len(rawBytes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未读取到有效文件数据"})
			return
		}
	}

	if !strings.HasSuffix(relPath, ".enc") {
		relPath += ".enc"
	}

	dstPath, err := h.getSafePath(filepath.Join("vault", relPath))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	encData, err := vault.EncryptData(rawBytes, vaultPass)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("文件加密失败: %v", err)})
		return
	}

	if err := os.WriteFile(dstPath, encData, 0600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"encrypted":  true,
		"saved_path": filepath.Join("vault", relPath),
	})
}

func (h *Handler) VaultDownload(c *gin.Context) {
	vaultPass := c.GetHeader("X-Vault-Password")
	if vaultPass == "" {
		vaultPass = c.Query("vault_password")
	}
	if vaultPass == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要提供独立安全文件夹密码 (X-Vault-Password)"})
		return
	}

	relPath := c.Query("path")
	if relPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing path parameter"})
		return
	}

	targetPath, err := h.getSafePath(filepath.Join("vault", relPath))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := os.Stat(targetPath); os.IsNotExist(err) && !strings.HasSuffix(targetPath, ".enc") {
		targetPath += ".enc"
	}

	encData, err := os.ReadFile(targetPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "安全文件不存在"})
		return
	}

	plainBytes, err := vault.DecryptData(encData, vaultPass)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	outFileName := filepath.Base(strings.TrimSuffix(targetPath, ".enc"))
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", outFileName))
	c.Data(http.StatusOK, "application/octet-stream", plainBytes)
}

func (h *Handler) Upload(c *gin.Context) {
	relPath := c.Query("path")
	if relPath == "" {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing file parameter"})
			return
		}
		relPath = fileHeader.Filename
	}

	dst, err := h.getSafePath(relPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	file, err := c.FormFile("file")
	if err == nil {
		if err := c.SaveUploadedFile(file, dst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		out, err := os.Create(dst)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer out.Close()
		if _, err := io.Copy(out, c.Request.Body); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "saved_path": relPath})
}

type UploadCheckRequest struct {
	Hash     string `json:"hash" form:"hash"`
	Size     int64  `json:"size" form:"size"`
	Filename string `json:"filename" form:"filename"`
	Path     string `json:"path" form:"path"`
}

func (h *Handler) CheckUploadInstant(c *gin.Context) {
	var req UploadCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil && req.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的秒传检测请求"})
		return
	}

	relPath := req.Path
	if relPath == "" {
		relPath = req.Filename
	}

	targetPath, err := h.getSafePath(relPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 1. If target file already exists at targetPath with same size, instant hit!
	if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() && fi.Size() == req.Size {
		c.JSON(http.StatusOK, gin.H{
			"status":     "hit",
			"instant":    true,
			"message":    "⚡ 秒传成功！目标存储已包含相同文件",
			"saved_path": relPath,
		})
		return
	}

	// 2. Check store for matching photo by hash or size
	existingPhoto := h.albumMgr.FindPhotoByHash(req.Hash, req.Size)
	if existingPhoto != nil && existingPhoto.Path != "" {
		if _, err := os.Stat(existingPhoto.Path); err == nil {
			_ = os.MkdirAll(filepath.Dir(targetPath), 0755)
			if err := copyFile(existingPhoto.Path, targetPath); err == nil {
				_, _ = h.albumMgr.ScanDirectory()
				c.JSON(http.StatusOK, gin.H{
					"status":     "hit",
					"instant":    true,
					"message":    "⚡ 秒传成功！通过哈希指纹匹配秒级复用文件",
					"saved_path": relPath,
				})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "miss",
		"instant": false,
		"message": "文件未建立索引，准备传统传输",
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func (h *Handler) Mkdir(c *gin.Context) {
	relPath := c.Query("path")
	if relPath == "" {
		var req PathRequest
		if err := c.ShouldBindJSON(&req); err == nil && req.Path != "" {
			relPath = req.Path
		}
	}
	if relPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "路径参数不能为空"})
		return
	}

	fullPath, err := h.getSafePath(relPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(fullPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("创建目录失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "目录创建成功"})
}

func (h *Handler) Rename(c *gin.Context) {
	oldPath := c.Query("old_path")
	newPath := c.Query("new_path")
	pathParam := c.Query("path")
	newNameParam := c.Query("new_name")

	if oldPath == "" || newPath == "" {
		var req RenameRequest
		if err := c.ShouldBindJSON(&req); err == nil {
			if req.OldPath != "" {
				oldPath = req.OldPath
			}
			if req.NewPath != "" {
				newPath = req.NewPath
			}
			if req.Path != "" {
				pathParam = req.Path
			}
			if req.NewName != "" {
				newNameParam = req.NewName
			}
		}
	}

	if oldPath == "" && pathParam != "" {
		oldPath = pathParam
	}

	if newPath == "" && oldPath != "" && newNameParam != "" {
		dir := filepath.Dir(oldPath)
		if dir == "." {
			dir = ""
		}
		newPath = filepath.Join(dir, newNameParam)
	}

	if oldPath == "" || newPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要提供原路径和新路径/新名称"})
		return
	}

	oldFullPath, err := h.getSafePath(oldPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "源路径非法: " + err.Error()})
		return
	}

	newFullPath, err := h.getSafePath(newPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "目标路径非法: " + err.Error()})
		return
	}

	cleanShareDir := filepath.Clean(h.shareDir)
	if oldFullPath == cleanShareDir {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能重命名根共享目录"})
		return
	}

	if _, err := os.Stat(oldFullPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "源文件或目录不存在"})
		return
	}

	if err := os.Rename(oldFullPath, newFullPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("重命名失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "重命名成功"})
}

func (h *Handler) Delete(c *gin.Context) {
	relPath := c.Query("path")
	if relPath == "" {
		var req PathRequest
		if err := c.ShouldBindJSON(&req); err == nil && req.Path != "" {
			relPath = req.Path
		}
	}
	if relPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "路径参数不能为空"})
		return
	}

	fullPath, err := h.getSafePath(relPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cleanShareDir := filepath.Clean(h.shareDir)
	if fullPath == cleanShareDir {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能删除根共享目录"})
		return
	}

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "目标不存在"})
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("删除失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "删除成功"})
}

func (h *Handler) GetAlbumConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"album_dir": h.albumMgr.GetAlbumDir(),
	})
}

func (h *Handler) UpdateAlbumConfig(c *gin.Context) {
	var req UpdateAlbumConfigReq
	dir := c.Query("album_dir")
	if dir == "" {
		if err := c.ShouldBindJSON(&req); err == nil && req.AlbumDir != "" {
			dir = req.AlbumDir
		}
	}

	if dir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "相册目录不能为空"})
		return
	}

	if err := h.albumMgr.SetAlbumDir(dir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "success",
		"message":   "相册目录更新成功",
		"album_dir": h.albumMgr.GetAlbumDir(),
	})
}

func (h *Handler) ListAlbumPhotos(c *gin.Context) {
	favOnly := c.Query("favorite") == "1" || c.Query("favorite") == "true"
	darkOnly := c.Query("dark") == "1" || c.Query("dark_only") == "true"
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))

	if page < 1 {
		page = 1
	}

	photos, err := h.albumMgr.ListPhotosFiltered(favOnly, darkOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	total := len(photos)
	var pagedPhotos []*store.Photo

	if limit <= 0 {
		pagedPhotos = photos
		limit = total
		if limit == 0 {
			limit = 1
		}
	} else {
		start := (page - 1) * limit
		if start < total {
			end := start + limit
			if end > total {
				end = total
			}
			pagedPhotos = photos[start:end]
		} else {
			pagedPhotos = []*store.Photo{}
		}
	}

	totalPages := 1
	if limit > 0 && total > 0 {
		totalPages = (total + limit - 1) / limit
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      "success",
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
		"count":       len(pagedPhotos),
		"photos":      pagedPhotos,
	})
}

func (h *Handler) ScanAlbumPhotos(c *gin.Context) {
	count, err := h.albumMgr.ScanDirectory()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": fmt.Sprintf("扫描完成，共索引 %d 项媒体", count),
		"count":   count,
	})
}

func (h *Handler) TogglePhotoFavorite(c *gin.Context) {
	id := c.Param("id")
	photo, err := h.albumMgr.ToggleFavorite(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":      "success",
		"is_favorite": photo.IsFavorite,
		"photo":       photo,
	})
}

func (h *Handler) DeleteAlbumPhoto(c *gin.Context) {
	id := c.Param("id")
	if err := h.albumMgr.DeletePhoto(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "照片删除成功",
	})
}

type BatchDeletePhotosRequest struct {
	PhotoIDs []string `json:"photo_ids"`
}

func (h *Handler) BatchDeleteAlbumPhotos(c *gin.Context) {
	var req BatchDeletePhotosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.PhotoIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "photo_ids不能为空"})
		return
	}

	deletedCount, err := h.albumMgr.BatchDeletePhotos(req.PhotoIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":        "success",
		"deleted_count": deletedCount,
	})
}

func (h *Handler) GetPhotoFile(c *gin.Context) {
	id := c.Param("id")
	photos, err := h.albumMgr.ListPhotos(false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, p := range photos {
		if p.ID == id {
			if p.MIMEType != "" {
				c.Header("Content-Type", p.MIMEType)
			}
			c.Header("Accept-Ranges", "bytes")
			http.ServeFile(c.Writer, c.Request, p.Path)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "媒体项目未找到"})
}

func (h *Handler) GetPhotoThumbnail(c *gin.Context) {
	id := c.Param("id")
	photos, err := h.albumMgr.ListPhotos(false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, p := range photos {
		if p.ID == id {
			c.Header("Cache-Control", "public, max-age=86400")
			c.Header("ETag", fmt.Sprintf("\"thumb-%s-%d\"", p.ID, p.Size))

			if p.IsVideo {
				c.Header("Content-Type", p.MIMEType)
				c.Header("Accept-Ranges", "bytes")
				http.ServeFile(c.Writer, c.Request, p.Path)
				return
			}

			thumbPath, err := h.albumMgr.EnsureThumbnail(p.Path, p.ID)
			if err == nil && thumbPath != "" {
				if fi, err := os.Stat(thumbPath); err == nil && fi.Size() > 0 {
					c.Header("Content-Type", "image/jpeg")
					http.ServeFile(c.Writer, c.Request, thumbPath)
					return
				}
			}

			// Fallback: If thumbnail generation fails or is unavailable, serve original image
			if p.MIMEType != "" {
				c.Header("Content-Type", p.MIMEType)
			}
			c.Header("Accept-Ranges", "bytes")
			http.ServeFile(c.Writer, c.Request, p.Path)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "媒体项目未找到"})
}

func (h *Handler) HandleWebDAV(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/shared") {
		h.webdavHandler.ServeHTTP(c.Writer, c.Request)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
}

func selectedOpt(limit, val int) string {
	if limit == val {
		return "selected"
	}
	return ""
}

func (h *Handler) RenderWebUI(c *gin.Context) {
	c.SetCookie("blackhole_auth", "blackhole", 86400*30, "/", "", false, false)
	relPathParam := c.Param("path")
	cleanRel := strings.Trim(relPathParam, "/")
	if cleanRel == "." {
		cleanRel = ""
	}

	fullPath, err := h.getSafePath(cleanRel)
	if err != nil {
		c.String(http.StatusBadRequest, "非法路径: %v", err)
		return
	}

	fi, err := os.Stat(fullPath)
	if err != nil {
		c.String(http.StatusNotFound, "文件或目录不存在")
		return
	}

	if !fi.IsDir() {
		if c.Query("download") == "1" {
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(fullPath)))
		}
		c.File(fullPath)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		c.String(http.StatusInternalServerError, "读取目录失败: %v", err)
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var breadcrumbsHTML strings.Builder
	breadcrumbsHTML.WriteString(`<a href="/ui/" class="crumb" data-i18n="root">🏠 根目录 / Root</a>`)
	if cleanRel != "" {
		parts := strings.Split(cleanRel, "/")
		accum := ""
		for _, part := range parts {
			if part == "" {
				continue
			}
			if accum == "" {
				accum = part
			} else {
				accum += "/" + part
			}
			breadcrumbsHTML.WriteString(fmt.Sprintf(` <span class="crumb-sep">/</span> <a href="/ui/%s/" class="crumb">%s</a>`, accum, part))
		}
	}

	var tableRowsHTML strings.Builder

	if cleanRel != "" {
		parentPath := filepath.Dir(cleanRel)
		if parentPath == "." {
			parentPath = ""
		}
		parentLink := "/ui/"
		if parentPath != "" {
			parentLink = "/ui/" + parentPath + "/"
		}
		tableRowsHTML.WriteString(fmt.Sprintf(`
		<tr class="parent-row">
			<td class="icon-cell">📁</td>
			<td colspan="3"><a href="%s" class="item-link parent-link" data-i18n="parentDir">.. (返回上一级 / Parent)</a></td>
			<td></td>
		</tr>`, parentLink))
	}

	searchQuery := strings.ToLower(strings.TrimSpace(c.Query("q")))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	if page < 1 {
		page = 1
	}

	var filteredEntries []os.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		if searchQuery != "" && !strings.Contains(strings.ToLower(name), searchQuery) {
			continue
		}
		filteredEntries = append(filteredEntries, entry)
	}

	totalItems := len(filteredEntries)
	totalPages := 1
	if limit > 0 && totalItems > 0 {
		totalPages = (totalItems + limit - 1) / limit
	}
	if page > totalPages {
		page = totalPages
	}

	var pagedEntries []os.DirEntry
	if limit <= 0 || totalItems == 0 {
		pagedEntries = filteredEntries
	} else {
		start := (page - 1) * limit
		end := start + limit
		if end > totalItems {
			end = totalItems
		}
		pagedEntries = filteredEntries[start:end]
	}

	filePaginationInfo := fmt.Sprintf("共 %d 项项目", totalItems)
	if totalPages > 1 {
		filePaginationInfo = fmt.Sprintf("共 %d 项项目 | 第 %d / %d 页", totalItems, page, totalPages)
	}

	var filePaginationControls strings.Builder
	if totalPages > 1 || limit > 0 {
		prevPage := page - 1
		if prevPage < 1 {
			prevPage = 1
		}
		nextPage := page + 1
		if nextPage > totalPages {
			nextPage = totalPages
		}

		prevDisabled := ""
		if page <= 1 {
			prevDisabled = "disabled"
		}
		nextDisabled := ""
		if page >= totalPages {
			nextDisabled = "disabled"
		}

		filePaginationControls.WriteString(fmt.Sprintf(`
			<button class="fluent-btn fluent-btn-subtle" onclick="changeFilePage(%d)" %s data-i18n="prevPage">◄ 上一页</button>
			<button class="fluent-btn fluent-btn-subtle" onclick="changeFilePage(%d)" %s data-i18n="nextPage">下一页 ►</button>
			<select id="filePageSizeSelect" class="search-box" style="width: auto; padding: 5px 10px; font-size:12px;" onchange="changeFilePageSize(this.value)">
				<option value="25" %s>25 条/页</option>
				<option value="50" %s>50 条/页</option>
				<option value="100" %s>100 条/页</option>
			</select>
		`, prevPage, prevDisabled, nextPage, nextDisabled,
			selectedOpt(limit, 25), selectedOpt(limit, 50), selectedOpt(limit, 100)))
	}

	for _, entry := range pagedEntries {
		name := entry.Name()
		isDir := entry.IsDir()
		isHidden := strings.HasPrefix(name, ".")
		icon := getFileIcon(name, isDir)
		ext := strings.ToLower(filepath.Ext(name))
		if !isDir && (ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif") {
			itemRelForIcon := name
			if cleanRel != "" {
				itemRelForIcon = cleanRel + "/" + name
			}
			icon = fmt.Sprintf(`<img src="/ui/%s" style="width:32px; height:32px; object-fit:cover; border-radius:4px; vertical-align:middle;" loading="lazy" decoding="async">`, itemRelForIcon)
		}

		itemRel := name
		if cleanRel != "" {
			itemRel = cleanRel + "/" + name
		}

		itemLink := "/ui/" + itemRel
		if isDir {
			itemLink += "/"
		}

		info, _ := entry.Info()
		sizeStr := "-"
		modTimeStr := "-"
		if info != nil {
			modTimeStr = info.ModTime().Format("2006-01-02 15:04:05")
			if !isDir {
				sizeStr = formatSize(info.Size())
			}
		}

		hideBtnText := "🔒 隐藏"
		hideBtnKey := "hide"
		if isHidden {
			hideBtnText = "👁️ 显示"
			hideBtnKey = "unhide"
		}

		var actionsHTML string
		if isDir {
			actionsHTML = fmt.Sprintf(`
				<button class="fluent-btn fluent-btn-subtle btn-hide" onclick="toggleHideItem('%s', %t)" data-i18n="%s">%s</button>
				<button class="fluent-btn fluent-btn-subtle btn-rename" onclick="promptRename('%s')" data-i18n="rename">✏️ 重命名</button>
				<button class="fluent-btn fluent-btn-danger btn-delete" onclick="confirmDelete('%s', true)" data-i18n="delete">🗑️ 删除</button>
			`, name, isHidden, hideBtnKey, hideBtnText, name, name)
		} else {
			actionsHTML = fmt.Sprintf(`
				<a class="fluent-btn fluent-btn-accent btn-download" href="%s?download=1" download data-i18n="download">⬇️ 下载</a>
				<button class="fluent-btn fluent-btn-subtle btn-reupload" onclick="triggerReupload('%s')" data-i18n="reupload">🔄 重新上传</button>
				<button class="fluent-btn fluent-btn-subtle btn-hide" onclick="toggleHideItem('%s', %t)" data-i18n="%s">%s</button>
				<button class="fluent-btn fluent-btn-subtle btn-rename" onclick="promptRename('%s')" data-i18n="rename">✏️ 重命名</button>
				<button class="fluent-btn fluent-btn-danger btn-delete" onclick="confirmDelete('%s', false)" data-i18n="delete">🗑️ 删除</button>
			`, itemLink, name, name, isHidden, hideBtnKey, hideBtnText, name, name)
		}

		linkClass := "file-link"
		if isDir {
			linkClass = "dir-link"
		}

		hiddenAttr := "false"
		hiddenClass := ""
		badgeHTML := ""
		if isHidden {
			hiddenAttr = "true"
			hiddenClass = "hidden-row"
			badgeHTML = ` <span class="fluent-badge fluent-badge-warning" data-i18n="hiddenBadge">🔒 隐私隐藏</span>`
		}

		tableRowsHTML.WriteString(fmt.Sprintf(`
		<tr data-name="%s" data-hidden="%s" class="%s">
			<td class="icon-cell">%s</td>
			<td><a href="%s" class="item-link %s">%s</a>%s</td>
			<td class="meta-cell">%s</td>
			<td class="meta-cell">%s</td>
			<td class="actions-cell">%s</td>
		</tr>`, name, hiddenAttr, hiddenClass, icon, itemLink, linkClass, name, badgeHTML, sizeStr, modTimeStr, actionsHTML))
	}

	c.Header("Content-Type", "text/html; charset=utf-8")

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Blackhole NAS - %s</title>
    <style>
        :root {
            /* Microsoft Fluent UI Design System Tokens (Dark Theme) */
            --fluent-bg: #111827;
            --fluent-surface-acrylic: rgba(31, 41, 55, 0.7);
            --fluent-surface-solid: #1f2937;
            --fluent-border: rgba(255, 255, 255, 0.12);
            --fluent-border-subtle: rgba(255, 255, 255, 0.08);
            --fluent-accent: #0078d4;
            --fluent-accent-hover: #106ebe;
            --fluent-accent-active: #005a9e;
            --fluent-text-primary: #ffffff;
            --fluent-text-secondary: #9ca3af;
            --fluent-text-muted: #6b7280;
            --fluent-font-family: 'Segoe UI Variable Display', 'Segoe UI', -apple-system, BlinkMacSystemFont, Roboto, sans-serif;
            --fluent-shadow-depth-8: 0 3.2px 7.2px 0 rgba(0, 0, 0, 0.24), 0 0.6px 1.8px 0 rgba(0, 0, 0, 0.18);
            --fluent-shadow-depth-16: 0 6.4px 14.4px 0 rgba(0, 0, 0, 0.32), 0 1.2px 3.6px 0 rgba(0, 0, 0, 0.24);
            --fluent-backdrop-blur: blur(20px) saturate(180%%);
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: var(--fluent-font-family);
            background: var(--fluent-bg);
            color: var(--fluent-text-primary);
            padding: 24px;
            min-height: 100vh;
            background-image: radial-gradient(circle at 50%%%% 0%%%%, rgba(0, 120, 212, 0.15), transparent 50%%%%);
        }

        .container {
            max-width: 1240px;
            margin: 0 auto;
        }

        header {
            display: flex;
            align-items: center;
            justify-content: space-between;
            margin-bottom: 24px;
            padding: 16px 24px;
            background: var(--fluent-surface-acrylic);
            backdrop-filter: var(--fluent-backdrop-blur);
            -webkit-backdrop-filter: var(--fluent-backdrop-blur);
            border: 1px solid var(--fluent-border);
            border-radius: 12px;
            box-shadow: var(--fluent-shadow-depth-8);
            flex-wrap: wrap;
            gap: 16px;
        }

        .brand {
            display: flex;
            align-items: center;
            gap: 20px;
        }
        .brand h1 {
            font-size: 20px;
            font-weight: 600;
            letter-spacing: -0.2px;
            color: var(--fluent-text-primary);
            display: flex;
            align-items: center;
            gap: 8px;
        }

        /* Fluent UI Segmented Navigation Tabs */
        .nav-tabs {
            display: flex;
            align-items: center;
            gap: 4px;
            background: rgba(0, 0, 0, 0.25);
            padding: 4px;
            border-radius: 8px;
            border: 1px solid var(--fluent-border-subtle);
        }
        .tab-btn {
            background: transparent;
            color: var(--fluent-text-secondary);
            border: none;
            padding: 6px 16px;
            border-radius: 6px;
            font-size: 13px;
            font-weight: 600;
            font-family: var(--fluent-font-family);
            cursor: pointer;
            transition: all 0.2s cubic-bezier(0.1, 0.9, 0.2, 1);
        }
        .tab-btn:hover {
            color: var(--fluent-text-primary);
            background: rgba(255, 255, 255, 0.05);
        }
        .tab-btn.active {
            background: var(--fluent-accent);
            color: #ffffff;
            box-shadow: 0 2px 6px rgba(0, 120, 212, 0.4);
        }

        .header-controls {
            display: flex;
            align-items: center;
            gap: 10px;
        }

        .breadcrumbs {
            background: var(--fluent-surface-acrylic);
            backdrop-filter: var(--fluent-backdrop-blur);
            -webkit-backdrop-filter: var(--fluent-backdrop-blur);
            border: 1px solid var(--fluent-border);
            padding: 12px 20px;
            border-radius: 10px;
            margin-bottom: 20px;
            font-size: 14px;
            display: flex;
            align-items: center;
            flex-wrap: wrap;
            gap: 6px;
            box-shadow: var(--fluent-shadow-depth-8);
        }
        .crumb { color: var(--fluent-accent); text-decoration: none; font-weight: 500; }
        .crumb:hover { text-decoration: underline; }
        .crumb-sep { color: var(--fluent-text-muted); }

        .action-bar {
            background: var(--fluent-surface-acrylic);
            backdrop-filter: var(--fluent-backdrop-blur);
            -webkit-backdrop-filter: var(--fluent-backdrop-blur);
            border: 1px solid var(--fluent-border);
            border-radius: 10px;
            padding: 16px 20px;
            margin-bottom: 20px;
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 12px;
            flex-wrap: wrap;
            box-shadow: var(--fluent-shadow-depth-8);
        }

        .btn-group {
            display: flex;
            align-items: center;
            gap: 8px;
            flex-wrap: wrap;
        }

        /* Fluent UI Button Component System */
        .fluent-btn {
            display: inline-flex;
            align-items: center;
            gap: 6px;
            padding: 7px 16px;
            font-size: 13px;
            font-weight: 600;
            font-family: var(--fluent-font-family);
            border-radius: 6px;
            border: 1px solid transparent;
            cursor: pointer;
            text-decoration: none;
            transition: all 0.15s cubic-bezier(0.1, 0.9, 0.2, 1);
        }
        .fluent-btn:disabled {
            opacity: 0.4;
            cursor: not-allowed;
            pointer-events: none;
        }
        .fluent-btn:active {
            transform: scale(0.98);
        }

        .fluent-btn-accent {
            background: var(--fluent-accent);
            color: #ffffff;
            border-color: rgba(255, 255, 255, 0.1);
        }
        .fluent-btn-accent:hover {
            background: var(--fluent-accent-hover);
        }

        .fluent-btn-subtle {
            background: rgba(255, 255, 255, 0.06);
            color: var(--fluent-text-primary);
            border-color: var(--fluent-border-subtle);
        }
        .fluent-btn-subtle:hover {
            background: rgba(255, 255, 255, 0.12);
            border-color: var(--fluent-border);
        }

        .fluent-btn-danger {
            background: rgba(239, 68, 68, 0.2);
            color: #fca5a5;
            border-color: rgba(239, 68, 68, 0.4);
        }
        .fluent-btn-danger:hover {
            background: rgba(239, 68, 68, 0.35);
            color: #ffffff;
        }

        .fluent-btn-active {
            border-color: var(--fluent-accent);
            background: rgba(0, 120, 212, 0.2);
            color: #60a5fa;
        }

        .fluent-badge {
            display: inline-flex;
            align-items: center;
            gap: 4px;
            font-size: 11px;
            font-weight: 600;
            padding: 2px 8px;
            border-radius: 12px;
            margin-left: 8px;
            vertical-align: middle;
        }
        .fluent-badge-warning {
            background: rgba(245, 158, 11, 0.15);
            color: #fbbf24;
            border: 1px solid rgba(245, 158, 11, 0.3);
        }

        .hidden-row {
            opacity: 0.7;
            background: rgba(0, 0, 0, 0.2);
        }

        .search-box {
            padding: 7px 14px;
            background: rgba(0, 0, 0, 0.3);
            border: 1px solid var(--fluent-border);
            border-radius: 6px;
            color: var(--fluent-text-primary);
            font-size: 13px;
            font-family: var(--fluent-font-family);
            width: 230px;
            outline: none;
            transition: all 0.2s ease;
        }
        .search-box:focus {
            border-color: var(--fluent-accent);
            box-shadow: 0 0 0 2px rgba(0, 120, 212, 0.3);
        }

        .status-box {
            display: none;
            padding: 12px 18px;
            border-radius: 8px;
            margin-bottom: 20px;
            font-size: 13px;
            font-weight: 500;
            backdrop-filter: var(--fluent-backdrop-blur);
        }
        .status-info { background: rgba(0, 120, 212, 0.15); border: 1px solid rgba(0, 120, 212, 0.3); color: #60a5fa; }
        .status-success { background: rgba(16, 185, 129, 0.15); border: 1px solid rgba(16, 185, 129, 0.3); color: #34d399; }
        .status-error { background: rgba(239, 68, 68, 0.15); border: 1px solid rgba(239, 68, 68, 0.3); color: #f87171; }

        /* Fluent Table Container */
        .table-container {
            background: var(--fluent-surface-acrylic);
            backdrop-filter: var(--fluent-backdrop-blur);
            -webkit-backdrop-filter: var(--fluent-backdrop-blur);
            border: 1px solid var(--fluent-border);
            border-radius: 12px;
            overflow: hidden;
            box-shadow: var(--fluent-shadow-depth-8);
        }

        table {
            width: 100%%%%;
            border-collapse: collapse;
            text-align: left;
        }

        th {
            background: rgba(0, 0, 0, 0.35);
            padding: 14px 18px;
            font-size: 12px;
            font-weight: 600;
            color: var(--fluent-text-secondary);
            text-transform: uppercase;
            letter-spacing: 0.6px;
            border-bottom: 1px solid var(--fluent-border);
        }

        td {
            padding: 12px 18px;
            border-bottom: 1px solid var(--fluent-border-subtle);
            font-size: 13px;
            vertical-align: middle;
        }

        tr:last-child td { border-bottom: none; }
        tbody tr { transition: background 0.15s ease; }
        tbody tr:hover { background: rgba(255, 255, 255, 0.04); }

        .icon-cell { font-size: 18px; width: 40px; text-align: center; }
        .item-link { text-decoration: none; font-weight: 500; color: var(--fluent-text-primary); }
        .dir-link { color: #38bdf8; }
        .file-link { color: var(--fluent-text-primary); }
        .parent-link { color: var(--fluent-text-secondary); font-style: italic; }
        .item-link:hover { text-decoration: underline; color: var(--fluent-accent); }
        .meta-cell { color: var(--fluent-text-secondary); font-size: 12px; white-space: nowrap; }
        .actions-cell { text-align: right; white-space: nowrap; }
        .actions-cell .fluent-btn { margin-left: 4px; padding: 4px 10px; font-size: 12px; }

        /* Google Photos Style Justified Gallery Grid */
        .photo-grid {
            display: flex;
            flex-wrap: wrap;
            gap: 12px;
            margin-top: 16px;
        }
        .photo-card {
            flex-grow: var(--aspect-ratio, 1.33);
            flex-basis: calc(180px * var(--aspect-ratio, 1.33));
            height: 200px;
            background: var(--fluent-surface-acrylic);
            backdrop-filter: var(--fluent-backdrop-blur);
            -webkit-backdrop-filter: var(--fluent-backdrop-blur);
            border: 1px solid var(--fluent-border);
            border-radius: 10px;
            overflow: hidden;
            cursor: pointer;
            transition: transform 0.2s cubic-bezier(0.1, 0.9, 0.2, 1), box-shadow 0.2s ease, border-color 0.2s ease;
            position: relative;
            box-shadow: var(--fluent-shadow-depth-8);
        }
        .photo-card:hover {
            transform: scale(1.02);
            box-shadow: var(--fluent-shadow-depth-16);
            border-color: var(--fluent-accent);
            z-index: 5;
        }
        .photo-thumb {
            width: 100%%%%;
            height: 100%%%%;
            object-fit: cover;
            background: rgba(0, 0, 0, 0.4);
            display: block;
        }
        .photo-meta-overlay {
            position: absolute;
            bottom: 0; left: 0; right: 0;
            padding: 24px 10px 8px 10px;
            background: linear-gradient(to top, rgba(0,0,0,0.85) 0%%%%, transparent 100%%%%);
            font-size: 11px;
            color: #ffffff;
            opacity: 0;
            transition: opacity 0.2s ease;
            display: flex;
            justify-content: space-between;
            align-items: flex-end;
            pointer-events: none;
        }
        .photo-card:hover .photo-meta-overlay {
            opacity: 1;
        }
        .photo-overlay-title {
            font-weight: 600;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
            max-width: 70%%%%;
        }
        .video-badge {
            position: absolute;
            top: 50%%%%;
            left: 50%%%%;
            transform: translate(-50%%%%, -50%%%%);
            background: rgba(0, 0, 0, 0.65);
            backdrop-filter: blur(8px);
            color: #ffffff;
            border: 1px solid rgba(255, 255, 255, 0.3);
            border-radius: 50%%%%;
            width: 44px;
            height: 44px;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 20px;
            pointer-events: none;
            box-shadow: 0 4px 12px rgba(0,0,0,0.4);
            transition: transform 0.2s ease, background 0.2s ease;
        }
        .photo-card:hover .video-badge {
            transform: translate(-50%%%%, -50%%%%) scale(1.1);
            background: var(--fluent-accent);
        }
        .fav-badge {
            position: absolute;
            top: 8px;
            right: 8px;
            background: rgba(0, 0, 0, 0.6);
            backdrop-filter: blur(8px);
            padding: 3px 7px;
            border-radius: 12px;
            font-size: 12px;
            z-index: 2;
        }
        .dark-badge {
            position: absolute;
            top: 8px;
            right: 38px;
            background: rgba(0, 0, 0, 0.75);
            color: #ffd166;
            font-size: 11px;
            padding: 2px 6px;
            border-radius: 4px;
            border: 1px solid rgba(255, 209, 102, 0.5);
            backdrop-filter: blur(4px);
            pointer-events: none;
            z-index: 2;
        }
        .photo-checkbox-overlay {
            position: absolute;
            top: 8px;
            left: 8px;
            z-index: 10;
            background: rgba(0, 0, 0, 0.5);
            backdrop-filter: blur(4px);
            padding: 3px 6px;
            border-radius: 6px;
            display: flex;
            align-items: center;
            cursor: pointer;
            border: 1px solid rgba(255,255,255,0.2);
        }
        .photo-checkbox-overlay input {
            cursor: pointer;
            width: 16px;
            height: 16px;
            accent-color: var(--fluent-accent);
        }
        .photo-card-selected {
            outline: 3px solid var(--fluent-accent);
            outline-offset: -3px;
        }

        /* Fluent UI Pagination Bar */
        .pagination-bar {
            display: flex;
            align-items: center;
            justify-content: space-between;
            padding: 14px 20px;
            margin-top: 20px;
            background: var(--fluent-surface-acrylic);
            backdrop-filter: var(--fluent-backdrop-blur);
            -webkit-backdrop-filter: var(--fluent-backdrop-blur);
            border: 1px solid var(--fluent-border);
            border-radius: 10px;
            box-shadow: var(--fluent-shadow-depth-8);
            flex-wrap: wrap;
            gap: 12px;
        }
        .pagination-controls {
            display: flex;
            align-items: center;
            gap: 8px;
        }

        /* Fluent Modal Surface & Interactive Lightbox */
        .modal {
            display: none;
            position: fixed;
            top: 0; left: 0; right: 0; bottom: 0;
            background: rgba(0, 0, 0, 0.88);
            backdrop-filter: blur(16px);
            z-index: 1000;
            align-items: center;
            justify-content: center;
            padding: 0;
        }
        .modal-content {
            background: var(--fluent-surface-solid);
            border: 1px solid var(--fluent-border);
            border-radius: 12px;
            max-width: 1040px;
            width: 100%%%%;
            max-height: 92vh;
            overflow: hidden;
            display: flex;
            flex-direction: column;
            box-shadow: var(--fluent-shadow-depth-16);
        }
        .modal-content-lightbox {
            background: rgba(10, 10, 12, 0.96);
            border: none;
            border-radius: 0;
            max-width: 100vw;
            width: 100vw;
            height: 100vh;
            max-height: 100vh;
            box-shadow: none;
            position: relative;
        }
        .modal-header {
            padding: 14px 20px;
            border-bottom: 1px solid var(--fluent-border);
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .modal-header-lightbox {
            position: absolute;
            top: 0; left: 0; right: 0;
            z-index: 10;
            background: linear-gradient(to bottom, rgba(0,0,0,0.75), rgba(0,0,0,0));
            border-bottom: none;
            padding: 16px 24px;
            pointer-events: none;
        }
        .modal-header-lightbox * {
            pointer-events: auto;
        }
        .modal-body {
            padding: 16px 20px;
            overflow-y: auto;
            text-align: center;
            display: flex;
            flex-direction: column;
            align-items: center;
        }
        .modal-body-lightbox {
            padding: 0;
            width: 100vw;
            height: 100vh;
            box-sizing: border-box;
            overflow: hidden;
            position: relative;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .modal-viewport {
            position: relative;
            width: 100vw;
            height: 100vh;
            overflow: hidden;
            display: flex;
            align-items: center;
            justify-content: center;
            background: #000;
            border-radius: 0;
            user-select: none;
        }
        .modal-viewport:-webkit-full-screen {
            width: 100vw;
            height: 100vh;
            border-radius: 0;
            background: #000;
        }
        .modal-viewport:fullscreen {
            width: 100vw;
            height: 100vh;
            border-radius: 0;
            background: #000;
        }
        .modal-preview {
            /* Reliable min-scale: use vw/vh units directly on img.
               Browser picks min(100vw/iw, 100vh/ih) => both dims always fit.
               Black bars fill the remaining space (set by viewport background:#000). */
            max-width: 100vw;
            max-height: 100vh;
            width: auto;
            height: auto;
            display: block;
            object-fit: contain;
            transition: transform 0.15s cubic-bezier(0.1, 0.9, 0.2, 1);
            cursor: grab;
            transform-origin: center center;
        }
        .modal-preview:active {
            cursor: grabbing;
        }
        .modal-video-player {
            max-width: 100%%%%;
            max-height: 100%%%%;
            border-radius: 8px;
            outline: none;
            box-shadow: var(--fluent-shadow-depth-8);
        }
        .lightbox-toolbar {
            position: absolute;
            bottom: 20px;
            left: 50%%%%;
            transform: translateX(-50%%%%);
            z-index: 10;
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 6px;
            background: rgba(20, 20, 24, 0.65);
            backdrop-filter: blur(16px);
            -webkit-backdrop-filter: blur(16px);
            padding: 8px 16px;
            border-radius: 24px;
            border: 1px solid rgba(255, 255, 255, 0.15);
            box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
            flex-wrap: wrap;
            transition: opacity 0.25s ease, transform 0.25s ease;
        }
        .modal-body-lightbox:hover .lightbox-toolbar {
            opacity: 1;
        }
        .lightbox-meta-overlay {
            position: absolute;
            bottom: 74px;
            left: 50%%%%;
            transform: translateX(-50%%%%);
            z-index: 9;
            background: rgba(0, 0, 0, 0.5);
            backdrop-filter: blur(10px);
            padding: 4px 14px;
            border-radius: 12px;
            font-size: 12px;
            color: rgba(255, 255, 255, 0.85);
            pointer-events: none;
            white-space: nowrap;
        }
        .lightbox-toolbar .fluent-btn {
            padding: 5px 12px;
            font-size: 12px;
        }

        .drop-hint {
            margin-top: 20px;
            text-align: center;
            color: var(--fluent-text-secondary);
            font-size: 13px;
        }
        @keyframes spin {
            0%% { transform: rotate(0deg); }
            100%% { transform: rotate(360deg); }
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="brand">
                <h1 data-i18n="title">🌌 Blackhole NAS</h1>
                <div class="nav-tabs">
                    <button class="tab-btn active" id="tabFiles" onclick="switchMainTab('files')" data-i18n="filesTab">📁 文件管理</button>
                    <button class="tab-btn" id="tabAlbum" onclick="switchMainTab('album')" data-i18n="albumTab">🖼️ 照片相册</button>
                </div>
            </div>
            <div class="header-controls">
                <button class="fluent-btn fluent-btn-subtle" onclick="toggleLanguage()" id="langBtn">🌐 English</button>
            </div>
        </header>

        <!-- COSMOS STAR P2P SYNC PROGRESS WIDGET -->
        <div id="syncProgressWidget" class="status-box status-info" style="display:none; margin-bottom: 20px; border-radius: 12px; padding: 16px 20px; box-shadow: var(--fluent-shadow-depth-8);">
            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom: 8px;">
                <span style="font-weight:600; font-size:14px; display:flex; align-items:center; gap:8px;">
                    <span style="display:inline-block; animation: spin 1.5s linear infinite;">🌌</span>
                    <span>Cosmos Star P2P 照片极速同步中...</span>
                </span>
                <span id="syncPercentText" style="font-weight:700; color:var(--fluent-accent); font-size:14px;">0.0%%</span>
            </div>
            <div style="background: rgba(0,0,0,0.3); height: 8px; border-radius: 4px; overflow:hidden; margin-bottom: 10px;">
                <div id="syncProgressBar" style="background: var(--fluent-accent); height: 100%%; width: 0%%; transition: width 0.4s ease;"></div>
            </div>
            <div style="display:flex; justify-content:space-between; font-size:12px; color:var(--fluent-text-secondary); flex-wrap:wrap; gap:8px;">
                <span id="syncFilesCount">已完成 0 / 0 项</span>
                <span id="syncCurrentFile">正在传输: -</span>
                <span id="syncSpeed">速度: 0.0 MB/s</span>
            </div>
        </div>

        <!-- FILE MANAGER VIEW -->
        <div id="filesView">
            <div class="breadcrumbs">
                %s
            </div>

            <div class="action-bar">
                <div class="btn-group">
                    <button class="fluent-btn fluent-btn-accent" onclick="document.getElementById('fileInput').click()" data-i18n="upload">📤 上传文件</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="promptCreateFolder()" data-i18n="mkdir">📁 新建目录</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="toggleShowHidden()" id="toggleHiddenBtn" data-i18n="showHidden">👁️ 显示隐藏项目</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="location.reload()" data-i18n="refresh">🔄 刷新</button>
                </div>
                <input type="text" id="searchInput" class="search-box" value="%s" placeholder="🔍 搜索此目录下项目..." data-i18n-placeholder="searchPlaceholder" oninput="filterTable()">
            </div>

            <div id="uploadStatus" class="status-box"></div>

            <input type="file" id="fileInput" multiple style="display:none;" onchange="uploadFiles(this.files)">
            <input type="file" id="reuploadInput" style="display:none;" onchange="handleReupload(this)">

            <div class="table-container">
                <table id="fileTable">
                    <thead>
                        <tr>
                            <th style="width: 40px;"></th>
                            <th data-i18n="name">名称</th>
                            <th data-i18n="size">大小</th>
                            <th data-i18n="modTime">修改时间</th>
                            <th style="text-align: right;" data-i18n="actions">操作</th>
                        </tr>
                    </thead>
                    <tbody>
                        %s
                    </tbody>
                </table>
            </div>

            <!-- Fluent UI File Manager Pagination Bar -->
            <div class="pagination-bar" id="filePaginationBar" style="display:flex; justify-content:space-between; align-items:center; margin-top:16px;">
                <div class="pagination-info" id="filePageInfoLabel" style="font-size:13px; color:var(--fluent-text-secondary);">%s</div>
                <div class="pagination-controls">
                    %s
                </div>
            </div>
        </div>

        <!-- PHOTO ALBUM VIEW -->
        <div id="albumView" style="display:none;">
            <div class="action-bar">
                <div class="btn-group">
                    <button class="fluent-btn fluent-btn-accent" onclick="scanAlbumPhotos()" data-i18n="scanAlbum">🔄 扫描相册目录</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="toggleFavFilter()" id="favFilterBtn" data-i18n="favOnly">❤️ 仅看收藏</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="toggleDarkFilter()" id="darkFilterBtn" data-i18n="darkOnly">🖤 纯黑误拍废片</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="toggleBatchSelectMode()" id="batchSelectModeBtn">☑ 批量选择</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="toggleSelectAllPhotos()" id="selectAllBtn" style="display:none;" data-i18n="selectAll">☑ 全选当前页</button>
                    <button class="fluent-btn fluent-btn-danger" onclick="executeBatchDeletePhotos()" id="batchDeleteBtn" data-i18n="batchDelete">🗑️ 批量删除所选 (0)</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="configAlbumDir()" data-i18n="setAlbumDir">⚙️ 设置相册路径</button>
                </div>
                <div class="album-dir-info" id="albumDirLabel" style="font-size:13px; color:var(--fluent-text-secondary);"></div>
            </div>

            <div id="photoStatus" class="status-box"></div>

            <div id="photoGrid" class="photo-grid"></div>

            <!-- Fluent UI Pagination Bar -->
            <div class="pagination-bar" id="albumPaginationBar" style="display:none;">
                <div class="pagination-info" id="pageInfoLabel" style="font-size:13px; color:var(--fluent-text-secondary);"></div>
                <div class="pagination-controls">
                    <button class="fluent-btn fluent-btn-subtle" onclick="changeAlbumPage(-1)" id="prevPageBtn" data-i18n="prevPage">◄ 上一页</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="changeAlbumPage(1)" id="nextPageBtn" data-i18n="nextPage">下一页 ►</button>
                    <select id="pageSizeSelect" class="search-box" style="width: auto; padding: 5px 10px; font-size:12px;" onchange="changePageSize(this.value)">
                        <option value="25">25 条/页</option>
                        <option value="50">50 条/页</option>
                        <option value="100">100 条/页</option>
                    </select>
                </div>
            </div>
        </div>

        <div class="drop-hint" data-i18n="dropHint">💡 提示：支持直接将文件拖拽至此页面进行上传 | 支持谷歌相册弹性瀑布流 | 播放视频 | 分页浏览 | 缩放/旋转/全屏图片</div>
    </div>

    <!-- FLUENT INPUT DIALOG MODAL -->
    <div id="fluentInputDialogModal" class="modal" onclick="closeInputDialog(event)">
        <div class="modal-content" style="max-width: 500px;" onclick="event.stopPropagation()">
            <div class="modal-header">
                <h3 id="inputDialogTitle" style="font-size:16px; font-weight:600;">设置路径</h3>
                <button class="fluent-btn fluent-btn-subtle" onclick="closeInputDialog()">✕</button>
            </div>
            <div class="modal-body" style="text-align:left; padding: 22px;">
                <p id="inputDialogPrompt" style="font-size:13px; color:var(--fluent-text-secondary); margin-bottom:12px;"></p>
                <input type="text" id="inputDialogValue" class="search-box" style="width:100%%%%; font-size:14px; padding:10px 14px;" placeholder="" onkeyup="if(event.key==='Enter') confirmInputDialog()">
                <div style="margin-top:22px; display:flex; justify-content:flex-end; gap:10px;">
                    <button class="fluent-btn fluent-btn-subtle" onclick="closeInputDialog()" data-i18n="cancel">取消</button>
                    <button id="inputDialogConfirmBtn" class="fluent-btn fluent-btn-accent" onclick="confirmInputDialog()" data-i18n="confirm">确定</button>
                </div>
            </div>
        </div>
    </div>

    <!-- FLUENT MODAL DIALOG (CONFIRM & ALERT) -->
    <div id="fluentModalDialog" class="modal" onclick="closeFluentDialog(event, false)">
        <div class="modal-content" style="max-width: 440px; border-radius: 12px; box-shadow: 0 16px 32px rgba(0, 0, 0, 0.28);" onclick="event.stopPropagation()">
            <div class="modal-header" style="border-bottom: 1px solid var(--fluent-border);">
                <h3 id="fluentDialogTitle" style="font-size:16px; font-weight:600; display:flex; align-items:center; gap:8px;">提示</h3>
                <button class="fluent-btn fluent-btn-subtle" onclick="closeFluentDialog(null, false)">✕</button>
            </div>
            <div class="modal-body" style="text-align:left; padding: 22px 24px;">
                <div id="fluentDialogContent" style="font-size:14px; line-height:1.6; color:var(--fluent-text-primary); margin-bottom:24px;"></div>
                <div style="display:flex; justify-content:flex-end; gap:12px;">
                    <button id="fluentDialogCancelBtn" class="fluent-btn fluent-btn-subtle" onclick="closeFluentDialog(null, false)">取消</button>
                    <button id="fluentDialogConfirmBtn" class="fluent-btn fluent-btn-accent" onclick="closeFluentDialog(null, true)">确定</button>
                </div>
            </div>
        </div>
    </div>

    <!-- PHOTO & VIDEO LIGHTBOX MODAL -->
    <div id="photoModal" class="modal" onclick="closePhotoModal(event)">
        <div class="modal-content modal-content-lightbox" onclick="event.stopPropagation()">
            <div class="modal-header modal-header-lightbox">
                <h3 id="modalPhotoTitle" style="font-size:15px; font-weight:600; color:#fff; text-shadow:0 1px 4px rgba(0,0,0,0.8); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; max-width:80%%%%;">媒体详情</h3>
                <button class="fluent-btn fluent-btn-subtle" onclick="closePhotoModal()" style="color:#fff; background:rgba(255,255,255,0.15);">✕</button>
            </div>
            <div class="modal-body modal-body-lightbox">
                <div id="modalViewport" class="modal-viewport" ondblclick="toggleFullscreen()">
                    <img id="modalPhotoImg" class="modal-preview" src="" alt="photo" style="display:none;">
                    <video id="modalVideoPlayer" class="modal-video-player" src="" controls autoplay loop style="display:none;"></video>
                </div>

                <!-- Floating Glassmorphism Overlay Toolbar -->
                <div class="lightbox-toolbar">
                    <button class="fluent-btn fluent-btn-subtle" onclick="navPrevPhoto()" title="上一张 (←)">⬅️ 上一张</button>
                    <button id="btnRotateCCW" class="fluent-btn fluent-btn-subtle" onclick="rotateCCW()" title="逆时针旋转 (Shift+R)">↺ 旋转</button>
                    <button id="btnRotateCW" class="fluent-btn fluent-btn-subtle" onclick="rotateCW()" title="顺时针旋转 (R)">↻ 旋转</button>
                    <button id="btnZoomOut" class="fluent-btn fluent-btn-subtle" onclick="zoomOut()" title="缩小 (-)">🔍-</button>
                    <button id="btnZoomReset" class="fluent-btn fluent-btn-subtle" onclick="resetTransformState()" title="重置">100%%%%</button>
                    <button id="btnZoomIn" class="fluent-btn fluent-btn-subtle" onclick="zoomIn()" title="放大 (+)">🔍+</button>
                    <button id="btnFullscreen" class="fluent-btn fluent-btn-subtle" onclick="toggleFullscreen()" title="全屏查看 (F)">⛶ 全屏</button>
                    <button class="fluent-btn fluent-btn-subtle" onclick="navNextPhoto()" title="下一张 (→)">下一张 ➡️</button>
                    <a id="modalViewOriginalBtn" class="fluent-btn fluent-btn-subtle" href="" target="_blank" title="在新标签页查看原图">👁️ 查看原图</a>
                    <a id="modalDownloadBtn" class="fluent-btn fluent-btn-accent" href="" download data-i18n="download">⬇️ 下载</a>
                    <button id="modalFavBtn" class="fluent-btn fluent-btn-subtle" onclick="toggleCurrentPhotoFav()">❤️ 收藏</button>
                    <button id="modalDeleteBtn" class="fluent-btn fluent-btn-danger" onclick="deleteCurrentPhoto()">🗑️ 删除</button>
                </div>

                <div id="modalPhotoMeta" class="lightbox-meta-overlay"></div>
            </div>
        </div>
    </div>

    <script>
    const currentPath = "%s";

    const i18n = {
        zh: {
            title: "🌌 Blackhole NAS",
            filesTab: "📁 文件管理",
            albumTab: "🖼️ 照片相册",
            root: "🏠 根目录",
            upload: "📤 上传文件",
            mkdir: "📁 新建目录",
            refresh: "🔄 刷新",
            showHidden: "👁️ 显示隐藏项目",
            hideHidden: "🙈 隐藏私密项目",
            searchPlaceholder: "🔍 搜索此目录下项目...",
            parentDir: ".. (返回上一级)",
            name: "名称",
            size: "大小",
            modTime: "修改时间",
            actions: "操作",
            download: "⬇️ 下载",
            reupload: "🔄 重新上传",
            rename: "✏️ 重命名",
            delete: "🗑️ 删除",
            hide: "🔒 隐匿",
            unhide: "👁️ 显示",
            scanAlbum: "🔄 扫描相册目录",
            favOnly: "❤️ 仅看收藏",
            setAlbumDir: "⚙️ 设置相册路径",
            dropHint: "💡 提示：支持直接将文件拖拽至此页面进行上传 | 支持谷歌相册弹性瀑布流 | 播放视频 | 分页浏览 | 缩放/旋转/全屏图片",
            promptMkdir: "请输入新目录名称：",
            promptRename: "请输入新的名称：",
            promptAlbumDir: "请输入默认相册存储路径：",
            confirmDeleteDir: "确定要删除目录 「{name}」 吗？\n此操作将递归删除目录下的所有内容，不可恢复！",
            confirmDeleteFile: "确定要删除文件 「{name}」 吗？",
            confirmDeletePhoto: "确定要从相册中删除此媒体项目吗？",
            confirmHide: "确定要隐匿项目 「{name}」 吗？（名称将自动添加前缀 .）",
            confirmUnhide: "确定要解除隐匿项目 「{name}」 吗？",
            uploading: "⏳ 正在上传中...",
            uploadSuccess: "🎉 全部上传完成！正在刷新页面...",
            uploadFailed: "⚠️ 上传完成: {completed} 成功, {failed} 失败。",
            reuploading: "🔄 正在重新上传覆盖 「{name}」...",
            reuploadSuccess: "🎉 文件重新上传成功！正在刷新...",
            reuploadFailed: "❌ 重新上传失败: ",
            hiddenBadge: "🔒 隐私隐匿",
            langText: "🌐 English",
            noPhotos: "🖼️ 当前相册为空，点击“扫描相册目录”或设置照片目录",
            cancel: "取消",
            confirm: "确定",
            fullscreen: "全屏",
            exitFullscreen: "退出全屏",
            prevPage: "◄ 上一页",
            nextPage: "下一页 ►",
            pageInfo: "共 {total} 项媒体 | 第 {page} / {totalPages} 页",
        },
        en: {
            title: "🌌 Blackhole NAS",
            filesTab: "📁 Files",
            albumTab: "🖼️ Gallery",
            root: "🏠 Root Directory",
            upload: "📤 Upload File",
            mkdir: "📁 New Folder",
            refresh: "🔄 Refresh",
            showHidden: "👁️ Show Hidden Items",
            hideHidden: "🙈 Hide Private Items",
            searchPlaceholder: "🔍 Search items in current folder...",
            parentDir: ".. (Go Up Level)",
            name: "Name",
            size: "Size",
            modTime: "Last Modified",
            actions: "Actions",
            download: "⬇️ Download",
            reupload: "🔄 Overwrite",
            rename: "✏️ Rename",
            delete: "🗑️ Delete",
            hide: "🔒 Hide",
            unhide: "👁️ Unhide",
            scanAlbum: "🔄 Scan Gallery",
            favOnly: "❤️ Favorites Only",
            setAlbumDir: "⚙️ Album Path",
            dropHint: "💡 Tip: Drag & drop files anywhere to upload | Google Photos style flex layout | Video playback | Page pagination | Zoom, Rotate & Fullscreen photos",
            promptMkdir: "Enter new folder name:",
            promptRename: "Enter new name:",
            promptAlbumDir: "Enter default photo album directory path:",
            confirmDeleteDir: "Are you sure you want to delete directory '{name}'?\nThis will permanently delete all contents!",
            confirmDeleteFile: "Are you sure you want to delete file '{name}'?",
            confirmDeletePhoto: "Delete this media item permanently?",
            confirmHide: "Hide item '{name}' for privacy? (Renames with prefix .)",
            confirmUnhide: "Unhide item '{name}'?",
            uploading: "⏳ Uploading files...",
            uploadSuccess: "🎉 Upload complete! Refreshing page...",
            uploadFailed: "⚠️ Upload finished: {completed} succeeded, {failed} failed.",
            reuploading: "🔄 Overwriting '{name}'...",
            reuploadSuccess: "🎉 Overwritten successfully! Refreshing...",
            reuploadFailed: "❌ Overwrite failed: ",
            hiddenBadge: "🔒 Private Hidden",
            langText: "🌐 中文",
            noPhotos: "🖼️ Gallery is empty. Click 'Scan Gallery' or set album path.",
            cancel: "Cancel",
            confirm: "OK",
            fullscreen: "Fullscreen",
            exitFullscreen: "Exit Fullscreen",
            prevPage: "◄ Prev",
            nextPage: "Next ►",
            pageInfo: "Total {total} items | Page {page} / {totalPages}",
        }
    };

    let currentLang = localStorage.getItem('blackhole_lang') || 'zh';
    let showHiddenState = localStorage.getItem('blackhole_show_hidden') === 'true';
    let favFilterState = false;
    let darkFilterState = false;
    let isBatchSelectMode = false;
    let selectedPhotoIDs = new Set();
    let currentPhotosList = [];
    let activePhotoObj = null;
    let currentAlbumDir = '/var/nas_share/photos';

    // Pagination State
    let albumPage = 1;
    let albumPageSize = 25;
    let albumTotalPhotos = 0;
    let albumTotalPages = 1;

    function switchMainTab(tab) {
        localStorage.setItem('blackhole_active_tab', tab);
        if (history.replaceState) {
            history.replaceState(null, null, '#' + tab);
        } else {
            window.location.hash = '#' + tab;
        }
        document.getElementById('tabFiles').classList.toggle('active', tab === 'files');
        document.getElementById('tabAlbum').classList.toggle('active', tab === 'album');
        document.getElementById('filesView').style.display = tab === 'files' ? 'block' : 'none';
        document.getElementById('albumView').style.display = tab === 'album' ? 'block' : 'none';
        if (tab === 'album') {
            loadAlbumPhotos(1);
            loadAlbumConfig();
        }
    }

    function updateLanguageUI() {
        const langData = i18n[currentLang];
        document.querySelectorAll('[data-i18n]').forEach(el => {
            const key = el.getAttribute('data-i18n');
            if (langData[key]) el.textContent = langData[key];
        });
        document.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
            const key = el.getAttribute('data-i18n-placeholder');
            if (langData[key]) el.placeholder = langData[key];
        });
        document.getElementById('langBtn').textContent = langData.langText;
        updateHiddenBtnUI();
        renderPaginationUI();
    }

    function toggleLanguage() {
        currentLang = currentLang === 'zh' ? 'en' : 'zh';
        localStorage.setItem('blackhole_lang', currentLang);
        updateLanguageUI();
        if (document.getElementById('albumView').style.display !== 'none') {
            renderPhotosGrid();
        }
    }

    function updateHiddenBtnUI() {
        const btn = document.getElementById('toggleHiddenBtn');
        const langData = i18n[currentLang];
        if (showHiddenState) {
            btn.textContent = langData.hideHidden;
            btn.classList.add('fluent-btn-active');
        } else {
            btn.textContent = langData.showHidden;
            btn.classList.remove('fluent-btn-active');
        }

        document.querySelectorAll('#fileTable tbody tr[data-hidden="true"]').forEach(row => {
            row.style.display = showHiddenState ? '' : 'none';
        });
    }

    function toggleShowHidden() {
        showHiddenState = !showHiddenState;
        localStorage.setItem('blackhole_show_hidden', showHiddenState);
        updateHiddenBtnUI();
        filterTable();
    }

    async function toggleHideItem(name, isCurrentlyHidden) {
        const langData = i18n[currentLang];
        const msg = isCurrentlyHidden
            ? langData.confirmUnhide.replace('{name}', name)
            : langData.confirmHide.replace('{name}', name);

        const ok = await showFluentConfirm({ title: isCurrentlyHidden ? '取消隐藏' : '隐藏项目', content: msg });
        if (!ok) return;

        let newName = '';
        if (isCurrentlyHidden) {
            newName = name.replace(/^\.+/, '');
        } else {
            newName = '.' + name;
        }

        const oldPath = currentPath ? currentPath + '/' + name : name;
        const newPath = currentPath ? currentPath + '/' + newName : newName;

        fetch('/api/rename', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ old_path: oldPath, new_path: newPath })
        })
        .then(r => r.json())
        .then(data => {
            if (data.status === 'success') {
                location.reload();
            } else {
                alert((langData.error || 'Error') + ': ' + (data.error || ''));
            }
        })
        .catch(err => alert(err));
    }

    // Fluent UI Modal Dialog System (replaces native alert and confirm)
    let fluentDialogResolve = null;

    function showFluentConfirm(options) {
        return new Promise((resolve) => {
            fluentDialogResolve = resolve;
            const dialog = document.getElementById("fluentModalDialog");
            const titleEl = document.getElementById("fluentDialogTitle");
            const contentEl = document.getElementById("fluentDialogContent");
            const confirmBtn = document.getElementById("fluentDialogConfirmBtn");
            const cancelBtn = document.getElementById("fluentDialogCancelBtn");

            const icon = options.isDanger ? "⚠️ " : (options.icon || "💡 ");
            titleEl.textContent = icon + (options.title || "确认");
            contentEl.textContent = options.content || "";

            confirmBtn.textContent = options.confirmText || "确定";
            cancelBtn.textContent = options.cancelText || "取消";
            cancelBtn.style.display = "";

            if (options.isDanger) {
                confirmBtn.className = "fluent-btn fluent-btn-danger";
            } else {
                confirmBtn.className = "fluent-btn fluent-btn-accent";
            }

            dialog.style.display = "flex";
        });
    }

    function showFluentAlert(options) {
        return new Promise((resolve) => {
            fluentDialogResolve = resolve;
            const dialog = document.getElementById("fluentModalDialog");
            const titleEl = document.getElementById("fluentDialogTitle");
            const contentEl = document.getElementById("fluentDialogContent");
            const confirmBtn = document.getElementById("fluentDialogConfirmBtn");
            const cancelBtn = document.getElementById("fluentDialogCancelBtn");

            const icon = options.icon || (options.isError ? "❌ " : "💡 ");
            titleEl.textContent = icon + (options.title || "提示");
            contentEl.textContent = (typeof options === "string") ? options : (options.content || "");

            confirmBtn.textContent = (options && options.buttonText) ? options.buttonText : "知道了";
            confirmBtn.className = "fluent-btn fluent-btn-accent";
            cancelBtn.style.display = "none";

            dialog.style.display = "flex";
        });
    }

    function closeFluentDialog(event, result) {
        if (event && event.target !== document.getElementById("fluentModalDialog")) return;
        const dialog = document.getElementById("fluentModalDialog");
        if (dialog) dialog.style.display = "none";
        if (fluentDialogResolve) {
            const res = fluentDialogResolve;
            fluentDialogResolve = null;
            res(result);
        }
    }

    document.addEventListener("keydown", (e) => {
        const dialog = document.getElementById("fluentModalDialog");
        if (dialog && dialog.style.display === "flex") {
            if (e.key === "Escape") {
                closeFluentDialog(null, false);
            } else if (e.key === "Enter") {
                closeFluentDialog(null, true);
            }
        }
    });

    // Fluent UI Dialog Helper
    let inputDialogCallback = null;
    function showInputDialog(options) {
        document.getElementById('inputDialogTitle').textContent = options.title || '';
        document.getElementById('inputDialogPrompt').textContent = options.promptText || '';
        const input = document.getElementById('inputDialogValue');
        input.value = options.defaultValue || '';
        inputDialogCallback = options.onConfirm;
        document.getElementById('fluentInputDialogModal').style.display = 'flex';
        setTimeout(() => input.focus(), 100);
    }

    function closeInputDialog(e) {
        if (!e || e.target === document.getElementById('fluentInputDialogModal') || e.target.tagName === 'BUTTON' || e.target.classList.contains('fluent-btn')) {
            document.getElementById('fluentInputDialogModal').style.display = 'none';
            inputDialogCallback = null;
        }
    }

    function confirmInputDialog() {
        const val = document.getElementById('inputDialogValue').value;
        document.getElementById('fluentInputDialogModal').style.display = 'none';
        if (inputDialogCallback) {
            const cb = inputDialogCallback;
            inputDialogCallback = null;
            cb(val);
        }
    }

    function loadAlbumConfig() {
        fetch('/api/album/config')
            .then(r => r.json())
            .then(data => {
                if (data.album_dir) {
                    currentAlbumDir = data.album_dir;
                    document.getElementById('albumDirLabel').textContent = '📂 ' + data.album_dir;
                }
            });
    }

    function configAlbumDir() {
        const langData = i18n[currentLang];
        showInputDialog({
            title: langData.setAlbumDir || '⚙️ 设置相册路径',
            promptText: langData.promptAlbumDir || '请输入默认相册存储路径：',
            defaultValue: currentAlbumDir,
            onConfirm: (newDir) => {
                if (!newDir || !newDir.trim()) return;
                fetch('/api/album/config', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ album_dir: newDir.trim() })
                })
                .then(r => r.json())
                .then(data => {
                    if (data.status === 'success') {
                        loadAlbumConfig();
                        scanAlbumPhotos();
                    } else {
                        alert(data.error || 'Error');
                    }
                });
            }
        });
    }

    function loadAlbumPhotos(page = albumPage) {
        albumPage = page;
        const url = '/api/album/photos?favorite=' + (favFilterState ? '1' : '0') +
                    '&dark=' + (darkFilterState ? '1' : '0') +
                    '&page=' + albumPage + '&limit=' + albumPageSize;

        fetch(url, { credentials: 'same-origin' })
            .then(r => {
                if (!r.ok) throw new Error('HTTP ' + r.status);
                return r.json();
            })
            .then(data => {
                currentPhotosList = data.photos || [];
                albumTotalPhotos = data.total || currentPhotosList.length;
                albumTotalPages = data.total_pages || 1;
                renderPhotosGrid();
                renderPaginationUI();
                updateBatchDeleteBtnUI();
            })
            .catch(err => {
                console.error('Failed to load album photos:', err);
                const grid = document.getElementById('photoGrid');
                if (grid) {
                    grid.innerHTML = '<div style="grid-column: 1/-1; text-align:center; padding:40px; color:var(--fluent-text-secondary);">⚠️ 加载照片列表失败: ' + err.message + '</div>';
                }
            });
    }

    function renderPaginationUI() {
        const bar = document.getElementById('albumPaginationBar');
        const langData = i18n[currentLang];
        if (!bar) return;

        if (albumTotalPhotos === 0) {
            bar.style.display = 'none';
            return;
        }
        bar.style.display = 'flex';

        const label = document.getElementById('pageInfoLabel');
        if (label) {
            const template = langData.pageInfo || '共 {total} 项媒体 | 第 {page} / {totalPages} 页';
            label.textContent = template.replace('{total}', albumTotalPhotos)
                                        .replace('{page}', albumPage)
                                        .replace('{totalPages}', albumTotalPages);
        }

        const prevBtn = document.getElementById('prevPageBtn');
        const nextBtn = document.getElementById('nextPageBtn');
        if (prevBtn) prevBtn.disabled = albumPage <= 1;
        if (nextBtn) nextBtn.disabled = albumPage >= albumTotalPages;
    }

    function changeAlbumPage(delta) {
        const newPage = albumPage + delta;
        if (newPage >= 1 && newPage <= albumTotalPages) {
            loadAlbumPhotos(newPage);
            window.scrollTo({ top: 0, behavior: 'smooth' });
        }
    }

    function changePageSize(size) {
        albumPageSize = parseInt(size, 10);
        albumPage = 1;
        loadAlbumPhotos(1);
    }

    function scanAlbumPhotos() {
        const status = document.getElementById('photoStatus');
        status.style.display = 'block';
        status.className = 'status-box status-info';
        status.textContent = '⏳ 正在扫描相册目录中...';

        fetch('/api/album/scan', { method: 'POST' })
            .then(r => r.json())
            .then(data => {
                status.className = 'status-box status-success';
                status.textContent = data.message || '🎉 扫描完成';
                loadAlbumPhotos(1);
                setTimeout(() => { status.style.display = 'none'; }, 2000);
            })
            .catch(err => {
                status.className = 'status-box status-error';
                status.textContent = '❌ 扫描失败: ' + err;
            });
    }

    function toggleFavFilter() {
        favFilterState = !favFilterState;
        const btn = document.getElementById('favFilterBtn');
        if (btn) btn.classList.toggle('fluent-btn-active', favFilterState);
        selectedPhotoIDs.clear();
        updateBatchDeleteBtnUI();
        loadAlbumPhotos(1);
    }

    function toggleDarkFilter() {
        darkFilterState = !darkFilterState;
        const btn = document.getElementById('darkFilterBtn');
        if (btn) btn.classList.toggle('fluent-btn-active', darkFilterState);
        selectedPhotoIDs.clear();
        updateBatchDeleteBtnUI();
        loadAlbumPhotos(1);
    }

    function toggleSelectPhoto(id) {
        if (selectedPhotoIDs.has(id)) {
            selectedPhotoIDs.delete(id);
        } else {
            selectedPhotoIDs.add(id);
        }
        updateBatchDeleteBtnUI();
        renderPhotosGrid();
    }

    function toggleSelectAllPhotos() {
        if (!currentPhotosList || !currentPhotosList.length) return;
        const allSelectedOnPage = currentPhotosList.every(p => selectedPhotoIDs.has(p.id));
        if (allSelectedOnPage) {
            currentPhotosList.forEach(p => selectedPhotoIDs.delete(p.id));
        } else {
            currentPhotosList.forEach(p => selectedPhotoIDs.add(p.id));
        }
        updateBatchDeleteBtnUI();
        renderPhotosGrid();
    }

    function toggleBatchSelectMode() {
        isBatchSelectMode = !isBatchSelectMode;
        if (!isBatchSelectMode) {
            selectedPhotoIDs.clear();
        }
        updateBatchDeleteBtnUI();
        renderPhotosGrid();
    }

    function updateBatchDeleteBtnUI() {
        const btn = document.getElementById('batchDeleteBtn');
        const selectBtn = document.getElementById('selectAllBtn');
        const modeBtn = document.getElementById('batchSelectModeBtn');
        const showBatchUI = isBatchSelectMode || darkFilterState || selectedPhotoIDs.size > 0;

        if (modeBtn) {
            modeBtn.textContent = isBatchSelectMode ? '✕ 退出选择' : '☑ 批量选择';
            modeBtn.classList.toggle('fluent-btn-accent', isBatchSelectMode);
        }
        if (selectBtn) {
            selectBtn.style.display = showBatchUI ? '' : 'none';
            if (currentPhotosList && currentPhotosList.length) {
                const allSelected = currentPhotosList.every(p => selectedPhotoIDs.has(p.id));
                selectBtn.textContent = allSelected ? '☑ 取消全选' : '☑ 全选当前页';
            }
        }
        if (btn) {
            btn.style.display = showBatchUI ? '' : 'none';
            btn.textContent = '🗑️ 批量删除所选 (' + selectedPhotoIDs.size + ')';
        }
    }

    async function executeBatchDeletePhotos() {
        if (selectedPhotoIDs.size === 0) {
            if (darkFilterState && currentPhotosList && currentPhotosList.length) {
                const confirmedDark = await showFluentConfirm({
                    title: '批量清理纯黑废片',
                    content: '确定要全选并批量删除当前翻页视图中的全部 ' + currentPhotosList.length + ' 张纯黑误拍废片吗？此操作不可撤销！',
                    confirmText: '全选并删除',
                    isDanger: true
                });
                if (confirmedDark) {
                    currentPhotosList.forEach(p => selectedPhotoIDs.add(p.id));
                } else {
                    return;
                }
            } else {
                showFluentAlert({ title: '提示', content: '请先勾选需要批量删除的照片！' });
                return;
            }
        }

        const count = selectedPhotoIDs.size;
        const confirmedDelete = await showFluentConfirm({
            title: '批量删除确认',
            content: '确定要永久删除已选中的 ' + count + ' 张照片文件及其记录吗？此操作不可恢复！',
            confirmText: '永久删除',
            isDanger: true
        });
        if (!confirmedDelete) {
            return;
        }

        const status = document.getElementById('photoStatus');
        status.style.display = 'block';
        status.className = 'status-box status-info';
        status.textContent = '⏳ 正在批量删除 ' + count + ' 张照片...';

        fetch('/api/album/photos/batch-delete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ photo_ids: Array.from(selectedPhotoIDs) })
        })
        .then(r => r.json())
        .then(data => {
            if (data.status === 'success') {
                status.className = 'status-box status-success';
                status.textContent = '🎉 成功批量删除 ' + (data.deleted_count || count) + ' 张照片！';
                selectedPhotoIDs.clear();
                updateBatchDeleteBtnUI();
                loadAlbumPhotos(albumPage);
                setTimeout(() => { status.style.display = 'none'; }, 2500);
            } else {
                status.className = 'status-box status-error';
                status.textContent = '❌ 批量删除失败: ' + (data.error || '');
            }
        })
        .catch(err => {
            status.className = 'status-box status-error';
            status.textContent = '❌ 请求失败: ' + err;
        });
    }

    function renderPhotosGrid() {
        const grid = document.getElementById('photoGrid');
        const langData = i18n[currentLang];

        if (!currentPhotosList || !currentPhotosList.length) {
            grid.innerHTML = '<div style="grid-column: 1/-1; text-align:center; padding:40px; color:var(--fluent-text-secondary);">' + langData.noPhotos + '</div>';
            return;
        }

        let html = '';
        currentPhotosList.forEach(p => {
            const fileUrl = '/api/album/photos/' + p.id + '/file';
            const thumbUrl = '/api/album/photos/' + p.id + '/thumbnail';
            const favBadge = p.is_favorite ? '<div class="fav-badge">❤️</div>' : '';
            const darkBadge = p.is_dark ? '<div class="dark-badge">🖤 纯黑</div>' : '';
            const isChecked = selectedPhotoIDs.has(p.id);
            const showCheckbox = isBatchSelectMode || darkFilterState || selectedPhotoIDs.size > 0;
            const checkOverlay = showCheckbox ? ('<div class="photo-checkbox-overlay" onclick="event.stopPropagation(); toggleSelectPhoto(\'' + p.id + '\')">' +
                '<input type="checkbox" ' + (isChecked ? 'checked' : '') + ' tabindex="-1">' +
            '</div>') : '';

            let aspect = 1.33;
            if (p.width && p.height && p.height > 0) {
                aspect = (p.width / p.height).toFixed(2);
            } else if (p.is_video) {
                aspect = 1.78;
            }

            let videoOverlay = '';
            if (p.is_video) {
                videoOverlay = '<div class="video-badge">▶</div>';
            }

            let thumbElem = '';
            if (p.is_video) {
                thumbElem = '<video class="photo-thumb" src="' + fileUrl + '#t=0.5" preload="metadata" muted></video>';
            } else {
                thumbElem = '<img class="photo-thumb" src="' + thumbUrl + '" alt="' + p.filename + '" loading="lazy" decoding="async" onerror="this.onerror=null; this.src=\'' + fileUrl + '\';">';
            }

            const clickHandler = (isBatchSelectMode || darkFilterState || selectedPhotoIDs.size > 0) ? ('toggleSelectPhoto(\'' + p.id + '\')') : ('openPhotoModal(\'' + p.id + '\')');
            html += '<div class="photo-card ' + (isChecked ? 'photo-card-selected' : '') + '" style="--aspect-ratio:' + aspect + ';" onclick="' + clickHandler + '">' +
                checkOverlay +
                favBadge +
                darkBadge +
                videoOverlay +
                thumbElem +
                '<div class="photo-meta-overlay">' +
                    '<div class="photo-overlay-title">' + p.filename + '</div>' +
                    '<div>' + (p.size / 1024).toFixed(1) + ' KB</div>' +
                '</div>' +
            '</div>';
        });
        grid.innerHTML = html;
    }

    // Lightbox Interactive Transformations, Panning & Fullscreen
    let transformState = {
        zoom: 1.0,
        rotate: 0,
        panX: 0,
        panY: 0,
        isDragging: false,
        startX: 0,
        startY: 0
    };

    function resetTransformState() {
        transformState.zoom = 1.0;
        transformState.rotate = 0;
        transformState.panX = 0;
        transformState.panY = 0;
        transformState.isDragging = false;
        applyTransform();
        updateZoomLabel();
    }

    function applyTransform() {
        const img = document.getElementById('modalPhotoImg');
        if (!img) return;
        img.style.transform = 'translate(' + transformState.panX + 'px, ' + transformState.panY + 'px) rotate(' + transformState.rotate + 'deg) scale(' + transformState.zoom + ')';
    }

    function updateZoomLabel() {
        const btn = document.getElementById('btnZoomReset');
        if (btn) {
            btn.textContent = Math.round(transformState.zoom * 100) + '%%';
        }
    }

    function rotateCW() {
        transformState.rotate = (transformState.rotate + 90) %% 360;
        applyTransform();
    }

    function rotateCCW() {
        transformState.rotate = (transformState.rotate - 90 + 360) %% 360;
        applyTransform();
    }

    function zoomIn() {
        transformState.zoom = Math.min(5.0, transformState.zoom + 0.25);
        applyTransform();
        updateZoomLabel();
    }

    function zoomOut() {
        transformState.zoom = Math.max(0.5, transformState.zoom - 0.25);
        if (transformState.zoom <= 1.0) {
            transformState.panX = 0;
            transformState.panY = 0;
        }
        applyTransform();
        updateZoomLabel();
    }

    function toggleFullscreen() {
        const viewport = document.getElementById('modalViewport');
        if (!document.fullscreenElement && !document.webkitFullscreenElement) {
            if (viewport.requestFullscreen) {
                viewport.requestFullscreen();
            } else if (viewport.webkitRequestFullscreen) {
                viewport.webkitRequestFullscreen();
            }
        } else {
            if (document.exitFullscreen) {
                document.exitFullscreen();
            } else if (document.webkitExitFullscreen) {
                document.webkitExitFullscreen();
            }
        }
    }

    function handleFullscreenChange() {
        const btn = document.getElementById('btnFullscreen');
        const langData = i18n[currentLang];
        if (document.fullscreenElement || document.webkitFullscreenElement) {
            if (btn) btn.textContent = '🗗 ' + (langData.exitFullscreen || '退出全屏');
        } else {
            if (btn) btn.textContent = '⛶ ' + (langData.fullscreen || '全屏');
        }
    }

    document.addEventListener('fullscreenchange', handleFullscreenChange);
    document.addEventListener('webkitfullscreenchange', handleFullscreenChange);

    function navPrevPhoto() {
        if (!activePhotoObj || !currentPhotosList || !currentPhotosList.length) return;
        const idx = currentPhotosList.findIndex(p => p.id === activePhotoObj.id);
        if (idx > 0) {
            openPhotoModal(currentPhotosList[idx - 1].id);
        } else if (idx === 0) {
            openPhotoModal(currentPhotosList[currentPhotosList.length - 1].id);
        }
    }

    function navNextPhoto() {
        if (!activePhotoObj || !currentPhotosList || !currentPhotosList.length) return;
        const idx = currentPhotosList.findIndex(p => p.id === activePhotoObj.id);
        if (idx >= 0 && idx < currentPhotosList.length - 1) {
            openPhotoModal(currentPhotosList[idx + 1].id);
        } else if (idx === currentPhotosList.length - 1) {
            openPhotoModal(currentPhotosList[0].id);
        }
    }

    function openPhotoModal(photoId) {
        const photo = currentPhotosList.find(p => p.id === photoId);
        if (!photo) return;
        activePhotoObj = photo;

        resetTransformState();

        const fileUrl = '/api/album/photos/' + photo.id + '/file';
        document.getElementById('modalPhotoTitle').textContent = photo.filename;
        document.getElementById('modalDownloadBtn').href = fileUrl + '?download=1';
        document.getElementById('modalViewOriginalBtn').href = fileUrl;
        document.getElementById('modalFavBtn').textContent = photo.is_favorite ? '💔 取消收藏' : '❤️ 收藏';
        
        const img = document.getElementById('modalPhotoImg');
        const video = document.getElementById('modalVideoPlayer');
        const btnRotateCW = document.getElementById('btnRotateCW');
        const btnRotateCCW = document.getElementById('btnRotateCCW');
        const btnZoomIn = document.getElementById('btnZoomIn');
        const btnZoomOut = document.getElementById('btnZoomOut');
        const btnZoomReset = document.getElementById('btnZoomReset');

        if (photo.is_video) {
            img.style.display = 'none';
            video.style.display = 'block';
            video.src = fileUrl;
            video.load();
            const playPromise = video.play();
            if (playPromise !== undefined) {
                playPromise.catch(e => console.log('Autoplay handled:', e));
            }

            btnRotateCW.style.display = 'none';
            btnRotateCCW.style.display = 'none';
            btnZoomIn.style.display = 'none';
            btnZoomOut.style.display = 'none';
            btnZoomReset.style.display = 'none';

            document.getElementById('modalPhotoMeta').textContent = '🎬 视频文件 | 大小: ' + (photo.size / 1024).toFixed(1) + ' KB | 相对路径: ' + photo.rel_path;
        } else {
            video.pause();
            video.style.display = 'none';
            img.style.display = 'block';
            img.src = fileUrl;

            btnRotateCW.style.display = 'inline-flex';
            btnRotateCCW.style.display = 'inline-flex';
            btnZoomIn.style.display = 'inline-flex';
            btnZoomOut.style.display = 'inline-flex';
            btnZoomReset.style.display = 'inline-flex';

            const dim = (photo.width && photo.height) ? (photo.width + ' x ' + photo.height) : '未知';
            document.getElementById('modalPhotoMeta').textContent = '分辨率: ' + dim + ' | 大小: ' + (photo.size / 1024).toFixed(1) + ' KB | 相对路径: ' + photo.rel_path;
        }

        document.getElementById('photoModal').style.display = 'flex';
    }

    function closePhotoModal(e) {
        if (!e || e.target === document.getElementById('photoModal') || e.target.tagName === 'BUTTON' || e.target.classList.contains('fluent-btn')) {
            if (document.fullscreenElement || document.webkitFullscreenElement) {
                if (document.exitFullscreen) document.exitFullscreen();
            }
            const video = document.getElementById('modalVideoPlayer');
            if (video) video.pause();
            document.getElementById('photoModal').style.display = 'none';
        }
    }

    function toggleCurrentPhotoFav() {
        if (!activePhotoObj) return;
        fetch('/api/album/photos/' + activePhotoObj.id + '/favorite', { method: 'POST' })
            .then(r => r.json())
            .then(data => {
                if (data.photo) {
                    activePhotoObj.is_favorite = data.photo.is_favorite;
                    document.getElementById('modalFavBtn').textContent = activePhotoObj.is_favorite ? '💔 取消收藏' : '❤️ 收藏';
                    loadAlbumPhotos(albumPage);
                }
            });
    }

    function deleteCurrentPhoto() {
        if (!activePhotoObj) return;
        const langData = i18n[currentLang];
        if (!confirm(langData.confirmDeletePhoto)) return;

        fetch('/api/album/photos/' + activePhotoObj.id, { method: 'DELETE' })
            .then(r => r.json())
            .then(data => {
                closePhotoModal();
                loadAlbumPhotos(albumPage);
            });
    }

    async function computeFastFileHash(file) {
        const chunkSize = 64 * 1024;
        const size = file.size;
        let headSlice;
        if (size <= chunkSize * 2) {
            headSlice = await file.arrayBuffer();
        } else {
            headSlice = await file.slice(0, chunkSize).arrayBuffer();
        }
        const bytes = new Uint8Array(headSlice);
        let hash = 0x811c9dc5;
        for (let i = 0; i < bytes.length; i++) {
            hash ^= bytes[i];
            hash += (hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24);
        }
        return (hash >>> 0).toString(16) + '_' + size;
    }

    async function uploadFiles(files) {
        if (!files || !files.length) return;
        const langData = i18n[currentLang];
        const status = document.getElementById('uploadStatus');
        status.style.display = 'block';
        status.className = 'status-box status-info';
        status.innerHTML = langData.uploading;
        let completed = 0, failed = 0, instantHits = 0;

        for (const file of files) {
            const rel = currentPath ? currentPath + '/' + file.name : file.name;
            const hash = await computeFastFileHash(file);

            // 1. Pre-check Instant Upload (秒传指纹预检)
            let isInstantHit = false;
            try {
                const checkRes = await fetch('/api/upload/check', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        hash: hash,
                        size: file.size,
                        filename: file.name,
                        path: rel
                    })
                });
                const checkData = await checkRes.json();
                if (checkData.instant || checkData.status === 'hit') {
                    isInstantHit = true;
                    completed++;
                    instantHits++;
                }
            } catch (e) {
                console.log('Instant check fallback to normal upload:', e);
            }

            // 2. Normal Upload if not matched
            if (!isInstantHit) {
                const formData = new FormData();
                formData.append('file', file);
                try {
                    const res = await fetch('/api/upload?path=' + encodeURIComponent(rel), { method: 'POST', body: formData });
                    const data = await res.json();
                    if (data.status === 'success') {
                        completed++;
                    } else {
                        failed++;
                    }
                } catch (e) {
                    failed++;
                }
            }

            let msg = langData.uploading + ' ✅ ' + completed + ' | ❌ ' + failed + ' / ' + files.length;
            if (instantHits > 0) {
                msg += ' ⚡ (' + instantHits + ' 秒传/Instant)';
            }
            status.innerHTML = msg;
        }

        if (failed === 0) {
            status.className = 'status-box status-success';
            let msg = langData.uploadSuccess;
            if (instantHits > 0) {
                msg += ' ⚡ (包含 ' + instantHits + ' 个秒传文件)';
            }
            status.innerHTML = msg;
        } else {
            status.className = 'status-box status-error';
            status.innerHTML = langData.uploadFailed.replace('{completed}', completed).replace('{failed}', failed);
        }
        setTimeout(() => location.reload(), 1200);
    }

    function promptCreateFolder() {
        const langData = i18n[currentLang];
        showInputDialog({
            title: langData.mkdir || '📁 新建目录',
            promptText: langData.promptMkdir || '请输入新目录名称：',
            defaultValue: '',
            onConfirm: (name) => {
                if (!name || !name.trim()) return;
                const dirName = name.trim();
                const targetPath = currentPath ? currentPath + '/' + dirName : dirName;
                fetch('/api/mkdir?path=' + encodeURIComponent(targetPath), { method: 'POST' })
                    .then(r => r.json())
                    .then(data => {
                        if (data.status === 'success') {
                            location.reload();
                        } else {
                            alert((langData.error || 'Error') + ': ' + (data.error || ''));
                        }
                    })
                    .catch(err => alert(err));
            }
        });
    }

    function promptRename(oldName) {
        const langData = i18n[currentLang];
        showInputDialog({
            title: langData.rename || '✏️ 重命名',
            promptText: langData.promptRename || '请输入新的名称：',
            defaultValue: oldName,
            onConfirm: (newName) => {
                if (!newName || !newName.trim() || newName === oldName) return;
                const oldPath = currentPath ? currentPath + '/' + oldName : oldName;
                const newPath = currentPath ? currentPath + '/' + newName.trim() : newName.trim();
                fetch('/api/rename', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ old_path: oldPath, new_path: newPath })
                })
                .then(r => r.json())
                .then(data => {
                    if (data.status === 'success') {
                        location.reload();
                    } else {
                        alert((langData.error || 'Error') + ': ' + (data.error || ''));
                    }
                })
                .catch(err => alert(err));
            }
        });
    }

    async function confirmDelete(name, isDir) {
        const langData = i18n[currentLang];
        const msg = isDir ? langData.confirmDeleteDir.replace('{name}', name) : langData.confirmDeleteFile.replace('{name}', name);
        const ok = await showFluentConfirm({
            title: isDir ? '删除文件夹' : '删除文件',
            content: msg,
            confirmText: '永久删除',
            isDanger: true
        });
        if (!ok) return;

        const targetPath = currentPath ? currentPath + '/' + name : name;
        fetch('/api/delete?path=' + encodeURIComponent(targetPath), { method: 'POST' })
            .then(r => r.json())
            .then(data => {
                if (data.status === 'success') {
                    location.reload();
                } else {
                    alert((langData.error || 'Error') + ': ' + (data.error || ''));
                }
            })
            .catch(err => alert(err));
    }

    let targetReuploadFile = '';
    function triggerReupload(fileName) {
        targetReuploadFile = fileName;
        document.getElementById('reuploadInput').click();
    }

    function handleReupload(input) {
        if (!input.files || !input.files[0] || !targetReuploadFile) return;
        const langData = i18n[currentLang];
        const file = input.files[0];
        const status = document.getElementById('uploadStatus');
        status.style.display = 'block';
        status.className = 'status-box status-info';
        status.innerHTML = langData.reuploading.replace('{name}', targetReuploadFile);

        const formData = new FormData();
        formData.append('file', file);
        const targetPath = currentPath ? currentPath + '/' + targetReuploadFile : targetReuploadFile;

        fetch('/api/upload?path=' + encodeURIComponent(targetPath), { method: 'POST', body: formData })
            .then(r => r.json())
            .then(data => {
                if (data.status === 'success') {
                    status.className = 'status-box status-success';
                    status.innerHTML = langData.reuploadSuccess;
                    setTimeout(() => location.reload(), 1000);
                } else {
                    status.className = 'status-box status-error';
                    status.innerHTML = langData.reuploadFailed + (data.error || '');
                }
            })
            .catch(err => {
                status.className = 'status-box status-error';
                status.innerHTML = langData.error + ': ' + err;
            });
    }

    function changeFilePage(newPage) {
        const url = new URL(window.location.href);
        url.searchParams.set('page', newPage);
        const q = document.getElementById('searchInput').value.trim();
        if (q) url.searchParams.set('q', q);
        else url.searchParams.delete('q');
        window.location.href = url.toString();
    }

    function changeFilePageSize(newLimit) {
        const url = new URL(window.location.href);
        url.searchParams.set('limit', newLimit);
        url.searchParams.set('page', 1);
        const q = document.getElementById('searchInput').value.trim();
        if (q) url.searchParams.set('q', q);
        else url.searchParams.delete('q');
        window.location.href = url.toString();
    }

    let searchDebounceTimer = null;
    function filterTable() {
        const q = document.getElementById('searchInput').value.toLowerCase().trim();
        const rows = document.querySelectorAll('#fileTable tbody tr');
        rows.forEach(row => {
            const name = row.getAttribute('data-name');
            if (!name) return;
            const isHiddenRow = row.getAttribute('data-hidden') === 'true';
            if (!showHiddenState && isHiddenRow) {
                row.style.display = 'none';
                return;
            }
            if (name.toLowerCase().includes(q)) {
                row.style.display = '';
            } else {
                row.style.display = 'none';
            }
        });

        clearTimeout(searchDebounceTimer);
        searchDebounceTimer = setTimeout(() => {
            const url = new URL(window.location.href);
            const currentQ = url.searchParams.get('q') || '';
            if (q !== currentQ) {
                if (q) url.searchParams.set('q', q);
                else url.searchParams.delete('q');
                url.searchParams.set('page', 1);
                window.location.href = url.toString();
            }
        }, 600);
    }

    document.addEventListener('DOMContentLoaded', () => {
        updateLanguageUI();

        let savedTab = 'album';
        if (window.location.hash === '#files') {
            savedTab = 'files';
        } else if (window.location.hash === '#album') {
            savedTab = 'album';
        } else {
            savedTab = localStorage.getItem('blackhole_active_tab') || 'album';
        }
        switchMainTab(savedTab);
        loadAlbumPhotos(1);

        // Mouse Wheel Zoom & Drag Panning for Lightbox Viewport
        const viewport = document.getElementById('modalViewport');
        if (viewport) {
            viewport.addEventListener('wheel', (e) => {
                if (document.getElementById('photoModal').style.display === 'flex' && activePhotoObj && !activePhotoObj.is_video) {
                    e.preventDefault();
                    if (e.deltaY < 0) zoomIn();
                    else zoomOut();
                }
            }, { passive: false });

            viewport.addEventListener('pointerdown', (e) => {
                if (activePhotoObj && !activePhotoObj.is_video && transformState.zoom > 1.0) {
                    transformState.isDragging = true;
                    transformState.startX = e.clientX - transformState.panX;
                    transformState.startY = e.clientY - transformState.panY;
                    viewport.setPointerCapture(e.pointerId);
                }
            });

            viewport.addEventListener('pointermove', (e) => {
                if (transformState.isDragging) {
                    transformState.panX = e.clientX - transformState.startX;
                    transformState.panY = e.clientY - transformState.startY;
                    applyTransform();
                }
            });

            viewport.addEventListener('pointerup', (e) => {
                transformState.isDragging = false;
            });
        }
    });

    document.addEventListener('keydown', (e) => {
        if (document.getElementById('photoModal').style.display === 'flex') {
            if (e.key === 'ArrowLeft') navPrevPhoto();
            else if (e.key === 'ArrowRight') navNextPhoto();
            else if (e.key === 'Escape') closePhotoModal();
            else if (e.key === 'r' || e.key === 'R') rotateCW();
            else if (e.key === 'f' || e.key === 'F') toggleFullscreen();
            else if (e.key === '=' || e.key === '+') zoomIn();
            else if (e.key === '-') zoomOut();
        }
    });

    function pollSyncProgress() {
        fetch('/api/p2p/progress')
            .then(r => r.json())
            .then(data => {
                const widget = document.getElementById('syncProgressWidget');
                if (!widget) return;
                if (data.active) {
                    widget.style.display = 'block';
                    const pct = (data.percent || 0).toFixed(1);
                    document.getElementById('syncPercentText').textContent = pct + '%%';
                    document.getElementById('syncProgressBar').style.width = pct + '%%';
                    document.getElementById('syncFilesCount').textContent = '已完成 ' + (data.completed_files || 0) + ' / ' + (data.total_files || '?') + ' 项';
                    document.getElementById('syncCurrentFile').textContent = '正在传输: ' + (data.current_file || '-');
                    document.getElementById('syncSpeed').textContent = '速度: ' + (data.speed_mbps || 0).toFixed(1) + ' MB/s';
                } else {
                    widget.style.display = 'none';
                }
            })
            .catch(() => {});
    }

    setInterval(pollSyncProgress, 2500);
    pollSyncProgress();

    const dropZone = document.body;
    dropZone.addEventListener('dragover', (e) => { e.preventDefault(); });
    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        if (e.dataTransfer.files && e.dataTransfer.files.length) {
            uploadFiles(e.dataTransfer.files);
        }
    });
    </script>
</body>
</html>`, cleanRel, breadcrumbsHTML.String(), searchQuery, tableRowsHTML.String(), filePaginationInfo, filePaginationControls.String(), cleanRel)

	c.String(http.StatusOK, html)
}
