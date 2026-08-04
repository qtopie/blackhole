# Photo Viewer & Batch Delete Specification

## Behavioral Contracts

1. **Photo Listing & Pagination Contract**:
   - `GET /api/album/photos?page=P&limit=L`:
     Must return total photo count, current page, total pages, and a list of photos sorted chronologically.
   - `GET /ui/*path`:
     Must render breadcrumbs, search box (`#searchInput`), table container (`#fileTable`), and pagination controls (`#filePaginationBar`).

2. **Pitch-Black / Lens-Cap Photo Detection Contract**:
   - For photos with average ITU-R BT.601 relative luminance \( Y_{avg} \le 12.0 \), `is_dark` MUST be set to `true`.
   - `GET /api/album/photos?dark=1`:
     Must filter and return only photos where `is_dark == true`.

3. **Batch Delete Contract**:
   - `POST /api/album/photos/batch-delete`:
     Payload: `{"photo_ids": ["id1", "id2", ...]}`
     Must remove photo original files from physical disk, delete thumbnail cache files, and delete records from database store.

4. **Fullscreen Lightbox & Adaptive Container Fitting Contract**:
   - **Container-Fit Viewport (`#modalViewport`)**: The image viewport MUST occupy the full modal body container space (`position: relative; width: 100%; height: 100%; overflow: hidden`).
   - **Floating Overlay Toolbar (`.lightbox-toolbar`)**: Floating controls (Header, Navigation, Zoom, Meta) MUST be overlayed with translucent/glassmorphism styling (`position: absolute; pointer-events: auto`) on top of the image container so they never occupy structural layout height or crop/squeeze the image.
   - **Adaptive Min-Scale Fit**: The preview image (`#modalPhotoImg`) MUST be scaled by the **minimum** of `(viewportWidth / imageWidth)` and `(viewportHeight / imageHeight)`, i.e. `object-fit: contain` within a container that is exactly `100vw × 100vh`. This guarantees both dimensions always fit: when a photo is wider than the screen it scales to viewport width; when it is taller it scales to viewport height; when both exceed the screen it picks the smaller ratio so the photo is always 100% visible in one glance.


