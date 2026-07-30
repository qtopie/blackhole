#!/usr/bin/env python3
"""
Blackhole NAS (Spacemit K1) 多协议自动化测试脚本 (Python 版)
包含: HTTP REST API、Vault AES-256 加解密、WebDAV 与 SMB 端口 socket 连通性测试
"""

import urllib.request
import urllib.parse
import json
import socket
import base64
import time
import sys

import os

TARGET_IP = os.getenv("TARGET_IP", "192.168.50.189")
PORT_HTTP = 50056
PORT_SMB = 445
PORT_NFS = 2049
USERNAME = "admin"
PASSWORD = "blackhole"
VAULT_PASS = "MySecretVault123"

def get_auth_header(user, password):
    auth_str = f"{user}:{password}"
    b64_auth = base64.b64encode(auth_str.encode('utf-8')).decode('utf-8')
    return f"Basic {b64_auth}"

def test_port(ip, port, name):
    print(f"👉 [TCP Port Check] 正在测试 {name} 端口 ({ip}:{port})...", end=" ")
    try:
        s = socket.create_connection((ip, port), timeout=3)
        s.close()
        print("✅ 成功连通!")
        return True
    except Exception as e:
        print(f"❌ 失败 ({e})")
        return False

def test_sysinfo():
    print(f"\n👉 [API /api/sysinfo] 获取 NAS 系统与存储容量指标...")
    url = f"http://{TARGET_IP}:{PORT_HTTP}/api/sysinfo"
    req = urllib.request.Request(url)
    req.add_header("Authorization", get_auth_header(USERNAME, PASSWORD))
    
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            print("✅ 响应成功:")
            print(f"   - 共享存储目录: {data.get('share_dir')}")
            print(f"   - 总容量: {data.get('total_gb')} GB")
            print(f"   - 剩余空间: {data.get('free_gb')} GB")
            print(f"   - 使用比例: {data.get('used_percent')}")
            return True
    except Exception as e:
        print(f"❌ 请求失败: {e}")
        return False

def test_upload():
    print(f"\n👉 [API /api/upload] 测试普通文件上传...")
    url = f"http://{TARGET_IP}:{PORT_HTTP}/api/upload?path=python_test.txt"
    content = f"Hello from Python Test Script on {time.ctime()}".encode('utf-8')
    
    req = urllib.request.Request(url, data=content, method='POST')
    req.add_header("Authorization", get_auth_header(USERNAME, PASSWORD))
    req.add_header("Content-Type", "application/octet-stream")
    
    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            print(f"✅ 上传成功: {data}")
            return True
    except Exception as e:
        print(f"❌ 上传失败: {e}")
        return False

def test_vault_upload_and_download():
    print(f"\n👉 [API /api/vault/*] 测试安全文件夹 AES-256 加密上传与解密读取...")
    path = "python_secret.txt"
    raw_text = f"Top Secret Data created at {time.ctime()}"
    
    # 1. 上传加密
    upload_url = f"http://{TARGET_IP}:{PORT_HTTP}/api/vault/upload?path={path}"
    req_up = urllib.request.Request(upload_url, data=raw_text.encode('utf-8'), method='POST')
    req_up.add_header("Authorization", get_auth_header(USERNAME, PASSWORD))
    req_up.add_header("X-Vault-Password", VAULT_PASS)
    req_up.add_header("Content-Type", "application/octet-stream")
    
    try:
        with urllib.request.urlopen(req_up) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            print(f"   [1/2] 🔒 加密上传成功: {data}")
    except Exception as e:
        print(f"   ❌ 加密上传失败: {e}")
        return False

    # 2. 解密下载
    download_url = f"http://{TARGET_IP}:{PORT_HTTP}/api/vault/download?path={path}"
    req_dl = urllib.request.Request(download_url, method='GET')
    req_dl.add_header("Authorization", get_auth_header(USERNAME, PASSWORD))
    req_dl.add_header("X-Vault-Password", VAULT_PASS)
    
    try:
        with urllib.request.urlopen(req_dl) as resp:
            decrypted_text = resp.read().decode('utf-8')
            print(f"   [2/2] 🔓 解密读取结果: \"{decrypted_text}\"")
            if decrypted_text == raw_text:
                print("✅ 验证一致: 加解密全流程成功!")
                return True
            else:
                print("❌ 错误: 解密后的内容与原内容不匹配")
                return False
    except Exception as e:
        print(f"   ❌ 解密读取失败: {e}")
        return False

def test_webdav_endpoint():
    print(f"\n👉 [WebDAV Endpoint /shared/] 测试挂载点...")
    url = f"http://{TARGET_IP}:{PORT_HTTP}/shared/"
    req = urllib.request.Request(url, method='PROPFIND')
    req.add_header("Authorization", get_auth_header(USERNAME, PASSWORD))
    
    try:
        with urllib.request.urlopen(req) as resp:
            print(f"✅ WebDAV Endpoint 状态码: {resp.status} (成功响应 PROPFIND)")
            return True
    except Exception as e:
        print(f"❌ WebDAV 测试失败: {e}")
        return False

def main():
    print("=" * 60)
    print("🚀 Blackhole NAS (SpacemiT K1) 自动功能测试 (Python 版)")
    print("=" * 60)
    
    # 1. 端口检查
    p1 = test_port(TARGET_IP, PORT_HTTP, "HTTP (WebDAV/API)")
    p2 = test_port(TARGET_IP, PORT_SMB, "SMB3")
    p3 = test_port(TARGET_IP, PORT_NFS, "NFSv4")
    
    if not (p1 and p2 and p3):
        print("\n⚠️ 警示: 部分服务端口未通，请检查 K1 开发板服务状态。")

    # 2. 功能测试
    test_sysinfo()
    test_upload()
    test_vault_upload_and_download()
    test_webdav_endpoint()

    print("\n" + "=" * 60)
    print("✨ 测试脚本执行完成!")
    print("=" * 60)

if __name__ == "__main__":
    main()
