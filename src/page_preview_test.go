package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenderPages renders the four pages with mock data to PNG files so the
// layout can be checked without a board. Run with:
//   go test -run TestRenderPages -v
func TestRenderPages(t *testing.T) {
	out := "preview_out"
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}
	a := previewApp()
	s := previewInfo()
	save := func(name string, im *Image) {
		writeScaledPNG(t, filepath.Join(out, name), im, 3)
	}
	save("page0_battery.png", a.renderPageIdx(0, s))
	low := s
	low.BatteryPct = 8
	low.BatteryMV = 7520
	low.RemainSec = 1080
	save("page0_battery_low.png", a.renderPageIdx(0, low))
	chg := s
	chg.ChargerOnline = true
	chg.ChargerMV = 12100
	chg.BatteryPct = 64
	chg.BatteryMA = 900
	save("page0_battery_charging.png", a.renderPageIdx(0, chg))
	save("page1_5g.png", a.renderPageIdx(1, s))
	save("page2_system.png", a.renderPageIdx(2, s))
	save("page3_devices.png", a.renderPageIdx(3, s))
}

func previewApp() *App {
	a := &App{cfg: defaultConfig(), usage: NewUsageStore()}
	now := time.Now()
	a.usage.data = usageFile{
		DayKey:   dayKey(now),
		MonthKey: monthKey(now),
		DayRx:    2900000000,
		DayTx:    320000000,
		MonthRx:  39800000000,
		MonthTx:  5800000000,
		LastRx:   1,
		LastTx:   1,
	}
	for i := 0; i < 60; i++ {
		a.powerHist[i] = 12 + 9*math.Sin(float64(i)/8) + 4*math.Sin(float64(i)/3)
	}
	a.powerIdx = 60
	for i := 0; i < 30; i++ {
		a.wanHist[i] = uint64(120000 + 700000*math.Abs(math.Sin(float64(i)/3)))
	}
	a.wanIdx = 30
	a.devices = []Device{
		{Name: "DESKTOP-LYC", IP: "192.168.2.145"},
		{Name: "NAS-01", IP: "192.168.2.101"},
		{Name: "iPhone-15", IP: "192.168.2.112", WiFi: 1},
		{Name: "MacBook", IP: "192.168.2.120", WiFi: 2},
		{Name: "ESP32DEV", IP: "192.168.2.130", WiFi: 1},
		{Name: "TV-Living", IP: "192.168.2.131", WiFi: 2},
		{Name: "Printer", IP: "192.168.2.140"},
		{Name: "Pixel-9", IP: "192.168.2.150", WiFi: 1},
		{Name: "iPad-Air", IP: "192.168.2.151", WiFi: 1},
	}
	a.modemOnline = true
	a.modemModule = "RM520N-GL"
	a.modemCarrier = "中国移动 CMCC"
	a.modemIP = "10.1.2.3"
	a.modemDNSms = 23
	a.modemRxBps = 10600000
	a.modemTxBps = 1550000
	return a
}

func previewInfo() SysInfo {
	return SysInfo{
		BatteryPct:     82,
		BatteryMV:      8150,
		BatteryMA:      -420,
		BatteryStatus:  "Discharging",
		BoardTempC:     29,
		FanRPM:         1200,
		CPU:            []int{15, 38, 6, 72, 22, 9, 55, 34},
		MemTotalKB:     1900000,
		MemAvailableKB: 1100000,
		OSVersion:      "ImmortalWrt SNAPSHOT r42943-89f1ef18c8",
		Kernel:         "6.18.55",
		SN:             "0x1234ABCD",
		RemainSec:      9300,
		PowerW:         3.2,
		LANRxBps:       420000,
		LANTxBps:       180000,
		WANRxBps:       420000,
		WANTxBps:       180000,
		WANUp:          true,
		WANHasIP:       true,
		LANUp:          true,
		WifiBoard:      3,
		WifiPcie:       0,
	}
}

func writeScaledPNG(t *testing.T, path string, im *Image, scale int) {
	w, h := im.W*scale, im.H*scale
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			c := im.Pix[y*im.W+x]
			r := int((c>>11)&0x1f) * 255 / 31
			g := int((c>>5)&0x3f) * 255 / 63
			b := int(c&0x1f) * 255 / 31
			cc := color.RGBA{uint8(r), uint8(g), uint8(b), 255}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					rgba.Set(x*scale+dx, y*scale+dy, cc)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, rgba); err != nil {
		t.Fatal(err)
	}
}

func TestRenderStatusBar(t *testing.T) {
	out := "preview_out"
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}
	on := barState{
		FiveG: true, Signal: 4,
		WANLinked: true, WANIP: true, LANLinked: true,
		BoardWiFi: true, BoardCount: 3,
		PcieWiFi: true, PcieCount: 0,
	}
	imOn := NewImage(PanelW, 30)
	imOn.Fill(ColorBlack)
	drawStatusBar(imOn, on)
	writeScaledPNG(t, filepath.Join(out, "statusbar_wifi_on.png"), imOn, 3)
	off := barState{
		FiveG: false, Signal: 0,
		WANLinked: false, LANLinked: true,
		BoardWiFi: false, BoardCount: 0,
		PcieWiFi: false, PcieCount: 0,
	}
	on4g := barState{
		FiveG: true, Signal: 3,
		WANLinked: true, WANIP: true, LANLinked: true,
		BoardWiFi: true, BoardCount: 3,
		PcieWiFi: true, PcieCount: 0,
		RAT: "4G",
	}
	im4g := NewImage(PanelW, 30)
	im4g.Fill(ColorBlack)
	drawStatusBar(im4g, on4g)
	writeScaledPNG(t, filepath.Join(out, "statusbar_4g.png"), im4g, 3)
	imOff := NewImage(PanelW, 30)
	imOff.Fill(ColorBlack)
	drawStatusBar(imOff, off)
	writeScaledPNG(t, filepath.Join(out, "statusbar_wifi_off.png"), imOff, 3)
}
