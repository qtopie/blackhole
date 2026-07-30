package config

import "os"

type Config struct {
	ShareDir string
	Port     string
	Username string
	Password string

	EnableSMB bool
	SMBPort   string

	EnableNFS bool
	NFSPort   string
}

func LoadConfig() *Config {
	shareDir := os.Getenv("BLACKHOLE_SHARE_DIR")
	if shareDir == "" {
		shareDir = "./nas_share"
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

	return &Config{
		ShareDir:  shareDir,
		Port:      port,
		Username:  username,
		Password:  password,
		EnableSMB: enableSMB,
		SMBPort:   smbPort,
		EnableNFS: enableNFS,
		NFSPort:   nfsPort,
	}
}
