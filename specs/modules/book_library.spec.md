# Book Library Specification

## Behavioral Contracts

### 1. Book Scanning & Listing Contract

- `ScanDirectory()` MUST walk the books directory recursively, skip hidden files/dirs (prefix `.`), and register only supported book formats (`.epub`, `.pdf`, `.mobi`, `.azw3`, `.txt`).
- Each scanned book MUST be assigned `ID = md5(relPath)`. Re-scanning the same file MUST be idempotent (preserve existing metadata where the file is unchanged).
- `GET /api/books/list` MUST return `{ "books": [...], "total": N, "page": P, "total_pages": M }`.
- The `search` query parameter MUST filter books by fuzzy match against `title`, `author`, and `filename` (case-insensitive, substring).

### 2. epub Metadata Extraction Contract

- For `.epub` files, the scanner MUST open the ZIP container and read `META-INF/container.xml` to locate the root `content.opf` path.
- From the OPF, `title` MUST be set from `<dc:title>` and `author` from `<dc:creator>`. If absent or the OPF is unparsable, `title` MUST fall back to the file base name (without extension) and `author` MUST be empty.
- Cover extraction MUST follow: OPF `<meta name="cover" content="{id}"/>` → `<manifest>` entry `href` for `{id}` → read that image from the ZIP. The extracted cover MUST be cached to `<books_dir>/.cache/covers/{bookID}.jpg`.
- If no cover reference exists, the file has no cover (`has_cover = false`) and extraction MUST NOT error out the whole scan; the book is still registered with filename-derived title.

### 3. Cover Serving Contract

- `GET /api/books/:id/cover` MUST return the cached cover image (Content-Type from detected MIME) when `has_cover` is true.
- When the book has no cover or the cache is missing, it MUST return `404` so the frontend can render the `📖` placeholder.
- Cover extraction MUST cap the extracted image at 5 MB to prevent ZIP-bomb style resource exhaustion.

### 4. Delete Contract

- `DELETE /api/books/:id` MUST remove: (a) the book file from disk, (b) the cover cache file if present, (c) the store record.
- Deleting a non-existent book id MUST return `404` with an error payload.

### 5. Upload Contract

- Uploads use the existing `POST /api/upload?path=books/<filename>` endpoint (multipart `file` field). A successful upload MUST persist the file under the books directory and return `{"status": "success"}`.
- The frontend bookshelf upload MUST target `books/` as the path prefix and MUST call `POST /api/books/scan` after completion to refresh the shelf.

### 6. Bookshelf UI Contract

- The web UI MUST render a `📚 书籍` tab button (`id="tabBooks"`, `data-i18n="booksTab"`) alongside the existing Files and Gallery tabs.
- The bookshelf view MUST be a cover grid: each card shows the cover image (or `📖` placeholder), title, author, and a format badge.
- The bookshelf MUST provide: upload button, search box (client-side debounce → `/api/books/list?search=`), a manual scan button, pagination controls, and per-card delete (hover-revealed) with confirmation.
- `switchMainTab('books')` MUST show `booksView`, hide other views, and persist the tab selection in `localStorage` (`blackhole_active_tab`).

### 7. Persistence Contract

- Book records MUST be persisted in the `store` in the same manner as photos (in-memory map + Dapr state + SurrealDB UPSERT/DELETE best-effort).
- `store` MUST expose `SaveBook`, `GetBook`, `ListBooks`, `DeleteBook`, `FindBookByHashOrSize` for the book module.

## Acceptance Scenarios (Harness)

1. **Scan a generated epub fixture**: Given a fixture epub containing a `container.xml`, an OPF with `<dc:title>`/`<dc:creator>` and a cover `<meta>` + manifest href + embedded cover image, scanning MUST yield a `Book` with correct `title`, `author`, `has_cover=true`, and a non-empty cover cache file.
2. **List + search + pagination**: After registering N books, `/api/books/list?search=<partial>&page=1&limit=5` MUST return only matching books, correct `total`, and `total_pages = ceil(total/limit)`.
3. **Fallback title**: A txt/plain or metadata-less epub MUST yield `title == base filename without extension` and `has_cover=false`.
4. **Delete cascade**: Deleting a book MUST remove the file, the cover cache, and the store record; a second delete MUST return 404.
5. **Upload + rescan**: POSTing an epub to `/api/upload?path=books/<name>` then `/api/books/scan` MUST make it appear in `/api/books/list`.
