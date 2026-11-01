package nfs

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/qtopie/blackhole/internal/config"
)

type Server struct {
	cfg        *config.Config
	listener   net.Listener
	hm         *HandleManager
	handler    *NFSHandler
	allowedIPs []*net.IPNet
	mu         sync.Mutex
	closed     bool
}

func NewServer(cfg *config.Config) *Server {
	hm := NewHandleManager(cfg.ShareDir)
	handler := NewNFSHandler(cfg.ShareDir, hm)

	var allowed []*net.IPNet
	if cfg.NFSAllowedIPs != "" {
		parts := strings.Split(cfg.NFSAllowedIPs, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !strings.Contains(p, "/") {
				if ip := net.ParseIP(p); ip != nil {
					if ip4 := ip.To4(); ip4 != nil {
						p = p + "/32"
					} else {
						p = p + "/128"
					}
				}
			}
			_, ipNet, err := net.ParseCIDR(p)
			if err == nil {
				allowed = append(allowed, ipNet)
			}
		}
	}

	return &Server{
		cfg:        cfg,
		hm:         hm,
		handler:    handler,
		allowedIPs: allowed,
	}
}

func (s *Server) IsIPAllowed(ipStr string) bool {
	if len(s.allowedIPs) == 0 {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, netRange := range s.allowedIPs {
		if netRange.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) Start() error {
	addr := ":" + s.cfg.NFSPort
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind NFSv4 server on port %s: %w", s.cfg.NFSPort, err)
	}
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()

	if len(s.allowedIPs) > 0 {
		fmt.Printf("🚀 NFSv4 protocol server listening on port :%s (Share: %s, Allowed IPs: %s)\n", s.cfg.NFSPort, s.cfg.ShareDir, s.cfg.NFSAllowedIPs)
	} else {
		fmt.Printf("🚀 NFSv4 protocol server listening on port :%s (Share: %s)\n", s.cfg.NFSPort, s.cfg.ShareDir)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}

		host, _, splitErr := net.SplitHostPort(conn.RemoteAddr().String())
		if splitErr == nil && !s.IsIPAllowed(host) {
			log.Printf("⚠️ [NFSv4] Connection from unauthorized IP rejected: %s", host)
			conn.Close()
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	for {
		payload, err := ReadRecord(conn)
		if err != nil {
			if err != io.EOF {
				// connection closed or reset
			}
			return
		}

		resp, err := s.processRPC(payload)
		if err != nil {
			log.Printf("nfs rpc error from %s: %v", conn.RemoteAddr(), err)
			return
		}

		if err := WriteRecord(conn, resp); err != nil {
			log.Printf("nfs write response to %s failed: %v", conn.RemoteAddr(), err)
			return
		}
	}
}

func (s *Server) processRPC(payload []byte) ([]byte, error) {
	r := NewXDRReader(payload)
	xid, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	mtype, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	if mtype != RPCMsgCall {
		return nil, fmt.Errorf("unexpected rpc msg type %d", mtype)
	}
	rpcVers, err := r.ReadUint32()
	if err != nil || rpcVers != 2 {
		return nil, fmt.Errorf("unsupported rpc version %d", rpcVers)
	}
	prog, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	vers, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	proc, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}

	// Read credentials
	_, _ = r.ReadUint32() // cred flavor
	_, _ = r.ReadBytes()  // cred body
	// Read verifier
	_, _ = r.ReadUint32() // verf flavor
	_, _ = r.ReadBytes()  // verf body

	w := NewXDRWriter()
	// RPC Reply Header
	w.WriteUint32(xid)
	w.WriteUint32(RPCMsgReply)
	w.WriteUint32(RPCMsgAccepted)
	// Verifier (flavor none, len 0)
	w.WriteUint32(AuthFlavorNone)
	w.WriteBytes(nil)

	if prog != RPCProgramNFS || vers != RPCVersion4 {
		// PROG_MISMATCH / PROG_UNAVAIL
		w.WriteUint32(1) // PROG_UNAVAIL
		return w.Bytes(), nil
	}

	switch proc {
	case RPCProcNull:
		w.WriteUint32(RPCAcceptSuccess)
		return w.Bytes(), nil

	case RPCProcCompound:
		compoundResp, err := s.handler.ProcessCompound(r)
		if err != nil {
			w.WriteUint32(0) // RPCAcceptSuccess, but internal error
			// Output error compound reply
			errWriter := NewXDRWriter()
			errWriter.WriteUint32(NFS4ERRSERVERFAULT)
			errWriter.WriteString("")
			errWriter.WriteUint32(0)
			w.WriteRaw(errWriter.Bytes())
			return w.Bytes(), nil
		}
		w.WriteUint32(RPCAcceptSuccess)
		w.WriteRaw(compoundResp)
		return w.Bytes(), nil

	default:
		w.WriteUint32(3) // PROC_UNAVAIL
		return w.Bytes(), nil
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	l := s.listener
	s.mu.Unlock()

	if l != nil {
		return l.Close()
	}
	return nil
}
