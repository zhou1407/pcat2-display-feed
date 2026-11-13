package main

import (
	"os"
	"sort"
	"strings"
)

// Device is one entry of the "connected devices" page, sourced from the
// dnsmasq DHCP leases plus the WiFi station lists.
type Device struct {
	Name string
	IP   string
	MAC  string
	WiFi int // 0 = wired/LAN, 1 = onboard WiFi (USB), 2 = PCIe WiFi
}

func loadDevices() []Device {
	wifi := wifiStationMACs()

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
		d.WiFi = wifi[mac]
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
