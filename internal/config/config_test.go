package config

import (
	"testing"
)

func TestLoadConfigWithBools(t *testing.T) {
	testCases := []struct {
		name          string
		envVars       map[string]string
		expectedValue bool
		checkField    func(cfg *Config) bool
	}{
		{
			name:          "EnableHostnameStage explicitly true",
			envVars:       map[string]string{"ENABLE_HOSTNAME_STAGE": "true"},
			expectedValue: true,
			checkField:    func(cfg *Config) bool { return cfg.EnableHostnameStage },
		},
		{
			name:          "EnableHostnameStage explicitly false",
			envVars:       map[string]string{"ENABLE_HOSTNAME_STAGE": "false"},
			expectedValue: false,
			checkField:    func(cfg *Config) bool { return cfg.EnableHostnameStage },
		},
		{
			name:          "EnableGeoIpStage default is false",
			envVars:       map[string]string{},
			expectedValue: false,
			checkField:    func(cfg *Config) bool { return cfg.EnableGeoIpStage },
		},
		{
			name:          "EnableRDNS explicitly false",
			envVars:       map[string]string{"ENABLE_RDNS": "false"},
			expectedValue: false,
			checkField:    func(cfg *Config) bool { return cfg.EnableRDNS },
		},
		{
			name:          "Invalid bool string uses default",
			envVars:       map[string]string{"ENABLE_MDNS": "not-a-bool"},
			expectedValue: true, // Default is true
			checkField:    func(cfg *Config) bool { return cfg.EnableMDNS },
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variables for this test case
			for key, value := range tc.envVars {
				t.Setenv(key, value)
			}

			cfg := Load()
			actualValue := tc.checkField(cfg)

			if actualValue != tc.expectedValue {
				t.Errorf("expected %v, but got %v", tc.expectedValue, actualValue)
			}
		})
	}
}
