// Plik internal/util/iputil.go

package util

import (
	"bytes"
	"fmt"
	"net"
	"strings"
)

// IsValidIP sprawdza, czy podany ciąg znaków jest poprawnym adresem IP (IPv4 lub IPv6)
func IsValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// IsIPv4 sprawdza, czy podany ciąg znaków jest poprawnym adresem IPv4
func IsIPv4(ip string) bool {
	parsedIP := net.ParseIP(ip)
	return parsedIP != nil && parsedIP.To4() != nil
}

// IsIPv6 sprawdza, czy podany ciąg znaków jest poprawnym adresem IPv6
func IsIPv6(ip string) bool {
	parsedIP := net.ParseIP(ip)
	return parsedIP != nil && parsedIP.To4() == nil
}

// ParseCIDR parsuje notację CIDR i zwraca adres IP oraz maskę podsieci
func ParseCIDR(cidr string) (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(cidr)
}

// IsIPInRange sprawdza, czy dany adres IP znajduje się w zakresie określonym przez CIDR
func IsIPInRange(ip string, cidr string) (bool, error) {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false, fmt.Errorf("nieprawidłowy adres IP: %s", ip)
	}

	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, fmt.Errorf("nieprawidłowy CIDR: %s", cidr)
	}

	return ipNet.Contains(parsedIP), nil
}

// GetLocalIP zwraca lokalny adres IP maszyny
func GetLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("nie można określić lokalnego adresu IP")
}

// ExpandIPv6 rozwija skrócony zapis adresu IPv6 do pełnej formy
func ExpandIPv6(ip string) string {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil || parsedIP.To4() != nil {
		return ip // Zwracamy oryginalny ciąg, jeśli to nie jest poprawny IPv6
	}
	return strings.ToUpper(fmt.Sprintf("%032x", []byte(parsedIP.To16())))
}

// CompareIPs porównuje dwa adresy IP, obsługując zarówno IPv4, jak i IPv6
func CompareIPs(ip1, ip2 string) (int, error) {
	parsedIP1 := net.ParseIP(ip1)
	parsedIP2 := net.ParseIP(ip2)

	if parsedIP1 == nil {
		return 0, fmt.Errorf("nieprawidłowy adres IP: %s", ip1)
	}
	if parsedIP2 == nil {
		return 0, fmt.Errorf("nieprawidłowy adres IP: %s", ip2)
	}

	return bytes.Compare(parsedIP1, parsedIP2), nil
}

// IsPrivateIP sprawdza, czy podany adres IP jest adresem prywatnym
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
