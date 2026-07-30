package downloader

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DownloadTask 记录后台下载任务状态
type DownloadTask struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	FileName  string    `json:"file_name"`
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Manager struct {
	tasksMu  sync.RWMutex
	tasks    map[string]*DownloadTask
	shareDir string
}

func NewManager(shareDir string) *Manager {
	return &Manager{
		tasks:    make(map[string]*DownloadTask),
		shareDir: shareDir,
	}
}

func (m *Manager) CreateTask(url, fileName string, useOget bool) *DownloadTask {
	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
	mode := "standard"
	if useOget {
		mode = "oget"
	}

	task := &DownloadTask{
		ID:        taskID,
		URL:       url,
		FileName:  fileName,
		Mode:      mode,
		Status:    "downloading",
		CreatedAt: time.Now(),
	}

	m.tasksMu.Lock()
	m.tasks[taskID] = task
	m.tasksMu.Unlock()

	go m.startBackgroundDownload(task)

	return task
}

func (m *Manager) GetTask(taskID string) (*DownloadTask, bool) {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	task, ok := m.tasks[taskID]
	return task, ok
}

func (m *Manager) GetAllTasks() map[string]*DownloadTask {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	
	result := make(map[string]*DownloadTask, len(m.tasks))
	for k, v := range m.tasks {
		result[k] = v
	}
	return result
}

func (m *Manager) startBackgroundDownload(task *DownloadTask) {
	savePath := m.shareDir
	if task.FileName != "" {
		savePath = filepath.Join(m.shareDir, task.FileName)
	}

	if task.Mode == "oget" {
		ogetBin, err := exec.LookPath("oget")
		if err != nil {
			ogetBin = "/opt/blackhole/oget"
		}

		args := []string{}
		if task.FileName != "" {
			args = append(args, "-file", savePath)
		}
		args = append(args, task.URL)

		cmd := exec.Command(ogetBin, args...)
		cmd.Dir = m.shareDir
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf

		if err := cmd.Run(); err != nil {
			m.tasksMu.Lock()
			task.Status = "failed"
			task.Error = fmt.Sprintf("oget error: %v, stderr: %s", err, errBuf.String())
			m.tasksMu.Unlock()
			return
		}
	} else {
		resp, err := http.Get(task.URL)
		if err != nil {
			m.tasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			m.tasksMu.Unlock()
			return
		}
		defer resp.Body.Close()

		if task.FileName == "" {
			parts := strings.Split(task.URL, "/")
			savePath = filepath.Join(m.shareDir, parts[len(parts)-1])
		}

		out, err := os.Create(savePath)
		if err != nil {
			m.tasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			m.tasksMu.Unlock()
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, resp.Body); err != nil {
			m.tasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			m.tasksMu.Unlock()
			return
		}
	}

	m.tasksMu.Lock()
	task.Status = "completed"
	m.tasksMu.Unlock()
}
