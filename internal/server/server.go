package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/qtopie/blackhole/internal/album"
	"github.com/qtopie/blackhole/internal/book"
	"github.com/qtopie/blackhole/internal/config"
	"github.com/qtopie/blackhole/internal/downloader"
	"github.com/qtopie/blackhole/internal/handler"
	"github.com/qtopie/blackhole/internal/store"
)

type Server struct {
	cfg        *config.Config
	download   *downloader.Manager
	handler    *handler.Handler
	httpServer *http.Server
}

func NewServer(cfg *config.Config) *Server {
	if cfg.PublicDir != "" {
		_ = os.MkdirAll(cfg.PublicDir, 0755)
	}
	dl := downloader.NewManager(cfg.ShareDir)
	st := store.NewStore(cfg)
	al := album.NewManager(cfg.AlbumDir, st)
	bk := book.NewManager(cfg.BooksDir, st)
	h := handler.NewHandler(cfg.ShareDir, dl, al, bk)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	basicAuth := gin.BasicAuth(gin.Accounts{
		cfg.Username: cfg.Password,
	})

	flexibleAuth := func(c *gin.Context) {
		authenticated := false
		u, p, ok := c.Request.BasicAuth()
		if ok && u == cfg.Username && p == cfg.Password {
			authenticated = true
		} else if c.Query("auth") == cfg.Password || c.Query("token") == cfg.Password {
			authenticated = true
		} else if cookie, err := c.Cookie("blackhole_auth"); err == nil && cookie == cfg.Password {
			authenticated = true
		}

		if authenticated {
			c.Set("authenticated", true)
			c.Next()
			return
		}

		c.Set("authenticated", false)

		// Anonymous read-only: only allow GET / HEAD for /public and /public/*
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" {
			reqPath := path.Clean(c.Request.URL.Path)
			if reqPath == "/public" || strings.HasPrefix(reqPath, "/public/") {
				c.Next()
				return
			}
		}

		basicAuth(c)
	}

	api := r.Group("/api", flexibleAuth)
	{
		api.GET("/sysinfo", h.GetSysInfo)
		api.GET("/p2p/progress", h.GetP2PProgress)
		api.POST("/vault/upload", h.VaultUpload)
		api.GET("/vault/download", h.VaultDownload)
		api.POST("/upload", h.Upload)
		api.POST("/upload/check", h.CheckUploadInstant)
		api.POST("/mkdir", h.Mkdir)
		api.POST("/rename", h.Rename)
		api.POST("/delete", h.Delete)
		api.DELETE("/delete", h.Delete)

		// Album APIs
		api.GET("/album/config", h.GetAlbumConfig)
		api.POST("/album/config", h.UpdateAlbumConfig)
		api.GET("/album/photos", h.ListAlbumPhotos)
		api.POST("/album/scan", h.ScanAlbumPhotos)
		api.POST("/album/photos/:id/favorite", h.TogglePhotoFavorite)
		api.DELETE("/album/photos/:id", h.DeleteAlbumPhoto)
		api.POST("/album/photos/batch-delete", h.BatchDeleteAlbumPhotos)
		api.GET("/album/photos/:id/file", h.GetPhotoFile)
		api.GET("/album/photos/:id/thumbnail", h.GetPhotoThumbnail)

		// Book Library APIs
		api.GET("/books/config", h.GetBooksConfig)
		api.POST("/books/config", h.UpdateBooksConfig)
		api.GET("/books/list", h.ListBooks)
		api.POST("/books/scan", h.ScanBooks)
		api.GET("/books/:id/cover", h.GetBookCover)
		api.GET("/books/:id/file", h.GetBookFile)
		api.DELETE("/books/:id", h.DeleteBook)
	}

	r.GET("/", flexibleAuth, h.RenderWebUI)

	r.NoRoute(flexibleAuth, func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/shared") {
			h.HandleWebDAV(c)
			return
		}
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" {
			h.RenderWebUI(c)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	return &Server{
		cfg:        cfg,
		download:   dl,
		handler:    h,
		httpServer: srv,
	}
}

func (s *Server) Start() error {
	fmt.Printf("🚀 Blackhole NAS server started! Listening on port :%s, share dir: %s, album dir: %s\n", s.cfg.Port, s.cfg.ShareDir, s.cfg.AlbumDir)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
