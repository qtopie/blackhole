# Module Spec: Cosmos-Star Blackhole 插件与自动挂载联动规范 (Auto-Mount Integration)

## 1. Overview
本规范定义了 Domour Drive (`blackhole`) 与集群协同系统 `cosmos-star` 的 `ext.nas.blackhole` 插件之间的联动及全自动挂载机制。
通过本规范，Blackhole 对外暴露标准 NAS 端点探活接口；`cosmos-star` 插件在生命周期启动 (`Start`) 或调度指令 (`Dispatch`) 时，能自动发现服务端协议、配置挂载点、执行原生 Linux NFSv4.2 极速挂载（失败自动回退至 NFSv4.0），并在插件停止时优雅卸载，使集群业务能够以原生 POSIX 文件系统透明访问 Blackhole 高性能存储。

## 2. Interface / API Contract

### 2.1 Blackhole 服务端端点发现 API
- **Endpoint:** `GET /api/nas/endpoints`
- **Auth:** Basic Auth 或 Public (与 `/api/sysinfo` 保持一致鉴权策略)
- **Response HTTP 200 JSON:**
  ```json
  {
    "server_version": "1.0.0",
    "share_dir": "/var/nas_share",
    "protocols": {
      "nfs": {
        "enabled": true,
        "port": 2049,
        "export_path": "/",
        "default_version": "4.2",
        "supported_versions": ["4.0", "4.1", "4.2"]
      },
      "smb": {
        "enabled": true,
        "port": 445,
        "share_name": "share"
      },
      "webdav": {
        "enabled": true,
        "port": 50056,
        "path": "/shared/"
      }
    }
  }
  ```

### 2.2 Cosmos-Star 插件配置契约 (`BlackholeClientConfig`)
- `server_url`: string (必填，例如 `http://192.168.50.189:50056`)
- `username`: string (可选，Basic Auth)
- `password`: string (可选，Basic Auth)
- `auto_mount`: bool (可选，默认 `true`)
- `mount_point`: string (可选，默认 `/mnt/blackhole_nfs`)
- `mount_proto`: string (可选，默认 `"nfs"`, 可选 `"smb"`, `"webdav"`)
- `nfs_version`: string (可选，默认 `"4.2"`)
- `sudo_password`: string (可选，用于无 root 权限时执行 mount/umount)
- `sudo_pass_file`: string (可选，例如 `~/.pass` 或绝对路径)

### 2.3 插件 Dispatch 拓展指令
- `get_nas_endpoints`: 无需入参，转发调用 Blackhole `/api/nas/endpoints`
- `get_mount_status`: 入参 `{"mount_point": "..."}`（可选），返回挂载状态、协议版本、容量信息
- `mount`: 入参 `{"mount_point": "...", "proto": "...", "version": "..."}`，动态触发挂载
- `unmount`: 入参 `{"mount_point": "...", "lazy": false}`，动态触发卸载

---

## 3. Acceptance Criteria (BDD)

### Feature 1: Blackhole 服务端 NAS 协议端点发现

#### Scenario 1: [SPEC-AUTO-001] 获取存储协议端点信息
- **Given** Blackhole 服务端已启动且 NFS、SMB、WebDAV 正常运行
- **When** 客户端发起 `GET /api/nas/endpoints` 请求
- **Then** 返回 HTTP 200 及包含 `protocols.nfs`、`protocols.smb`、`protocols.webdav` 的完整 JSON 结构，其中 NFS 默认版本为 `"4.2"`，端口为 `2049`
- **Mapped Test:** `internal/handler/handler_test.go:TestGetNASEndpoints`

---

### Feature 2: 插件启动生命周期的自动挂载 (Auto-Mount)

#### Scenario 2: [SPEC-AUTO-002] 启动插件时自动探测并挂载 NFSv4.2
- **Given** 插件通过 `Provision` 配置了有效的 `server_url`，且 `auto_mount = true`
- **When** 宿主引擎调用插件 `Start()`
- **Then** 插件解析 `server_url` 获取服务器主机 IP，请求 `/api/nas/endpoints` 确认 NFSv4.2 开启；
- **And** 检查当前系统 `/proc/mounts`，若未挂载则执行 Linux 原生挂载命令 `mount -t nfs -o vers=4.2,port=2049 <ip>:/ <mount_point>`；
- **And** 挂载成功后，在本地文件系统产生可写目录，返回启动成功
- **Mapped Test:** `../cosmos-star/pkg/ext/plugin/nas_plugin_test.go:TestBlackholeAutoMount_StartLifecycle`

#### Scenario 3: [SPEC-AUTO-003] NFSv4.2 不支持时自动优雅降级至 NFSv4.0
- **Given** 客户端宿主机内核不支持 NFSv4.2 挂载（或 mount -o vers=4.2 失败）
- **When** 执行 NFS 自动挂载
- **Then** 捕获挂载错误并自动重试 `mount -t nfs -o vers=4.0,port=2049 <ip>:/ <mount_point>`，挂载成功且记录日志
- **Mapped Test:** `../cosmos-star/pkg/ext/plugin/nas_plugin_test.go:TestBlackholeAutoMount_FallbackV40`

---

### Feature 3: 插件停止生命周期的优雅卸载 (Graceful Unmount)

#### Scenario 4: [SPEC-AUTO-004] 停止插件时自动卸载清理
- **Given** 插件此前由 `Start()` 成功完成了自动挂载
- **When** 宿主引擎调用插件 `Stop()`
- **Then** 插件检查 `/proc/mounts` 确认存在挂载点，安全执行 `umount <mount_point>`（若繁忙支持 `umount -l` 回退），释放资源并返回清理成功
- **Mapped Test:** `../cosmos-star/pkg/ext/plugin/nas_plugin_test.go:TestBlackholeAutoMount_StopLifecycle`

---

### Feature 4: Dispatch 动态挂载控制与状态查询

#### Scenario 5: [SPEC-AUTO-005] 通过 Dispatch 查询挂载状态
- **Given** 插件已成功挂载 NAS 目录
- **When** 调用 `Dispatch(ctx, "", "get_mount_status", payload)`
- **Then** 返回 JSON: `{"mounted": true, "mount_point": "/mnt/blackhole_nfs", "proto": "nfs", "version": "4.2", "server": "192.168.50.189"}`
- **Mapped Test:** `../cosmos-star/pkg/ext/plugin/nas_plugin_test.go:TestBlackholeDispatch_MountStatus`

#### Scenario 6: [SPEC-AUTO-006] 通过 Dispatch 动态触发 mount 与 unmount
- **Given** 插件运行中且目标挂载点未挂载
- **When** 调用 `Dispatch(ctx, "", "mount", {"mount_point": "/tmp/custom_mnt", "proto": "nfs"})`
- **Then** 成功执行挂载并返回成功状态；
- **When** 紧接着调用 `Dispatch(ctx, "", "unmount", {"mount_point": "/tmp/custom_mnt"})`
- **Then** 成功执行卸载
- **Mapped Test:** `../cosmos-star/pkg/ext/plugin/nas_plugin_test.go:TestBlackholeDispatch_ManualMountUnmount`
