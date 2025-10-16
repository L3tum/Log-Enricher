# Go Log Enricher

This service automatically enriches log files with valuable metadata and forwards them to configured backends. It is designed to run as a sidecar or standalone service to add context to your logs with minimal configuration.

## Main Features

-   **Real-time Log Processing**: Monitors directories for log files and processes new lines as they are written.
-   **Stateful Processing**: Keeps track of processed file offsets and file identity (via inodes) to prevent data duplication across restarts and log rotations.
-   **High-Performance Caching**: Caches enrichment lookups (like DNS results) to reduce latency and redundant external queries.
-   **Multi-Stage Enrichment**: Each stage can be enabled or disabled independently.
    -   **Hostname Resolution**: Enriches logs by resolving IP addresses to hostnames.
    -   **GeoIP Lookup**: Adds geographical data for IP addresses using a local MaxMind database.
    -   **CrowdSec Intelligence**: Checks IPs against the CrowdSec Threat Intelligence database.
-   **Flexible Backends**: Forwards enriched logs to multiple destinations. Currently supports:
    -   **File**: Saves enriched logs to a new file with a `.enriched` suffix.
    -   **Loki**: Sends logs directly to a Grafana Loki instance via HTTP.

## Performance and Efficiency

The service is engineered for high throughput and minimal resource consumption, making it suitable for high-volume logging environments. This is achieved through several zero-allocation and resource pooling strategies to minimize garbage collector (GC) pressure.

-   **Zero-Allocation Log Tailing**: Log lines are read from disk directly into pooled buffers (`sync.Pool`), eliminating memory allocations in the I/O hot path.
-   **Log Entry Pooling**: Parsed log entries (`map[string]interface{}`) are reused from a shared pool, drastically reducing the number of small allocations that would otherwise churn the GC.
-   **Marshal-Once Broadcasting**: Enriched logs are converted to JSON a single time into a pooled buffer. This single, pre-marshaled byte slice is then sent to all configured backends, avoiding redundant work and allocations.
-   **Efficient Network I/O**: A shared, keep-alive enabled HTTP client is used for all external enrichment lookups, and streaming decoders are used where possible to minimize memory usage when parsing API responses.

### Hostname Resolution Explained

The service uses a multi-layered approach to resolve IP addresses to hostnames, maximizing the chances of a successful lookup, especially in local network environments. Each method can be enabled or disabled individually.

-   **rDNS**: Performs standard reverse DNS lookups against a configured public or private DNS server.
-   **mDNS**: Uses multicast DNS for zero-configuration name resolution of `.local` domains, common with Apple devices and Linux systems running Avahi.
-   **LLMNR**: Leverages Link-Local Multicast Name Resolution, a protocol used by modern Windows systems for local name resolution when DNS fails.
-   **NetBIOS**: Queries for NetBIOS names, which is useful for identifying legacy Windows devices or systems on a local network.

## Quick Start

    ```sh
    docker run -d \
      --name log-enricher \
      -e "BACKENDS=file,loki" \
      -e "LOKI_URL=http://your-loki-instance:3100" \
      -v /path/to/your/logs:/logs \
      -v /path/to/app/cache:/cache \
      ghcr.io/l3tum/log-enricher
    ```

## Configuration

Configuration is managed exclusively through environment variables. The service does not accept command-line flags.

