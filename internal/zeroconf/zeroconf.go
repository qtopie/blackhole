package zeroconf

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/grandcat/zeroconf"
	"github.com/qtopie/blackhole/internal/config"
)

type Service struct {
	servers []*zeroconf.Server
}

func RegisterServices(cfg *config.Config) (*Service, error) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "spacemit-k1"
	}

	var servers []*zeroconf.Server

	// 1. Register WebDAV (_webdav._tcp)
	webdavPort, _ := strconv.Atoi(cfg.Port)
	webdavSrv, err := zeroconf.Register(
		hostname+" (WebDAV)",
		"_webdav._tcp",
		"local.",
		webdavPort,
		[]string{"txtv=1", "path=/shared"},
		nil,
	)
	if err != nil {
		log.Printf("Failed to register mDNS WebDAV service: %v", err)
	} else {
		servers = append(servers, webdavSrv)
	}

	// 2. Register SMB (_smb._tcp)
	if cfg.EnableSMB {
		smbPort, _ := strconv.Atoi(cfg.SMBPort)
		smbSrv, err := zeroconf.Register(
			hostname,
			"_smb._tcp",
			"local.",
			smbPort,
			[]string{"txtv=1"},
			nil,
		)
		if err != nil {
			log.Printf("Failed to register mDNS SMB service: %v", err)
		} else {
			servers = append(servers, smbSrv)
		}
	}

	// 3. Register Device Info / HTTP (_http._tcp)
	httpPort, _ := strconv.Atoi(cfg.Port)
	httpSrv, err := zeroconf.Register(
		hostname+" (Domour Drive)",
		"_http._tcp",
		"local.",
		httpPort,
		[]string{"txtv=1", "vendor=Domour"},
		nil,
	)
	if err != nil {
		log.Printf("Failed to register mDNS HTTP service: %v", err)
	} else {
		servers = append(servers, httpSrv)
	}

	fmt.Printf("📡 mDNS / Zeroconf services registered on local network (Hostname: %s)\n", hostname)

	return &Service{servers: servers}, nil
}

func (s *Service) Shutdown() {
	for _, srv := range s.servers {
		srv.Shutdown()
	}
}
