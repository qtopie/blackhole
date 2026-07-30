# Blackhole Project Layout

This document describes the standard Go project structure used in **Blackhole NAS**.

## Directory Structure

```text
blackhole/
├── cmd/
│   └── main.go                 # Application entry point, CLI flag & signal handling
├── internal/
│   ├── config/
│   │   └── config.go           # Environment variables and system configuration loader
│   ├── downloader/
│   │   └── downloader.go       # Background download manager
│   ├── handler/
│   │   └── handler.go          # HTTP API handlers, Web UI, and WebDAV router integration
│   ├── nfs/
│   │   └── nfs.go              # NFSv4 user-space protocol server stub
│   ├── server/
│   │   └── server.go           # Gin engine setup, authentication, and HTTP server lifecycle
│   ├── smb/
│   │   └── smb.go              # Native SMB2/SMB3 protocol server implementation
│   ├── vault/
│   │   └── vault.go            # AES-256-GCM + PBKDF2 Zero-Knowledge encrypted vault
│   └── zeroconf/
│       └── zeroconf.go         # mDNS / DNS-SD local network service discovery broadcast
├── docs/
│   ├── project-layout.md       # Project architecture and layout documentation
│   └── regression-testing/
│       └── smb-testing.md      # Regression test cases for SMB2/SMB3 protocol
├── scripts/
│   └── deploy-k1.sh            # Deployment script for SpacemiT K1 RISC-V SBC
├── tests/
│   ├── test_blackhole.sh       # Shell verification script
│   ├── test_blackhole.py       # Python API and WebDAV test suite
│   ├── test_smb_protocol.py    # Python SMB2/SMB3 raw packet test script
│   └── test_smb3.py            # Python SMB3 protocol handshake test suite
├── bin/                        # Output directory for compiled binaries
├── blackhole.service           # Systemd unit service configuration
├── go.mod                      # Go module definition
├── go.sum                      # Go module checksums
└── README.md                   # Project overview, features, and user guide
```

## Package Overview

- **`cmd/`**: Contains the main package for building executable binaries. No business logic should reside here.
- **`internal/`**: Private application code. External projects cannot import packages under `internal/`.
  - **`config`**: Loads configurations from environment variables (`BLACKHOLE_PORT`, `BLACKHOLE_PASS`, etc.).
  - **`handler`**: Contains REST API implementations, Web UI renderer, and WebDAV proxying.
  - **`server`**: Assembles Gin HTTP routes, authentication middlewares, and handles HTTP server graceful shutdown.
  - **`smb`**: Low-level TCP socket listener parsing NetBIOS frames and SMB2/SMB3 header packets.
  - **`nfs`**: NFSv4 TCP listener handling network RPC mounts.
  - **`vault`**: Cryptographic module for secure file storage using AES-256-GCM with PBKDF2 salt key derivation.
  - **`zeroconf`**: Registers mDNS DNS-SD services for network auto-discovery on macOS/Linux.
