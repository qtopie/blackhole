package book

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qtopie/blackhole/internal/config"
	"github.com/qtopie/blackhole/internal/store"
)

// createTestJPEG returns in-memory JPEG bytes for embedding into epub fixtures.
func createTestJPEG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: 120, B: uint8(y % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("failed to encode sample image: %v", err)
	}
	return buf.Bytes()
}

// createTestEpub builds a minimal but valid epub ZIP in dir with the given
// title/author, and optionally an embedded cover image.
func createTestEpub(t *testing.T, dir, filename, title, author string, withCover bool) string {
	t.Helper()
	path := filepath.Join(dir, filename)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	containerXML := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	addZipEntry(t, zw, "META-INF/container.xml", []byte(containerXML))

	manifestItems := `<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>`
	if withCover {
		manifestItems = `<item id="cover-image" href="cover.jpg" media-type="image/jpeg"/>` + manifestItems
	}

	coverMeta := ""
	if withCover {
		coverMeta = `<meta name="cover" content="cover-image"/>`
	}

	opfXML := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="BookId">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>` + title + `</dc:title>
    <dc:creator opf:role="aut">` + author + `</dc:creator>
    ` + coverMeta + `
  </metadata>
  <manifest>
    ` + manifestItems + `
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`
	addZipEntry(t, zw, "OEBPS/content.opf", []byte(opfXML))

	chapterXML := `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + title + `</title></head>
<body><h1>` + title + `</h1><p>Test chapter content.</p></body></html>`
	addZipEntry(t, zw, "OEBPS/chapter.xhtml", []byte(chapterXML))

	if withCover {
		addZipEntry(t, zw, "OEBPS/cover.jpg", createTestJPEG(t, 64))
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write epub: %v", err)
	}
	return path
}

func addZipEntry(t *testing.T, zw *zip.Writer, name string, data []byte) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("failed to create zip entry %s: %v", name, err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("failed to write zip entry %s: %v", name, err)
	}
}

func newTestManager(t *testing.T, booksDir string) *Manager {
	t.Helper()
	cfg := config.LoadConfig()
	cfg.ShareDir = filepath.Dir(booksDir)
	st := store.NewStore(cfg)
	return NewManager(booksDir, st)
}

func TestScanEpubExtractsMetadataAndCover(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "book_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	booksDir := filepath.Join(tempDir, "books")
	mgr := newTestManager(t, booksDir)

	createTestEpub(t, booksDir, "mybook.epub", "Alice in Wonderland", "Lewis Carroll", true)

	count, err := mgr.ScanDirectory()
	if err != nil {
		t.Fatalf("failed to scan directory: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 book scanned, got %d", count)
	}

	books, err := mgr.ListBooks("")
	if err != nil || len(books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(books))
	}
	b := books[0]
	if b.Title != "Alice in Wonderland" {
		t.Fatalf("expected title 'Alice in Wonderland', got %q", b.Title)
	}
	if b.Author != "Lewis Carroll" {
		t.Fatalf("expected author 'Lewis Carroll', got %q", b.Author)
	}
	if b.Format != "epub" {
		t.Fatalf("expected format epub, got %q", b.Format)
	}
	if !b.HasCover {
		t.Fatalf("expected has_cover true")
	}

	cachePath, err := mgr.EnsureCover(b.ID)
	if err != nil {
		t.Fatalf("failed to extract cover: %v", err)
	}
	fi, err := os.Stat(cachePath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty cover cache file at %s, got err=%v size=%d", cachePath, err, fi.Size())
	}
}

func TestScanFallsBackToFilenameForTxt(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "book_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	booksDir := filepath.Join(tempDir, "books")
	mgr := newTestManager(t, booksDir)

	if err := os.WriteFile(filepath.Join(booksDir, "notes.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write txt: %v", err)
	}

	count, err := mgr.ScanDirectory()
	if err != nil {
		t.Fatalf("failed to scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 book, got %d", count)
	}

	books, _ := mgr.ListBooks("")
	if len(books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(books))
	}
	if books[0].Title != "notes" {
		t.Fatalf("expected fallback title 'notes', got %q", books[0].Title)
	}
	if books[0].HasCover {
		t.Fatalf("expected no cover for txt book")
	}

	// Cover extraction must fail cleanly for non-epub books.
	if _, err := mgr.EnsureCover(books[0].ID); err == nil {
		t.Fatalf("expected error extracting cover from txt book")
	}
}

func TestListBooksSearchAndSort(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "book_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	booksDir := filepath.Join(tempDir, "books")
	mgr := newTestManager(t, booksDir)

	createTestEpub(t, booksDir, "a.epub", "Golang in Practice", "Jane Doe", false)
	createTestEpub(t, booksDir, "b.epub", "Rust for Beginners", "John Smith", false)

	if _, err := mgr.ScanDirectory(); err != nil {
		t.Fatalf("failed to scan: %v", err)
	}

	all, _ := mgr.ListBooks("")
	if len(all) != 2 {
		t.Fatalf("expected 2 books, got %d", len(all))
	}

	// Search matches title, author and filename.
	byTitle, _ := mgr.ListBooks("golang")
	if len(byTitle) != 1 || byTitle[0].Title != "Golang in Practice" {
		t.Fatalf("search by title failed: %+v", byTitle)
	}
	byAuthor, _ := mgr.ListBooks("john")
	if len(byAuthor) != 1 || byAuthor[0].Author != "John Smith" {
		t.Fatalf("search by author failed: %+v", byAuthor)
	}
	byFilename, _ := mgr.ListBooks("a.epub")
	if len(byFilename) != 1 {
		t.Fatalf("search by filename failed: %+v", byFilename)
	}
	noMatch, _ := mgr.ListBooks("nonexistent")
	if len(noMatch) != 0 {
		t.Fatalf("expected no matches, got %d", len(noMatch))
	}
}

