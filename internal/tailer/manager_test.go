package tailer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"log-enricher/internal/config"
	"log-enricher/internal/models"
	"log-enricher/internal/pipeline"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPipelineManager struct{}

func (m *stubPipelineManager) GetProcessPipeline(filePath string) pipeline.ProcessPipeline {
	return &stubProcessPipeline{}
}

type stubProcessPipeline struct{}

func (p *stubProcessPipeline) Process(entry *models.LogEntry) bool {
	return true
}

type stubBackend struct{}

func (b *stubBackend) Send(entry *models.LogEntry) error { return nil }
func (b *stubBackend) Shutdown()                         {}
func (b *stubBackend) Name() string                      { return "stub" }
func (b *stubBackend) CloseWriter(sourcePath string) {
	stubBackendMu.Lock()
	defer stubBackendMu.Unlock()
	stubBackendClosed = append(stubBackendClosed, sourcePath)
}

var (
	stubBackendMu     sync.Mutex
	stubBackendClosed []string
)

func newTestManager(t *testing.T, cfg *config.Config) *ManagerImpl {
	t.Helper()
	stubBackendMu.Lock()
	stubBackendClosed = nil
	stubBackendMu.Unlock()
	manager, err := NewManagerImpl(cfg, &stubPipelineManager{}, &stubBackend{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = manager.watcher.Close()
	})
	return manager
}

func TestNewManagerImpl_ValidatesAppIdentificationRegex(t *testing.T) {
	cfg := &config.Config{
		LogBasePath:       t.TempDir(),
		LogFileExtensions: []string{".log"},
	}

	cfg.AppIdentificationRegex = "("
	_, err := NewManagerImpl(cfg, &stubPipelineManager{}, &stubBackend{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid AppIdentificationRegex")

	cfg.AppIdentificationRegex = `(?P<service>[^/]+)`
	_, err = NewManagerImpl(cfg, &stubPipelineManager{}, &stubBackend{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "named capture group 'app'")
}

func TestManagerImpl_GetAppNameForPath(t *testing.T) {
	t.Run("prefers static app name", func(t *testing.T) {
		cfg := &config.Config{
			AppName:                "static-app",
			AppIdentificationRegex: `.*/(?P<app>[^/]+)/[^/]+\.log$`,
			LogBasePath:            t.TempDir(),
			LogFileExtensions:      []string{".log"},
		}

		manager := newTestManager(t, cfg)
		assert.Equal(t, "static-app", manager.getAppNameForPath("/var/log/nginx/access.log"))
	})

	t.Run("extracts app name from regex", func(t *testing.T) {
		cfg := &config.Config{
			AppIdentificationRegex: `.*/(?P<app>[^/]+)/[^/]+\.log$`,
			LogBasePath:            t.TempDir(),
			LogFileExtensions:      []string{".log"},
		}

		manager := newTestManager(t, cfg)
		assert.Equal(t, "nginx", manager.getAppNameForPath("/var/log/nginx/access.log"))
	})

	t.Run("falls back to directory name", func(t *testing.T) {
		cfg := &config.Config{
			LogBasePath:       t.TempDir(),
			LogFileExtensions: []string{".log"},
		}

		manager := newTestManager(t, cfg)
		assert.Equal(t, "service", manager.getAppNameForPath("/var/log/service/app.log"))
	})

	t.Run("uses default when directory is not usable", func(t *testing.T) {
		cfg := &config.Config{
			LogBasePath:       t.TempDir(),
			LogFileExtensions: []string{".log"},
		}

		manager := newTestManager(t, cfg)
		assert.Equal(t, "log-enricher", manager.getAppNameForPath("app.log"))
	})
}

func TestManagerImpl_GetMatchingLogFiles(t *testing.T) {
	tempDir := t.TempDir()

	require.NoError(t, osWriteFile(filepath.Join(tempDir, "app.log"), "a"))
	require.NoError(t, osWriteFile(filepath.Join(tempDir, "skip.txt"), "b"))
	require.NoError(t, osWriteFile(filepath.Join(tempDir, "sub", "worker.log"), "c"))
	require.NoError(t, osWriteFile(filepath.Join(tempDir, "sub", "other.json"), "d"))

	cfg := &config.Config{
		LogBasePath:       tempDir,
		LogFileExtensions: []string{".log", ".txt"},
	}

	manager := newTestManager(t, cfg)

	files, err := manager.getMatchingLogFiles(tempDir)
	require.NoError(t, err)

	for i := range files {
		files[i] = filepath.Clean(files[i])
	}
	slices.Sort(files)

	expected := []string{
		filepath.Clean(filepath.Join(tempDir, "app.log")),
		filepath.Clean(filepath.Join(tempDir, "skip.txt")),
		filepath.Clean(filepath.Join(tempDir, "sub", "worker.log")),
	}
	slices.Sort(expected)

	assert.Equal(t, expected, files)
}

func TestManagerImpl_StartTailingFile_IgnoresMatchingFiles(t *testing.T) {
	cfg := &config.Config{
		LogBasePath:       t.TempDir(),
		LogFileExtensions: []string{".log"},
		LogFilesIgnored:   `ignored\.log$`,
	}

	manager := newTestManager(t, cfg)
	manager.startTailingFile(context.Background(), filepath.Join(cfg.LogBasePath, "ignored.log"))

	manager.mu.Lock()
	defer manager.mu.Unlock()
	assert.Len(t, manager.tailedFiles, 0)
}

func TestManagerImpl_StopTailingFile_ClosesWriterOnce(t *testing.T) {
	cfg := &config.Config{
		LogBasePath:       t.TempDir(),
		LogFileExtensions: []string{".log"},
	}
	manager := newTestManager(t, cfg)
	path := filepath.Join(cfg.LogBasePath, "service.log")

	cancelCalled := 0
	manager.mu.Lock()
	manager.tailedFiles[path] = func() { cancelCalled++ }
	manager.mu.Unlock()

	manager.stopTailingFile(path)
	manager.stopTailingFile(path) // Idempotency check

	manager.mu.Lock()
	_, exists := manager.tailedFiles[path]
	manager.mu.Unlock()
	assert.False(t, exists)
	assert.Equal(t, 1, cancelCalled)

	stubBackendMu.Lock()
	defer stubBackendMu.Unlock()
	require.Len(t, stubBackendClosed, 1)
	assert.Equal(t, path, stubBackendClosed[0])
}

func osWriteFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
