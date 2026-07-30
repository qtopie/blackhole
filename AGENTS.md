# AGENTS.md

Welcome to the **Blackhole NAS** project!

This document provides developer guidelines for AI agents and human contributors working on this codebase.

## Repository Overview

Blackhole is a high-performance, ultra-lightweight standalone NAS core server written in Go, specifically optimized for RISC-V architectures (such as SpacemiT K1 / Milk-V Jupiter) and resource-constrained devices.

## Project Layout

This project strictly follows the standard Go project layout. For full details on package responsibilities and directory structure, please refer to [docs/project-layout.md](file:///home/qtopierw/workspace/projects/blackhole/docs/project-layout.md).

### Quick Summary

- **Entry Point**: [cmd/main.go](file:///home/qtopierw/workspace/projects/blackhole/cmd/main.go)
- **Internal Logic**: Located under `internal/`:
  - `internal/config`: Configuration loader
  - `internal/handler`: HTTP & Web UI handlers
  - `internal/server`: HTTP server & Gin routing
  - `internal/smb`: Native SMB2/SMB3 protocol server
  - `internal/nfs`: NFSv4 server stub
  - `internal/vault`: AES-256-GCM Zero-Knowledge encryption
  - `internal/zeroconf`: mDNS / DNS-SD service broadcast

## Coding Guidelines

1. **Language & Style**: Follow standard Go conventions (`gofmt`, `go vet`).
2. **Log Messages**: Use English for all log outputs and console messages.
3. **Internal Packages**: Keep core domain logic inside `internal/` packages.
4. **Cross-Compilation**: Ensure compatibility with `GOOS=linux GOARCH=riscv64 CGO_ENABLED=0`.
