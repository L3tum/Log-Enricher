# Go Log Enricher

`log-enricher` tails log files, runs each line through a configurable pipeline, and writes enriched output to a backend (`file` or `loki`).

The current architecture is:
- Tailing and file discovery: `internal/tailer/*`
- Line processing: `internal/processor/*`
- Pipeline stages: `internal/pipeline/*`
- Backend interface: `internal/backends/backend.go`

## Features

- Real-time recursive log directory watching via `fsnotify`
- Stateful resume across restarts with inode/size/modtime checks
- Stage-based processing pipeline (`STAGE_<N>_*` env config)
- Structured and JSON parsing stages
- Enrichment stages for client IP, hostname, and GeoIP
- Output to local enriched files or Grafana Loki

## Quick Start

### File backend

```bash
docker run -d \
  --name log-enricher \
  -e "BACKEND=file" \
  -e "LOG_BASE_PATH=/logs" \
  -e "LOG_FILE_EXTENSIONS=.log" \
  -e "ENRICHED_FILE_SUFFIX=.enriched" \
  -v /path/to/logs:/logs \
  -v /path/to/cache:/cache \
  ghcr.io/l3tum/log-enricher
```

### Loki backend

```bash
docker run -d \
  --name log-enricher \
  -e "BACKEND=loki" \
  -e "LOKI_URL=http://loki:3100" \
  -e "LOG_BASE_PATH=/logs" \
  -v /path/to/logs:/logs \
  -v /path/to/cache:/cache \
  ghcr.io/l3tum/log-enricher
```

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `STATE_FILE_PATH` | `/cache/state.json` | Persistent state file path |
| `LOG_BASE_PATH` | `/logs` | Root directory to watch recursively |
| `LOG_FILE_EXTENSIONS` | `.log` | Comma-separated file suffixes to process |
| `LOG_FILES_IGNORED` | `` | Regex for files to ignore |
| `BACKEND` | `file` | Output backend: `file` or `loki` |
| `LOKI_URL` | `` | Loki endpoint (required for `BACKEND=loki`) |
| `ENRICHED_FILE_SUFFIX` | `.enriched` | Suffix used by file backend |
| `APP_NAME` | `` | Static app label for output |
| `APP_IDENTIFICATION_REGEX` | `` | Regex with named group `app` to derive app from file path |
| `LOG_LEVEL` | `INFO` | Global log level (`DEBUG`, `INFO`, `WARN`, `ERROR`) |

## Pipeline Configuration

Stages are configured in ascending order:
- `STAGE_0_TYPE=...`
- `STAGE_1_TYPE=...`
- ...

Optional stage scoping:
- `STAGE_<N>_APPLIES_TO=<regex>`

Stage parameters:
- `STAGE_<N>_<PARAM>=...`

Examples:
- `STAGE_0_TYPE=client_ip_extraction`
- `STAGE_0_CLIENT_IP_FIELDS=remote_addr,x-forwarded-for`
- `STAGE_1_TYPE=geoip_enrichment`
- `STAGE_1_GEOIP_DATABASE_PATH=/geoip/GeoLite2-City.mmdb`

## Available Stage Types

### `json_parser`
Parses `logEntry.LogLine` as JSON into fields. No required params.

### `structured_parser`
Parses key/value text logs with regex.
- `pattern` (optional): regex with named groups, or exactly 2 unnamed groups for key/value

### `client_ip_extraction`
Extracts client IP from configured fields.
- `client_ip_fields` (required): comma-separated candidate field names
- `target_field` (optional, default `client_ip`)

### `timestamp_extraction`
Parses timestamps from configured fields.
- `timestamp_fields` (optional): comma-separated field names (defaults are included)
- `custom_layouts` (optional): comma-separated Go time layouts

### `filter`
Drops/keeps logs based on matching rules.
- `action` (`drop` or `keep`, default `drop`)
- `match` (`any` or `all`, default `any`)
- `regex`
- `json_field`
- `json_value`
- `min_size`
- `max_size`
- `max_age` (seconds)

### `hostname_enrichment`
Hostname enrichment from IP using multiple discovery methods.
- `client_ip_field` (default `client_ip`)
- `client_hostname_field` (default `client_hostname`)
- `dns_server`
- `enable_rdns`
- `enable_mdns`
- `enable_llmnr`
- `enable_netbios`

### `geoip_enrichment`
Adds country from MaxMind database.
- `geoip_database_path` (required for stage activation)
- `client_ip_field` (default `client_ip`)
- `client_country_field` (default `client_country`)

### `template_resolver`
Renders template strings using values from entry fields.
- `template_field` (required)
- `values_prefix` (required)
- `output_field` (optional, defaults to `<template_field>_rendered`)
- `placeholder` (optional regex for non-Go-template placeholders)

### `templated_enrichment`
Computes a field from a Go template over existing fields.
- `template` (required)
- `field` (required)

## Example Pipeline

```bash
STAGE_0_TYPE=json_parser

STAGE_1_TYPE=client_ip_extraction
STAGE_1_CLIENT_IP_FIELDS=client_ip,remote_addr,x-forwarded-for
STAGE_1_TARGET_FIELD=client_ip

STAGE_2_TYPE=timestamp_extraction
STAGE_2_TIMESTAMP_FIELDS=time,timestamp,@timestamp

STAGE_3_TYPE=hostname_enrichment
STAGE_3_CLIENT_IP_FIELD=client_ip
STAGE_3_CLIENT_HOSTNAME_FIELD=client_hostname
STAGE_3_ENABLE_RDNS=true

STAGE_4_TYPE=geoip_enrichment
STAGE_4_GEOIP_DATABASE_PATH=/geoip/GeoLite2-City.mmdb
STAGE_4_CLIENT_IP_FIELD=client_ip
STAGE_4_CLIENT_COUNTRY_FIELD=client_country

STAGE_5_TYPE=filter
STAGE_5_ACTION=drop
STAGE_5_MATCH=any
STAGE_5_REGEX=(?i)healthcheck
```

## Development Checks

Before merging changes:

```bash
go test ./...
go test -race ./...
go test -count=1 ./...
```
