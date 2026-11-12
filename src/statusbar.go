package main

type barState struct {
	FiveG      bool // modem present
	Signal     int  // 0..4
	WANLinked  bool
	WANIP      bool
	LANLinked  bool
	BoardWiFi  bool // radio present
	PcieWiFi   bool
	BoardCount int
	PcieCount  int
	RAT        string // cellular radio access technology: 3G/4G/5G (empty = 5G placeholder)
}

// drawStatusBar renders the 30px status bar: 5G + signal, LAN, WAN, board WiFi,
// PCIe WiFi. Groups are evenly distributed across the 172px width.
func drawStatusBar(im *Image, st barState) {
	im.Rect(0, 0, im.W, 30, ColorBlack)
	im.HLine(0, 29, im.W, ColorDark)

	rat := st.RAT
	if rat == "" {
		rat = "5G"
	}
	tw5 := TextWidth(rat, 12)
	g1 := tw5 + 4 + 20
	g2 := maxInt(16, TextWidth("LAN", 10))
	g3 := maxInt(16, TextWidth("WAN", 10))
	g4 := maxInt(12, TextWidth("0", 10))
	g5 := g4
	ws := [5]int{g1, g2, g3, g4, g5}
	total := 0
	for _, w := range ws {
		total += w
	}
	// keep a bigger margin on both sides: the panel corners are rounded
	const inset = 10
	gap := (im.W - 2*inset - total) / 4
	if gap < 2 {
		gap = 2
	}
	var xs [5]int
	xs[0] = inset
	for i := 1; i < 5; i++ {
		xs[i] = xs[i-1] + ws[i-1] + gap
	}

	// RAT label (3G/4G/5G) + signal bars; bars span the same 24px as icon+label
	col5 := ColorGray
	if st.FiveG {
		col5 = ColorWhite
	}
	im.Text(xs[0], 12, rat, col5, 12)
	bx := xs[0] + tw5 + 4
	for i := 0; i < 4; i++ {
		h := 7 + i*5
		c := ColorDark
		if st.FiveG && i < st.Signal {
			c = ColorGreen
		}
		im.Rect(bx+i*5, 24-h, 3, h, c)
	}

	lanC := ColorGray
	if st.LANLinked {
		lanC = ColorGreen
	}
	wanC := ColorGray
	if st.WANLinked {
		if st.WANIP {
			wanC = ColorGreen
		} else {
			wanC = ColorWhite
		}
	}
	drawEthGroup(im, xs[1], ws[1], "LAN", lanC)
	drawEthGroup(im, xs[2], ws[2], "WAN", wanC)
	drawWifiGroup(im, xs[3], ws[3], st.BoardWiFi, st.BoardCount)
	drawWifiGroup(im, xs[4], ws[4], st.PcieWiFi, st.PcieCount)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func drawEthGroup(im *Image, x, w int, label string, c uint16) {
	iconX := x + (w-16)/2
	im.DrawIcon(iconEth, iconX, 2, c)
	tw := TextWidth(label, 10)
	im.Text(x+(w-tw)/2, 14, label, c, 10)
}

func drawWifiGroup(im *Image, x, w int, on bool, count int) {
	iconC := ColorDark
	if on {
		iconC = ColorGreen
	}
	iconX := x + (w-12)/2
	im.DrawIcon(iconWifi, iconX, 3, iconC)
	s := itoa(count)
	tw := TextWidth(s, 10)
	cc := ColorGray
	if on {
		cc = ColorWhite
	}
	im.Text(x+(w-tw)/2, 14, s, cc, 10)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for v > 0 && i > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
