package main

import (
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Device is one entry of the "connected devices" page, sourced from the
// dnsmasq DHCP leases plus the WiFi station lists.
type Device struct {
	Name string
	IP   string
	MAC  string
	WiFi int // 0 = wired/LAN, 1 = board WiFi (wlan0), 2 = PCIe WiFi (wlan1)
}

func loadDevices() []Device {
	wifi := map[string]int{"": 0}
	for i, ifname := range []string{"wlan0", "wlan1"} {
		out, err := exec.Command("iw", "dev", ifname, "station", "dump").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "Station" {
				wifi[strings.ToLower(f[1])] = i + 1
			}
		}
	}

	var devs []Device
	b, err := os.ReadFile("/tmp/dhcp.leases")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		mac := strings.ToLower(f[1])
		ip := f[2]
		host := f[3]
		if host == "*" || host == "" {
			host = "dev-" + mac[len(mac)-5:]
		}
		d := Device{Name: host, IP: ip, MAC: mac}
		if d.WiFi = wifi[mac]; d.WiFi == 0 {
			d.WiFi = 0
		}
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool {
		return ipLess(devs[i].IP, devs[j].IP)
	})
	return devs
}

func ipLess(a, b string) bool {
	ia, ib := ipVal(a), ipVal(b)
	return ia < ib
}

func ipVal(s string) uint32 {
	var v uint32
	for _, part := range strings.Split(s, ".") {
		var n uint32
		for _, c := range part {
			if c < '0' || c > '9' {
				continue
			}
			n = n*10 + uint32(c-'0')
		}
		v = v<<8 | (n & 0xff)
	}
	return v
}
