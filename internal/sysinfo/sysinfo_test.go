package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSampler(t *testing.T) {
	proc := t.TempDir()
	write(t, proc, "stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400 0 0 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0 0 0\nintr 1\n")
	write(t, proc, "meminfo", "MemTotal:       8000000 kB\nMemFree:  100 kB\nMemAvailable:   6000000 kB\n")
	write(t, proc, "uptime", "3600.50 7000.00\n")
	write(t, proc, "loadavg", "0.42 0.30 0.20 1/100 1234\n")
	write(t, proc, "net/dev", `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 0 0 0 0 0 0 0 999999 0 0 0 0 0 0 0
  eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0
veth12: 555555 0 0 0 0 0 0 0 555555 0 0 0 0 0 0 0
`)
	s := &Sampler{Proc: proc, DiskPath: proc}
	s.sample()
	st := s.Snapshot()
	if st.CPUs != 2 || st.MemTotal != 8000000*1024 || st.MemUsed != 2000000*1024 || st.UptimeSeconds != 3600.5 || st.Load1 != 0.42 {
		t.Fatalf("%+v", st)
	}
	if st.DiskTotal == 0 || st.DiskUsed > st.DiskTotal {
		t.Fatalf("disk %+v", st)
	}
	if st.CPUPercent != 0 {
		t.Fatal("no CPU percentage before the second sample")
	}
	// Second sample: 100 more busy jiffies out of 200 → 50 %.
	write(t, proc, "stat", "cpu  150 0 150 900 0 0 0 0 0 0\n")
	write(t, proc, "net/dev", "  eth0: 3000 0 0 0 0 0 0 0 6000 0 0 0 0 0 0 0\n    lo: 1 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0\n")
	s.sample()
	st = s.Snapshot()
	if st.CPUPercent < 49.9 || st.CPUPercent > 50.1 {
		t.Fatalf("cpu %v", st.CPUPercent)
	}
	if st.NetRxRate <= 0 || st.NetTxRate <= st.NetRxRate {
		t.Fatalf("net rx=%v tx=%v (loopback and veth must be ignored)", st.NetRxRate, st.NetTxRate)
	}
	if st.Time.IsZero() || st.Timezone == "" {
		t.Fatal("time")
	}
}

func TestMissingFilesAreHarmless(t *testing.T) {
	s := &Sampler{Proc: filepath.Join(t.TempDir(), "nope"), NetDev: "/nonexistent"}
	s.sample()
	s.sample()
	if st := s.Snapshot(); st.CPUs != 0 || st.NetHost {
		t.Fatalf("%+v", st)
	}
}
