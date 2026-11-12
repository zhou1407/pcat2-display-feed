package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SysInfo is a snapshot of the data shown on the test pages.
type SysInfo struct {
	BatteryPct    int
	BatteryMV     int
	BatteryMA     int
	BatteryStatus string
	ChargerOnline bool
	ChargerMV     int

	BoardTempC int
	FanRPM     int

	CPU []int

	MemTotalKB     int
	MemAvailableKB int
	DiskPct        int

	UptimeSec int64
	Load1     float64

	OSVersion string
	Kernel    string

	LANRxBps uint64
	LANTxBps uint64
	WANRxBps uint64
	WANTxBps uint64

	PowerW    float64 // signed battery power: >0 charging, <0 discharging (W)
	RemainSec int64   // estimated remaining seconds while discharging
	WANUp     bool    // WAN link up
	WANHasIP  bool    // WAN has a default route
	LANUp     bool
	WifiBoard int // board WiFi station count (0 when no radio)
	WifiPcie  int // PCIe WiFi station count
	SN        string
}

// Sensor samples sysfs/procfs and keeps previous values for rates and percentages.
type Sensor struct {
	prevCPU  [][2]uint64
	prevNet  map[string][2]uint64
	prevTime time.Time

	tempPath string
	fanPath  string
}

func NewSensor() *Sensor {
	return &Sensor{prevNet: make(map[string][2]uint64)}
}

func readFileTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readSysInt(path string) int {
	v, err := strconv.Atoi(readFileTrim(path))
	if err != nil {
		return 0
	}
	return v
}

func readFirstFloat(path string) float64 {
	f := strings.Fields(readFileTrim(path))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func (s *Sensor) locateHwmon() {
	if s.tempPath != "" && s.fanPath != "" {
		return
	}
	ents, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return
	}
	for _, e := range ents {
		p := "/sys/class/hwmon/" + e.Name()
		name := readFileTrim(p + "/name")
		if s.tempPath == "" && strings.Contains(name, "pcat_pm_hwmon_temp_mb") {
			s.tempPath = p + "/temp1_input"
		}
		if s.fanPath == "" && strings.Contains(name, "pcat_pm_hwmon_speed_fan") {
			s.fanPath = p + "/fan1_input"
		}
	}
}

func readCPUStat() [][2]uint64 {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}
	var out [][2]uint64
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu") || strings.HasPrefix(line, "cpu ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		var idle, total uint64
		for i := 1; i < len(f); i++ {
			v, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				continue
			}
			total += v
			if i == 4 {
				idle = v
			}
		}
		out = append(out, [2]uint64{idle, total})
	}
	return out
}

func (s *Sensor) sampleCPU() []int {
	cur := readCPUStat()
	pcts := make([]int, len(cur))
	for i := range cur {
		if i >= len(s.prevCPU) {
			continue
		}
		di := cur[i][0] - s.prevCPU[i][0]
		dt := cur[i][1] - s.prevCPU[i][1]
		if dt > 0 {
			p := int(100 * (dt - di) / dt)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			pcts[i] = p
		}
	}
	s.prevCPU = cur
	return pcts
}

func readMeminfo() (totalKB, availKB int) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			totalKB, _ = strconv.Atoi(f[1])
		case "MemAvailable:":
			availKB, _ = strconv.Atoi(f[1])
		}
	}
	return
}

func readDiskPct(path string) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	if st.Blocks == 0 {
		return 0
	}
	return int(100 * (st.Blocks - st.Bavail) / st.Blocks)
}

func readNetDev() map[string][2]uint64 {
	m := make(map[string][2]uint64)
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		f := strings.Fields(line[i+1:])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		m[name] = [2]uint64{rx, tx}
	}
	return m
}

// cellularIface returns the network interface that carries cellular
// traffic (qmodem creates wwan*/qmi*/rmnet* when a modem is up), or "" when
// no cellular interface exists. The 5G usage counters must never fall back to
// eth0: that is the home WAN link, not cellular traffic.
func cellularIface() string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "wwan") || strings.HasPrefix(n, "qmi") || strings.HasPrefix(n, "rmnet") {
			return n
		}
	}
	return ""
}

func readOSVersion() string {
	b, err := os.ReadFile("/etc/openwrt_release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "DISTRIB_DESCRIPTION=") {
			return strings.Trim(strings.TrimPrefix(line, "DISTRIB_DESCRIPTION="), "'\"")
		}
	}
	return ""
}

