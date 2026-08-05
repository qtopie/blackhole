# RFC-0003: 书籍图书馆（Book Library）模块

- **Status:** Under Review
- **Author:** Copilot Agent
- **Created Date:** 2026-08-04

## 1. Summary

在 Blackhole NAS 中新增 **📚 书籍图书馆** 功能，提供与 🖼️ 照片相册平行的书架体验：

- 封面网格书架（读取 epub 内嵌封面图）
- epub 元数据解析（书名 / 作者，从 epub 容器内 `content.opf` 提取）
- 上传 / 删除 / 搜索 / 分页
- 复用现有 `/api/upload`、`blackhole_auth` cookie 鉴权、`store` 持久化、单文件 Fluent UI

## 2. Motivation

用户已通过 `/ui/books/` 文件管理手动上传 epub，但缺少相册级别的图书浏览体验：

1. **无封面**：epub 内嵌封面图无法预览，只能靠文件名区分
2. **无元数据**：书名、作者信息缺失，无法搜索/整理
3. **无书架 UI**：文件表格不是图书友好视图

相册模块（`internal/album` + `/api/album/*` + `🖼️ 照片相册` tab）已验证该模式的可行性，书籍图书馆复用相同架构即可低成本交付。

## 3. Detailed Design

### 3.1 目录结构

```
internal/book/
  book.go        # BookManager：扫描、epub 解析、封面提取、删除
  book_test.go   # 单元测试
internal/store/store.go   # 新增 Book 类型 + Save/Get/List/DeleteBook
internal/handler/handler.go # 新增 /api/books/* 路由 + 📚 书籍 tab UI
```

书籍目录：`<share_dir>/books`（默认 `./nas_share/books`，环境变量 `BLACKHOLE_BOOKS_DIR` 可覆盖）。

### 3.2 支持格式

| 格式 | 元数据解析 | 封面提取 |
| :--- | :--- | :--- |
| `.epub` | ✅ ZIP 容器 → `container.xml` → `content.opf` → `<dc:title>` / `<dc:creator>` | ✅ 封面 manifest 项 |
| `.pdf` / `.mobi` / `.azw3` / `.txt` | ❌ 仅文件名回退 | ❌ 无封面（占位图） |

### 3.3 epub 元数据解析流程

1. 以 ZIP 方式打开 epub
2. 读取 `META-INF/container.xml`，取 `rootfile full-path` → `content.opf`
3. 解析 OPF 的 `<metadata>`：`<dc:title>`、`<dc:creator>`
4. 封面：OPF `<meta name="cover" content="{cover-id}"/>` → `<manifest>` 中 `{cover-id}` 对应的 `href` → 从 zip 提取该图片
5. 封面缓存至 `<books_dir>/.cache/covers/{bookID}.jpg`（相册缩略图同模式）

### 3.4 Store 数据结构

```go
type Book struct {
    ID          string    `json:"id"`           // md5(relPath)
    Filename    string    `json:"filename"`
    Path        string    `json:"path"`
    RelPath     string    `json:"rel_path"`
    Title       string    `json:"title"`        // opf title 或文件名回退
    Author      string    `json:"author"`       // opf creator
    Format      string    `json:"format"`       // epub/pdf/mobi/azw3/txt
    Size        int64     `json:"size"`
    Hash        string    `json:"hash"`
    HasCover    bool      `json:"has_cover"`
    MIMEType    string    `json:"mime_type"`
    CreatedAt   time.Time `json:"created_at"`
}
```

### 3.5 REST API

| 端点 | 方法 | 说明 |
| :--- | :--- | :--- |
| `/api/books/list?page=&limit=&search=` | GET | 分页 + 搜索（书名/作者/文件名模糊匹配） |
| `/api/books/scan` | POST | 手动触发书籍目录扫描 |
| `/api/books/:id/cover` | GET | 返回封面图（无封面返回 404，前端用占位图） |
| `/api/books/:id/file` | GET | 下载/阅读书籍文件 |
| `/api/books/:id` | DELETE | 删除书籍文件 + store 记录 + 封面缓存 |
| `/api/upload?path=books/<name>` | POST | 复用现有上传，成功后可触发 books 扫描 |

全部路由挂载在 `/api` 组（`flexibleAuth` cookie 鉴权）。

### 3.6 UI（handler.go 单文件 HTML）

- 新增 tab 按钮：`📚 书籍`（`id="tabBooks"`），与 `🖼️ 照片相册` 平行
- `booksView`：封面网格书架
  - 卡片：封面图（无封面 → `📖` 占位）+ 书名 + 作者 + 格式徽标
  - 操作：hover 显示删除按钮
  - 顶部：上传按钮（`fileInput` 复用）+ 搜索框 + 扫描按钮
- 分页栏复用相册分页样式
- `switchMainTab('books')` 分支 + `localStorage` tab 记忆

### 3.7 上传后联动

前端上传按钮指向 `books` 目录上传（`/api/upload?path=books/<filename>`），完成后调用 `/api/books/scan` 刷新书架。

## 4. Alternatives Considered

| 方案 | 结论 |
| :--- | :--- |
| **复用文件管理 + 前端过滤** | 放弃：无封面/元数据，仍不是书架体验 |
| **Calibre 服务集成** | 放弃：引入重型外部依赖，与轻量单二进制设计冲突 |
| **PDF 封面第一页渲染** | 放弃：需引入 PDF 渲染库，收益低；epub 是主要格式 |

## 5. Security & Performance Considerations

- **路径安全**：`getSafePath` 复用，确保书籍路径不能逃逸 `<share_dir>`；`path` query 做清理
- **ZIP 炸弹防护**：解析 epub 时限制单文件解压大小（封面 ≤ 5MB），仅读取不整体解压
- **扫描性能**：`ScanDirectory` 复用相册的 `filepath.Walk` + 后台异步扫描模式；大文件（>5MB）哈希采样
- **封面缓存**：`.cache/covers/` 目录，删除书籍时清理对应封面
- **鉴权**：所有 `/api/books/*` 与现有 `/api/album/*` 一致，受 `flexibleAuth` 保护
