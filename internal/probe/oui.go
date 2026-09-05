package probe

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

// These MA-L assignments were verified against the IEEE public OUI registry
// (https://standards-oui.ieee.org/oui/oui.txt): 00-1B-78 is registered to
// Hewlett Packard and 00-80-77 is registered to Brother Industries, Ltd.
var ouiVendors = map[string]string{
	"00:1B:78": "HP",
	"00:80:77": "Brother",
}

var errARPEntryNotFound = errors.New("ARP entry not found")

func probeOUI(ip string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("ARP cache lookup is only implemented on Linux")
	}
	return probeOUIWith(ip, primeARP, openARPCache, time.Sleep)
}

func probeOUIWith(ip string, prime func(string), open func() (io.ReadCloser, error), sleep func(time.Duration)) (string, error) {
	prime(ip)
	mac, err := lookupARPWithRetry(ip, open, sleep)
	if err != nil {
		return "", err
	}
	vendor, ok := lookupOUI(mac)
	if !ok {
		return "", fmt.Errorf("MAC %s has no embedded OUI match", mac)
	}
	return vendor, nil
}

func primeARP(ip string) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(ip, "9"), 100*time.Millisecond)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_, _ = conn.Write([]byte{0})
}

func openARPCache() (io.ReadCloser, error) {
	return os.Open("/proc/net/arp")
}

func lookupARPWithRetry(ip string, open func() (io.ReadCloser, error), sleep func(time.Duration)) (net.HardwareAddr, error) {
	for attempt := 0; attempt < 3; attempt++ {
		file, err := open()
		if err != nil {
			return nil, fmt.Errorf("read ARP cache: %w", err)
		}
		mac, findErr := findARPAddress(file, ip)
		closeErr := file.Close()
		if findErr == nil {
			if closeErr != nil {
				return nil, fmt.Errorf("close ARP cache: %w", closeErr)
			}
			return mac, nil
		}
		if !errors.Is(findErr, errARPEntryNotFound) || attempt == 2 {
			return nil, findErr
		}
		sleep(150 * time.Millisecond)
	}
	panic("unreachable")
}

func findARPAddress(file io.Reader, ip string) (net.HardwareAddr, error) {
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] != ip {
			continue
		}
		mac, err := net.ParseMAC(fields[3])
		if err != nil {
			return nil, fmt.Errorf("ARP cache has invalid MAC for %s: %w", ip, err)
		}
		return mac, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read ARP cache: %w", err)
	}
	return nil, fmt.Errorf("%w: IP %s is not present in ARP cache", errARPEntryNotFound, ip)
}

func lookupOUI(mac net.HardwareAddr) (string, bool) {
	if len(mac) < 3 {
		return "", false
	}
	prefix := strings.ToUpper(fmt.Sprintf("%02X:%02X:%02X", mac[0], mac[1], mac[2]))
	vendor, ok := ouiVendors[prefix]
	return vendor, ok
}
