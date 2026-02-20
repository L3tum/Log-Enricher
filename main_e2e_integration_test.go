//go:build integration
// +build integration

package main

import (
	"context"
	"fmt"
	"io"
	"log-enricher/internal/config"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type e2eExpectedEntry struct {
	msg       string
	app       string
	clientIP  string
	country   string
	timestamp time.Time
}

type lokiObservedEntry struct {
	streamApp string
	fields    map[string]any
	timestamp time.Time
}

type lokiQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func TestE2E_PromtailTailerLokiStreaming(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancelCtx := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancelCtx()

	tempRoot := t.TempDir()
	directDir := filepath.Join(tempRoot, "direct")
	promtailSourceRoot := filepath.Join(tempRoot, "promtail-source-root")
	statePath := filepath.Join(tempRoot, "state.json")
	geoIPPath := filepath.Join(tempRoot, "geoip-test.mmdb")
	promtailConfigPath := filepath.Join(tempRoot, "promtail-config.yml")

	require.NoError(t, os.MkdirAll(directDir, 0o777))
	require.NoError(t, os.MkdirAll(promtailSourceRoot, 0o777))
	require.NoError(t, writeGeoIPTestDB(geoIPPath))

	directFileOne := filepath.Join(directDir, "direct-a.log")
	directFileTwo := filepath.Join(directDir, "direct-b.log")
	promtailFileOne := "/var/log/promtail/promtail-a.log"
	promtailFileTwo := "/var/log/promtail/promtail-b.log"
	for _, path := range []string{directFileOne, directFileTwo} {
		require.NoError(t, os.WriteFile(path, nil, 0o644))
	}

	lokiContainer, err := testcontainers.Run(
		ctx,
		"grafana/loki:2.9.8",
		testcontainers.WithExposedPorts("3100/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/ready").
				WithPort("3100/tcp").
				WithStartupTimeout(2*time.Minute),
		),
	)
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, lokiContainer)

	lokiHost, err := lokiContainer.Host(ctx)
	require.NoError(t, err)
	lokiPort, err := lokiContainer.MappedPort(ctx, "3100/tcp")
	require.NoError(t, err)
	lokiBaseURL := "http://" + net.JoinHostPort(lokiHost, lokiPort.Port())

	receiverPort := getFreeTCPPort(t)
	receiverBindAddr := net.JoinHostPort("0.0.0.0", strconv.Itoa(receiverPort))
	receiverReadyAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(receiverPort))
	appCfg := &config.Config{
		LogLevel:                 "ERROR",
		Backend:                  "loki",
		LokiURL:                  lokiBaseURL,
		StateFilePath:            statePath,
		LogBasePath:              directDir,
		LogFileExtensions:        []string{".log"},
		AppName:                  "tailer-app",
		PromtailHTTPEnabled:      true,
		PromtailHTTPAddr:         receiverBindAddr,
		PromtailHTTPMaxBodyBytes: 10 * 1024 * 1024,
		PromtailHTTPSourceRoot:   promtailSourceRoot,
		Stages: []config.StageConfig{
			{Type: "json_parser"},
			{
				Type: "client_ip_extraction",
				Params: map[string]any{
					"client_ip_fields": "ip,remote_addr",
					"target_field":     "client_ip",
				},
			},
			{
				Type: "timestamp_extraction",
				Params: map[string]any{
					"timestamp_fields": "ts",
				},
			},
			{
				Type: "hostname_enrichment",
				Params: map[string]any{
					"enable_rdns":    false,
					"enable_mdns":    false,
					"enable_llmnr":   false,
					"enable_netbios": false,
				},
			},
			{
				Type: "geoip_enrichment",
				Params: map[string]any{
					"geoip_database_path":  geoIPPath,
					"client_ip_field":      "client_ip",
					"client_country_field": "client_country",
				},
			},
		},
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	done := make(chan error, 1)
	go func() {
		done <- runApplication(appCtx, appCfg)
	}()

	receiverReadyURL := "http://" + receiverReadyAddr + "/ready"
	require.Eventually(t, func() bool {
		resp, reqErr := http.Get(receiverReadyURL)
		if reqErr != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)

	require.NoError(t, writePromtailConfig(promtailConfigPath, receiverPort))

	promtailContainer, err := testcontainers.Run(
		ctx,
		"grafana/promtail:2.9.8",
		testcontainers.WithCmd("-config.file=/etc/promtail/config.yml"),
		testcontainers.WithExposedPorts("9080/tcp"),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      promtailConfigPath,
			ContainerFilePath: "/etc/promtail/config.yml",
			FileMode:          0o644,
		}),
		testcontainers.WithHostPortAccess(receiverPort),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9080/tcp").WithStartupTimeout(90*time.Second),
		),
	)
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, promtailContainer)

	require.NoError(t, ensureContainerLogFiles(ctx, promtailContainer, promtailFileOne, promtailFileTwo))

	// Give tailer and Promtail a short moment to settle before writes.
	time.Sleep(1 * time.Second)

	runID := fmt.Sprintf("e2e-run-%d", time.Now().UnixNano())
	baseTS := time.Now().UTC().Truncate(time.Second)

	expectedByMsg := map[string]e2eExpectedEntry{}
	batchOneMsgs := []string{}
	batchTwoMsgs := []string{}

	addExpected := func(writeFn func(string, map[string]any) error, filePath string, offsetSec int, msg string, app string, ipField string, ipValue string, normalizedIP string, country string) {
		ts := baseTS.Add(time.Duration(offsetSec) * time.Second)
		payload := map[string]any{
			"ts":      ts.Format(time.RFC3339Nano),
			"msg":     msg,
			"e2e_run": runID,
		}
		payload[ipField] = ipValue
		require.NoError(t, writeFn(filePath, payload))
		expectedByMsg[msg] = e2eExpectedEntry{
			msg:       msg,
			app:       app,
			clientIP:  normalizedIP,
			country:   country,
			timestamp: ts,
		}
	}

	addExpected(appendJSONLine, directFileOne, 1, "direct-a-batch-1", "tailer-app", "ip", "1.1.1.5", "1.1.1.5", "AU")
	addExpected(appendJSONLine, directFileTwo, 2, "direct-b-batch-1", "tailer-app", "remote_addr", "8.8.8.8:443", "8.8.8.8", "US")
	addExpected(func(path string, payload map[string]any) error {
		return appendJSONLineInContainer(ctx, promtailContainer, path, payload)
	}, promtailFileOne, 3, "promtail-a-batch-1", "promtail-app", "ip", "1.1.1.9", "1.1.1.9", "AU")
	addExpected(func(path string, payload map[string]any) error {
		return appendJSONLineInContainer(ctx, promtailContainer, path, payload)
	}, promtailFileTwo, 4, "promtail-b-batch-1", "promtail-app", "remote_addr", "8.8.8.8:8443", "8.8.8.8", "US")
	batchOneMsgs = append(batchOneMsgs, "direct-a-batch-1", "direct-b-batch-1", "promtail-a-batch-1", "promtail-b-batch-1")

	queryStart := baseTS.Add(-2 * time.Minute)

	require.Eventually(t, func() bool {
		observed, _, fetchErr := fetchObservedEntries(ctx, lokiBaseURL, runID, queryStart, time.Now().UTC().Add(2*time.Minute))
		if fetchErr != nil {
			return false
		}
		return containsAllMessages(observed, batchOneMsgs)
	}, 90*time.Second, 1*time.Second, "batch 1 logs did not arrive in Loki in time")

	addExpected(appendJSONLine, directFileOne, 5, "direct-a-batch-2", "tailer-app", "ip", "1.1.1.11", "1.1.1.11", "AU")
	addExpected(appendJSONLine, directFileTwo, 6, "direct-b-batch-2", "tailer-app", "remote_addr", "8.8.8.8:9443", "8.8.8.8", "US")
	addExpected(func(path string, payload map[string]any) error {
		return appendJSONLineInContainer(ctx, promtailContainer, path, payload)
	}, promtailFileOne, 7, "promtail-a-batch-2", "promtail-app", "ip", "1.1.1.19", "1.1.1.19", "AU")
	addExpected(func(path string, payload map[string]any) error {
		return appendJSONLineInContainer(ctx, promtailContainer, path, payload)
	}, promtailFileTwo, 8, "promtail-b-batch-2", "promtail-app", "remote_addr", "8.8.8.8:10443", "8.8.8.8", "US")
	batchTwoMsgs = append(batchTwoMsgs, "direct-a-batch-2", "direct-b-batch-2", "promtail-a-batch-2", "promtail-b-batch-2")

	allMsgs := append(append([]string{}, batchOneMsgs...), batchTwoMsgs...)
	require.Eventually(t, func() bool {
		observed, _, fetchErr := fetchObservedEntries(ctx, lokiBaseURL, runID, queryStart, time.Now().UTC().Add(2*time.Minute))
		if fetchErr != nil {
			return false
		}
		return containsAllMessages(observed, allMsgs)
	}, 90*time.Second, 1*time.Second, "batch 2 logs did not arrive in Loki in time")

	observed, streamApps, err := fetchObservedEntries(ctx, lokiBaseURL, runID, queryStart, time.Now().UTC().Add(2*time.Minute))
	require.NoError(t, err)

	for msg, expected := range expectedByMsg {
		entries := observed[msg]
		require.Lenf(t, entries, 1, "message %s should appear exactly once in Loki", msg)
		entry := entries[0]

		assert.Equal(t, expected.app, entry.streamApp, "app label mismatch for %s", msg)
		assert.Equal(t, expected.clientIP, stringField(entry.fields, "client_ip"), "client_ip mismatch for %s", msg)
		assert.Equal(t, expected.country, stringField(entry.fields, "client_country"), "client_country mismatch for %s", msg)
		assert.Equal(t, expected.msg, stringField(entry.fields, "msg"), "msg field mismatch for %s", msg)
		assert.Equal(t, runID, stringField(entry.fields, "e2e_run"), "run marker mismatch for %s", msg)
		assert.Equal(t, expected.timestamp.UnixNano(), entry.timestamp.UnixNano(), "timestamp mismatch for %s", msg)
	}

	assert.Contains(t, streamApps, "tailer-app")
	assert.Contains(t, streamApps, "promtail-app")

	appCancel()
	select {
	case appErr := <-done:
		require.NoError(t, appErr)
	case <-time.After(20 * time.Second):
		t.Fatal("runApplication did not shut down in time")
	}
}

