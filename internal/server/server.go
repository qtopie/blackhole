package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/qtopie/blackhole/internal/config"
	"github.com/qtopie/blackhole/internal/downloader"
	"github.com/qtopie/blackhole/internal/handler"
)

type Server struct {
	cfg        *config.Config
	download   *downloader.Manager
	handler    *handler.Handler
	httpServer *http.Server
}

func NewServer(cfg *config.Config) *Server {
	dl := downloader.NewManager(cfg.ShareDir)
	h := handler.NewHandler(cfg.ShareDir, dl)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	auth := gin.BasicAuth(gin.Accounts{
		cfg.Username: cfg.Password,
	})

	api := r.Group("/api", auth)
	{
		api.GET("/sysinfo", h.GetSysInfo)
		api.POST("/vault/upload", h.VaultUpload)
		api.GET("/vault/download", h.VaultDownload)
		api.POST("/upload", h.Upload)
	}

	ui := r.Group("/ui", auth)
	{
		ui.GET("/*path", h.RenderWebUI)
	}

	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/ui/")
	})

	r.NoRoute(auth, h.HandleWebDAV)

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
	fmt.Printf("🚀 Blackhole NAS server started! Listening on port :%s, share dir: %s\n", s.cfg.Port, s.cfg.ShareDir)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
