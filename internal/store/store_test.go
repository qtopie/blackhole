package store

import (
	"testing"
	"time"

	"github.com/qtopie/blackhole/internal/config"
)

func TestStorePhotoAndAlbum(t *testing.T) {
	cfg := config.LoadConfig()
	st := NewStore(cfg)

	p := &Photo{
		ID:        "photo_1",
		Filename:  "vacation.jpg",
		Path:      "/tmp/photos/vacation.jpg",
		RelPath:   "vacation.jpg",
		Size:      1024,
		CreatedAt: time.Now(),
	}

	if err := st.SavePhoto(p); err != nil {
		t.Fatalf("failed to save photo: %v", err)
	}

	retrieved, err := st.GetPhoto("photo_1")
	if err != nil || retrieved.Filename != "vacation.jpg" {
		t.Fatalf("unexpected photo: %v, %v", retrieved, err)
	}

	photos, err := st.ListPhotos()
	if err != nil || len(photos) != 1 {
		t.Fatalf("expected 1 photo in list, got %d", len(photos))
	}

	// Delete photo
	if err := st.DeletePhoto("photo_1"); err != nil {
		t.Fatalf("failed to delete photo: %v", err)
	}

	photos, _ = st.ListPhotos()
	if len(photos) != 0 {
		t.Fatalf("expected 0 photos after delete, got %d", len(photos))
	}
}
