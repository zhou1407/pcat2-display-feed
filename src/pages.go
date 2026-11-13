package main

import (
	"fmt"
	"strconv"
	"strings"
	"math"
	"os"
	"regexp"
)

const pageCount = 4

func (a *App) barState(s SysInfo) barState {
	signalBars := a.modemSignal
	if a.modemOnline && signalBars < 1 {
		signalBars = 1
	}
	return barState{
		FiveG:      a.modemOnline,
		Signal:     signalBars,
		WANLinked:  s.WANUp,
		WANIP:      s.WANHasIP,
		LANLinked:  s.LANUp,
		BoardWiFi:  s.WifiBoardUp,
		PcieWiFi:   s.WifiPcieUp,
		BoardCount: s.WifiBoard,
		PcieCount:  s.WifiPcie,
		RAT:        a.modemRAT,
	}
}

func hasIface(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name)
	return err == nil
}

// wifiEnabled reports whether a wireless interface exists and is up (IFF_UP).
func wifiEnabled(name string) bool {
	b, err := os.ReadFile("/sys/class/net/" + name + "/flags")
	if err != nil {
		return false
	}
	v, perr := strconv.ParseUint(strings.TrimSpace(string(b)), 0, 64)
	if perr != nil {
		return false
	}
	return v&0x1 != 0
}

// renderPageIdx returns the full image for a given page index.
func (a *App) renderPageIdx(page int, s SysInfo) *Image {
	im := NewImage(PanelW, PanelH)
	im.Fill(ColorBlack)
	drawStatusBar(im, a.barState(s))
	switch page {
	case 0:
		a.renderBattery(im, s)
	case 1:
		a.render5G(im, s)
	case 2:
		a.renderSystemPage(im, s)
	default:
		a.renderDevices(im, s)
	}
	drawDots(im, page, pageCount)
	return im
}

// ---------- page 0: battery + power + br-lan ----------

func (a *App) renderBattery(im *Image, s SysInfo) {
	W := im.W
	im.Text(4, 31, "电池", ColorWhite, 16)
	if s.BatteryPct < 10 {
		im.RText(W-4, 33, "请及时充电", ColorRed, 12)
	} else {
		im.RText(W-4, 27, fmt.Sprintf("%d%%", s.BatteryPct), batteryColor(s.BatteryPct), 20)
	}

	// power state tag: charging / power-bank output / discharging
	stateTag, stateCol := "", ColorGray
	switch {
	case s.Charging:
		stateTag, stateCol = "充电中", ColorGreen
	case s.ChargerOnline && s.PowerW < -0.2:
		stateTag, stateCol = "对外供电", ColorOrange
	case s.PowerW < -0.2:
		stateTag, stateCol = "放电中", ColorGray
	}
	if stateTag != "" {
		im.Text(44, 33, stateTag, stateCol, 10)
	}

	// battery bar
	im.Rect(4, 52, W-8, 14, ColorDarker)
	fill := int(float64(W-10) * float64(clampi(s.BatteryPct, 0, 100)) / 100.0)
	if fill > 0 {
		im.Rect(5, 53, fill, 12, batteryColor(s.BatteryPct))
	}
	im.Border(4, 52, W-8, 14, ColorDark)

	// power panel (60s curve, style B)
	drawPowerPanel(im, 4, 70, W-8, 65, a.powerSeries(), s.PowerW > 0.2, s.PowerW)

	im.Text(4, 138, "电压", ColorGray, 10)
	im.Text(4, 148, fmt.Sprintf("%.2f V", float64(s.BatteryMV)/1000.0), ColorWhite, 16)
	im.RText(W-4, 138, "剩余", ColorGray, 10)
	rem := "--"
	if s.RemainSec > 0 {
		rem = fmtDur(s.RemainSec)
	} else if s.Charging {
		rem = "充电中"
	}
	im.RText(W-4, 148, rem, ColorWhite, 16)

	im.HLine(4, 173, W-8, ColorDark)

	im.Text(4, 176, "br-lan 速率", ColorWhite, 16)
	im.Text(4, 200, "下行", ColorGreen, 16)
im.RText(W-4, 198, fmtRate(float64(s.LANRxBps)), ColorGreen, 20)
	im.Text(4, 224, "上行", ColorOrange, 16)
im.RText(W-4, 222, fmtRate(float64(s.LANTxBps)), ColorOrange, 20)
	im.Text(4, 247, "30s", ColorGray, 10)
	drawRateBars(im, 4, 254, W-8, 46, a.wanSeries())
}

