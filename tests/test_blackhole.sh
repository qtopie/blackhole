#!/bin/bash
set -e

TARGET_IP="192.168.50.189"
HTTP_PORT="50056"
USER="admin"
PASS="blackhole"

echo "=========================================="
echo "🧪 Blackhole NAS (SpacemiT K1) 多协议功能测试"
echo "=========================================="
echo "目标 IP: $TARGET_IP"
echo ""

# 1. 测试端口连通性
echo "1️⃣ 检查网络端口 (HTTP:50056, SMB:445, NFS:2049)..."
nc -zv -w 3 $TARGET_IP $HTTP_PORT 445 2049 || true
echo ""

# 2. 测试 HTTP REST API - Sysinfo
echo "2️⃣ 测试 REST API (/api/sysinfo)..."
SYSINFO=$(curl -s -u $USER:$PASS http://$TARGET_IP:$HTTP_PORT/api/sysinfo)
echo "响应结果: $SYSINFO"
echo ""

# 3. 测试 普通文件上传 (/api/upload)
echo "3️⃣ 测试文件上传 (/api/upload)..."
TEST_FILE="/tmp/blackhole_test_file.txt"
echo "Hello Blackhole NAS from SpacemiT K1! $(date)" > $TEST_FILE

UPLOAD_RESP=$(curl -s -u $USER:$PASS -F "file=@$TEST_FILE" "http://$TARGET_IP:$HTTP_PORT/api/upload?path=test_upload.txt")
echo "上传结果: $UPLOAD_RESP"
echo ""

# 4. 测试 安全文件夹加密上传 (/api/vault/upload)
echo "4️⃣ 测试安全文件夹加密上传 (/api/vault/upload)..."
VAULT_PASS="SecretKey123"
VAULT_RESP=$(curl -s -u $USER:$PASS -H "X-Vault-Password: $VAULT_PASS" -F "file=@$TEST_FILE" "http://$TARGET_IP:$HTTP_PORT/api/vault/upload?path=secret_doc.txt")
echo "加密上传结果: $VAULT_RESP"
echo ""

# 5. 测试 安全文件夹解密下载 (/api/vault/download)
echo "5️⃣ 测试安全文件夹解密下载 (/api/vault/download)..."
DOWNLOADED_VAULT="/tmp/blackhole_vault_decrypted.txt"
curl -s -u $USER:$PASS -H "X-Vault-Password: $VAULT_PASS" "http://$TARGET_IP:$HTTP_PORT/api/vault/download?path=secret_doc.txt" -o $DOWNLOADED_VAULT
echo "解密文件内容: $(cat $DOWNLOADED_VAULT)"
echo ""

# 6. 测试 WebDAV 目录获取 (/shared/)
echo "6️⃣ 测试 WebDAV Endpoint (/shared/)..."
WEBDAV_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -u $USER:$PASS http://$TARGET_IP:$HTTP_PORT/shared/)
echo "WebDAV 返回状态码: $WEBDAV_STATUS"
echo ""

# 7. 测试 SMB3 客户端挂载/通信 (smbclient)
echo "7️⃣ 测试 SMB3 协议通信 (smbclient)..."
if command -v smbclient &> /dev/null; then
    smbclient -L //$TARGET_IP -N -p 445 || echo "smbclient 已测试完成"
else
    echo "提示: 本地未安装 smbclient 工具，可通过 python3 / nc 验证"
fi
echo ""

# 清理临时测试文件
rm -f $TEST_FILE $DOWNLOADED_VAULT

echo "=========================================="
echo "✅ 所有基础 API 与多协议测试脚本执行结束！"
echo "=========================================="