func writeGeoIPTestDB(path string) error {
	writer, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: "GeoIP2-Country",
		IPVersion:    4,
		RecordSize:   24,
	})
	if err != nil {
		return err
	}

	type record struct {
		cidr    string
		country string
	}
	for _, entry := range []record{
		{cidr: "1.1.1.0/24", country: "AU"},
		{cidr: "8.8.8.0/24", country: "US"},
	} {
		_, network, parseErr := net.ParseCIDR(entry.cidr)
		if parseErr != nil {
			return parseErr
		}
		insertErr := writer.Insert(network, mmdbtype.Map{
			"country": mmdbtype.Map{
				"iso_code": mmdbtype.String(entry.country),
			},
		})
		if insertErr != nil {
			return insertErr
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = writer.WriteTo(file)
	return err
}

func writePromtailConfig(path string, receiverPort int) error {
	configText := fmt.Sprintf(`server:
  http_listen_port: 9080
  grpc_listen_port: 0

positions:
  filename: /tmp/positions.yaml

clients:
  - url: http://%s:%d/loki/api/v1/push

scrape_configs:
  - job_name: promtail-e2e
    static_configs:
      - targets:
          - localhost
        labels:
          app: promtail-app
          __path__: /var/log/promtail/*.log
`, testcontainers.HostInternal, receiverPort)
	return os.WriteFile(path, []byte(configText), 0o644)
}

func appendJSONLine(path string, payload map[string]any) error {
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func appendJSONLineInContainer(ctx context.Context, c testcontainers.Container, path string, payload map[string]any) error {
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf("printf '%%s\\n' %s >> %s", shellSingleQuote(string(line)), shellSingleQuote(path))
	exitCode, output, err := c.Exec(ctx, []string{"sh", "-c", cmd})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		out, _ := io.ReadAll(output)
		return fmt.Errorf("failed to append to %s in container (exit %d): %s", path, exitCode, strings.TrimSpace(string(out)))
	}

	return nil
}

func ensureContainerLogFiles(ctx context.Context, c testcontainers.Container, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}

	commands := make([]string, 0, len(paths)*2)
	seenDirs := make(map[string]struct{}, len(paths))
	for _, filePath := range paths {
		dir := path.Dir(filePath)
		if dir != "." {
			if _, ok := seenDirs[dir]; !ok {
				commands = append(commands, "mkdir -p "+shellSingleQuote(dir))
				seenDirs[dir] = struct{}{}
			}
		}
		commands = append(commands, "touch "+shellSingleQuote(filePath))
	}

	cmd := strings.Join(commands, " && ")
	exitCode, output, err := c.Exec(ctx, []string{"sh", "-c", cmd})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		out, _ := io.ReadAll(output)
		return fmt.Errorf("failed to prepare log files in container (exit %d): %s", exitCode, strings.TrimSpace(string(out)))
	}

	return nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func fetchObservedEntries(ctx context.Context, lokiBaseURL string, runID string, start time.Time, end time.Time) (map[string][]lokiObservedEntry, []string, error) {
	query := fmt.Sprintf(`{job="log-enricher"} |= %q`, runID)
	requestURL := fmt.Sprintf("%s/loki/api/v1/query_range?query=%s&start=%d&end=%d&limit=1000&direction=forward",
		strings.TrimSuffix(lokiBaseURL, "/"),
		url.QueryEscape(query),
		start.UTC().UnixNano(),
		end.UTC().UnixNano(),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("unexpected Loki query status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed lokiQueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, nil, err
	}
	if parsed.Status != "success" || parsed.Data.ResultType != "streams" {
		return nil, nil, fmt.Errorf("unexpected Loki query response status=%s resultType=%s", parsed.Status, parsed.Data.ResultType)
	}

	observed := make(map[string][]lokiObservedEntry)
	appSet := make(map[string]struct{})

	for _, stream := range parsed.Data.Result {
		streamApp := strings.TrimSpace(stream.Stream["app"])
		if streamApp != "" {
			appSet[streamApp] = struct{}{}
		}

		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}

			ns, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid Loki timestamp value %q: %w", value[0], err)
			}

			fields := map[string]any{}
			if err := json.Unmarshal([]byte(value[1]), &fields); err != nil {
				continue
			}

			msg := stringField(fields, "msg")
			if msg == "" {
				continue
			}

			observed[msg] = append(observed[msg], lokiObservedEntry{
				streamApp: streamApp,
				fields:    fields,
				timestamp: time.Unix(0, ns).UTC(),
			})
		}
	}

	apps := make([]string, 0, len(appSet))
	for app := range appSet {
		apps = append(apps, app)
	}
	slices.Sort(apps)

	return observed, apps, nil
}

func containsAllMessages(observed map[string][]lokiObservedEntry, expectedMsgs []string) bool {
	for _, msg := range expectedMsgs {
		if len(observed[msg]) == 0 {
			return false
		}
	}
	return true
}

func stringField(fields map[string]any, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return value
}

func getFreeTCPPort(t *testing.T) int {
	t.Helper()

	const (
		startPort = 3500
		endPort   = 4500
	)

	for port := startPort; port <= endPort; port++ {
		addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		_ = listener.Close()
		return port
	}

	t.Fatalf("failed to find available receiver port in range %d-%d", startPort, endPort)
	return 0
}
