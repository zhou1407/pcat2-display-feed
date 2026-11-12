package main

import "fmt"

const pageCount = 3

func fmtUptime(sec int64) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func bpsMbps(b uint64) float64 {
	return float64(b) * 8 / 1e6
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (a *App) drawHeader(im *Image, title string) {
	im.Rect(0, 0, im.W, 20, ColorDark)
	im.HLine(0, 19, im.W, ColorGray)
	im.Text(4, 2, title, ColorWhite, 16)
}

func (a *App) drawFooter(im *Image, text string) {
	im.Rect(0, im.H-16, im.W, 16, ColorDark)
	im.HLine(0, im.H-17, im.W, ColorGray)
	im.TextCenter(im.H-16, text, ColorGray, 12)
}

// renderPageIdx returns the full image for a given page index.
func (a *App) renderPageIdx(page int, s SysInfo) *Image {
	im := NewImage(PanelW, PanelH)
	im.Fill(ColorBlack)
	switch page {
	case 0:
		a.renderSystem(im, s)
	case 1:
		a.renderCPU(im, s)
	default:
		a.renderDevice(im, s)
	}
	return im
}

func (a *App) renderSystem(im *Image, s SysInfo) {
	a.drawHeader(im, "PCAT2 / SYSTEM")
	y := 28
	im.Text(6, y, "BATTERY", ColorGray, 12)
	y += 16
	im.Bar(6, y, im.W-12, 20, s.BatteryPct, ColorGreen, ColorDarker, ColorGray)
	y += 26
	im.Text(6, y, fmt.Sprintf("%d%%   %.2fV   %+dmA", s.BatteryPct, float64(s.BatteryMV)/1000, s.BatteryMA), ColorWhite, 12)
	y += 18
	im.Text(6, y, "Status: "+s.BatteryStatus, ColorGray, 12)
	y += 18
	chg := "No"
	if s.ChargerOnline {
		chg = "Yes"
	}
	im.Text(6, y, fmt.Sprintf("Charger: %s  %.2fV", chg, float64(s.ChargerMV)/1000), ColorGray, 12)
	y += 22
	im.Text(6, y, "Uptime: "+fmtUptime(s.UptimeSec), ColorWhite, 12)
	y += 18
	im.Text(6, y, fmt.Sprintf("SPI: %.1f MHz", float64(a.disp.maxSpeed)/1e6), ColorGray, 12)
	a.drawFooter(im, "KEY: next page 1Hz")
}

func (a *App) renderCPU(im *Image, s SysInfo) {
	a.drawHeader(im, "PCAT2 / CPU")
	y := 24
	n := len(s.CPU)
	if n > 8 {
		n = 8
	}
	for i := 0; i < n; i++ {
		v := s.CPU[i]
		im.Text(4, y, fmt.Sprintf("c%d", i), ColorGray, 12)
		im.Bar(22, y, im.W-52, 13, v, ColorGreen2, ColorDarker, ColorGray)
		im.Text(im.W-26, y, fmt.Sprintf("%3d%%", v), ColorWhite, 12)
		y += 16
	}
	y += 2
	memPct := 0
	if s.MemTotalKB > 0 {
		memPct = 100 * (s.MemTotalKB - s.MemAvailableKB) / s.MemTotalKB
	}
	im.Text(4, y, "MEM", ColorGray, 12)
	y += 14
	im.Bar(4, y, im.W-8, 13, memPct, ColorBlue2, ColorDarker, ColorGray)
	y += 18
	im.Text(4, y, fmt.Sprintf("%d/%d MB  load %.2f", (s.MemTotalKB-s.MemAvailableKB)/1024, s.MemTotalKB/1024, s.Load1), ColorWhite, 12)
	y += 18
	im.Text(4, y, fmt.Sprintf("disk /  %d%% used", s.DiskPct), ColorGray, 12)
	y += 16
	im.Text(4, y, fmt.Sprintf("LAN %.1f/%.1f Mb", bpsMbps(s.LANRxBps), bpsMbps(s.LANTxBps)), ColorGray, 12)
	y += 14
	im.Text(4, y, fmt.Sprintf("WAN %.1f/%.1f Mb", bpsMbps(s.WANRxBps), bpsMbps(s.WANTxBps)), ColorGray, 12)
	a.drawFooter(im, "KEY: next page 1Hz")
}

func (a *App) renderDevice(im *Image, s SysInfo) {
	awake, page, brightness, keys := a.uiState()
	a.drawHeader(im, "PCAT2 / DEVICE")
	y := 28
	im.Text(6, y, fmt.Sprintf("Board temp: %d C", s.BoardTempC), ColorWhite, 12)
	y += 18
	im.Text(6, y, fmt.Sprintf("Fan: %d RPM", s.FanRPM), ColorWhite, 12)
	y += 18
	im.Text(6, y, fmt.Sprintf("Pwr key: %d presses", keys), ColorGray, 12)
	y += 18
	im.Text(6, y, fmt.Sprintf("Page: %d/%d", page+1, pageCount), ColorGray, 12)
	y += 18
	state := "OFF"
	if awake {
		state = "ON"
	}
	im.Text(6, y, fmt.Sprintf("Screen: %s  bright %d%%", state, brightness), ColorGray, 12)
	y += 22
	im.Text(6, y, "OS:", ColorGray, 12)
	y += 14
	im.Text(6, y, truncateRunes(s.OSVersion, 23), ColorWhite, 12)
	y += 16
	im.Text(6, y, "Kernel: "+truncateRunes(s.Kernel, 18), ColorGray, 12)
	y += 18
	im.Text(6, y, "SPI: "+fmt.Sprintf("%.1f MHz", float64(a.disp.maxSpeed)/1e6), ColorGray, 12)
	a.drawFooter(im, "KEY: next page 1Hz")
}
