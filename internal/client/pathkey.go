package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

// PathKeyV1 identifies a dial/egress path without including credentials.
// It is intentionally based on transport-routing fields, not display names.
func PathKeyV1(n Node) string {
	get := func(key string) string {
		if v, ok := n.Raw[key].(string); ok {
			return v
		}
		return ""
	}
	serverName := get("servername")
	if serverName == "" {
		serverName = get("sni")
	}
	network := get("network")
	path := get("path")
	if ws, ok := n.Raw["ws-opts"].(map[string]interface{}); ok {
		if path == "" {
			if v, ok := ws["path"].(string); ok {
				path = v
			}
		}
		if network == "" {
			network = "ws"
		}
	}
	gateway := get("dialer-proxy")
	if gateway == "" {
		gateway = get("proxy-server")
	}
	canonical := fmt.Sprintf("v1|protocol=%s|server=%s|port=%s|sni=%s|network=%s|path=%s|gateway=%s", n.Protocol, n.Server, strconv.Itoa(nPort(n)), serverName, network, path, gateway)
	sum := sha256.Sum256([]byte(canonical))
	return "v1:" + hex.EncodeToString(sum[:])
}

func nPort(n Node) int {
	if v, ok := n.Raw["port"].(float64); ok {
		return int(v)
	}
	if v, ok := n.Raw["port"].(int); ok {
		return v
	}
	return 0
}