// Sample takes one snapshot of all data sources.
func (s *Sensor) Sample() SysInfo {
	var si SysInfo
	s.locateHwmon()

	si.BatteryPct = readSysInt("/sys/class/power_supply/battery/capacity")
	si.BatteryMV = readSysInt("/sys/class/power_supply/battery/voltage_now") / 1000
	si.BatteryMA = readSysInt("/sys/class/power_supply/battery/current_now") / 1000
	si.BatteryStatus = readFileTrim("/sys/class/power_supply/battery/status")
	si.ChargerOnline = readSysInt("/sys/class/power_supply/charger/online") == 1
	si.ChargerMV = readSysInt("/sys/class/power_supply/charger/voltage_now") / 1000

	if s.tempPath != "" {
		si.BoardTempC = readSysInt(s.tempPath) / 1000
	}
	if s.fanPath != "" {
		si.FanRPM = readSysInt(s.fanPath)
	}

	si.CPU = s.sampleCPU()
	si.MemTotalKB, si.MemAvailableKB = readMeminfo()
	si.DiskPct = readDiskPct("/")

	si.UptimeSec = int64(readFirstFloat("/proc/uptime"))
	si.Load1 = readFirstFloat("/proc/loadavg")
	si.OSVersion = readOSVersion()
	si.Kernel = readFileTrim("/proc/sys/kernel/osrelease")

	net := readNetDev()
	if !s.prevTime.IsZero() {
		dt := time.Since(s.prevTime).Seconds()
		if dt > 0.2 {
			rate := func(name string) (uint64, uint64) {
				cur := net[name]
				old := s.prevNet[name]
				var drx, dtx uint64
				if cur[0] >= old[0] {
					drx = cur[0] - old[0]
				}
				if cur[1] >= old[1] {
					dtx = cur[1] - old[1]
				}
				return uint64(float64(drx) / dt), uint64(float64(dtx) / dt)
			}
			si.LANRxBps, si.LANTxBps = rate("br-lan")
			si.WANRxBps, si.WANTxBps = rate("eth0")
		}
	}
	s.prevNet = net
	s.prevTime = time.Now()

	// Power / remaining time (battery power_now is in microwatts).
	if pw := readFirstFloat("/sys/class/power_supply/battery/power_now"); pw != 0 {
		si.PowerW = pw / 1e6
	} else {
		si.PowerW = float64(si.BatteryMV) * float64(si.BatteryMA) / 1e3
	}
	if !si.ChargerOnline && si.PowerW < -0.2 && si.BatteryPct > 0 {
		const energyFullWh = 51.8
		si.RemainSec = int64(energyFullWh * float64(si.BatteryPct) / 100.0 / -si.PowerW * 3600)
	}

	// Link state: eth0 = WAN, br-lan = LAN.
	si.WANUp = readOperstate("eth0") == "up"
	si.LANUp = readOperstate("br-lan") == "up"
	si.WANHasIP = defaultRouteVia("eth0")

	// WiFi station counts (empty when the board has no radio/driver).
	si.WifiBoard = wifiStationCount("wlan0")
	si.WifiPcie = wifiStationCount("wlan1")

	si.SN = readSN()
	return si
}

func readOperstate(name string) string {
	return readFileTrim("/sys/class/net/" + name + "/operstate")
}

func defaultRouteVia(ifname string) bool {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == ifname && f[1] == "00000000" {
			return true
		}
	}
	return false
}

// wifiStationCount counts associated stations via `iw` (busybox/system might
// lack the tool; any failure yields 0).
func wifiStationCount(ifname string) int {
	if _, err := os.Stat("/sys/class/net/" + ifname); err != nil {
		return 0
	}
	out, err := exec.Command("iw", "dev", ifname, "station", "dump").Output()
	if err != nil {
		return 0
	}
	return strings.Count(string(out), "Station ")
}

// readSN reads the Photonicat2 serial number from the eMMC boot partition
// (the stock firmware stores it there; format is a printable ASCII string).
func readSN() string {
	f, err := os.Open("/dev/mmcblk0boot1")
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 10000)
	n, _ := f.Read(buf)
	buf = buf[:n]
	// The stock firmware stores a JSON blob in the eMMC boot1 partition.
	// It is UTF-16LE, so drop the interleaved NUL bytes before parsing.
	if n > 2 && buf[1] == 0 {
		nb := make([]byte, 0, n/2)
		for i := 0; i+1 < n; i += 2 {
			if buf[i] != 0 {
				nb = append(nb, buf[i])
			}
		}
		buf = nb
	} else if idx := bytes.IndexByte(buf, 0); idx >= 0 {
		buf = buf[:idx]
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(buf, &payload); err != nil {
		return ""
	}
	for _, k := range []string{"sn", "machine_sn"} {
		if v, ok := payload[k].(string); ok && v != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
