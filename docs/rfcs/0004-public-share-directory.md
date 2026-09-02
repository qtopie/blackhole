# RFC-0004: 简洁路由体系（/ 为主 UI，/public/* 为公开免认证目录）

- **Status:** Under Review
- **Author:** Antigravity Agent
- **Created Date:** 2026-08-15

## 1. Summary

重构 Blackhole NAS 路由结构，移除冗余的 `/ui` 前缀，确立简洁清晰的路由语义：
1. **`/` 及任意子路径 `/*path`**：直接承载 NAS 原有的全量 Web UI（文件管理、照片相册、书籍、保险箱等），受认证保护。
2. **`/public` 及 `/public/*path`**：作为公开免认证只读目录：
   - 外部未认证用户可免登录浏览 `/public` 下的文件列表并直接下载文件。
   - 支持通过符号链接（`ln -s`）将任意外部目录或文件挂载到 `public` 目录下对外免密分享。
   - 未认证用户在此目录下严格只读，禁止任何写操作（上传、删除、重命名、建目录）。
3. **彻底废除旧的 `/ui` 前缀**，无需保留兼容逻辑。

## 2. Detailed Design

### 2.1 路由架构

```
GET  /                      -> 全量 Web UI（受认证保护，认证后可访问私有文件/相册/书籍）
GET  /photos, /books, ...   -> 私有目录/文件访问（受认证保护）
GET  /public                -> 公开目录 Web UI（免认证只读）
GET  /public/*filepath      -> 公开文件下载 / 公开子目录浏览（免认证只读，支持符号链接）
POST /api/upload            -> 上传接口（强制要求认证，401 Unauthorized）
POST /api/mkdir             -> 新建目录（强制要求认证，401 Unauthorized）
POST /api/delete            -> 删除接口（强制要求认证，401 Unauthorized）
POST /api/rename            -> 重命名接口（强制要求认证，401 Unauthorized）
```

### 2.2 鉴权机制 (`flexibleAuth`)

- 若用户已通过认证（Basic Auth / Auth Cookie / Token Query）：标记为已认证，允许访问任何私有路径及 API 写操作。
- 若用户未认证：
  - 仅允许以 `GET` / `HEAD` 方法访问 `/public` 及 `/public/*` 路径。
  - 访问 `/` 根目录、私有目录或执行任何写操作（`POST`/`PUT`/`DELETE`）时，立即返回 `401 Unauthorized` 认证挑战。
- 访问 `/public` 时绝不向客户端下发管理员认证 Cookie。

### 2.3 UI 链接与视图

- `RenderWebUI` 生成的链接从旧的 `/ui/<path>` 全面更新为直接根相对路径 `/<path>`。
- 访客在 `/public` 下浏览时：
  - 面包屑根节点为 `/public/`。
  - 隐藏上传、新建目录、相册、书籍等私有入口与文件删除/重命名操作，仅提供直接下载。
- 管理员登录后访问 `/` 或 `/public`：
  - 拥有完整的读写操作栏与所有 Tab。
