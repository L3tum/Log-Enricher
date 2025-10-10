# Go Log Enricher

This service automatically enriches log files with valuable metadata and forwards them to configured backends. It is designed to run as a sidecar or standalone service to add context to your logs with minimal configuration.

## Main Features

-   **Real-time Log Processing**: Monitors directories for log files and processes new lines as they are written.
-   **Stateful Processing**: Keeps track of processed file offsets to prevent data duplication across restarts.
-   **High-Performance Caching**: Caches enrichment lookups (like DNS results) to reduce latency and redundant external queries.
-   **Multi-Stage Enrichment**: Each stage can be enabled or disabled independently.
    -   **Hostname Resolution**: Enriches logs by resolving IP addresses to hostnames.
    -   **GeoIP Lookup**: Adds geographical data for IP addresses using a local MaxMind database.
    -   **CrowdSec Intelligence**: Checks IPs against the CrowdSec Threat Intelligence database.
-   **Flexible Backends**: Forwards enriched logs to multiple destinations. Currently supports:
    -   **File**: Saves enriched logs to a new file with a `.enriched` suffix.
    -   **Loki**: Sends logs directly to a Grafana Loki instance via HTTP.

### Hostname Resolution Explained

The service uses a multi-layered approach to resolve IP addresses to hostnames, maximizing the chances of a successful lookup, especially in local network environments. Each method can be enabled or disabled individually.

-   **rDNS**: Performs standard reverse DNS lookups against a configured public or private DNS server.
-   **mDNS**: Uses multicast DNS for zero-configuration name resolution of `.local` domains, common with Apple devices and Linux systems running Avahi.
-   **LLMNR**: Leverages Link-Local Multicast Name Resolution, a protocol used by modern Windows systems for local name resolution when DNS fails.
-   **NetBIOS**: Queries for NetBIOS names, which is useful for identifying legacy Windows devices or systems on a local network.

## Quick Start

1.  **Build the Docker image**:
    ```sh
    docker build -t go-log-enricher .
    ```

2.  **Run the service**:
    ```sh
    docker run -d \
      --name log-enricher \
      -e "BACKENDS=file,loki" \
      -e "LOKI_URL=http://your-loki-instance:3100" \
      -v /path/to/your/logs:/logs \
      -v /path/to/app/cache:/cache \
      go-log-enricher
    ```

## Configuration

Configuration is managed exclusively through environment variables. The service does not accept command-line flags.

| Environment Variable | Default Value | Description |
| --- | --- | --- |
| `DNS_SERVER` | `8.8.8.8:53` | DNS server to use for rDNS lookups. |
| `CACHE_SIZE` | `10000` | Number of enrichment results to keep in the cache. |
| `STATE_FILE_PATH` | `/cache/state.json` | Path to the file for persisting processor state. |
| `REQUERY_INTERVAL` | `5m` | How often to re-query a cached entry. |
| `LOG_BASE_PATH` | `/logs` | The base directory where log files are monitored. |
| `LOG_FILE_EXTENSIONS` | `.log` | Comma-separated list of file extensions to monitor. |
| `CLIENT_IP_FIELDS` | `client_ip,remote_addr` | Comma-separated list of JSON fields containing the IP to enrich. |
| `GEOIP_DATABASE_PATH` | `""` | Path to the MaxMind GeoIP database file. |
| `BACKENDS` | `file` | Comma-separated list of enabled backends (e.g., `file,loki`). |
| `LOKI_URL` | `""` | The HTTP URL for the Loki backend (e.g., `http://loki:3100`). |
| `ENRICHED_FILE_SUFFIX` | `.enriched` | Suffix for log files saved by the 'file' backend. |
| `ENABLE_HOSTNAME_STAGE` | `true` | Master switch for the entire hostname enrichment stage. |
| `ENABLE_GEOIP_STAGE` | `false` | Enables the GeoIP enrichment stage. |
| `ENABLE_CROWDSEC_STAGE` | `false` | Enables the CrowdSec enrichment stage. |
| `ENABLE_RDNS` | `true` | Enables standard reverse DNS lookups. |
| `ENABLE_MDNS` | `true` | Enables mDNS lookups. |
| `ENABLE_LLMNR` | `true` | Enables LLMNR lookups. |
| `ENABLE_NETBIOS` | `true` | Enables NetBIOS lookups. |
| `CROWDSEC_LAPI_URL` | `""` | URL for the CrowdSec LAPI. |
| `CROWDSEC_LAPI_KEY` | `""` | API Key for the CrowdSec LAPI. |

## Contributing

This is a personal hobby project built primarily for my own needs. While I appreciate community interest and ideas, please understand that I may not be able to address all feature requests or issues immediately.

If you have an idea or find a bug, feel free to open an issue to start a discussion.