func (a *App) powerSeries() [60]float64 {
	var out [60]float64
	n := 60
	start := a.powerIdx % n
	for i := 0; i < n; i++ {
		out[i] = a.powerHist[(start+i)%n]
	}
	return out
}

func (a *App) wanSeries() [30]uint64 {
	var out [30]uint64
	n := 30
	start := a.wanIdx % n
	for i := 0; i < n; i++ {
		out[i] = a.wanHist[(start+i)%n]
	}
	return out
}

// drawPowerPanel renders the 60s power curve: bottom 0W, top 40W, grid lines at
// 10/20/30W. Style B colours: discharge = yellow bg + green curve, charge swaps.
func drawPowerPanel(im *Image, x, y, w, h int, hist [60]float64, charging bool, curSigned float64) {
	bg := rgb565(255, 230, 140)
	curve := rgb565(20, 125, 60)
	if charging {
		bg = rgb565(20, 125, 60)
		curve = rgb565(255, 230, 140)
	}
	im.Rect(x, y, w, h, bg)
	grid := colorLerp(bg, ColorBlack, 0.22)
	for _, watt := range []int{10, 20, 30} {
		gy := y + h - 2 - int(float64(watt)/40.0*float64(h-4))
		im.HLine(x+1, gy, w-2, grid)
	}
	prev := -1
	for px := 0; px < w-2; px++ {
		t := float64(px) / float64(w-3) * 59.0
		i := int(t)
		f := t - float64(i)
		if i >= 59 {
			i = 58
			f = 1
		}
		v := hist[i]*(1-f) + hist[i+1]*f
		v = clampf(v, 0, 40)
		py := y + h - 2 - int(v/40.0*float64(h-4))
		if prev >= 0 {
			lo, hi := py, prev
			if lo > hi {
				lo, hi = hi, lo
			}
			for yy := lo; yy <= hi; yy++ {
				im.Set(x+1+px, yy, curve)
				im.Set(x+1+px, yy+1, curve)
				im.Set(x+1+px, yy+2, curve)
			}
		} else {
			im.Set(x+1+px, py, curve)
			im.Set(x+1+px, py+1, curve)
			im.Set(x+1+px, py+2, curve)
		}
		prev = py
	}
	dark := colorLerp(bg, ColorBlack, 0.62)
	im.Text(x+3, y+2, "功率 60s", dark, 10)
	var curTxt string
	switch {
	case curSigned > 0.05:
		curTxt = fmt.Sprintf("+%.1f W", curSigned)
	case curSigned < -0.05:
		curTxt = fmt.Sprintf("%.1f W", curSigned)
	default:
		curTxt = "0.0 W"
	}
	im.RText(x+w-3, y+2, curTxt, dark, 10)
	_ = dark
}

// drawRateBars draws the br-lan rate history as bars.
func drawRateBars(im *Image, x, y, w, h int, hist [30]uint64) {
	maxv := uint64(1)
	for _, v := range hist {
		if v > maxv {
			maxv = v
		}
	}
	barw := 5
	total := 30 * barw
	x0 := x + (w-total)/2
	for i, v := range hist {
		bh := int(math.Sqrt(float64(v)/float64(maxv)) * float64(h))
		if bh < 1 {
			bh = 1
		}
		im.Rect(x0+i*barw, y+h-bh, barw-1, bh, ColorGreen)
	}
}

// ---------- page 1: 5G ----------

