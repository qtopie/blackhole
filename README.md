# Blackhole NAS Server

Blackhole is a high-performance, ultra-lightweight standalone NAS core server written in Go, specifically optimized for RISC-V architectures (such as SpacemiT K1 / Milk-V Jupiter) and resource-constrained devices.

## Key Features

- **Multi-Protocol File Sharing**: Integrated WebDAV support for native mounting across macOS (Finder), Windows (Network Location), iOS (FE File Explorer, Documents), and Android.
- **Embedded `oget` Engine**: Native integration of multi-protocol transfer engines supporting HTTP/HTTPS, FTP, BitTorrent (`.torrent`), Magnet links (`magnet:?xt=...`), and P2P acceleration.
- **Encrypted Safe Folder (Vault)**: AES-256-GCM authenticated encryption paired with PBKDF2 key derivation for secure, zero-knowledge file vaulting (zero plaintext exposure on disk).
- **Web Interface**: Automatic, responsive dark-mode HTML directory renderer when accessing via standard web browsers.
- **System Monitoring**: Real-time disk capacity, total/free space, and usage percentage API.
- **Low Footprint**: Tiny single binary (<12MB) with low memory overhead (<10MB RAM).

## REST API Overview

Base URL: `http://<server-ip>:50056` (Basic Authentication required)

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/sysinfo` | `GET` | Retrieve disk capacity, free space, and usage metrics |
| `/api/upload` | `POST` | Upload files via Form Multipart or Direct Stream to NAS storage |
| `/api/download` | `POST` | Trigger asynchronous remote download (HTTP, FTP, BitTorrent, Magnet) |
| `/api/download/status` | `GET` | Query progress and status of background downloads |
| `/api/vault/upload` | `POST` | Encrypt and save files to the Vault (`X-Vault-Password` header required) |
| `/api/vault/download` | `GET` | Decrypt and read files from the Vault (`X-Vault-Password` header required) |
| `/shared/*` | `ANY` | WebDAV mount endpoint / Browser directory viewer |

## Quick Start & Deployment

### Local Build & Run

```bash
cd cmd
go run main.go
```

### Cross-Compile for SpacemiT K1 (RISC-V 64)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -o bin/blackhole-server-riscv64 cmd/main.go
```

### Deploy to SpacemiT K1 via Systemd

```bash
bash deploy-k1.sh <spacemit-k1-ip>
```
