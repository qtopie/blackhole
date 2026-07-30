package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qtopie/blackhole/internal/config"
	"github.com/qtopie/blackhole/internal/nfs"
	"github.com/qtopie/blackhole/internal/server"
	"github.com/qtopie/blackhole/internal/smb"
	"github.com/qtopie/blackhole/internal/zeroconf"
)

func main() {
	cfg := config.LoadConfig()

	if err := os.MkdirAll(cfg.ShareDir, 0755); err != nil {
		log.Fatalf("Failed to create Blackhole NAS share directory: %v", err)
	}

	httpSrv := server.NewServer(cfg)
	go func() {
		if err := httpSrv.Start(); err != nil {
			log.Fatalf("Blackhole NAS HTTP/WebDAV server error: %v", err)
		}
	}()

	var smbSrv *smb.Server
	if cfg.EnableSMB {
		smbSrv = smb.NewServer(cfg)
		go func() {
			if err := smbSrv.Start(); err != nil {
				log.Printf("SMB3 server error: %v", err)
			}
		}()
	}

	var nfsSrv *nfs.Server
	if cfg.EnableNFS {
		nfsSrv = nfs.NewServer(cfg)
		go func() {
			if err := nfsSrv.Start(); err != nil {
				log.Printf("NFSv4 server error: %v", err)
			}
		}()
	}

	zc, err := zeroconf.RegisterServices(cfg)
	if err != nil {
		log.Printf("mDNS registration error: %v", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if zc != nil {
		zc.Shutdown()
	}
	if smbSrv != nil {
		_ = smbSrv.Shutdown(ctx)
	}
	if nfsSrv != nil {
		_ = nfsSrv.Shutdown(ctx)
	}
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("Error shutting down Blackhole NAS HTTP server: %v", err)
	}
	fmt.Println("Blackhole NAS server stopped")
}
