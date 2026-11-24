package interfaces

import (
	"strings"
)

// IPStacks holds the IPv4 and IPv6 addresses resolved for a proxy's exit IP.
type IPStacks struct {
	IPv4 []string
	IPv6 []string
}

// Init initialises empty slices and returns the receiver.
func (ips *IPStacks) Init() *IPStacks {
	if ips == nil {
		ips = &IPStacks{}
	}
	ips.IPv4 = []string{}
	ips.IPv6 = []string{}
	return ips
}

// Count returns the total number of IP addresses.
func (ips *IPStacks) Count() int {
	if ips == nil {
		return 0
	}
	return len(ips.IPv4) + len(ips.IPv6)
}

// GeoInfo holds geographic and network metadata for a single IP address.
type GeoInfo struct {
	Org           string  `json:"organization"`
	Lon           float32 `json:"longitude"`
	Lat           float32 `json:"latitude"`
	TimeZone      string  `json:"timezone"`
	ISP           string  `json:"isp"`
	ASN           int     `json:"asn"`
	ASNOrg        string  `json:"asn_organization"`
	Country       string  `json:"country"`
	IP            string  `json:"ip"`
	ContinentCode string  `json:"continent_code"`
	CountryCode   string  `json:"country_code"`
	CountryNameZH string  `json:"country_name_zh"`
	StackType     string  `json:"stackType"`
}

// IsV6 returns true when the IP is an IPv6 address.
func (gi *GeoInfo) IsV6() bool {
	return gi != nil && gi.IP != "" && strings.Contains(gi.IP, ":")
}
