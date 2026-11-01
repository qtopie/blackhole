# Module Spec: NFSv4+ 协议服务器实现 (NFSv4 Protocol Support)

## 1. Overview
在 `internal/nfs/` 下提供轻量级自包含的 NFSv4.0 协议服务端，处理 TCP 端口 2049 上的 ONC-RPC/XDR 消息与 COMPOUND 过程。该模块不依赖任何外部 portmapper、rpcbind 或外部二进制服务，可直接与 Linux 原生内核 NFSv4 客户端（`mount -t nfs -o vers=4.0 ...`）完成会话建立、目录树检索、属性读取与文件数据 I/O。

## 2. Interface / API Contract

### 2.1 网络通信契约
- **监听协议与端口**：TCP `:2049`（由 `cfg.NFSPort` 控制，环境变量 `BLACKHOLE_NFS_PORT`）。
- **帧协议**：RFC 5531 Section 11 Record Marking（4 字节大端分片头）。
- **RPC 协议头**：RFC 5531 ONC RPC v2，`Program=100003`, `Version=4`, `Proc=0 (NULL)` 或 `Proc=1 (COMPOUND)`。
- **安全认证**：`AUTH_NONE` (0) 与 `AUTH_SYS` (1)。

### 2.2 核心操作响应契约
- **COMPOUND 错误短路**：当复合操作数组中某操作失败时，停止执行后续操作，并以该错误码作为整个 COMPOUND 回包的状态码。
- **状态码定义（RFC 7530 nfsstat4）**：
  - `NFS4_OK = 0`
  - `NFS4ERR_PERM = 1`
  - `NFS4ERR_NOENT = 2`
  - `NFS4ERR_IO = 5`
  - `NFS4ERR_ACCESS = 13`
  - `NFS4ERR_INVAL = 22`
  - `NFS4ERR_STALE = 70`
  - `NFS4ERR_BADHANDLE = 10001`
  - `NFS4ERR_NOTSUPP = 10004`

## 3. Acceptance Criteria (BDD)

### Feature: NFSv4 核心挂载与文件服务

#### Scenario 1: [SPEC-NFS-001] RPC NULL 连通性探测
- **Given** NFSv4 服务已启动在 `:2049`
- **When** 客户端发送 RPC Program=100003, Version=4, Proc=0 的 NULL 请求
- **Then** 服务端应在 100ms 内返回 `MSG_ACCEPTED` 且状态为 `SUCCESS` 的空响应
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_RPC_Null`

#### Scenario 2: [SPEC-NFS-002] 客户端租约初始化与防重放 (SETCLIENTID)
- **Given** 客户端发起包含 `OP_SETCLIENTID` 的 COMPOUND 请求
- **When** 携带客户端标识字符串与 8 字节随机 Verifier
- **Then** 服务端成功记录客户端记录，返回 `NFS4_OK` 及服务端分配的 64-bit ClientID 与 SetClientIDConfirm Verifier
- **And** 后续 `OP_SETCLIENTID_CONFIRM` 调用验证成功
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_SetClientID_And_Confirm`

#### Scenario 3: [SPEC-NFS-003] 根文件句柄与文件系统属性获取 (PUTROOTFH + GETATTR)
- **Given** 客户端已完成握手，发起复合操作 `[OP_PUTROOTFH, OP_GETFH, OP_GETATTR]`
- **When** 请求包含 `FATTR4_SUPPORTED_ATTRS`, `FATTR4_FSID`, `FATTR4_TYPE`, `FATTR4_LEASE_TIME`
- **Then** 服务端返回当前根句柄为有效句柄
- **And** 属性位图正确反序列化，返回 `FATTR4_TYPE=NF4DIR`, `FATTR4_LEASE_TIME=60s`
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_PutRootFH_GetAttr`

#### Scenario 4: [SPEC-NFS-004] 目录枚举与文件名解析 (LOOKUP + READDIR)
- **Given** 共享根目录下存在测试文件 `hello.txt` 与子目录 `public`
- **When** 客户端发送 `[OP_PUTROOTFH, OP_READDIR]`
- **Then** 服务端流式返回目录条目数组，包含 `hello.txt` 和 `public` 及其对应的文件句柄与类型
- **When** 客户端发送 `[OP_PUTROOTFH, OP_LOOKUP("public"), OP_GETFH]`
- **Then** CFH 成功指向 `public` 目录句柄且状态码为 `NFS4_OK`
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Lookup_And_ReadDir`

