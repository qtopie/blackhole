package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/qtopie/blackhole/internal/config"
)

type Photo struct {
	ID         string    `json:"id"`
	Filename   string    `json:"filename"`
	Path       string    `json:"path"`
	RelPath    string    `json:"rel_path"`
	Size       int64     `json:"size"`
	Hash       string    `json:"hash"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	MIMEType   string    `json:"mime_type"`
	MediaType  string    `json:"media_type"`
	IsVideo    bool      `json:"is_video"`
	CreatedAt  time.Time `json:"created_at"`
	TakenAt    time.Time `json:"taken_at"`
	IsFavorite bool      `json:"is_favorite"`
	IsDark     bool      `json:"is_dark"`
	Luminance  float64   `json:"luminance"`
	AlbumID    string    `json:"album_id"`
	Tags       []string  `json:"tags"`
}

type Album struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	CoverPhotoID string    `json:"cover_photo_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Store struct {
	cfg        *config.Config
	httpClient *http.Client

	mu     sync.RWMutex
	photos map[string]*Photo
	albums map[string]*Album
}

func NewStore(cfg *config.Config) *Store {
	return &Store{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		photos: make(map[string]*Photo),
		albums: make(map[string]*Album),
	}
}

// Dapr State API Helper
func (s *Store) saveDaprState(key string, value interface{}) error {
	url := fmt.Sprintf("http://%s:%s/v1.0/state/%s", s.cfg.DaprHost, s.cfg.DaprPort, s.cfg.DaprStateStore)
	bodyData := []map[string]interface{}{
		{
			"key":   key,
			"value": value,
		},
	}
	buf, err := json.Marshal(bodyData)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("dapr state error: %s", resp.Status)
}

// SurrealDB SQL Helper
func (s *Store) querySurrealDB(sqlQuery string) ([]byte, error) {
	url := fmt.Sprintf("%s/sql", s.cfg.SurrealURL)
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(sqlQuery))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.cfg.SurrealUser, s.cfg.SurrealPass)
	req.Header.Set("surreal-ns", s.cfg.SurrealNS)
	req.Header.Set("surreal-db", s.cfg.SurrealDB)
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return out, nil
	}
	return nil, fmt.Errorf("surrealdb error (%d): %s", resp.StatusCode, string(out))
}

func (s *Store) InitDB() {
	initSQL := fmt.Sprintf("DEFINE NAMESPACE IF NOT EXISTS %s; USE NS %s; DEFINE DATABASE IF NOT EXISTS %s; USE DB %s;",
		s.cfg.SurrealNS, s.cfg.SurrealNS, s.cfg.SurrealDB, s.cfg.SurrealDB)
	_, _ = s.querySurrealDB(initSQL)
}

func (s *Store) SavePhoto(photo *Photo) error {
	s.mu.Lock()
	s.photos[photo.ID] = photo
	s.mu.Unlock()

	// Try Dapr State Store
	_ = s.saveDaprState("photo:"+photo.ID, photo)

	// Try SurrealDB SQL
	photoJSON, err := json.Marshal(photo)
	if err == nil {
		sql := fmt.Sprintf("USE NS %s; USE DB %s; UPSERT photo:`%s` CONTENT %s;",
			s.cfg.SurrealNS, s.cfg.SurrealDB, photo.ID, string(photoJSON))
		_, _ = s.querySurrealDB(sql)
	}

	return nil
}

func (s *Store) GetPhoto(id string) (*Photo, error) {
	s.mu.RLock()
	p, ok := s.photos[id]
	s.mu.RUnlock()
	if ok {
		return p, nil
	}
	return nil, fmt.Errorf("photo not found")
}

func (s *Store) DeletePhotos(ids []string) error {
	s.mu.Lock()
	for _, id := range ids {
		delete(s.photos, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		sql := fmt.Sprintf("USE NS %s; USE DB %s; DELETE photo:`%s`;", s.cfg.SurrealNS, s.cfg.SurrealDB, id)
		_, _ = s.querySurrealDB(sql)
	}
	return nil
}

func (s *Store) ListPhotos() ([]*Photo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Photo, 0, len(s.photos))
	for _, p := range s.photos {
		list = append(list, p)
	}
	return list, nil
}

func (s *Store) FindPhotoByHashOrSize(hash string, size int64) *Photo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.photos {
		if hash != "" && p.Hash == hash {
			return p
		}
		if size > 0 && p.Size == size {
			return p
		}
	}
	return nil
}

func (s *Store) DeletePhoto(id string) error {
	s.mu.Lock()
	delete(s.photos, id)
	s.mu.Unlock()

	// Try SurrealDB delete
	sql := fmt.Sprintf("USE NS %s; USE DB %s; DELETE photo:`%s`;", s.cfg.SurrealNS, s.cfg.SurrealDB, id)
	_, _ = s.querySurrealDB(sql)

	return nil
}

func (s *Store) SaveAlbum(album *Album) error {
	s.mu.Lock()
	s.albums[album.ID] = album
	s.mu.Unlock()

	_ = s.saveDaprState("album:"+album.ID, album)
	return nil
}

func (s *Store) ListAlbums() ([]*Album, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Album, 0, len(s.albums))
	for _, a := range s.albums {
		list = append(list, a)
	}
	return list, nil
}
