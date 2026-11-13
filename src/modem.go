package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	qmodemConfigPath = "/etc/config/qmodem"
	modemPollEvery   = 2 * time.Second
)

var (
	modemSectionRe = regexp.MustCompile(`config\s+modem-device\s+'([^']+)'`)
	pingTimeRe     = regexp.MustCompile(`time=([0-9]+(?:\.[0-9]+)?)`)
	numberRe       = regexp.MustCompile(`-?[0-9]+(?:\.[0-9]+)?`)
)

type modemInfoItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type modemInfoReply struct {
	ModemInfo []modemInfoItem `json:"modem_info"`
}

type modemIfaceStatus struct {
	Up       bool   `json:"up"`
	L3Device string `json:"l3_device"`
	IPv4     []struct {
		Address string `json:"address"`
		Mask    int    `json:"mask"`
	} `json:"ipv4-address"`
	DNS []string `json:"dns-server"`
}

// modemState caches what we last saw from qmodem/ubus.
type modemState struct {
	module   string
	carrier  string
	rat      string
	signal   int
	ip       string
	dns      string
	dnsMs    int
	iface    string
	rx       uint64
	tx       uint64
	rxBps    float64
	txBps    float64
	lastNet  time.Time
	lastSlow time.Time
	lastRAT  time.Time
	lastSig  time.Time
	lastPing time.Time
}

// modemLoop polls qmodem every 2s and keeps the App status fields up to date.
func (a *App) modemLoop() {
	t := time.NewTicker(modemPollEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			a.pollModem()
		case <-a.stop:
			return
		}
	}
}

func (a *App) pollModem() {
	st := &a.modem
	now := time.Now()

	sec := findQmodemSection()
	if sec == "" {
		a.clearModem(st)
		return
	}
	var is modemIfaceStatus
	if !ubusJSON("network.interface."+sec, "status", "", &is) || !is.Up || len(is.IPv4) == 0 {
		a.clearModem(st)
		return
	}

	st.iface = is.L3Device
	st.ip = is.IPv4[0].Address
	if len(is.DNS) > 0 {
		st.dns = is.DNS[0]
	}

	if st.iface != "" {
		rx, tx := readIfaceCounters(st.iface)
		if !st.lastNet.IsZero() {
			dt := now.Sub(st.lastNet).Seconds()
			if dt > 0.2 {
				if rx >= st.rx {
					st.rxBps = float64(rx-st.rx) / dt
				} else {
					st.rxBps = 0
				}
				if tx >= st.tx {
					st.txBps = float64(tx-st.tx) / dt
				} else {
					st.txBps = 0
				}
			}
		}
		st.rx, st.tx = rx, tx
		st.lastNet = now
	}

	if st.module == "" || now.Sub(st.lastSlow) > 60*time.Second {
		if v := qmodemValue(sec, "info", "name"); v != "" {
			st.module = v
		}
		if v := qmodemValue(sec, "sim_info", "ISP"); v != "" {
			st.carrier = operatorName(v)
		}
		st.lastSlow = now
	}

	if st.rat == "" || now.Sub(st.lastRAT) > 5*time.Second {
		if v := qmodemValue(sec, "network_info", "Network Type"); v != "" {
			st.rat = ratLabel(v)
		}
		st.lastRAT = now
	}

	if now.Sub(st.lastSig) > 5*time.Second || st.signal == 0 {
		bars := 0
		if v := qmodemValue(sec, "cell_info", "RSRP"); v != "" {
			if rsrp, ok := firstNumber(v); ok {
				bars = rsrpBars(rsrp)
			}
		}
		if bars == 0 {
			if v := qmodemValue(sec, "cell_info", "SINR"); v != "" {
				if sinr, ok := firstNumber(v); ok {
					bars = sinrBars(sinr)
				}
			}
		}
		if bars > 0 {
			st.signal = bars
		}
		st.lastSig = now
	}

	if st.dns != "" && (st.dnsMs == 0 || now.Sub(st.lastPing) > 3*time.Second) {
		if ms, ok := pingMs(st.iface, st.dns); ok {
			st.dnsMs = ms
		} else {
			st.dnsMs = 0
		}
		st.lastPing = now
	}

	a.mu.Lock()
	a.modemOnline = true
	a.modemModule = st.module
	a.modemCarrier = st.carrier
	a.modemIP = st.ip
	a.modemDNSms = st.dnsMs
	a.modemRAT = st.rat
	a.modemSignal = st.signal
	a.modemRxBps = st.rxBps
	a.modemTxBps = st.txBps
	a.mu.Unlock()
}

