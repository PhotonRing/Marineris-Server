// Package sysinfo samples host metrics for the hub widgets: CPU, memory,
// disk, network throughput, uptime and load.
//
// /proc/stat, /proc/meminfo, /proc/uptime and /proc/loadavg are not
// namespaced, so inside the container they describe the host. Network
// counters are per network namespace: the host's are read from a bind mount
// of the host's /proc/1/net/dev (see compose.yml), falling back to the
// container's own.
package sysinfo

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Stats is one snapshot.
type Stats struct {
	Time      time.Time `json:"time"`
	Timezone  string    `json:"timezone"`
	UTCOffset int       `json:"utc_offset"` // seconds east of UTC
	Hostname  string    `json:"hostname,omitempty"`

	CPUPercent float64 `json:"cpu_percent"`
	CPUs       int     `json:"cpus"`
	Load1      float64 `json:"load1"`

	MemTotal uint64 `json:"mem_total"`
	MemUsed  uint64 `json:"mem_used"`

	DiskTotal uint64 `json:"disk_total"`
	DiskUsed  uint64 `json:"disk_used"`

	NetRxRate float64 `json:"net_rx_rate"` // bytes per second
	NetTxRate float64 `json:"net_tx_rate"`
	NetHost   bool    `json:"net_host"` // counters are the host's

	UptimeSeconds float64 `json:"uptime_seconds"`
}

// Sampler keeps the latest snapshot up to date.
type Sampler struct {
	Proc     string // usually /proc
	NetDev   string // host /proc/1/net/dev bind mount, optional
	DiskPath string // filesystem to report (the data directory)
	Hostname func(ctx context.Context) string
	Interval time.Duration

	mu       sync.Mutex
	last     Stats
	prevCPU  [2]uint64 // idle, total
	prevNet  [2]uint64 // rx, tx
	prevTime time.Time
	host     string
}

// Start samples until ctx is done.
func (s *Sampler) Start(ctx context.Context) {
	if s.Interval == 0 {
		s.Interval = 2 * time.Second
	}
	if s.Hostname != nil {
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		s.host = s.Hostname(hctx)
		cancel()
	}
	s.sample()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sample()
		}
	}
}

// Snapshot returns the latest values, with the current time.
func (s *Sampler) Snapshot() Stats {
	s.mu.Lock()
	st := s.last
	s.mu.Unlock()
	now := time.Now()
	st.Time = now
	st.Timezone, st.UTCOffset = now.Zone()
	if name := now.Location().String(); name != "Local" {
		st.Timezone = name
	} else if tz := os.Getenv("TZ"); tz != "" {
		st.Timezone = tz
	}
	return st
}

func (s *Sampler) sample() {
	now := time.Now()
	var st Stats
	st.Hostname = s.host

	idle, total, cpus := readCPU(filepath.Join(s.Proc, "stat"))
	st.CPUs = cpus
	st.MemTotal, st.MemUsed = readMem(filepath.Join(s.Proc, "meminfo"))
	st.UptimeSeconds = firstFloat(filepath.Join(s.Proc, "uptime"))
	st.Load1 = firstFloat(filepath.Join(s.Proc, "loadavg"))
	if s.DiskPath != "" {
		var fs syscall.Statfs_t
		if syscall.Statfs(s.DiskPath, &fs) == nil {
			bs := uint64(fs.Bsize) //nolint:gosec // block size is positive
			st.DiskTotal = fs.Blocks * bs
			st.DiskUsed = (fs.Blocks - fs.Bfree) * bs
		}
	}
	netFile := filepath.Join(s.Proc, "net", "dev")
	if s.NetDev != "" {
		if _, err := os.Stat(s.NetDev); err == nil {
			netFile = s.NetDev
			st.NetHost = true
		}
	}
	rx, tx := readNet(netFile)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prevTime.IsZero() {
		if dt := total - s.prevCPU[1]; dt > 0 && total >= s.prevCPU[1] {
			busy := float64(dt-(idle-s.prevCPU[0])) / float64(dt) * 100
			st.CPUPercent = clamp(busy)
		}
		if secs := now.Sub(s.prevTime).Seconds(); secs > 0 {
			if rx >= s.prevNet[0] {
				st.NetRxRate = float64(rx-s.prevNet[0]) / secs
			}
			if tx >= s.prevNet[1] {
				st.NetTxRate = float64(tx-s.prevNet[1]) / secs
			}
		}
	}
	s.prevCPU = [2]uint64{idle, total}
	s.prevNet = [2]uint64{rx, tx}
	s.prevTime = now
	s.last = st
}

func clamp(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// readCPU returns the idle and total jiffies of the aggregate "cpu" line and
// the number of CPUs.
func readCPU(path string) (idle, total uint64, cpus int) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed /proc path
	if err != nil {
		return 0, 0, 0
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] != "cpu" {
			cpus++
			continue
		}
		for i, v := range f[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			// guest and guest_nice (fields 9-10) are already counted in user/nice.
			if i < 8 {
				total += n
			}
			if i == 3 || i == 4 { // idle, iowait
				idle += n
			}
		}
	}
	return idle, total, cpus
}

func readMem(path string) (total, used uint64) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed /proc path
	if err != nil {
		return 0, 0
	}
	var avail uint64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	if avail <= total {
		used = total - avail
	}
	return total, used
}

// readNet sums the counters of physical-looking interfaces (skipping
// loopback and Docker's virtual interfaces).
func readNet(path string) (rx, tx uint64) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed path
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "ifb") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

func firstFloat(path string) float64 {
	b, err := os.ReadFile(path) //nolint:gosec // fixed /proc path
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}
