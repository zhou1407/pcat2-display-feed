package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const usagePath = "/etc/pcat2d/usage.json"

type usageFile struct {
	DayKey   string `json:"day_key"`
	MonthKey string `json:"month_key"`
	DayRx    uint64 `json:"day_rx"`
	DayTx    uint64 `json:"day_tx"`
	MonthRx  uint64 `json:"month_rx"`
	MonthTx  uint64 `json:"month_tx"`
	LastRx   uint64 `json:"last_rx"`
	LastTx   uint64 `json:"last_tx"`
	SavedAt  int64  `json:"saved_at"`
}

// UsageStore keeps day/month interface counters on the persistent overlay
// (/etc is overlayfs on this firmware, /var is tmpfs). Counters are sampled
// from /proc/net/dev; the deltas survive reboots because the totals are saved.
type UsageStore struct {
	mu       sync.Mutex
	data     usageFile
	lastSave time.Time
}

func NewUsageStore() *UsageStore {
	u := &UsageStore{}
	if b, err := os.ReadFile(usagePath); err == nil {
		_ = json.Unmarshal(b, &u.data)
	}
	return u
}

func dayKey(t time.Time) string   { return t.Format("2006-01-02") }
func monthKey(t time.Time) string { return t.Format("2006-01") }

// Update accumulates interface counter deltas into day/month totals.
func (u *UsageStore) Update(t time.Time, rx, tx uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()

	dk, mk := dayKey(t), monthKey(t)
	if u.data.DayKey != dk {
		u.data.DayKey = dk
		u.data.DayRx = 0
		u.data.DayTx = 0
	}
	if u.data.MonthKey != mk {
		u.data.MonthKey = mk
		u.data.MonthRx = 0
		u.data.MonthTx = 0
	}
	if u.data.LastRx != 0 || u.data.LastTx != 0 {
		if rx >= u.data.LastRx {
			u.data.DayRx += rx - u.data.LastRx
			u.data.MonthRx += rx - u.data.LastRx
		}
		if tx >= u.data.LastTx {
			u.data.DayTx += tx - u.data.LastTx
			u.data.MonthTx += tx - u.data.LastTx
		}
	}
	u.data.LastRx = rx
	u.data.LastTx = tx
	if time.Since(u.lastSave) > 60*time.Second {
		u.saveLocked(t)
	}
}

func (u *UsageStore) saveLocked(t time.Time) {
	u.data.SavedAt = t.Unix()
	if err := os.MkdirAll("/etc/pcat2d", 0755); err != nil {
		return
	}
	b, err := json.MarshalIndent(u.data, "", "  ")
	if err != nil {
		return
	}
	tmp := usagePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, usagePath)
	u.lastSave = t
}

// FlushNow writes the counters immediately (called on shutdown/SIGTERM).
func (u *UsageStore) FlushNow() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.saveLocked(time.Now())
}

func (u *UsageStore) DayBytes() uint64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.data.DayRx + u.data.DayTx
}

func (u *UsageStore) MonthBytes() uint64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.data.MonthRx + u.data.MonthTx
}

// readIfaceCounters returns the raw rx/tx byte counters of one interface.
func readIfaceCounters(name string) (uint64, uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		i := strings.Index(line, ":")
		if i < 0 || strings.TrimSpace(line[:i]) != name {
			continue
		}
		f := strings.Fields(line[i+1:])
		if len(f) < 9 {
			return 0, 0
		}
		rx := parseU64(f[0])
		tx := parseU64(f[8])
		return rx, tx
	}
	return 0, 0
}

func parseU64(s string) uint64 {
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return v
		}
		v = v*10 + uint64(c-'0')
	}
	return v
}
