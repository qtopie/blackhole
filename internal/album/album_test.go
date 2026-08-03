package album

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/qtopie/blackhole/internal/config"
	"github.com/qtopie/blackhole/internal/store"
)

func createSampleImage(t *testing.T, dir, filename string) string {
	path := filepath.Join(dir, filename)
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for x := 0; x < 100; x++ {
		for y := 0; y < 100; y++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create sample image: %v", err)
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("failed to encode sample image: %v", err)
	}
	return path
}

func TestAlbumManagerScanAndList(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "album_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := config.LoadConfig()
	st := store.NewStore(cfg)
	mgr := NewManager(tempDir, st)

	createSampleImage(t, tempDir, "test1.jpg")
	createSampleImage(t, tempDir, "test2.png")
	os.WriteFile(filepath.Join(tempDir, "document.pdf"), []byte("pdf content"), 0644)

	count, err := mgr.ScanDirectory()
	if err != nil {
		t.Fatalf("failed to scan directory: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 scanned image files, got %d", count)
	}

	photos, err := mgr.ListPhotos(false)
	if err != nil || len(photos) != 2 {
		t.Fatalf("expected 2 photos in list, got %d", len(photos))
	}

	// Test Favorite Toggle
	favPhoto, err := mgr.ToggleFavorite(photos[0].ID)
	if err != nil || !favPhoto.IsFavorite {
		t.Fatalf("expected photo to be favorite")
	}

	favList, _ := mgr.ListPhotos(true)
	if len(favList) != 1 {
		t.Fatalf("expected 1 favorite photo, got %d", len(favList))
	}

	// Test Delete
	if err := mgr.DeletePhoto(photos[0].ID); err != nil {
		t.Fatalf("failed to delete photo: %v", err)
	}

	photosAfter, _ := mgr.ListPhotos(false)
	if len(photosAfter) != 1 {
		t.Fatalf("expected 1 photo after delete, got %d", len(photosAfter))
	}
}
