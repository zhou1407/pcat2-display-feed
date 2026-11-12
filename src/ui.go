package main

import (
	"fmt"
	"math"
)

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func rgb565(r, g, b int) uint16 {
	r = clampi(r, 0, 255)
	g = clampi(g, 0, 255)
	b = clampi(b, 0, 255)
	return uint16((r>>3)<<11 | (g>>2)<<5 | (b>>3))
}

func rgb565Parts(c uint16) (int, int, int) {
	r := int(c>>11) & 0x1f
	g := int(c>>5) & 0x3f
	b := int(c) & 0x1f
	return r << 3, g << 2, b << 3
}

func colorLerp(a, b uint16, t float64) uint16 {
	t = clampf(t, 0, 1)
	ar, ag, ab := rgb565Parts(a)
	br, bg, bb := rgb565Parts(b)
	return rgb565(
		int(float64(ar)+float64(br-ar)*t),
		int(float64(ag)+float64(bg-ag)*t),
		int(float64(ab)+float64(bb-ab)*t),
	)
}

// hsv565 converts HSV (h in degrees, s/v in 0..1) to RGB565.
func hsv565(h, s, v float64) uint16 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	s = clampf(s, 0, 1)
	v = clampf(v, 0, 1)
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return rgb565(int((r+m)*255), int((g+m)*255), int((b+m)*255))
}

// batteryColor: 100% green -> 0% red (user requirement).
func batteryColor(pct int) uint16 {
	return hsv565(120.0*float64(clampi(pct, 0, 100))/100.0, 0.85, 0.95)
}

func memColor(pct int) uint16 {
	hue := 120.0 * (1.0 - float64(clampi(pct, 0, 100))/100.0)
	return hsv565(hue, 0.85, 0.95)
}

// tempColor follows the mockup stops: 0 blue, 15 cyan, 30 green, 45 yellow, 60+ red.
func tempColor(c float64) uint16 {
	stops := []struct {
		t float64
		r, g, b int
	}{
		{0, 0, 0, 255},
		{15, 0, 255, 255},
		{30, 0, 255, 0},
		{45, 255, 255, 0},
		{60, 255, 0, 0},
	}
	if c <= stops[0].t {
		return rgb565(stops[0].r, stops[0].g, stops[0].b)
	}
	for i := 1; i < len(stops); i++ {
		if c <= stops[i].t {
			t := (c - stops[i-1].t) / (stops[i].t - stops[i-1].t)
			return rgb565(
				int(float64(stops[i-1].r)+float64(stops[i].r-stops[i-1].r)*t),
				int(float64(stops[i-1].g)+float64(stops[i].g-stops[i-1].g)*t),
				int(float64(stops[i-1].b)+float64(stops[i].b-stops[i-1].b)*t),
			)
		}
	}
	last := stops[len(stops)-1]
	return rgb565(last.r, last.g, last.b)
}

// fmtRate formats bytes/s as B/s, KB/s or MB/s (decimal units).
func fmtRate(bps float64) string {
	switch {
	case bps < 1000:
		return fmt.Sprintf("%.0f B/s", bps)
	case bps < 1e6:
		return fmt.Sprintf("%.1f KB/s", bps/1e3)
	default:
		return fmt.Sprintf("%.2f MB/s", bps/1e6)
	}
}

// fmtByteAmt formats a byte count as GB (or MB for small values).
func fmtByteAmt(n uint64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

func fmtDur(sec int64) string {
	if sec < 0 {
		return "--"
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// RText draws s so that its right edge is at xRight.
func (im *Image) RText(xRight, y int, s string, c uint16, size int) {
	im.Text(xRight-TextWidth(s, size), y, s, c, size)
}

// DrawIcon draws the tinted 1-bit mask at (x, y).
func (im *Image) DrawIcon(m iconMask, x, y int, c uint16) {
	for row := 0; row < m.H; row++ {
		bits := m.Rows[row]
		for col := 0; col < m.W; col++ {
			if bits&(1<<(m.W-1-col)) != 0 {
				im.Set(x+col, y+row, c)
			}
		}
	}
}

// drawDots draws the page indicator dots.
func drawDots(im *Image, active, total int) {
	y := im.H - 7
	x0 := im.W/2 - (total-1)*5
	for i := 0; i < total; i++ {
		c := ColorDark
		if i == active {
			c = ColorGreen
		}
		im.Rect(x0+i*10, y, 4, 4, c)
	}
}