#### Scenario 5: [SPEC-NFS-005] 文件只读与写入 I/O (OPEN + READ + WRITE + CLOSE)
- **Given** 客户端对 `/hello.txt` 发起 `[OP_PUTFH, OP_OPEN]`
- **When** 发送 `OP_READ` 请求 offset=0, count=1024
- **Then** 返回文件内容字节流及 `eof` 标记
- **When** 发送 `OP_WRITE` 写入新数据
- **Then** 服务端完成磁盘同步写入，返回已写入字节数并更新文件修改时间
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_File_Read_Write`

#### Scenario 6: [SPEC-NFS-006] 客户端 IP 白名单访问控制 (IP Whitelist Access Control)
- **Given** 服务端配置环境变量 `BLACKHOLE_NFS_ALLOWED_IPS="192.168.50.240,127.0.0.1"`
- **When** 未在白名单内的客户端 IP（如 `192.168.1.99`）尝试建立 TCP 连接并发送 RPC 请求
- **Then** 服务端立即拒绝并断开连接
- **When** 处于白名单内的客户端 IP（如 `192.168.50.240` 或 `127.0.0.1`）连接
- **Then** 服务端接受连接并正常处理 NFSv4 操作
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_IP_Whitelist`

#### Scenario 7: [SPEC-NFS-007] 目录与软链接创建 (OP_CREATE + OP_READLINK)
- **Given** 客户端在根目录准备新建目录 `new_dir` 或软链接 `sym_link` 指向 `hello.txt`
- **When** 发送 `[OP_PUTROOTFH, OP_CREATE(type=NF4DIR, name='new_dir'), OP_GETFH]`
- **Then** 服务端成功创建新目录并更新 CFH 为新目录句柄，返回 `NFS4_OK`
- **When** 发送 `[OP_PUTROOTFH, OP_CREATE(type=NF4LNK, name='sym_link', link='hello.txt')]`
- **And** 后续发送 `[OP_PUTROOTFH, OP_LOOKUP('sym_link'), OP_READLINK]`
- **Then** 服务端成功创建符号链接并返回其指向的字符串 `'hello.txt'`
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Create_And_Readlink`

#### Scenario 8: [SPEC-NFS-008] 文件与目录重命名与跨目录移动 (OP_RENAME)
- **Given** 共享目录下存在文件 `new_dir/subfile.txt`
- **When** 发送 `[OP_PUTROOTFH, OP_LOOKUP('new_dir'), OP_SAVEFH, OP_PUTROOTFH, OP_RENAME(old='new_dir/subfile.txt', new='moved_subfile.txt')]`
- **Then** 服务端成功将目标文件重命名/移动至根目录，返回 `NFS4_OK` 及变更验证
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Rename`

#### Scenario 9: [SPEC-NFS-009] 显式数据持久化刷盘确认 (OP_COMMIT)
- **Given** 客户端已打开某文件句柄
- **When** 发送 `[OP_PUTFH, OP_COMMIT(offset=0, count=4096)]`
- **Then** 服务端完成磁盘同步并返回 `NFS4_OK` 及 8 字节 Write Verifier
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Commit`

#### Scenario 10: [SPEC-NFS-010] 硬链接创建与文件引用关联 (OP_LINK)
- **Given** 根目录下存在已有文件 `hello.txt`
- **When** 发送 `[OP_PUTROOTFH, OP_LOOKUP('hello.txt'), OP_SAVEFH, OP_PUTROOTFH, OP_LINK(newname='hello_hardlink.txt')]`
- **Then** 服务端成功创建同 inode 的硬链接文件，返回 `NFS4_OK`
- **And** 两者查询 `FATTR4_FILEID` 完全一致
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Hardlink`

