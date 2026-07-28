#!/bin/bash
set -e

K1_HOST=${1:-"spacemit-k1"}

echo "🚀 开始交叉编译 Blackhole NAS 核心服务 (Linux RISC-V 64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -o bin/blackhole-server-riscv64 cmd/main.go

echo "📦 传输文件至 SpacemiT K1 开发板 ($K1_HOST)..."
ssh root@$K1_HOST "mkdir -p /opt/blackhole /var/nas_share"
scp bin/blackhole-server-riscv64 root@$K1_HOST:/opt/blackhole/blackhole-server
scp blackhole.service root@$K1_HOST:/etc/systemd/system/blackhole.service

echo "⚙️ 在 K1 上配置并启动 Systemd 服务..."
ssh root@$K1_HOST "chmod +x /opt/blackhole/blackhole-server && systemctl daemon-reload && systemctl enable --now blackhole"

echo "✅ 部署完成！查看状态..."
ssh root@$K1_HOST "systemctl status blackhole --no-pager"
