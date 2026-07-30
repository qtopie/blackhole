package handler

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/qtopie/blackhole/internal/downloader"
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

type Handler struct {
	shareDir      string
	downloadMgr   *downloader.Manager
	webdavHandler *webdav.Handler
}

func NewHandler(shareDir string, downloadMgr *downloader.Manager) *Handler {
	return &Handler{
		shareDir:    shareDir,
		downloadMgr: downloadMgr,
		webdavHandler: &webdav.Handler{
			Prefix:     "/shared",
			FileSystem: webdav.Dir(shareDir),
			LockSystem: webdav.NewMemLS(),
		},
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
		// 支持 Stream
		rawBytes, err = io.ReadAll(c.Request.Body)
		if err != nil || len(rawBytes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未读取到有效文件数据"})
			return
		}
	}

	// 确保后缀为 .enc
	if !strings.HasSuffix(relPath, ".enc") {
		relPath += ".enc"
	}

	// 安全文件夹根目录存放在 shareDir/vault 下
	dstPath := filepath.Join(h.shareDir, "vault", filepath.Clean(relPath))
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 执行 AES-256-GCM 加密
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

	// 允许指定带 .enc 或不带 .enc
	targetPath := filepath.Join(h.shareDir, "vault", filepath.Clean(relPath))
	if _, err := os.Stat(targetPath); os.IsNotExist(err) && !strings.HasSuffix(targetPath, ".enc") {
		targetPath += ".enc"
	}

	encData, err := os.ReadFile(targetPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "安全文件不存在"})
		return
	}

	// 执行解密
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

	dst := filepath.Join(h.shareDir, filepath.Clean(relPath))
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


func (h *Handler) HandleWebDAV(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/shared") {
		h.webdavHandler.ServeHTTP(c.Writer, c.Request)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
}

func (h *Handler) RenderWebUI(c *gin.Context) {
	relPath := c.Param("path")
	if relPath == "" {
		relPath = "/"
	}

	fullPath := filepath.Join(h.shareDir, filepath.Clean(relPath))
	fi, err := os.Stat(fullPath)
	if err != nil {
		c.String(http.StatusNotFound, "File or directory not found")
		return
	}

	if !fi.IsDir() {
		c.File(fullPath)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to read directory: %v", err)
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Blackhole NAS - %s</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; padding: 20px; background: #0f172a; color: #f8fafc; }
        h1 { color: #38bdf8; font-size: 20px; }
        ul { list-style: none; padding: 0; }
        li { padding: 8px 12px; border-bottom: 1px solid #1e293b; display: flex; justify-content: space-between; }
        a { color: #7dd3fc; text-decoration: none; }
        a:hover { text-decoration: underline; }
        .meta { color: #64748b; font-size: 14px; }
    </style>
</head>
<body>
    <h1>📂 Blackhole NAS: %s</h1>
    <ul>
`, relPath, relPath)

	if relPath != "/" && relPath != "" {
		html += `<li><a href="../">📁 .. (Up one level)</a></li>`
	}

	for _, entry := range entries {
		name := entry.Name()
		icon := "📄"
		link := name
		if entry.IsDir() {
			icon = "📁"
			link += "/"
		}
		info, _ := entry.Info()
		sizeStr := ""
		if info != nil && !entry.IsDir() {
			sizeStr = fmt.Sprintf("%.2f KB", float64(info.Size())/1024.0)
		}

		html += fmt.Sprintf(`<li><span>%s <a href="%s">%s</a></span><span class="meta">%s</span></li>`, icon, link, name, sizeStr)
	}

	html += `
    </ul>
</body>
</html>`

	c.String(http.StatusOK, html)
}
