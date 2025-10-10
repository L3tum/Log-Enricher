package models

type DeviceInfo struct {
	Hostname string              `json:"hostname"`
	IPv4s    map[string]struct{} `json:"ipv4s"`
	IPv6s    map[string]struct{} `json:"ipv6s"`
}

type GeoInfo struct {
	Country   string  `json:"country,omitempty"`
	City      string  `json:"city,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
}

type CrowdsecDecision struct {
	Scope    string `json:"scope,omitempty"`
	Type     string `json:"type,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Scenario string `json:"scenario,omitempty"`
	Duration string `json:"duration,omitempty"`
}

type CrowdsecInfo struct {
	IsBanned  bool               `json:"isBanned,omitempty"`
	Decisions []CrowdsecDecision `json:"decisions,omitempty"`
}

type Result struct {
	Found    bool
	Hostname string
	MAC      string
	Geo      *GeoInfo
	Crowdsec *CrowdsecInfo
}

func NewDeviceInfo() *DeviceInfo {
	return &DeviceInfo{
		IPv4s: make(map[string]struct{}),
		IPv6s: make(map[string]struct{}),
	}
}
