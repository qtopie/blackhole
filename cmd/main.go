package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/disk"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/net/webdav"
)

// SysInfoResponse 描述 Blackhole NAS 系统的运行容量指标
type SysInfoResponse struct {
	ShareDir    string `json:"share_dir"`
	TotalGB     uint64 `json:"total_gb"`
	FreeGB      uint64 `json:"free_gb"`
	UsedPercent string `json:"used_percent"`
}

// DownloadTask 记录后台下载任务状态
type DownloadTask struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	FileName  string    `json:"file_name"`
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	tasksMu sync.RWMutex
	tasks   = make(map[string]*DownloadTask)
)

// ─── 安全文件夹 (Vault) 独立加密工具组 ──────────────────────────────────────────

const (
	SaltSize = 16
	KeyLen   = 32 // AES-256
)

func deriveKey(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, 10000, KeyLen, sha256.New)
}

func encryptData(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// 打包存储格式: salt (16 bytes) + nonce (12 bytes) + ciphertext
	finalData := make([]byte, 0, len(salt)+len(nonce)+len(ciphertext))
	finalData = append(finalData, salt...)
	finalData = append(finalData, nonce...)
	finalData = append(finalData, ciphertext...)

	return finalData, nil
}

func decryptData(data []byte, passphrase string) ([]byte, error) {
	if len(data) < SaltSize+12 {
		return nil, fmt.Errorf("加密数据无效或长度过短")
	}

	salt := data[:SaltSize]
	nonce := data[SaltSize : SaltSize+12]
	ciphertext := data[SaltSize+12:]

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败：密码错误或数据损坏")
	}

	return plaintext, nil
}

