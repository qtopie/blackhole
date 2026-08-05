package book

import (
	"archive/zip"
	"crypto/md5"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/qtopie/blackhole/internal/store"
)

// maxCoverBytes caps the extracted cover image to prevent ZIP-bomb style resource exhaustion.
const maxCoverBytes = 5 * 1024 * 1024

type Manager struct {
	mu       sync.RWMutex
	booksDir string
	store    *store.Store
}

func NewManager(booksDir string, st *store.Store) *Manager {
	if err := os.MkdirAll(booksDir, 0755); err != nil {
		fmt.Printf("⚠️ Warning: Failed to create books directory %s: %v\n", booksDir, err)
	}
	st.InitDB()
	mgr := &Manager{
		booksDir: booksDir,
		store:    st,
	}
	// Run initial background scan
	go func() {
		_, _ = mgr.ScanDirectory()
	}()
	return mgr
}

func (m *Manager) GetBooksDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.booksDir
}

func (m *Manager) SetBooksDir(newDir string) error {
	cleanDir := filepath.Clean(newDir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return fmt.Errorf("failed to create books directory: %w", err)
	}
	m.mu.Lock()
	m.booksDir = cleanDir
	m.mu.Unlock()

	_, err := m.ScanDirectory()
	return err
}

func isBookFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".epub", ".pdf", ".mobi", ".azw3", ".txt":
		return true
	default:
		return false
	}
}

func generateID(relPath string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(relPath)))
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

func (m *Manager) ScanDirectory() (int, error) {
	currentDir := m.GetBooksDir()
	if _, err := os.Stat(currentDir); os.IsNotExist(err) {
		return 0, fmt.Errorf("books directory does not exist: %s", currentDir)
	}

	scannedCount := 0
	err := filepath.Walk(currentDir, func(fp string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if fp != currentDir && (strings.HasPrefix(name, ".") || name == "covers") {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		if !isBookFile(info.Name()) {
			return nil
		}

		relPath, err := filepath.Rel(currentDir, fp)
		if err != nil {
			relPath = info.Name()
		}

		bookID := generateID(relPath)
		ext := strings.ToLower(filepath.Ext(info.Name()))
		format := strings.TrimPrefix(ext, ".")
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			switch format {
			case "epub":
				mimeType = "application/epub+zip"
			case "mobi":
				mimeType = "application/x-mobipocket-ebook"
			case "azw3":
				mimeType = "application/vnd.amazon.ebook"
			case "txt":
				mimeType = "text/plain"
			default:
				mimeType = "application/octet-stream"
			}
		}

		existing, _ := m.store.GetBook(bookID)

		title, author, hasCover := filenameFallback(info.Name()), "", false
		if format == "epub" {
			title, author, hasCover = extractEpubMetadata(fp)
		}

		hashVal := computeFileHash(fp, info.Size())

		book := &store.Book{
			ID:        bookID,
			Filename:  info.Name(),
			Path:      fp,
			RelPath:   relPath,
			Title:     title,
			Author:    author,
			Format:    format,
			Size:      info.Size(),
			Hash:      hashVal,
			HasCover:  hasCover,
			MIMEType:  mimeType,
			CreatedAt: info.ModTime(),
		}
		if existing != nil {
			book.CreatedAt = existing.CreatedAt
		}

		_ = m.store.SaveBook(book)
		scannedCount++
		return nil
	})

	if scannedCount > 0 {
		m.AsyncPreGenerateCovers()
	}

	return scannedCount, err
}

func filenameFallback(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// extractEpubMetadata reads an epub ZIP container and returns title, author, and whether a cover reference exists.
func extractEpubMetadata(epubPath string) (title, author string, hasCover bool) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return "", "", false
	}
	defer zr.Close()

	opfPath, err := findOPFPath(&zr.Reader)
	if err != nil || opfPath == "" {
		return "", "", false
	}

	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, opfPath) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", "", false
		}
		data, err := io.ReadAll(io.LimitReader(rc, 2*1024*1024))
		rc.Close()
		if err != nil {
			return "", "", false
		}

		var opf opfDocument
		if err := xml.Unmarshal(data, &opf); err != nil {
			return "", "", false
		}

		for _, m := range opf.Metadata.Items {
			switch m.XMLName.Local {
			case "title":
				if title == "" {
					title = strings.TrimSpace(m.Value)
				}
			case "creator":
				if author == "" {
					author = strings.TrimSpace(m.Value)
				}
			}
		}

		coverID := ""
		for _, meta := range opf.Metadata.Items {
			if meta.XMLName.Local == "meta" && strings.EqualFold(meta.Name, "cover") {
				coverID = meta.Content
				break
			}
		}
		if coverID == "" {
			for _, meta := range opf.Metadata.Items {
				if meta.XMLName.Local == "meta" && strings.EqualFold(meta.Name, "cover-image-id") {
					coverID = meta.Content
					break
				}
			}
		}

		if coverID != "" {
			opfDir := path.Dir(opfPath)
			for _, item := range opf.Manifest.Items {
				if item.ID == coverID {
					href := item.Href
					if !strings.HasPrefix(href, "/") {
						href = path.Join(opfDir, href)
					}
					hasCover = zipContains(&zr.Reader, href)
					break
				}
			}
		}

		return title, author, hasCover
	}
	return title, author, false
}

func findOPFPath(zr *zip.Reader) (string, error) {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "META-INF/container.xml") {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := io.ReadAll(io.LimitReader(rc, 1024*1024))
			if err != nil {
				return "", err
			}
			var container containerDocument
			if err := xml.Unmarshal(data, &container); err != nil {
				return "", err
			}
			for _, rf := range container.Rootfiles.Rootfile {
				if rf.FullPath != "" {
					return rf.FullPath, nil
				}
			}
		}
	}
	return "", fmt.Errorf("container.xml not found")
}