func (a *App) clearModem(st *modemState) {
	st.iface = ""
	st.ip = ""
	st.dnsMs = 0
	st.rxBps = 0
	st.txBps = 0
	st.signal = 0
	st.lastNet = time.Time{}

	a.mu.Lock()
	a.modemOnline = false
	a.modemIP = ""
	a.modemDNSms = 0
	a.modemRAT = ""
	a.modemSignal = 0
	a.modemRxBps = 0
	a.modemTxBps = 0
	a.mu.Unlock()
}

func ubusJSON(obj, method, args string, out any) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmdArgs := []string{"call", obj, method}
	if args != "" {
		cmdArgs = append(cmdArgs, args)
	}
	b, err := exec.CommandContext(ctx, "ubus", cmdArgs...).Output()
	if err != nil {
		return false
	}
	return json.Unmarshal(b, out) == nil
}

func qmodemValue(sec, method, key string) string {
	var reply modemInfoReply
	args := `{"config_section":"` + sec + `"}`
	if !ubusJSON("qmodem", method, args, &reply) {
		return ""
	}
	for _, it := range reply.ModemInfo {
		if it.Key == key {
			return strings.TrimSpace(it.Value)
		}
	}
	return ""
}

func findQmodemSection() string {
	b, err := os.ReadFile(qmodemConfigPath)
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	first := ""
	for _, m := range modemSectionRe.FindAllStringSubmatch(string(b), -1) {
		sec := m[1]
		if seen[sec] {
			continue
		}
		seen[sec] = true
		var is modemIfaceStatus
		if ubusJSON("network.interface."+sec, "status", "", &is) && is.Up && len(is.IPv4) > 0 {
			return sec
		}
		if first == "" {
			first = sec
		}
	}
	return first
}

// operatorName maps China MCC/MNC (460xx) to an operator display name.
func operatorName(isp string) string {
	s := strings.TrimSpace(isp)
	switch s {
	case "46000", "46002", "46004", "46007", "46008":
		return "中国移动"
	case "46001", "46006", "46009":
		return "中国联通"
	case "46003", "46005", "46011":
		return "中国电信"
	case "46015":
		return "中国广电"
	}
	if strings.HasPrefix(s, "460") {
		return "中国运营商"
	}
	return s
}

func ratLabel(v string) string {
	u := strings.ToUpper(v)
	switch {
	case strings.Contains(u, "NR"), strings.Contains(u, "5G"):
		return "5G"
	case strings.Contains(u, "LTE"), strings.Contains(u, "4G"):
		return "4G"
	case strings.Contains(u, "WCDMA"), strings.Contains(u, "UMTS"), strings.Contains(u, "HSPA"), strings.Contains(u, "3G"):
		return "3G"
	case strings.Contains(u, "GSM"), strings.Contains(u, "GPRS"), strings.Contains(u, "EDGE"), strings.Contains(u, "2G"):
		return "2G"
	}
	return ""
}

func firstNumber(s string) (float64, bool) {
	m := numberRe.FindString(s)
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(m, 64)
	return v, err == nil
}

func rsrpBars(rsrp float64) int {
	switch {
	case rsrp >= -85:
		return 4
	case rsrp >= -95:
		return 3
	case rsrp >= -105:
		return 2
	}
	return 1
}

func sinrBars(sinr float64) int {
	switch {
	case sinr >= 20:
		return 4
	case sinr >= 13:
		return 3
	case sinr >= 5:
		return 2
	}
	return 1
}

func pingMs(iface, host string) (int, bool) {
	if iface == "" || host == "" {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ping", "-I", iface, "-c", "1", "-W", "1", host).CombinedOutput()
	m := pingTimeRe.FindSubmatch(out)
	if len(m) < 2 {
		return 0, false
	}
	if f, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
		return int(f), true
	}
	return 0, false
}