|  Environment Variable        | Default Value | Description |
|------------------------------| --- | --- |
| CACHE_SIZE                   | 10000 | Number of enrichment results to keep in the cache. |
| STATE_FILE_PATH              | /cache/state.json | Path to the file for persisting processor state. |
| REQUERY_INTERVAL             | 5m | How often to re-query a cached entry. |
| LOG_BASE_PATH                | /logs | The base directory where log files are monitored. |
| LOG_FILE_EXTENSIONS          | .log | Comma-separated list of file extensions to monitor. |
| PLAINTEXT_PROCESSING_ENABLED | true | Enables processing of non-JSON log lines. |
| BACKENDS                     | file | Comma-separated list of enabled backends (e.g., file,loki). |
| LOKI_URL                     | "" | The HTTP URL for the Loki backend (e.g., http://loki:3100). |
| ENRICHED_FILE_SUFFIX         | .enriched | Suffix for log files saved by the 'file' backend. |

### Pipeline Configuration
The log processing pipeline is defined by a sequence of stages, configured via environment variables. Stages are defined by STAGE_<N>_* variables, where <N> is the index of the stage, starting from 0. They are executed in ascending order.

STAGE_N_TYPE: Defines the type of the stage (e.g., client_ip_extraction, enrichment, filter).

STAGE_N_PARAMETER: Sets a parameter for that specific stage. Parameter names are case-insensitive.

### Stage: client_ip_extraction
This stage finds a client IP address in a JSON log entry and adds it to the client_ip field. This is required for the enrichment stage to work.

| Parameter | Description | Example |
| --- | --- | --- |
| client_ip_fields | Comma-separated list of fields to check for an IP address, in order of priority. | client_ip,remote_addr,x_forwarded_for |
| regex_enabled | If true, falls back to searching for an IP in the msg field or the raw log line if no IP is found in the specified fields. | true |

**Example**

````bash
# STAGE 0: Find the client IP
STAGE_0_TYPE=client_ip_extraction
STAGE_0_CLIENT_IP_FIELDS=remote_addr,x-forwarded-for
STAGE_0_REGEX_ENABLED=true
````

### Stage: enrichment
This stage enriches the log entry with data based on the extracted client_ip. It can perform hostname resolution, GeoIP lookups, and CrowdSec threat intelligence checks.

| Parameter | Description | Example |
| --- | --- | --- |
| enable_hostname | Set to true to enable hostname resolution. | true |
| enable_geoip | Set to true to enable GeoIP lookups. | true |
| enable_crowdsec | Set to true to enable CrowdSec CAPI lookups. | false |
| hostname_dns_server | DNS server for rDNS lookups. | 1.1.1.1:53 |
| hostname_enable_rdns | Enables standard reverse DNS lookups. | true |
| hostname_enable_mdns | Enables mDNS lookups. | true |
| hostname_enable_llmnr | Enables LLMNR lookups. | true |
| hostname_enable_netbios | Enables NetBIOS lookups. | true |
| geoip_database_path | Path to the MaxMind GeoIP database file. | /geoip/GeoLite2-City.mmdb |
| crowdsec_lapi_url | URL for the CrowdSec LAPI. | http://crowdsec:8080 |
| crowdsec_lapi_key | API Key for the CrowdSec LAPI. | yoursupersecretkey |

**Example**

````bash
# STAGE 1: Enrich the log with GeoIP and Hostname data
STAGE_1_TYPE=enrichment
STAGE_1_ENABLE_GEOIP=true
STAGE_1_GEOIP_DATABASE_PATH=/geoip/GeoLite2-City.mmdb
STAGE_1_ENABLE_HOSTNAME=true
STAGE_1_HOSTNAME_DNS_SERVER=8.8.8.8:53
````

### Stage: filter
This stage filters out log entries based on a set of criteria.

| Parameter | Description | Example |
| --- | --- | --- |
| action | drop (default) or keep. The action to take if the rules match. | drop |
| match | any (default) or all. Whether any rule or all rules must match. | any |
| regex | A regex pattern to match against the raw log line. | (?i)health check |
| json_field | A field to check in a JSON log. | http_status |
| json_value | A regex to match against the value of json_field. | ^5.. |
| min_size | Minimum line size in characters to keep the log. | 10 |
| max_size | Maximum line size in characters to keep the log. | 4096 |

**Example**

````bash
# STAGE 2: Filter out noisy logs
STAGE_2_TYPE=filter
STAGE_2_ACTION=drop
STAGE_2_MATCH=any
STAGE_2_REGEX=(?i)bot
STAGE_2_JSON_FIELD=user_agent
STAGE_2_JSON_VALUE=HealthChecker
````

**Full Pipeline Example**

````bash
docker run -d \
  --name log-enricher \
  -e "BACKENDS=loki" \
  -e "LOKI_URL=http://loki:3100" \
  \
  # Stage 0: Extract the client IP from one of these fields
  -e "STAGE_0_TYPE=client_ip_extraction" \
  -e "STAGE_0_CLIENT_IP_FIELDS=ip,client_ip,remote_addr" \
  \
  # Stage 1: Enrich with GeoIP and Hostname
  -e "STAGE_1_TYPE=enrichment" \
  -e "STAGE_1_ENABLE_GEOIP=true" \
  -e "STAGE_1_GEOIP_DATABASE_PATH=/geoip/GeoLite2-City.mmdb" \
  -e "STAGE_1_ENABLE_HOSTNAME=true" \
  \
  # Stage 2: Drop logs from internal IPs
  -e "STAGE_2_TYPE=filter" \
  -e "STAGE_2_ACTION=drop" \
  -e "STAGE_2_JSON_FIELD=client_ip" \
  -e "STAGE_2_JSON_VALUE=^192\\.168\\." \
  \
  -v /path/to/your/logs:/logs \
  -v /path/to/app/cache:/cache \
  -v /path/to/geoip.mmdb:/geoip/GeoLite2-City.mmdb \
  ghcr.io/l3tum/log-enricher
````

## Contributing

This is a personal hobby project built primarily for my own needs. While I appreciate community interest and ideas, please understand that I may not be able to address all feature requests or issues immediately.


If you have an idea or find a bug, feel free to open an issue to start a discussion.