func zipContains(zr *zip.Reader, name string) bool {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}

// coverCachePath returns the absolute path to the cached cover for a book ID.
func (m *Manager) coverCachePath(bookID string) string {
	cacheBase := filepath.Dir(m.booksDir)
	if cacheBase == "." {
		cacheBase = m.booksDir
	}
	return filepath.Join(cacheBase, ".cache", "covers", bookID+".jpg")
}

// ExtractCover extracts the embedded cover image from an epub and caches it to <books_dir>/.cache/covers/{bookID}.jpg.
func (m *Manager) ExtractCover(bookID string) (string, error) {
	book, err := m.store.GetBook(bookID)
	if err != nil {
		return "", err
	}
	if book.Format != "epub" || !book.HasCover {
		return "", fmt.Errorf("no cover available for book")
	}

	cachePath := m.coverCachePath(bookID)
	if fi, err := os.Stat(cachePath); err == nil && fi.Size() > 0 {
		return cachePath, nil
	}

	zr, err := zip.OpenReader(book.Path)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	opfPath, err := findOPFPath(&zr.Reader)
	if err != nil || opfPath == "" {
		return "", fmt.Errorf("no opf found: %w", err)
	}

	coverHref, err := findCoverHref(&zr.Reader, opfPath)
	if err != nil {
		return "", err
	}

	for _, zf := range zr.File {
		if !strings.EqualFold(zf.Name, coverHref) {
			continue
		}
		if zf.UncompressedSize64 > maxCoverBytes {
			return "", fmt.Errorf("cover image exceeds size limit")
		}
		rc, err := zf.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxCoverBytes+1))
		rc.Close()
		if err != nil {
			return "", err
		}
		if len(data) > maxCoverBytes {
			return "", fmt.Errorf("cover image exceeds size limit")
		}

		_ = os.MkdirAll(filepath.Dir(cachePath), 0755)
		if err := os.WriteFile(cachePath, data, 0644); err != nil {
			return "", err
		}
		return cachePath, nil
	}

	return "", fmt.Errorf("cover image not found in epub")
}

func findCoverHref(zr *zip.Reader, opfPath string) (string, error) {
	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, opfPath) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 2*1024*1024))
		rc.Close()
		if err != nil {
			return "", err
		}
		var opf opfDocument
		if err := xml.Unmarshal(data, &opf); err != nil {
			return "", err
		}
		coverID := ""
		for _, meta := range opf.Metadata.Items {
			if meta.XMLName.Local == "meta" && (strings.EqualFold(meta.Name, "cover") || strings.EqualFold(meta.Name, "cover-image-id")) {
				coverID = meta.Content
				break
			}
		}
		if coverID != "" {
			opfDir := path.Dir(opfPath)
			for _, item := range opf.Manifest.Items {
				if item.ID == coverID {
					href := item.Href
					if !strings.HasPrefix(href, "/") {
						href = path.Join(opfDir, href)
					}
					return href, nil
				}
			}
		}
		return "", fmt.Errorf("cover reference not found in opf")
	}
	return "", fmt.Errorf("opf file not found")
}

// EnsureCover returns the cached cover path, generating it if needed.
func (m *Manager) EnsureCover(bookID string) (string, error) {
	return m.ExtractCover(bookID)
}

// CoverMIMEType returns a best-effort Content-Type for the cached cover file.
func (m *Manager) CoverMIMEType(cachePath string) string {
	f, err := os.Open(cachePath)
	if err != nil {
		return "image/jpeg"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	ct := http.DetectContentType(buf[:n])
	if ct == "" || strings.HasPrefix(ct, "text/") {
		return "image/jpeg"
	}
	return ct
}

func (m *Manager) AsyncPreGenerateCovers() {
	books, err := m.store.ListBooks()
	if err != nil || len(books) == 0 {
		return
	}
	go func() {
		for _, b := range books {
			if b.Format == "epub" && b.HasCover {
				_, _ = m.ExtractCover(b.ID)
			}
		}
	}()
}

func (m *Manager) ListBooks(search string) ([]*store.Book, error) {
	all, err := m.store.ListBooks()
	if err != nil {
		return nil, err
	}

	search = strings.ToLower(strings.TrimSpace(search))
	result := make([]*store.Book, 0, len(all))
	for _, b := range all {
		if search != "" {
			haystack := strings.ToLower(b.Title + " " + b.Author + " " + b.Filename)
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		result = append(result, b)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (m *Manager) DeleteBook(bookID string) error {
	b, err := m.store.GetBook(bookID)
	if err != nil {
		return err
	}

	if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete book file: %w", err)
	}
	_ = os.Remove(m.coverCachePath(bookID))
	return m.store.DeleteBook(bookID)
}

// --- epub XML structures ---

type containerDocument struct {
	Rootfiles struct {
		Rootfile []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfile"`
	} `xml:"rootfiles"`
}

type opfElement struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type opfDocument struct {
	Metadata struct {
		Items []opfElement `xml:",any"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			XMLName xml.Name
			ID      string `xml:"id,attr"`
			Href    string `xml:"href,attr"`
		} `xml:",any"`
	} `xml:"manifest"`
}

// FindBookByHashOrSize delegates to the store.
func (m *Manager) FindBookByHashOrSize(hash string, size int64) *store.Book {
	return m.store.FindBookByHashOrSize(hash, size)
}

var _ = http.StatusOK