#### Scenario 11: [SPEC-NFS-011] 文件锁授予与冲突拒绝 (OP_LOCK + OP_LOCKT)
- **Given** 客户端对 `/hello.txt` 申请排他写锁 (WRITE_LT) 范围 `0..1024`
- **When** 发送 `[OP_PUTFH, OP_LOCK(type=WRITE_LT, offset=0, len=1024, owner=ClientA)]`
- **Then** 服务端授予锁，返回 `NFS4_OK` 及分配的 `lock_stateid`
- **When** 另一个持有者 ClientB 发送 `[OP_PUTFH, OP_LOCKT(type=WRITE_LT, offset=512, len=512, owner=ClientB)]` 探测该区间
- **Then** 服务端探测发现区间重叠冲突，返回 `NFS4ERR_DENIED` 并携带冲突锁持有者信息
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Lock_Conflict`

#### Scenario 12: [SPEC-NFS-012] 文件解锁与锁持有者注销 (OP_LOCKU + OP_RELEASE_LOCKOWNER)
- **Given** ClientA 已持有 `/hello.txt` 的字节区间锁
- **When** 发送 `[OP_PUTFH, OP_LOCKU(offset=0, len=1024, lock_stateid=...)]`
- **Then** 服务端解除锁占用，返回 `NFS4_OK` 并更新 stateid
- **When** 后续发送 `OP_RELEASE_LOCKOWNER(owner=ClientA)`
- **Then** 服务端清理其所有残留锁状态，返回 `NFS4_OK`
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_Lock_Unlock_Release`

#### Scenario 13: [SPEC-NFS-013] NFSv4.1/4.2 会话握手与槽位序列管理 (EXCHANGE_ID + CREATE_SESSION + SEQUENCE)
- **Given** 客户端以 `minorversion=2` (NFSv4.2) 发起会话初始化
- **When** 发送 `OP_EXCHANGE_ID`
- **Then** 服务端响应 `NFS4_OK` 并分配 64-bit clientid 及会话能力掩码
- **When** 后续发送 `OP_CREATE_SESSION` 与 `OP_SEQUENCE`
- **Then** 服务端成功建立 16 字节 `sessionid`，并在每次 COMPOUND 前置处理 Sequence 槽位与 Exactly-Once 幂等标记
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_V42_Session_Handshake`

#### Scenario 14: [SPEC-NFS-014] NFSv4.2 服务端直拷零网络流量传输 (OP_COPY)
- **Given** 服务端存在大文件 `/huge_src.dat`
- **When** 客户端发送 `[OP_PUTFH(dst), OP_SAVEFH(src), OP_COPY(src_offset=0, dst_offset=0, count=10485760)]`
- **Then** 服务端直接在本地利用内核 `copy_file_range` 完成数据复制，网络层零负载，返回 `NFS4_OK` 及写入确认
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_V42_ServerSideCopy`

#### Scenario 15: [SPEC-NFS-015] NFSv4.2 空间快速预分配与稀疏文件打洞 (OP_ALLOCATE + OP_DEALLOCATE)
- **Given** 客户端已打开某文件
- **When** 发送 `[OP_PUTFH, OP_ALLOCATE(offset=0, length=10485760)]`
- **Then** 服务端调用 `fallocate` 瞬间预分配空间且不写入脏数据，文件大小更新为 10MB
- **When** 发送 `[OP_PUTFH, OP_DEALLOCATE(offset=0, length=5242880)]`
- **Then** 服务端完成打洞（Punch Hole），释放底层物理块并保留文件大小不变
- **Mapped Test:** `internal/nfs/nfs_test.go:TestNFS_V42_Allocate_Deallocate`
