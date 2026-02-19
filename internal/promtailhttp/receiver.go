package promtailhttp

import (
	"compress/gzip"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log-enricher/internal/backends"
	"log-enricher/internal/config"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/processor"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/grafana/loki/pkg/push"
	"github.com/prometheus/prometheus/promql/parser"
)

const (
	protobufContentType = "application/x-protobuf"
	jsonContentType     = "application/json"
	fallbackAppName     = "promtail"
	defaultFallbackFile = "stream.log"
)

type Receiver struct {
	pm          pipeline.Manager
	backend     backends.Backend
	defaultApp  string
	sourceRoot  string
	maxBodySize int64
	bearerToken string
	server      *http.Server
}

type normalizedEntry struct {
	app       string
	source    string
	timestamp time.Time
	line      []byte
}

type jsonPushRequest struct {
	Streams []jsonPushStream `json:"streams"`
}

type jsonPushStream struct {
	Stream  map[string]string   `json:"stream"`
	Labels  string              `json:"labels"`
	Values  [][]json.RawMessage `json:"values"`
	Entries []jsonPushEntry     `json:"entries"`
}

type jsonPushEntry struct {
	TS        json.RawMessage `json:"ts"`
	Timestamp json.RawMessage `json:"timestamp"`
	Line      string          `json:"line"`
}

