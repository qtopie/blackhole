package nfs

import (
	"crypto/rand"
	"encoding/binary"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const (
	HandleLen = 32
)

// HandleManager creates, tracks, and resolves NFSv4 opaque filehandles.
type HandleManager struct {
	rootDir   string
	mu        sync.RWMutex
	pathToFH  map[string][]byte
	fhToPath  map[string]string
	salt      uint64
}

func NewHandleManager(rootDir string) *HandleManager {
	var saltBytes [8]byte
	_, _ = rand.Read(saltBytes[:])
	salt := binary.BigEndian.Uint64(saltBytes[:])

	hm := &HandleManager{
		rootDir:  rootDir,
		pathToFH: make(map[string][]byte),
		fhToPath: make(map[string]string),
		salt:     salt,
	}

	// Register root handle
	_ = hm.GetOrCreateFH("")
	return hm
}

// GetOrCreateFH returns or creates a stable 32-byte handle for relative path.
func (hm *HandleManager) GetOrCreateFH(relPath string) []byte {
	cleanRel := filepath.Clean(relPath)
	if cleanRel == "." || cleanRel == "/" {
		cleanRel = ""
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	if fh, ok := hm.pathToFH[cleanRel]; ok {
		return fh
	}

	// Generate new handle
	fh := make([]byte, HandleLen)
	fh[0] = 'N'
	fh[1] = 'F'
	fh[2] = 0x01 // Version 1
	if cleanRel == "" {
		fh[3] = 0x01 // Root
	} else {
		fh[3] = 0x00
	}

	// Try getting physical inode
	absPath := filepath.Join(hm.rootDir, cleanRel)
	var ino uint64
	if fi, err := os.Lstat(absPath); err == nil {
		if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
			ino = stat.Ino
		}
	}
	binary.BigEndian.PutUint64(fh[4:12], ino)

	// FNV hash of cleanRel path
	h := fnv.New64a()
	h.Write([]byte(cleanRel))
	pathHash := h.Sum64()
	binary.BigEndian.PutUint64(fh[12:20], pathHash)

	// Salt verification
	binary.BigEndian.PutUint64(fh[20:28], hm.salt^pathHash)

	hm.pathToFH[cleanRel] = fh
	hm.fhToPath[string(fh)] = cleanRel
	return fh
}

// ResolvePath resolves an opaque filehandle to its relative path and absolute path.
func (hm *HandleManager) ResolvePath(fh []byte) (relPath string, absPath string, ok bool) {
	if len(fh) != HandleLen || fh[0] != 'N' || fh[1] != 'F' || fh[2] != 0x01 {
		return "", "", false
	}

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	rel, exists := hm.fhToPath[string(fh)]
	if !exists {
		return "", "", false
	}
	return rel, filepath.Join(hm.rootDir, rel), true
}

// RootFH returns the root directory filehandle.
func (hm *HandleManager) RootFH() []byte {
	return hm.GetOrCreateFH("")
}

// Rename updates internal handle mappings when a path is renamed.
func (hm *HandleManager) Rename(oldRel, newRel string) {
	cleanOld := filepath.Clean(oldRel)
	cleanNew := filepath.Clean(newRel)
	if cleanOld == "." || cleanOld == "/" {
		cleanOld = ""
	}
	if cleanNew == "." || cleanNew == "/" {
		cleanNew = ""
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	fh, ok := hm.pathToFH[cleanOld]
	if ok {
		delete(hm.pathToFH, cleanOld)
		hm.pathToFH[cleanNew] = fh
		hm.fhToPath[string(fh)] = cleanNew
	}
}
