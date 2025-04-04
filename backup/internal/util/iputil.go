package util

import (
	"bytes"
	"fmt"
	"net"
	"strings"
)

// IsValidIP checks if the given string is a valid IP address
func IsValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// IsIPv4 checks if the given string is a valid IPv4 address
func IsIPv4(ip string) bool {
	parsedIP := net.ParseIP(ip)
	return parsedIP != nil && parsedIP.To4() != nil
}

// IsIPv6 checks if the given string is a valid IPv6 address
func IsIPv6(ip string) bool {
	parsedIP := net.ParseIP(ip)
	return parsedIP != nil && parsedIP.To4() == nil
}

// ParseCIDR parses CIDR notation and returns IP and subnet mask
func ParseCIDR(cidr string) (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(cidr)
}

// IsIPInRange checks if an IP address is within a CIDR range
func IsIPInRange(ip string, cidr string) (bool, error) {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false, fmt.Errorf("invalid IP address: %s", ip)
	}

	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, fmt.Errorf("invalid CIDR: %s", cidr)
	}

	return ipNet.Contains(parsedIP), nil
}

// ExpandIPv6 expands a shortened IPv6 address to its full form
func ExpandIPv6(ip string) string {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil || parsedIP.To4() != nil {
		return ip // Return original if not a valid IPv6
	}
	return strings.ToUpper(fmt.Sprintf("%032x", []byte(parsedIP.To16())))
}

// CompareIPs compares two IP addresses
func CompareIPs(ip1, ip2 string) (int, error) {
	parsedIP1 := net.ParseIP(ip1)
	parsedIP2 := net.ParseIP(ip2)

	if parsedIP1 == nil {
		return 0, fmt.Errorf("invalid IP address: %s", ip1)
	}
	if parsedIP2 == nil {
		return 0, fmt.Errorf("invalid IP address: %s", ip2)
	}

	return bytes.Compare(parsedIP1, parsedIP2), nil
}

// IsPrivateIP checks if the IP address is private
func IsPrivateIP(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	privateIPBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	}

	for _, block := range privateIPBlocks {
		_, ipNet, err := net.ParseCIDR(block)
		if err != nil {
			continue
		}
		if ipNet.Contains(parsedIP) {
			return true
		}
	}
	return false
}