func NewReceiver(cfg *config.Config, pm pipeline.Manager, backend backends.Backend) (*Receiver, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if pm == nil {
		return nil, fmt.Errorf("pipeline manager is required")
	}
	if backend == nil {
		return nil, fmt.Errorf("backend is required")
	}

	maxBodySize := int64(cfg.PromtailHTTPMaxBodyBytes)
	if maxBodySize <= 0 {
		maxBodySize = 10 * 1024 * 1024
	}

	sourceRoot := strings.TrimSpace(cfg.PromtailHTTPSourceRoot)
	if sourceRoot == "" {
		sourceRoot = "/cache/promtail"
	}

	r := &Receiver{
		pm:          pm,
		backend:     backend,
		defaultApp:  strings.TrimSpace(cfg.AppName),
		sourceRoot:  filepath.Clean(sourceRoot),
		maxBodySize: maxBodySize,
		bearerToken: strings.TrimSpace(cfg.PromtailHTTPBearerToken),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/push", r.handlePush)
	mux.HandleFunc("/api/prom/push", r.handlePush)
	mux.HandleFunc("/ready", r.handleReady)

	r.server = &http.Server{
		Addr:              cfg.PromtailHTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return r, nil
}

func (r *Receiver) Start() error {
	listener, err := net.Listen("tcp", r.server.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen for Promtail HTTP receiver on %s: %w", r.server.Addr, err)
	}

	r.server.Addr = listener.Addr().String()
	slog.Info("Promtail HTTP receiver enabled", "addr", r.server.Addr)

	go func() {
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Promtail HTTP receiver failed", "error", err)
		}
	}()

	return nil
}

func (r *Receiver) Shutdown(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}

func (r *Receiver) Addr() string {
	return r.server.Addr
}

func (r *Receiver) Handler() http.Handler {
	return r.server.Handler
}

func (r *Receiver) handleReady(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (r *Receiver) handlePush(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !r.isAuthorized(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	entries, status, err := r.parseRequest(w, req)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	if err := r.processEntries(entries); err != nil {
		slog.Error("Failed to process Promtail request", "error", err)
		http.Error(w, "failed to process logs", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (r *Receiver) parseRequest(w http.ResponseWriter, req *http.Request) ([]normalizedEntry, int, error) {
	contentType := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, http.StatusUnsupportedMediaType, fmt.Errorf("unsupported content type: %s", contentType)
	}

	body, status, err := r.readBody(w, req)
	if err != nil {
		return nil, status, err
	}

	switch mediaType {
	case protobufContentType:
		return r.parseProtobuf(body)
	case jsonContentType:
		return r.parseJSON(body)
	default:
		return nil, http.StatusUnsupportedMediaType, fmt.Errorf("unsupported content type: %s", mediaType)
	}
}

func (r *Receiver) readBody(w http.ResponseWriter, req *http.Request) ([]byte, int, error) {
	limited := http.MaxBytesReader(w, req.Body, r.maxBodySize)
	defer limited.Close()

	reader := io.Reader(limited)
	encoding := strings.TrimSpace(strings.ToLower(req.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
		// no-op
	case "gzip":
		gzr, err := gzip.NewReader(limited)
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid gzip payload: %w", err)
		}
		defer gzr.Close()
		reader = gzr
	default:
		return nil, http.StatusUnsupportedMediaType, fmt.Errorf("unsupported content encoding: %s", encoding)
	}

	body, err := io.ReadAll(io.LimitReader(reader, r.maxBodySize+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "http: request body too large") {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("payload too large")
		}
		return nil, http.StatusBadRequest, fmt.Errorf("failed to read request body: %w", err)
	}

	if int64(len(body)) > r.maxBodySize {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("payload too large")
	}

	return body, http.StatusOK, nil
}

func (r *Receiver) parseProtobuf(body []byte) ([]normalizedEntry, int, error) {
	decoded, err := snappy.Decode(nil, body)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid snappy payload: %w", err)
	}
	if int64(len(decoded)) > r.maxBodySize {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("payload too large")
	}

	var req push.PushRequest
	if err := proto.Unmarshal(decoded, &req); err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed to unmarshal protobuf payload: %w", err)
	}

	entries, err := r.normalizeProtoEntries(req.Streams)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	return entries, http.StatusOK, nil
}

func (r *Receiver) parseJSON(body []byte) ([]normalizedEntry, int, error) {
	var req jsonPushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed to unmarshal JSON payload: %w", err)
	}

	entries, err := r.normalizeJSONEntries(req.Streams)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	return entries, http.StatusOK, nil
}

func (r *Receiver) normalizeProtoEntries(streams []push.Stream) ([]normalizedEntry, error) {
	entries := make([]normalizedEntry, 0, len(streams))

	for streamIdx, stream := range streams {
		labels, err := parseLabelString(stream.Labels)
		if err != nil {
			return nil, fmt.Errorf("invalid labels for stream %d: %w", streamIdx, err)
		}

		app := r.deriveAppName(labels)
		source := sanitizeSourcePath(r.sourceRoot, deriveSourcePath(labels), fmt.Sprintf("stream-%d.log", streamIdx))
		for _, entry := range stream.Entries {
			if entry.Line == "" {
				// Empty lines are valid, but they should still pass through the normal pipeline.
			}
			entries = append(entries, normalizedEntry{
				app:       app,
				source:    source,
				timestamp: entry.Timestamp,
				line:      []byte(entry.Line),
			})
		}
	}

	return entries, nil
}

func (r *Receiver) normalizeJSONEntries(streams []jsonPushStream) ([]normalizedEntry, error) {
	entries := make([]normalizedEntry, 0, len(streams))

	for streamIdx, stream := range streams {
		labels := cloneLabels(stream.Stream)
		if len(labels) == 0 {
			parsed, err := parseLabelString(stream.Labels)
			if err != nil {
				return nil, fmt.Errorf("invalid labels for stream %d: %w", streamIdx, err)
			}
			labels = parsed
		}

		app := r.deriveAppName(labels)
		source := sanitizeSourcePath(r.sourceRoot, deriveSourcePath(labels), fmt.Sprintf("stream-%d.log", streamIdx))

		for tupleIdx, tuple := range stream.Values {
			if len(tuple) < 2 {
				return nil, fmt.Errorf("stream %d values[%d] must contain at least timestamp and line", streamIdx, tupleIdx)
			}

			ts, err := parseTimestampRaw(tuple[0])
			if err != nil {
				return nil, fmt.Errorf("invalid timestamp at stream %d values[%d]: %w", streamIdx, tupleIdx, err)
			}

			line, err := parseLineRaw(tuple[1])
			if err != nil {
				return nil, fmt.Errorf("invalid line at stream %d values[%d]: %w", streamIdx, tupleIdx, err)
			}

			entries = append(entries, normalizedEntry{
				app:       app,
				source:    source,
				timestamp: ts,
				line:      []byte(line),
			})
		}

		for entryIdx, entry := range stream.Entries {
			rawTS := entry.TS
			if len(rawTS) == 0 {
				rawTS = entry.Timestamp
			}
			if len(rawTS) == 0 {
				return nil, fmt.Errorf("missing timestamp at stream %d entries[%d]", streamIdx, entryIdx)
			}

			ts, err := parseTimestampRaw(rawTS)
			if err != nil {
				return nil, fmt.Errorf("invalid timestamp at stream %d entries[%d]: %w", streamIdx, entryIdx, err)
			}

			entries = append(entries, normalizedEntry{
				app:       app,
				source:    source,
				timestamp: ts,
				line:      []byte(entry.Line),
			})
		}
	}

	return entries, nil
}

func (r *Receiver) processEntries(entries []normalizedEntry) error {
	processors := make(map[string]*processor.LogProcessorImpl, len(entries))

	for _, entry := range entries {
		key := entry.app + "\x00" + entry.source
		lp, ok := processors[key]
		if !ok {
			lp = processor.NewLogProcessor(entry.app, entry.source, r.pm.GetProcessPipeline(entry.source), r.backend)
			processors[key] = lp
		}

		if err := lp.ProcessLineWithTimestamp(entry.line, entry.timestamp); err != nil {
			return err
		}
	}

	return nil
}

func (r *Receiver) deriveAppName(labels map[string]string) string {
	for _, key := range []string{"app", "service", "job"} {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v
		}
	}

	if r.defaultApp != "" {
		return r.defaultApp
	}
	return fallbackAppName
}

func deriveSourcePath(labels map[string]string) string {
	for _, key := range []string{"filename", "__path__", "path", "source"} {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v
		}
	}
	return ""
}

