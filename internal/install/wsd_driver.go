package install

import (
	"net"
	"sort"
	"strings"
)

// selectWSDRAWDriver reuses a driver only when Windows already maps it to RAW
// 9100 at this exact literal IP. An unrelated installed OEM driver is not enough
// evidence of compatibility. Conflicting driver mappings leave IPP as fallback.
func selectWSDRAWDriver(queues []InstalledQueue, ip net.IP) (driver, source string) {
	var candidates []InstalledQueue
	for _, queue := range queues {
		if !queue.PortKnown || queue.Protocol != 1 || queue.PortNumber != 9100 || !ip.Equal(net.ParseIP(strings.TrimSpace(queue.HostAddress))) || strings.TrimSpace(queue.DriverName) == "" || isIPPClassDriver(queue.DriverName) {
			continue
		}
		candidates = append(candidates, queue)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].PrinterName < candidates[j].PrinterName })
	for _, queue := range candidates {
		if driver != "" && !strings.EqualFold(driver, queue.DriverName) {
			return "", ""
		}
		if driver == "" {
			driver, source = queue.DriverName, queue.PrinterName
		}
	}
	return driver, source
}
