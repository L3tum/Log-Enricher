package network

import (
	"log-enricher/internal/models"
	"sync"
	"testing"
)

// This file is for testing functions in neighbor_common.go

func TestNeighborCache(t *testing.T) {
	// Reset global state for this test
	ipToMac = make(map[string]string)
	macToDev = make(map[string]*models.DeviceInfo)

	// Test Store and Borrow
	t.Run("Store and Borrow Hostname", func(t *testing.T) {
		mac := "00:11:22:33:44:55"
		hostname := "my-device"

		// Should be empty initially
		if h := BorrowHostnameViaMAC(mac); h != "" {
			t.Errorf("expected empty hostname for new mac, got %q", h)
		}

		StoreHostnameForMAC(mac, hostname)

		if h := BorrowHostnameViaMAC(mac); h != hostname {
			t.Errorf("expected hostname %q, got %q", hostname, h)
		}

		// Test update
		newHostname := "my-new-device"
		StoreHostnameForMAC(mac, newHostname)
		if h := BorrowHostnameViaMAC(mac); h != newHostname {
			t.Errorf("expected updated hostname %q, got %q", newHostname, h)
		}
	})

	t.Run("Store and Get IP to MAC", func(t *testing.T) {
		ip := "192.168.1.100"
		mac := "aa:bb:cc:dd:ee:ff"

		// Use a manual write to the map since WatchNeighbors is hard to test.
		// This tests GetMACForIP in isolation.
		ipToMacMu.Lock()
		ipToMac[ip] = mac
		ipToMacMu.Unlock()

		if m := GetMACForIP(ip); m != mac {
			t.Errorf("expected mac %q, got %q", mac, m)
		}

		// Test non-existent IP
		if m := GetMACForIP("1.1.1.1"); m != "" {
			t.Errorf("expected empty mac for unknown IP, got %q", m)
		}
	})

	t.Run("Concurrent Access", func(t *testing.T) {
		mac := "de:ad:be:ef:00:00"
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			StoreHostnameForMAC(mac, "writer1")
		}()
		go func() {
			defer wg.Done()
			StoreHostnameForMAC(mac, "writer2")
		}()

		wg.Wait()
		// We can't know which one wins, but we can check that it doesn't crash.
		// A read after the writes should succeed.
		if h := BorrowHostnameViaMAC(mac); h == "" {
			t.Errorf("expected a hostname to be set after concurrent writes, but got empty")
		}
	})
}
