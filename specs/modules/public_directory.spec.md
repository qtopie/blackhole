# Module Spec: 简洁路由与公开只读目录 (/ 与 /public/*)

## 1. Overview

为 Blackhole NAS 建立简洁路由体系：直接以根路径 `/` 承载原有 Web UI，以 `/public/*` 承载公开免认证只读目录。未认证访客访问 `/public/*` 可免密浏览并直接下载（含符号链接目标）；访问根路径 `/` 及私有内容需认证；未认证写请求一律 401 拦截。废弃旧 `/ui` 路径。

## 2. Interface / API Contract

### 2.1 路由访问与鉴权契约

| 端点 | 方法 | 认证状态 | 预期行为 / 返回状态码 |
| :--- | :--- | :--- | :--- |
| `/` 或 `/<私有目录>` | GET | 已认证 (Admin) | `200 OK`，呈现全量管理 Web UI |
| `/` 或 `/<私有目录>` | GET | 未认证 (Anonymous) | `401 Unauthorized`，触发 Basic Auth 挑战 |
| `/public` 或 `/public/` | GET | 未认证 (Anonymous) | `200 OK`，呈现 Public 目录只读 Web UI |
| `/public/*path` | GET | 未认证 (Anonymous) | `200 OK`，下载对应公开文件（含符号链接）或浏览子目录 |
| `/public/*path?download=1`| GET | 未认证 (Anonymous) | `200 OK`，触发附件下载（含 Content-Disposition 头） |
| `/api/upload` | POST | 未认证 (Anonymous) | `401 Unauthorized`，拒绝匿名上传 |
| `/api/mkdir` | POST | 未认证 (Anonymous) | `401 Unauthorized`，拒绝匿名创建目录 |
| `/api/delete` | POST/DELETE | 未认证 (Anonymous) | `401 Unauthorized`，拒绝匿名删除 |
| `/api/rename` | POST | 未认证 (Anonymous) | `401 Unauthorized`，拒绝匿名重命名 |
| 所有 API / UI 路由 | ANY | 已认证 (Admin) | `200 OK` / 正常处理（具备完整读写权限） |

### 2.2 符号链接（Symlink）解析契约

1. 位于 `public/` 目录下的普通文件符号链接（如 `public/link.txt -> /var/data/real.txt`）：
   - `GET /public/link.txt` MUST 返回 `real.txt` 的内容，状态码 `200 OK`。
2. 位于 `public/` 目录下的目录符号链接（如 `public/ext_dir -> /var/data/external_folder`）：
   - `GET /public/ext_dir/` MUST 列出外部目录中的文件列表，状态码 `200 OK`。
   - `GET /public/ext_dir/file.bin` MUST 支持下载外部子目录下的文件，状态码 `200 OK`。

## 3. Acceptance Criteria (BDD)

### Feature: / Main UI & /public Anonymous Read-Only Access

#### Scenario 1: [SPEC-ROUTE-001] 匿名用户访问 /public 目录与下载普通文件
- **Given** 未认证客户端
- **When** 请求 `GET /public`、`GET /public/` 或 `GET /public/sample.txt`
- **Then** 返回 `200 OK`，成功呈现 public 列表或下载文件内容，且响应头不包含管理员认证 Cookie。
- **Mapped Test:** `internal/server/server_test.go:TestPublicRoute_AnonymousAccess`

#### Scenario 2: [SPEC-ROUTE-002] 匿名用户下载 /public 跨目录符号链接文件及子目录
- **Given** 管理员在 `public/` 下创建指向外部文件的符号链接 `public/sym_doc.pdf` 及符号链接目录 `public/sym_dir`
- **When** 匿名客户端发起 `GET /public/sym_doc.pdf` 及 `GET /public/sym_dir/`
- **Then** 均返回 `200 OK`，内容与外部目标完全一致。
- **Mapped Test:** `internal/server/server_test.go:TestPublicRoute_SymlinkAccess`

#### Scenario 3: [SPEC-ROUTE-003] 匿名用户在 /public 下的写操作被 401 拦截
- **Given** 未认证客户端
- **When** 发起 `POST /api/upload?path=public/hack.txt` 或 `POST /api/mkdir`
- **Then** 服务端必须拦截并返回 `401 Unauthorized`。
- **Mapped Test:** `internal/server/server_test.go:TestPublicRoute_AnonymousWriteForbidden`

#### Scenario 4: [SPEC-ROUTE-004] 匿名用户访问根路径 / 及私有目录被 401 拦截
- **Given** 未认证客户端
- **When** 请求 `GET /` 或 `GET /private_folder/`
- **Then** 服务端必须返回 `401 Unauthorized`。
- **Mapped Test:** `internal/server/server_test.go:TestPublicRoute_PrivateAccessBlocked`

#### Scenario 5: [SPEC-ROUTE-005] 认证用户直接访问根路径 / 具备全量管理功能
- **Given** 已认证管理员客户端
- **When** 请求 `GET /` 及发起 `POST /api/upload?path=root_file.txt`
- **Then** `GET /` 返回全量 Web UI 根目录，写操作返回 `200 OK`。
- **Mapped Test:** `internal/server/server_test.go:TestPublicRoute_AuthenticatedAdminAccess`