func TestDeleteBookRemovesFileStoreAndCoverCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "book_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	booksDir := filepath.Join(tempDir, "books")
	mgr := newTestManager(t, booksDir)

	createTestEpub(t, booksDir, "delete-me.epub", "Delete Me", "Author", true)
	if _, err := mgr.ScanDirectory(); err != nil {
		t.Fatalf("failed to scan: %v", err)
	}

	books, _ := mgr.ListBooks("")
	if len(books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(books))
	}
	b := books[0]

	// Generate the cover cache first.
	if _, err := mgr.EnsureCover(b.ID); err != nil {
		t.Fatalf("failed to ensure cover: %v", err)
	}
	coverPath := mgr.coverCachePath(b.ID)
	if _, err := os.Stat(coverPath); err != nil {
		t.Fatalf("expected cover cache to exist: %v", err)
	}

	if err := mgr.DeleteBook(b.ID); err != nil {
		t.Fatalf("failed to delete book: %v", err)
	}

	if _, err := os.Stat(b.Path); !os.IsNotExist(err) {
		t.Fatalf("expected book file removed, err=%v", err)
	}
	if _, err := os.Stat(coverPath); !os.IsNotExist(err) {
		t.Fatalf("expected cover cache removed, err=%v", err)
	}
	if _, err := mgr.ListBooks(""); err != nil || len(mustListBooks(t, mgr)) != 0 {
		t.Fatalf("expected store to be empty after delete")
	}

	// Cascade: second delete must fail with an error.
	if err := mgr.DeleteBook(b.ID); err == nil {
		t.Fatalf("expected error on second delete")
	}
}

func mustListBooks(t *testing.T, mgr *Manager) []*store.Book {
	t.Helper()
	books, err := mgr.ListBooks("")
	if err != nil {
		t.Fatalf("failed to list books: %v", err)
	}
	return books
}

func TestScanSkipsNonBookAndHiddenFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "book_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	booksDir := filepath.Join(tempDir, "books")
	mgr := newTestManager(t, booksDir)

	if err := os.MkdirAll(filepath.Join(booksDir, ".hidden"), 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(booksDir, "ignore.pdf"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(booksDir, ".hidden", "secret.epub"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(booksDir, "cover.jpg"), []byte("not a book"), 0644)

	count, err := mgr.ScanDirectory()
	if err != nil {
		t.Fatalf("failed to scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only 1 book (ignore.pdf), got %d", count)
	}

	books := mustListBooks(t, mgr)
	if len(books) != 1 || !strings.HasSuffix(books[0].Filename, ".pdf") {
		t.Fatalf("expected single pdf book, got %+v", books)
	}
}

// TestScanHarnessFixture validates the committed harness epub fixture
// (harness/fixtures/sample_book.epub) end-to-end: metadata + cover extraction.
func TestScanHarnessFixture(t *testing.T) {
	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "harness", "fixtures", "sample_book.epub"))
	if err != nil {
		t.Fatalf("failed to resolve fixture path: %v", err)
	}
	if _, err := os.Stat(fixturePath); os.IsNotExist(err) {
		t.Skipf("harness fixture not present: %s", fixturePath)
	}

	tempDir, err := os.MkdirTemp("", "book_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	booksDir := filepath.Join(tempDir, "books")
	if err := os.MkdirAll(booksDir, 0755); err != nil {
		t.Fatalf("failed to mkdir books dir: %v", err)
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(booksDir, "sample_book.epub"), data, 0644); err != nil {
		t.Fatalf("failed to copy fixture: %v", err)
	}

	mgr := newTestManager(t, booksDir)
	if _, err := mgr.ScanDirectory(); err != nil {
		t.Fatalf("failed to scan fixture: %v", err)
	}

	books := mustListBooks(t, mgr)
	if len(books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(books))
	}
	b := books[0]
	if b.Title != "The Blackhole Adventures" {
		t.Fatalf("expected fixture title, got %q", b.Title)
	}
	if b.Author != "Qtopie Team" {
		t.Fatalf("expected fixture author, got %q", b.Author)
	}
	if !b.HasCover {
		t.Fatalf("expected fixture book to have cover")
	}
	cachePath, err := mgr.EnsureCover(b.ID)
	if err != nil {
		t.Fatalf("failed to extract cover: %v", err)
	}
	fi, err := os.Stat(cachePath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty cover cache: err=%v size=%d", err, fi.Size())
	}
	if mgr.CoverMIMEType(cachePath) != "image/jpeg" {
		t.Fatalf("expected image/jpeg content type, got %q", mgr.CoverMIMEType(cachePath))
	}
}
