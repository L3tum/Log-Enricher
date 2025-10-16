package enrichment

import (
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	once   sync.Once
	client *http.Client
)

// Get returns a shared, singleton instance of an http.Client configured for performance.
func Get() *http.Client {
	once.Do(func() {
		// This transport is configured to reuse TCP connections (Keep-Alive)
		// and has sensible timeouts and connection pool settings.
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConnsPerHost:   10,
		}
		client = &http.Client{
			Transport: transport,
		}
	})
	return client
}
