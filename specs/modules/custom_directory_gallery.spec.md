# Custom Directory Gallery Specification

- **Module:** `album` & `handler`
- **Associated RFC:** `docs/rfcs/0005-custom-directory-photo-gallery.md`

## Behavioral Contracts

### 1. Directory Path Resolution & Security Contract

- When a directory query parameter `dir` is supplied to album endpoints:
  - If `dir` is a relative path (does not begin with `/`), it MUST be resolved relative to `shareDir` (`h.shareDir`).
  - If `dir` is an absolute path (begins with `/`), it MUST be cleaned with `filepath.Clean`.
  - For unauthenticated/read-only sessions (e.g. guest accessing `/public/*`), `dir` MUST strictly reside within `publicDir` (`/public`), returning HTTP `401 Unauthorized` or `400 Bad Request` if an attempt is made to escape to private directories.
  - If the resolved directory does not exist or is not a directory, the endpoint MUST return HTTP `400 Bad Request` or `404 Not Found` with a descriptive error message.

### 2. Custom Directory Photos Listing Contract

- `GET /api/album/photos?dir=<path>&page=P&limit=L&favorite=0/1&dark=0/1`:
  - When `dir` is specified, the system MUST return ONLY photos whose physical file paths reside within that target directory (`photo.Path` starts with `targetDir`).
  - If no photos for `targetDir` currently exist in the store, the system MUST automatically perform an on-demand scan of `targetDir` before returning results.
  - The response JSON structure MUST match standard photo listing:
    ```json
    {
      "status": "success",
      "total": N,
      "page": P,
      "limit": L,
      "total_pages": M,
      "dir": "<resolved_directory_path>",
      "photos": [...]
    }
    ```

### 3. Targeted Directory Scan Contract

- `POST /api/album/scan?dir=<path>`:
  - When `dir` is provided, `ScanDirectoryPath(targetDir)` MUST walk only `targetDir` recursively, skipping hidden directories and thumbnail cache folders.
  - The scan MUST index all supported media formats (`.jpg`, `.jpeg`, `.png`, `.webp`, `.gif`, `.bmp`, `.mp4`, `.mkv`, `.webm`, `.avi`, `.mov`) into `store`.
  - The response JSON MUST return `{"status": "success", "message": "...", "count": N, "dir": "<targetDir>"}`.

### 4. Efficient Media Retrieval Contract

- `GET /api/album/photos/:id/file` and `GET /api/album/photos/:id/thumbnail`:
  - MUST lookup the photo record in O(1) via `store.GetPhoto(id)` rather than iterating through full photo lists.
  - If the requested media file exists on disk, `GetPhotoFile` MUST serve the content with appropriate MIME headers and range support.
  - If a cached thumbnail is missing, `GetPhotoThumbnail` MUST generate it dynamically, cache it, and serve it.

### 5. Web UI Dual Navigation Contract

- **File Manager to Gallery**:
  - In `filesView`, the action bar MUST render a `🖼️ 查看图片合集` (View Image Gallery) button.
  - Clicking this button MUST switch to Gallery view and pass the current directory path (e.g. `switchMainTab('album', currentDir)` or URL hash `/#album?dir=...`).
  - In Gallery view, a `📁 返回文件列表` (Back to Files) button MUST be displayed when a non-default directory is being viewed, returning the user directly to the file manager at that directory path.
- **Album View Custom Directory Switcher**:
  - The `albumView` action bar MUST show the active directory path.
  - Clicking `📂 切换目录` (Switch Directory) MUST prompt an input dialog allowing the user to enter or paste any directory path.
  - When viewing a custom directory, a `🏠 默认相册` (Default Album) button MUST be available to instantly restore the default album view.

## Acceptance Scenarios (Harness)

1. **Custom Directory Listing & Isolation**:
   - Given two directories `dirA` and `dirB` containing distinct test images, `GET /api/album/photos?dir=dirA` MUST only return images belonging to `dirA`, and `GET /api/album/photos?dir=dirB` MUST only return images belonging to `dirB`.
2. **On-demand Auto-Scan for Custom Directory**:
   - Calling `GET /api/album/photos?dir=<unscanned_dir>` for a newly created folder with 3 images MUST automatically index the files and return `total = 3`.
3. **Invalid Directory Handling**:
   - Calling `GET /api/album/photos?dir=non_existent_folder_xyz` MUST return HTTP 400 or 404 with error details.
4. **O(1) Media Serving**:
   - Querying `GET /api/album/photos/:id/file` for an indexed photo in a custom directory MUST return HTTP 200 with the exact file bytes.
