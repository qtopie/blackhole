package nfs

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/qtopie/blackhole/internal/config"
)

type Server struct {
	cfg      *config.Config
	listener net.Listener
}

func NewServer(cfg *config.Config) *Server {
	return &Server{
		cfg: cfg,
	}
}

func (s *Server) Start() error {
	addr := ":" + s.cfg.NFSPort
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind NFSv4 server on port %s: %w", s.cfg.NFSPort, err)
	}
	s.listener = l
	fmt.Printf("🚀 NFSv4 protocol server listening on port :%s (Share: %s)\n", s.cfg.NFSPort, s.cfg.ShareDir)

	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-context.Background().Done():
				return nil
			default:
				// Listener closed
				return nil
			}
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	// Process NFSv4 RPC COMPOUND requests
	log.Printf("Incoming NFS connection from %s", conn.RemoteAddr())
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
