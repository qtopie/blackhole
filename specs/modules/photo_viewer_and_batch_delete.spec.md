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
