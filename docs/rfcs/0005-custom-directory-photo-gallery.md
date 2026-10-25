# RFC-0005: 任意指定目录查看图片合集与隐私安全控制 (Custom Directory Photo Gallery)

- **Status:** Under Review (Updating for Open Mode & Hidden Mode Controls)
- **Author:** Antigravity Agent
- **Created Date:** 2026-10-25

## 1. Summary

在 Blackhole NAS 中新增「任意指定目录查看图片合集」功能，并建立完备的**开放模式（Open/Public Mode）**与**隐藏模式（Hidden/Privacy Mode）**访问隔离控制：
1. **开放模式（Open Mode / `/public` 免密访问）**：
   - 未认证访客或只读浏览状态下，仅允许查看位于 `/public/*` 目录下的图片合集。
   - 严禁匿名访问私有目录（如 `/photos`, `/.relex`, `/个人文件`, `/vault` 等）的相册，接口一律返回 `401 Unauthorized`。
2. **隐藏模式控制（Hidden Mode / `show_hidden`）**：
   - 默认状态下「隐藏模式关闭」(`show_hidden=0`)：
     - 默认相册仅展示 `albumDir`（如 `/var/nas_share/photos`）内的照片，严禁混入任何路径包含 `/.` 的私密隐藏文件；
     - 禁止查看任何私密/隐藏目录（路径中包含以 `.` 开头的段，如 `.relex`、`.cache` 等）的相册与扫描操作，请求直接返回 `403 Forbidden`；
   - 仅当用户在文件管理中主动点击「👁️ 显示隐藏项目」（隐藏模式开启，`show_hidden=1`）时，相册才允许浏览、索引并展示私密隐藏目录中的照片合集。
3. **UI 双向直达与无缝切换**：
   - 在「📁 文件管理」与「🖼️ 照片相册」中无缝联动，保持当前所选路径、隐藏模式状态与灯箱大图体验。

## 2. Motivation

NAS 用户的数据既包含公开分享内容（`/public`）、日常相册（`/photos`），也包含高度私密的隐匿内容（如以 `.` 开头的隐藏文件夹，例如 `.relex`）。
若缺乏清晰的模式区分：
1. 匿名访客可能越权探测或窥视私有目录的相册；
2. 当管理员在普通视图（隐藏模式关闭）下浏览相册时，私密隐藏文件夹中的敏感照片可能误被展示；
3. 指定目录功能若未经隐私校验，可能直接绕过文件管理器的「隐藏私密项目」控制。

因此，必须在 API 与 UI 层将开放模式（公开 vs 私有）与隐藏模式（可见 vs 隐匿）彻底打通与闭环。

## 3. Detailed Design

### 3.1 路径解析与模式判定规范

定义路径属性判定函数：
- **`isPublic(path)`**: 路径位于 `/var/nas_share/public`（或相对路径以 `public` 开头）。
- **`isHiddenPath(path)`**: 路径中任意一段以 `.` 开头（例如 `.relex`, `photos/.secret/pic.jpg`）。

### 3.2 开放模式（Open Mode）访问控制

- 客户端请求如果未携带认证凭证（即 `authenticated == false`）：
  - 属于开放模式。
  - `GET /api/album/photos`：
    - `dir` 必须显式指定且必须位于 `public` 目录下（`isPublic(dir) == true`）。
    - 若 `dir` 未指定（企图访问默认私有相册）或 `dir` 指向私有目录（如 `photos`, `.relex`），立即返回 `401 Unauthorized` 并触发登录挑战。
  - `GET /api/album/photos/:id/file` 及 `thumbnail`：
    - 校验对应媒体文件的物理路径，若不在 `public` 目录下，立即返回 `401 Unauthorized`。

### 3.3 隐藏模式（Hidden Mode）访问控制

- 认证管理员访问时，受 `show_hidden` 状态控制（前端通过 `localStorage.getItem('blackhole_show_hidden')` 维护并在请求中传递 `?show_hidden=1/0`）：
  - **隐藏模式关闭（`show_hidden != 1`）**：
    - 默认相册（`dir == ""`）仅检索 `albumDir` 下的非隐藏照片（过滤掉所有 `isHiddenPath(p.Path)` 的条目）。
    - 若请求中指定了隐藏目录（`isHiddenPath(dir) == true`）：
      - 拦截并返回 `403 Forbidden`：`{"error": "私密隐藏模式已关闭，无法查看私有/隐藏目录相册，请先开启显示隐藏项目"}`。
      - `POST /api/album/scan?dir=...` 同样拒绝扫描隐藏目录。
  - **隐藏模式开启（`show_hidden == 1`）**：
    - 允许管理员查看与扫描指定的隐藏目录（如 `.relex`）。
    - 默认相册中若包含隐藏子目录的照片亦允许呈现。

### 3.4 UI 交互与状态同步

- 前端在相册中请求 `/api/album/photos` 时自动附加当前 `&show_hidden=` 参数；
- 当在文件管理器中切换「👁️ 显示隐藏项目 / 🙈 隐藏私密项目」时，相册的展示状态随之动态联动；
- 若用户尝试在隐藏模式关闭状态下切换至 `.relex` 等私密目录，UI 给出明确友好提示并引导开启「显示隐藏项目」。

## 4. Acceptance Criteria & Test Scenarios

1. **[SPEC-GALLERY-001] 开放模式拦截私有相册**: 未认证客户端请求 `GET /api/album/photos`（默认私有相册）或 `GET /api/album/photos?dir=photos` 返回 401。
2. **[SPEC-GALLERY-002] 开放模式允许公开相册**: 未认证客户端请求 `GET /api/album/photos?dir=public/wallpapers` 成功返回 200 及公开图片。
3. **[SPEC-GALLERY-003] 隐藏模式关闭拦截私密目录**: 已认证管理员在 `show_hidden=0` 时请求 `GET /api/album/photos?dir=.relex` 返回 403 Forbidden。
4. **[SPEC-GALLERY-004] 隐藏模式开启允许私密目录**: 已认证管理员在 `show_hidden=1` 时请求 `GET /api/album/photos?dir=.relex` 成功返回 200 并展现 `.relex` 内照片。
5. **[SPEC-GALLERY-005] 默认相册彻底隔离私密隐藏照片**: 在隐藏模式关闭时，默认相册绝对不展示任何路径包含 `/.` 的照片。