func (a *App) render5G(im *Image, s SysInfo) {
	W := im.W
	im.Text(4, 31, "5G 模组", ColorWhite, 16)
	if a.modemOnline {
		im.RText(W-4, 33, "SA", ColorGreen, 12)
	} else {
		im.RText(W-4, 33, "无模组", ColorGray, 12)
	}

	labels := []string{"模组", "运营商", "IP", "DNS"}
	values := []string{"--", "--", "--", "--"}
	if a.modemOnline {
		values = a.modemInfo()
	}
	y := 54
	for i := range labels {
		im.Text(4, y, labels[i], ColorGray, 12)
		im.RText(W-4, y-2, values[i], ColorWhite, 16)
		y += 24
	}
	im.HLine(4, 146, W-8, ColorDark)

	im.Text(4, 148, "速率", ColorWhite, 16)
	rx, tx := 0.0, 0.0
	if a.modemOnline {
		rx, tx = a.modemRates()
	}
	im.Text(4, 172, "下行", ColorGreen, 16)
	im.RText(W-4, 170, fmtRate(rx), ColorGreen, 20)
	im.Text(4, 196, "上行", ColorOrange, 16)
	im.RText(W-4, 194, fmtRate(tx), ColorOrange, 20)

	// daily / monthly usage (left, right-aligned GB) + remaining ring (right)
	dayB := a.usage.DayBytes()
	monB := a.usage.MonthBytes()
	im.Text(4, 232, "日", ColorGray, 12)
	im.Text(4, 256, "月", ColorGray, 12)
	im.RText(98, 230, fmtByteAmt(dayB), ColorWhite, 16)
	im.RText(98, 254, fmtByteAmt(monB), ColorWhite, 16)

	limit := a.cfg.MonthlyLimitGB
	if limit <= 0 {
		limit = 100
	}
	limitB := uint64(limit) * 1000000000
	remB := uint64(0)
	if monB < limitB {
		remB = limitB - monB
	}
	frac := float64(remB) / float64(limitB)
	if frac > 1 {
		frac = 1
	}
	col := ColorGreen
	switch {
	case frac < 0.10:
		col = ColorRed
	case frac < 0.30:
		col = ColorYellow
	}
	cx, cy, r, thick := 134, 255, 26, 6
	drawRing(im, cx, cy, r, thick, frac, col, ColorDarker)
	im.Text(cx-TextWidth("剩余", 10)/2, cy-10, "剩余", ColorGray, 10)
	pct := int(frac*100 + 0.5)
	pcts := fmt.Sprintf("%d%%", pct)
	im.Text(cx-TextWidth(pcts, 16)/2, cy+1, pcts, ColorWhite, 16)
	lims := fmt.Sprintf("%dGB", limit)
	im.Text(cx-TextWidth(lims, 10)/2, 292, lims, ColorGray, 10)
	im.Text(4, 281, "每月 1 日 清零", ColorGray, 10)
}

// drawRing draws a progress ring starting at 12 o'clock, clockwise.
func drawRing(im *Image, cx, cy, r, thick int, frac float64, fg, track uint16) {
	frac = clampf(frac, 0, 1)
	rin := float64(r - thick/2)
	rout := float64(r + thick/2)
	for y := cy - r - thick; y <= cy+r+thick; y++ {
		for x := cx - r - thick; x <= cx+r+thick; x++ {
			dx := float64(x - cx)
			dy := float64(y - cy)
			d := math.Hypot(dx, dy)
			if d < rin || d > rout {
				continue
			}
			angle := math.Atan2(dx, -dy)
			if angle < 0 {
				angle += 2 * math.Pi
			}
			c := track
			if angle/(2*math.Pi) <= frac {
				c = fg
			}
			im.Set(x, y, c)
		}
	}
}

// ---------- page 2: system ----------

func (a *App) renderSystemPage(im *Image, s SysInfo) {
	W := im.W
	im.Text(4, 31, "系统", ColorWhite, 16)
	im.RText(W-4, 33, "CPU", ColorGray, 12)

	// CPU bars
	n := len(s.CPU)
	if n > 8 {
		n = 8
	}
	const barw, gap, top, hgt = 16, 2, 52, 56
	x0 := (W - (8*barw + 7*gap)) / 2
	for i := 0; i < 8; i++ {
		v := 0
		if i < n {
			v = s.CPU[i]
		}
		bx := x0 + i*(barw+gap)
		im.Rect(bx, top, barw, hgt, ColorDarker)
		bh := v * hgt / 100
		col := ColorGreen
		switch {
		case v >= 80:
			col = ColorRed
		case v >= 50:
			col = ColorYellow
		}
		if bh > 0 {
			im.Rect(bx+1, top+hgt-bh, barw-2, bh, col)
		}
		im.Text(bx+(barw-TextWidth(itoa(i), 10))/2, 110, itoa(i), ColorGray, 10)
	}

	// memory
	memPct := 0
	if s.MemTotalKB > 0 {
		memPct = 100 * (s.MemTotalKB - s.MemAvailableKB) / s.MemTotalKB
	}
	im.Text(4, 126, "内存", ColorWhite, 16)
	im.RText(W-4, 126, fmt.Sprintf("%d%%", memPct), ColorWhite, 16)
	drawBar(im, 4, 144, W-8, 18, float64(memPct)/100, memColor(memPct))

	// fan
	im.Text(4, 168, "风扇", ColorWhite, 16)
	im.RText(W-4, 168, fmt.Sprintf("%d RPM", s.FanRPM), ColorWhite, 16)
	drawBar(im, 4, 186, W-8, 18, float64(s.FanRPM)/18000.0, rgb565(0, 200, 200))

	// board temperature
	im.Text(4, 210, "板温", ColorWhite, 16)
	im.RText(W-4, 210, fmt.Sprintf("%d ℃", s.BoardTempC), ColorWhite, 16)
	drawBar(im, 4, 228, W-8, 18, float64(s.BoardTempC)/90.0, tempColor(float64(s.BoardTempC)))

	// OS / SN
	im.Text(4, 252, "OS", ColorGreen, 12)
	im.RText(W-4, 250, shortOS(s.OSVersion), ColorYellow, 12)
	im.Text(4, 278, "SN", ColorGreen, 12)
	sn := s.SN
	if sn == "" {
		sn = "--"
	}
	im.RText(W-4, 276, sn, ColorYellow, 12)
}

