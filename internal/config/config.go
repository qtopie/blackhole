package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	ShareDir string
	AlbumDir string
	Port     string
	Username string
	Password string

	EnableSMB bool
	SMBPort   string

	EnableNFS bool
	NFSPort   string

	DaprHost       string
	DaprPort       string
	DaprStateStore string

	SurrealURL  string
	SurrealUser string
	SurrealPass string
	SurrealNS   string
	SurrealDB   string
}

func LoadConfig() *Config {
	shareDir := os.Getenv("BLACKHOLE_SHARE_DIR")
	if shareDir == "" {
		shareDir = "./nas_share"
	}

	albumDir := os.Getenv("BLACKHOLE_ALBUM_DIR")
	if albumDir == "" {
		albumDir = filepath.Join(shareDir, "photos")
	}

	port := os.Getenv("BLACKHOLE_PORT")
	if port == "" {
		port = "50056"
	}
	username := os.Getenv("BLACKHOLE_USER")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("BLACKHOLE_PASS")
	if password == "" {
		password = "blackhole"
	}

	enableSMB := os.Getenv("BLACKHOLE_ENABLE_SMB") != "false"
	smbPort := os.Getenv("BLACKHOLE_SMB_PORT")
	if smbPort == "" {
		smbPort = "445"
	}

	enableNFS := os.Getenv("BLACKHOLE_ENABLE_NFS") != "false"
	nfsPort := os.Getenv("BLACKHOLE_NFS_PORT")
	if nfsPort == "" {
		nfsPort = "2049"
	}

	daprHost := os.Getenv("DAPR_HOST")
	if daprHost == "" {
		daprHost = "127.0.0.1"
	}
	daprPort := os.Getenv("DAPR_HTTP_PORT")
	if daprPort == "" {
		daprPort = "3500"
	}
	daprStateStore := os.Getenv("DAPR_STATE_STORE")
	if daprStateStore == "" {
		daprStateStore = "statestore"
	}

	surrealURL := os.Getenv("SURREALDB_URL")
	if surrealURL == "" {
		surrealURL = "http://127.0.0.1:38000"
	}
	surrealUser := os.Getenv("SURREALDB_USER")
	if surrealUser == "" {
		surrealUser = "root"
	}
	surrealPass := os.Getenv("SURREALDB_PASS")
	if surrealPass == "" {
		surrealPass = "root"
	}
	surrealNS := os.Getenv("SURREALDB_NS")
	if surrealNS == "" {
		surrealNS = "blackhole"
	}
	surrealDB := os.Getenv("SURREALDB_DB")
	if surrealDB == "" {
		surrealDB = "blackhole"
	}

	return &Config{
		ShareDir:       shareDir,
		AlbumDir:       albumDir,
		Port:           port,
		Username:       username,
		Password:       password,
		EnableSMB:      enableSMB,
		SMBPort:        smbPort,
		EnableNFS:      enableNFS,
		NFSPort:        nfsPort,
		DaprHost:       daprHost,
		DaprPort:       daprPort,
		DaprStateStore: daprStateStore,
		SurrealURL:     surrealURL,
		SurrealUser:    surrealUser,
		SurrealPass:    surrealPass,
		SurrealNS:      surrealNS,
		SurrealDB:      surrealDB,
	}
}
