// Package security contains host-independent security primitives used by the
// gateway and tenant boundaries.
package security

import (
	"crypto/rand"
	"net"
)

// GenerateToken creates a random alphanumeric token with the OpenKitty prefix.
func GenerateToken(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if length < 0 {
		length = 0
	}
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	for i, value := range b {
		b[i] = chars[int(value)%len(chars)]
	}
	return "ok_" + string(b)
}

// IsLocalAddress reports whether ip is loopback or belongs to this host. It
// deliberately does not trust an entire private subnet.
func IsLocalAddress(ip string) bool {
	if ip == "" || ip == "localhost" {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() {
		return true
	}
	if localIPs, err := localIPs(); err == nil {
		for _, localIP := range localIPs {
			if localIP.Equal(parsed) {
				return true
			}
		}
	}
	return false
}

func localIPs() ([]net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, networkInterface := range interfaces {
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok {
				ips = append(ips, network.IP)
			}
		}
	}
	return ips, nil
}
