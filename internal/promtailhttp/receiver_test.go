package promtailhttp

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"log-enricher/internal/config"
	"log-enricher/internal/models"
	"log-enricher/internal/pipeline"

	"github.com/goccy/go-json"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/grafana/loki/pkg/push"
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

type captureBackend struct {
	mu      sync.Mutex
	entries []*models.LogEntry
	err     error
}

func (b *captureBackend) Send(entry *models.LogEntry) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	fields := make(map[string]interface{}, len(entry.Fields))
	for k, v := range entry.Fields {
		fields[k] = v
	}
	b.entries = append(b.entries, &models.LogEntry{
		Fields:     fields,
		LogLine:    append([]byte(nil), entry.LogLine...),
		Timestamp:  entry.Timestamp,
		SourcePath: entry.SourcePath,
		App:        entry.App,
	})
	return b.err
}

func (b *captureBackend) Shutdown()                     {}
func (b *captureBackend) Name() string                  { return "capture" }
func (b *captureBackend) CloseWriter(sourcePath string) {}

func (b *captureBackend) snapshot() []*models.LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*models.LogEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

func newTestReceiver(t *testing.T, cfg *config.Config, backend *captureBackend) (*Receiver, *httptest.Server) {
	t.Helper()
	r, err := NewReceiver(cfg, &stubPipelineManager{}, backend)
	require.NoError(t, err)
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	return r, srv
}

func TestReceiver_ProtobufSnappyAndPathSanitization(t *testing.T) {
	sourceRoot := t.TempDir()
	cfg := &config.Config{
		AppName:                  "",
		PromtailHTTPAddr:         "127.0.0.1:0",
		PromtailHTTPMaxBodyBytes: 1024 * 1024,
		PromtailHTTPSourceRoot:   sourceRoot,
	}
	backend := &captureBackend{}
	_, srv := newTestReceiver(t, cfg, backend)

	ts := time.Date(2026, time.January, 2, 3, 4, 5, 6, time.UTC)
	reqBody := buildProtobufBody(t, push.PushRequest{
		Streams: []push.Stream{
			{
				Labels: `{app="orders-api",filename="../../etc/passwd"}`,
				Entries: []push.Entry{
					{Timestamp: ts, Line: "hello"},
				},
			},
		},
	})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", protobufContentType)

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	entries := backend.snapshot()
	require.Len(t, entries, 1)
	assert.Equal(t, "orders-api", entries[0].App)
	assert.Equal(t, "hello", string(entries[0].LogLine))
	assert.Equal(t, ts, entries[0].Timestamp)
	assert.Equal(t, filepath.Join(sourceRoot, "etc", "passwd"), entries[0].SourcePath)
}

func TestReceiver_ProtobufSnappyGzipOnLegacyRoute(t *testing.T) {
	cfg := &config.Config{
		AppName:                  "",
		PromtailHTTPAddr:         "127.0.0.1:0",
		PromtailHTTPMaxBodyBytes: 1024 * 1024,
		PromtailHTTPSourceRoot:   t.TempDir(),
	}
	backend := &captureBackend{}
	_, srv := newTestReceiver(t, cfg, backend)

	ts := time.Date(2026, time.March, 5, 6, 7, 8, 9, time.UTC)
	raw := buildProtobufBody(t, push.PushRequest{
		Streams: []push.Stream{
			{
				Labels: `{job="worker",filename="/var/log/worker.log"}`,
				Entries: []push.Entry{
					{Timestamp: ts, Line: "line-from-gzip"},
				},
			},
		},
	})

	compressed := gzipBytes(t, raw)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/prom/push", bytes.NewReader(compressed))
	require.NoError(t, err)
	req.Header.Set("Content-Type", protobufContentType)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	entries := backend.snapshot()
	require.Len(t, entries, 1)
	assert.Equal(t, "worker", entries[0].App)
	assert.Equal(t, "line-from-gzip", string(entries[0].LogLine))
	assert.Equal(t, ts, entries[0].Timestamp)
}

func TestReceiver_JSONValuesAndGzip(t *testing.T) {
	sourceRoot := t.TempDir()
	cfg := &config.Config{
		AppName:                  "",
		PromtailHTTPAddr:         "127.0.0.1:0",
		PromtailHTTPMaxBodyBytes: 1024 * 1024,
		PromtailHTTPSourceRoot:   sourceRoot,
	}
	backend := &captureBackend{}
	_, srv := newTestReceiver(t, cfg, backend)

	ts := time.Date(2026, time.April, 10, 11, 12, 13, 14, time.UTC)
	payload := map[string]any{
		"streams": []any{
			map[string]any{
				"stream": map[string]string{
					"service":  "payments",
					"filename": `C:\logs\payments\app.log`,
				},
				"values": [][]any{
					{strconvFormatInt(ts.UnixNano()), "json-line"},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", bytes.NewReader(gzipBytes(t, body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", jsonContentType)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	entries := backend.snapshot()
	require.Len(t, entries, 1)
	assert.Equal(t, "payments", entries[0].App)
	assert.Equal(t, "json-line", string(entries[0].LogLine))
	assert.Equal(t, ts.UTC(), entries[0].Timestamp)
	assert.Equal(t, filepath.Join(sourceRoot, "logs", "payments", "app.log"), entries[0].SourcePath)
}

func TestReceiver_RejectsMalformedJSONBatchWithoutProcessing(t *testing.T) {
	cfg := &config.Config{
		AppName:                  "",
		PromtailHTTPAddr:         "127.0.0.1:0",
		PromtailHTTPMaxBodyBytes: 1024 * 1024,
		PromtailHTTPSourceRoot:   t.TempDir(),
	}
	backend := &captureBackend{}
	_, srv := newTestReceiver(t, cfg, backend)

	payload := `{"streams":[{"stream":{"app":"orders"},"values":[["1735689600000000000","ok"],["not-a-ts","bad"]]}]}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", strings.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", jsonContentType)

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, backend.snapshot())
}

func TestReceiver_ValidationAndAuthResponses(t *testing.T) {
	cfg := &config.Config{
		AppName:                  "default-app",
		PromtailHTTPAddr:         "127.0.0.1:0",
		PromtailHTTPMaxBodyBytes: 16,
		PromtailHTTPBearerToken:  "secret",
		PromtailHTTPSourceRoot:   t.TempDir(),
	}
	backend := &captureBackend{}
	_, srv := newTestReceiver(t, cfg, backend)

	t.Run("method not allowed", func(t *testing.T) {
		resp, err := srv.Client().Get(srv.URL + "/loki/api/v1/push")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	t.Run("unauthorized", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", jsonContentType)

		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("unsupported content type", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", strings.NewReader("x"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "text/plain")

		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	})

	t.Run("request too large", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", strings.NewReader(`{"streams":[{"values":[["1","xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"]]}]}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", jsonContentType)

		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	})
}

func TestReceiver_ReadyEndpoint(t *testing.T) {
	cfg := &config.Config{
		AppName:                  "",
		PromtailHTTPAddr:         "127.0.0.1:0",
		PromtailHTTPMaxBodyBytes: 1024 * 1024,
		PromtailHTTPSourceRoot:   t.TempDir(),
	}
	backend := &captureBackend{}
	_, srv := newTestReceiver(t, cfg, backend)

	resp, err := srv.Client().Get(srv.URL + "/ready")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func buildProtobufBody(t *testing.T, req push.PushRequest) []byte {
	t.Helper()
	raw, err := proto.Marshal(&req)
	require.NoError(t, err)
	return snappy.Encode(nil, raw)
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func strconvFormatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
