package album

import (
	"crypto/md5"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/qtopie/blackhole/internal/store"
)

type Manager struct {
	mu       sync.RWMutex
	albumDir string
	store    *store.Store
}

func NewManager(albumDir string, st *store.Store) *Manager {
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		fmt.Printf("⚠️ Warning: Failed to create album directory %s: %v\n", albumDir, err)
	}
	st.InitDB()
	mgr := &Manager{
		albumDir: albumDir,
		store:    st,
	}
	// Run initial background scan
	go func() {
		_, _ = mgr.ScanDirectory()
	}()
	return mgr
}

func (m *Manager) GetAlbumDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.albumDir
}

func (m *Manager) SetAlbumDir(newDir string) error {
	cleanDir := filepath.Clean(newDir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return fmt.Errorf("failed to create album directory: %w", err)
	}
	m.mu.Lock()
	m.albumDir = cleanDir
	m.mu.Unlock()

	_, err := m.ScanDirectory()
	return err
}

func isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp":
		return true
	default:
		return false
	}
}

func isVideoFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp4", ".mkv", ".webm", ".avi", ".mov":
		return true
	default:
		return false
	}
}

func isMediaFile(filename string) bool {
	return isImageFile(filename) || isVideoFile(filename)
}

func generateID(relPath string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(relPath)))
}

func (m *Manager) ScanDirectory() (int, error) {
	currentDir := m.GetAlbumDir()
	if _, err := os.Stat(currentDir); os.IsNotExist(err) {
		return 0, fmt.Errorf("album directory does not exist: %s", currentDir)
	}

	scannedCount := 0
	err := filepath.Walk(currentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if path != currentDir && (strings.HasPrefix(name, ".") || name == "thumbnails") {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		if !isMediaFile(info.Name()) {
			return nil
		}

		isVideo := isVideoFile(info.Name())
		mediaType := "image"
		if isVideo {
			mediaType = "video"
		}

		relPath, err := filepath.Rel(currentDir, path)
		if err != nil {
			relPath = info.Name()
		}

		photoID := generateID(relPath)
		width, height := 0, 0

		if !isVideo && info.Size() < 5*1024*1024 {
			if f, err := os.Open(path); err == nil {
				if cfg, _, err := image.DecodeConfig(f); err == nil {
					width = cfg.Width
					height = cfg.Height
				}
				f.Close()
			}
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			if isVideo {
				switch ext {
				case ".mp4":
					mimeType = "video/mp4"
				case ".webm":
					mimeType = "video/webm"
				case ".mkv":
					mimeType = "video/x-matroska"
				case ".avi":
					mimeType = "video/x-msvideo"
				case ".mov":
					mimeType = "video/quicktime"
				default:
					mimeType = "video/mp4"
				}
			} else {
				mimeType = "image/jpeg"
			}
		}

		existing, _ := m.store.GetPhoto(photoID)
		isFav := false
		isDark := false
		var lum float64 = 0
		if existing != nil {
			isFav = existing.IsFavorite
			isDark = existing.IsDark
			lum = existing.Luminance
		}

		hashVal := computeFileHash(path, info.Size())

		photo := &store.Photo{
			ID:         photoID,
			Filename:   info.Name(),
			Path:       path,
			RelPath:    relPath,
			Size:       info.Size(),
			Hash:       hashVal,
			Width:      width,
			Height:     height,
			MIMEType:   mimeType,
			MediaType:  mediaType,
			IsVideo:    isVideo,
			CreatedAt:  info.ModTime(),
			TakenAt:    info.ModTime(),
			IsFavorite: isFav,
			IsDark:     isDark,
			Luminance:  lum,
		}

		_ = m.store.SavePhoto(photo)
		scannedCount++
		return nil
	})

	if scannedCount > 0 {
		m.AsyncPreGenerateThumbnails()
	}

	return scannedCount, err
}

func (m *Manager) AsyncPreGenerateThumbnails() {
	photos, err := m.store.ListPhotos()
	if err != nil || len(photos) == 0 {
		return
	}

	go func() {
		for _, p := range photos {
			if p.IsVideo {
				continue
			}
			_, _ = m.EnsureThumbnail(p.Path, p.ID)
			runtime.GC()
		}
	}()
}

func computeFileHash(filePath string, size int64) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := md5.New()
	if size <= 5*1024*1024 {
		_, _ = io.Copy(h, f)
	} else {
		buf := make([]byte, 1024*1024)
		n, _ := f.Read(buf)
		h.Write(buf[:n])
		_, _ = f.Seek(size/2, io.SeekStart)
		n, _ = f.Read(buf)
		h.Write(buf[:n])
		if size > int64(len(buf)) {
			_, _ = f.Seek(size-int64(len(buf)), io.SeekStart)
			n, _ = f.Read(buf)
			h.Write(buf[:n])
		}
		h.Write([]byte(fmt.Sprintf("%d", size)))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (m *Manager) FindPhotoByHash(hash string, size int64) *store.Photo {
	return m.store.FindPhotoByHashOrSize(hash, size)
}

func (m *Manager) ListPhotos(favoriteOnly bool) ([]*store.Photo, error) {
	return m.ListPhotosFiltered(favoriteOnly, false)
}

func (m *Manager) ListPhotosFiltered(favoriteOnly, darkOnly bool) ([]*store.Photo, error) {
	all, err := m.store.ListPhotos()
	if err != nil {
		return nil, err
	}

	result := make([]*store.Photo, 0, len(all))
	for _, p := range all {
		if favoriteOnly && !p.IsFavorite {
			continue
		}
		if darkOnly && !p.IsDark {
			continue
		}
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result, nil
}

func (m *Manager) ToggleFavorite(photoID string) (*store.Photo, error) {
	p, err := m.store.GetPhoto(photoID)
	if err != nil {
		return nil, err
	}
	p.IsFavorite = !p.IsFavorite
	if err := m.store.SavePhoto(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (m *Manager) DeletePhoto(photoID string) error {
	p, err := m.store.GetPhoto(photoID)
	if err != nil {
		return err
	}

	// Remove file from filesystem
	if err := os.Remove(p.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete photo file: %w", err)
	}

	return m.store.DeletePhoto(photoID)
}

func (m *Manager) BatchDeletePhotos(ids []string) (int, error) {
	deletedCount := 0
	cacheBase := filepath.Dir(m.albumDir)
	if cacheBase == "." {
		cacheBase = m.albumDir
	}

	for _, id := range ids {
		p, err := m.store.GetPhoto(id)
		if err == nil && p != nil {
			_ = os.Remove(p.Path)
			thumbPath := filepath.Join(cacheBase, ".cache", "photos", "thumbnails", id+".jpg")
			_ = os.Remove(thumbPath)
			deletedCount++
		}
	}
	_ = m.store.DeletePhotos(ids)
	return deletedCount, nil
}