func parseLabelString(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return map[string]string{}, nil
	}

	parsed, err := parser.ParseMetric(value)
	if err != nil {
		return nil, err
	}

	labels := make(map[string]string, len(parsed))
	for _, label := range parsed {
		labels[string(label.Name)] = string(label.Value)
	}

	return labels, nil
}

func parseTimestampRaw(raw json.RawMessage) (time.Time, error) {
	var timestampString string
	if err := json.Unmarshal(raw, &timestampString); err == nil {
		return parseTimestampString(timestampString)
	}

	var timestampInt int64
	if err := json.Unmarshal(raw, &timestampInt); err == nil {
		return time.Unix(0, timestampInt).UTC(), nil
	}

	return time.Time{}, fmt.Errorf("timestamp must be a JSON string or integer")
}

func parseTimestampString(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("timestamp is empty")
	}

	if ns, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(0, ns).UTC(), nil
	}

	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp format")
}

func parseLineRaw(raw json.RawMessage) (string, error) {
	var line string
	if err := json.Unmarshal(raw, &line); err != nil {
		return "", fmt.Errorf("line must be a string")
	}
	return line, nil
}

func sanitizeSourcePath(root, candidate, fallback string) string {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanRoot == "" {
		cleanRoot = "."
	}

	safeFallback := sanitizeRelativePath(fallback)
	if safeFallback == "" {
		safeFallback = defaultFallbackFile
	}

	relativePath := sanitizeRelativePath(candidate)
	if relativePath == "" {
		relativePath = safeFallback
	}

	candidatePath := filepath.Clean(filepath.Join(cleanRoot, relativePath))
	if isPathEscapingRoot(cleanRoot, candidatePath) {
		return filepath.Join(cleanRoot, safeFallback)
	}

	return candidatePath
}

func sanitizeRelativePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	value = strings.ReplaceAll(value, "\\", "/")
	if len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' {
		value = value[2:]
	}
	value = strings.TrimLeft(value, "/")

	parts := strings.Split(value, "/")
	safeParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		safeParts = append(safeParts, part)
	}

	if len(safeParts) == 0 {
		return ""
	}
	return filepath.Join(safeParts...)
}

func isPathEscapingRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)

	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return true
	}
	if rel == ".." {
		return true
	}
	return strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(labels))
	for k, v := range labels {
		result[k] = v
	}
	return result
}

func (r *Receiver) isAuthorized(req *http.Request) bool {
	if r.bearerToken == "" {
		return true
	}

	authHeader := req.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(token), []byte(r.bearerToken)) == 1
}
