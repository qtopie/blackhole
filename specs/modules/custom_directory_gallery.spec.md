# Custom Directory Gallery Specification

- **Module:** `album` & `handler`
- **Associated RFC:** `docs/rfcs/0005-custom-directory-photo-gallery.md`

## Behavioral Contracts

### 1. Open Mode Access Contract (开放模式)

- An unauthenticated client (anonymous session or guest accessing `/public/*`):
  - Is strictly in **Open Mode**.
  - `GET /api/album/photos`:
    - MUST provide a `dir` parameter that resolves within the public root (`/var/nas_share/public`).
    - If `dir` is empty (attempting to view default private album) or resolves outside `/public` (e.g. `photos`, `.relex`, `vault`, root `/`), the server MUST respond with HTTP `401 Unauthorized` and trigger a Basic Auth challenge.
  - `GET /api/album/photos/:id/file` and `GET /api/album/photos/:id/thumbnail`:
    - MUST verify that the referenced photo's physical disk path resides within the public directory (`/var/nas_share/public`). If it resides in a private directory, the server MUST return HTTP `401 Unauthorized`.

### 2. Hidden Mode & Privacy Control Contract (隐藏模式)

- The server and client use `show_hidden` (`1` or `0`, boolean) to control access to private/hidden directories:
  - Any directory path where any component starts with `.` (e.g. `.relex`, `photos/.hidden`, `.cache`) is defined as a **Hidden/Private Directory** (`isHiddenPath == true`).
  - **When Hidden Mode is Closed (`show_hidden != 1`)**:
    - Default album listing (`dir == ""`): MUST ONLY return photos originating from `albumDir` (`/var/nas_share/photos`), and MUST EXCLUDE any photo whose file path contains a hidden component (`isHiddenPath(photo.Path) == true`).
    - Targeted directory listing (`GET /api/album/photos?dir=...`): If the resolved `dir` is a hidden directory (`isHiddenPath(dir) == true`), the server MUST return HTTP `403 Forbidden` with an error message: `"私密隐藏模式已关闭，无法查看私有/隐藏目录的相册，请先开启显示隐藏项目"`.
    - Targeted directory scanning (`POST /api/album/scan?dir=...`): If `dir` is hidden, the scan MUST be rejected with HTTP `403 Forbidden`.
  - **When Hidden Mode is Open (`show_hidden == 1`)**:
    - An authenticated administrator MAY view and scan hidden/private directories (such as `.relex`).
    - The returned collection MUST contain the indexed photos within that target directory.

### 3. Dual UI Navigation & State Synchronization Contract

- In `filesView`, the `toggleShowHiddenBtn` toggles `showHiddenState` (`blackhole_show_hidden`).
- When switching from File Manager to Gallery (`viewCurrentDirGallery()`):
  - If the directory being viewed is hidden and `showHiddenState` is false, the UI MUST warn the user and prompt them to enable "显示隐藏项目".
- When `albumView` fetches photos:
  - The request URL MUST include `&show_hidden=1` when `showHiddenState` is true, and `&show_hidden=0` when false.
- When `albumView` is in default mode:
  - It NEVER exposes hidden directory photos when hidden mode is closed.

## Acceptance Criteria (Harness BDD Scenarios)

1. **[SPEC-GALLERY-001] Open Mode Blocks Private Default & Private Custom Album**:
   - Given an unauthenticated client, requesting `GET /api/album/photos` or `GET /api/album/photos?dir=photos` MUST return HTTP 401.
2. **[SPEC-GALLERY-002] Open Mode Allows Public Directory Album**:
   - Given an unauthenticated client, requesting `GET /api/album/photos?dir=public/wallpapers` MUST return HTTP 200 with the matching photos.
3. **[SPEC-GALLERY-003] Hidden Mode OFF Blocks Hidden Directory Album & Scan**:
   - Given an authenticated admin with `show_hidden=0`, requesting `GET /api/album/photos?dir=.relex` or `POST /api/album/scan` with `dir=.relex` MUST return HTTP 403 Forbidden.
4. **[SPEC-GALLERY-004] Hidden Mode ON Allows Hidden Directory Album**:
   - Given an authenticated admin with `show_hidden=1`, requesting `GET /api/album/photos?dir=.relex&show_hidden=1` MUST return HTTP 200 with photos in `.relex`.
5. **[SPEC-GALLERY-005] Default Album Excludes Hidden Photos When Hidden Mode OFF**:
   - When querying `GET /api/album/photos?show_hidden=0`, photos whose paths contain hidden folders (e.g. `/.relex/`) MUST NOT be present in the returned list.