func main() {
	shareDir := os.Getenv("BLACKHOLE_SHARE_DIR")
	if shareDir == "" {
		shareDir = "./nas_share"
	}
	port := os.Getenv("BLACKHOLE_PORT")
	if port == "" {
		port = "50056"
	}
	username := os.Getenv("BLACKHOLE_USER")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("BLACKHOLE_PASS")
	if password == "" {
		password = "password"
	}

	if err := os.MkdirAll(shareDir, 0755); err != nil {
		log.Fatalf("无法创建 Blackhole NAS 共享目录: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	auth := gin.BasicAuth(gin.Accounts{
		username: password,
	})

	// 1. 系统与标准文件传输 API 组
	api := r.Group("/api", auth)
	{
		api.GET("/sysinfo", func(c *gin.Context) {
			usage, err := disk.UsageWithContext(c.Request.Context(), shareDir)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取磁盘容量失败: %v", err)})
				return
			}

			c.JSON(http.StatusOK, SysInfoResponse{
				ShareDir:    shareDir,
				TotalGB:     usage.Total / (1024 * 1024 * 1024),
				FreeGB:      usage.Free / (1024 * 1024 * 1024),
				UsedPercent: fmt.Sprintf("%.2f%%", usage.UsedPercent),
			})
		})

		// ─── 安全文件夹 (Vault) 专属加解密 API ────────────────────────────────

		// 上传加密文件到安全文件夹 (.enc 格式落盘)
		api.POST("/vault/upload", func(c *gin.Context) {
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
			dstPath := filepath.Join(shareDir, "vault", filepath.Clean(relPath))
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			// 执行 AES-256-GCM 加密
			encData, err := encryptData(rawBytes, vaultPass)
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
		})

		// 解密并读取安全文件夹中的文件
		api.GET("/vault/download", func(c *gin.Context) {
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
			targetPath := filepath.Join(shareDir, "vault", filepath.Clean(relPath))
			if _, err := os.Stat(targetPath); os.IsNotExist(err) && !strings.HasSuffix(targetPath, ".enc") {
				targetPath += ".enc"
			}

			encData, err := os.ReadFile(targetPath)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "安全文件不存在"})
				return
			}

			// 执行解密
			plainBytes, err := decryptData(encData, vaultPass)
			if err != nil {
				c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
				return
			}

			outFileName := filepath.Base(strings.TrimSuffix(targetPath, ".enc"))
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", outFileName))
			c.Data(http.StatusOK, "application/octet-stream", plainBytes)
		})

		// 直接文件上传接口
		api.POST("/upload", func(c *gin.Context) {
			relPath := c.Query("path")
			if relPath == "" {
				fileHeader, err := c.FormFile("file")
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "missing file parameter"})
					return
				}
				relPath = fileHeader.Filename
			}

			dst := filepath.Join(shareDir, filepath.Clean(relPath))
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
		})

		// 异步远程下载发起接口
		api.POST("/download", func(c *gin.Context) {
			var req struct {
				URL      string `json:"url" binding:"required"`
				FileName string `json:"file_name"`
				UseOget  bool   `json:"use_oget"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
			mode := "standard"
			if req.UseOget {
				mode = "oget"
			}

			task := &DownloadTask{
				ID:        taskID,
				URL:       req.URL,
				FileName:  req.FileName,
				Mode:      mode,
				Status:    "downloading",
				CreatedAt: time.Now(),
			}

			tasksMu.Lock()
			tasks[taskID] = task
			tasksMu.Unlock()

			go startBackgroundDownload(task, shareDir)

			c.JSON(http.StatusOK, gin.H{
				"task_id": taskID,
				"status":  "downloading",
				"mode":    mode,
			})
		})

		// 查询后台下载任务状态
		api.GET("/download/status", func(c *gin.Context) {
			taskID := c.Query("task_id")
			tasksMu.RLock()
			defer tasksMu.RUnlock()
			if taskID != "" {
				task, ok := tasks[taskID]
				if !ok {
					c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
					return
				}
				c.JSON(http.StatusOK, task)
				return
			}
			c.JSON(http.StatusOK, tasks)
		})
	}

	// 2. WebDAV 文件传输挂载入口
	webdavHandler := &webdav.Handler{
		Prefix:     "/shared",
		FileSystem: webdav.Dir(shareDir),
		LockSystem: webdav.NewMemLS(),
	}

	r.NoRoute(auth, func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/shared") {
			if c.Request.Method == http.MethodGet {
				relPath := strings.TrimPrefix(c.Request.URL.Path, "/shared")
				if relPath == "" {
					relPath = "/"
				}
				targetPath := filepath.Join(shareDir, filepath.Clean(relPath))
				fi, err := os.Stat(targetPath)
				if err == nil && fi.IsDir() {
					renderDirectoryHTML(c, shareDir, relPath)
					return
				}
			}

			webdavHandler.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		fmt.Printf("🚀 Blackhole NAS 服务已启动！监听端口 :%s，存储路径: %s\n", port, shareDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Blackhole NAS 发生错误: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	fmt.Println("Blackhole NAS 服务已停止")
}

func startBackgroundDownload(task *DownloadTask, shareDir string) {
	savePath := shareDir
	if task.FileName != "" {
		savePath = filepath.Join(shareDir, task.FileName)
	}

	if task.Mode == "oget" {
		ogetBin, err := exec.LookPath("oget")
		if err != nil {
			ogetBin = "/opt/blackhole/oget"
		}

		args := []string{}
		if task.FileName != "" {
			args = append(args, "-file", savePath)
		}
		args = append(args, task.URL)

		cmd := exec.Command(ogetBin, args...)
		cmd.Dir = shareDir
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf

		if err := cmd.Run(); err != nil {
			tasksMu.Lock()
			task.Status = "failed"
			task.Error = fmt.Sprintf("oget error: %v, stderr: %s", err, errBuf.String())
			tasksMu.Unlock()
			return
		}
	} else {
		resp, err := http.Get(task.URL)
		if err != nil {
			tasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			tasksMu.Unlock()
			return
		}
		defer resp.Body.Close()

		if task.FileName == "" {
			parts := strings.Split(task.URL, "/")
			savePath = filepath.Join(shareDir, parts[len(parts)-1])
		}

		out, err := os.Create(savePath)
		if err != nil {
			tasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			tasksMu.Unlock()
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, resp.Body); err != nil {
			tasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			tasksMu.Unlock()
			return
		}
	}

	tasksMu.Lock()
	task.Status = "completed"
	tasksMu.Unlock()
}

func renderDirectoryHTML(c *gin.Context, baseDir string, relPath string) {
	fullPath := filepath.Join(baseDir, filepath.Clean(relPath))
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		c.String(http.StatusInternalServerError, "读取目录失败: %v", err)
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
    <h1>📂 Blackhole NAS 存储目录: %s</h1>
    <ul>
`, relPath, relPath)

	if relPath != "/" && relPath != "" {
		html += `<li><a href="../">📁 .. (返回上级目录)</a></li>`
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