func drawBar(im *Image, x, y, w, h int, frac float64, fg uint16) {
	if frac > 1 {
		frac = 1
	}
	if frac < 0 {
		frac = 0
	}
	im.Rect(x, y, w, h, ColorDarker)
	fill := int(float64(w-2) * frac)
	if fill > 0 {
		im.Rect(x+1, y+1, fill, h-2, fg)
	}
}

var osRevRe = regexp.MustCompile(`r[0-9]+`)

func shortOS(s string) string {
	if m := osRevRe.FindString(s); m != "" {
		return m
	}
	if len(s) > 14 {
		return s[:14]
	}
	return s
}

// ---------- page 3: connected devices ----------

func (a *App) renderDevices(im *Image, s SysInfo) {
	W := im.W
	im.Text(4, 30, "接入设备", ColorWhite, 16)
	im.RText(W-4, 32, fmt.Sprintf("在线 %d", len(a.devices)), ColorGray, 12)

	if len(a.devices) == 0 {
		im.TextCenter(150, "未发现在线设备", ColorGray, 12)
	} else {
		start := a.subpage * 5
		for i := 0; i < 5; i++ {
			idx := start + i
			if idx >= len(a.devices) {
				break
			}
			d := a.devices[idx]
			y := 54 + i*44
			switch d.WiFi {
			case 1, 2:
				im.DrawIcon(iconWifi, 4, y+2, ColorGreen)
				im.Text(17, y+2, itoa(d.WiFi), ColorGray, 10)
			default:
				im.DrawIcon(iconEth, 4, y+4, ColorGreen)
			}
			im.RText(W-4, y+2, d.IP, ColorWhite, 12)
			name := d.Name
			avail := W - 8 - 8
			if TextWidth(name, 12) > avail {
				ell := "…"
				ellw := TextWidth(ell, 12)
				for len(name) > 1 && TextWidth(name, 12) > avail-ellw {
					name = name[:len(name)-1]
				}
				name += ell
			}
			im.Text(8, y+20, name, ColorGray, 12)
		}
	}

	pages := a.devPages()
	if pages > 1 {
		footer := fmt.Sprintf("第 %d/%d 页 · 共 %d 台", a.subpage+1, pages, len(a.devices))
		im.TextCenter(296, footer, ColorGray, 10)
	}
}

func (a *App) devPages() int {
	p := (len(a.devices) + 4) / 5
	if p < 1 {
		p = 1
	}
	return p
}

// modemInfo returns the 5G page values (module, carrier, IP, DNS ping).
func (a *App) modemInfo() []string {
	out := []string{"--", "--", "--", "--"}
	if a.modemModule != "" {
		out[0] = a.modemModule
	}
	if a.modemCarrier != "" {
		out[1] = a.modemCarrier
	}
	if a.modemIP != "" {
		out[2] = a.modemIP
	}
	if a.modemDNSms > 0 {
		out[3] = fmt.Sprintf("%d ms", a.modemDNSms)
	}
	return out
}

func (a *App) modemRates() (float64, float64) {
	return a.modemRxBps, a.modemTxBps
}

