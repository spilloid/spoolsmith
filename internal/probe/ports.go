package probe

import (
	"context"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

var testedPorts = []int{80, 443, 9100, 631}

func probePorts(ctx context.Context, host string) []int {
	var wg sync.WaitGroup
	var mutex sync.Mutex
	var open []int
	for _, port := range testedPorts {
		port := port
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				return
			}
			conn.Close()
			mutex.Lock()
			open = append(open, port)
			mutex.Unlock()
		}()
	}
	wg.Wait()
	sort.Ints(open)
	return open
}
