package main

// RGB565 colors as sent to the GC9307 (MADCTL 0x48, big-endian RGB565 verified
// on hardware: red / green / blue / white / black / yellow / cyan / magenta).
const (
	ColorBlack   uint16 = 0x0000
	ColorWhite   uint16 = 0xFFFF
	ColorGray    uint16 = 0x8410
	ColorDark    uint16 = 0x2104
	ColorDarker  uint16 = 0x10A2
	ColorGreen   uint16 = 0x07E0
	ColorGreen2  uint16 = 0x2FE5
	ColorRed     uint16 = 0xF800
	ColorRed2    uint16 = 0xE8C5
	ColorBlue    uint16 = 0x001F
	ColorBlue2   uint16 = 0x0335
	ColorYellow  uint16 = 0xFFE0
	ColorCyan    uint16 = 0x07FF
	ColorMagenta uint16 = 0xF81F
	ColorOrange  uint16 = 0xFD20
)

// Image is a simple RGB565 framebuffer with drawing helpers.
type Image struct {
	W, H int
	Pix  []uint16
}

func NewImage(w, h int) *Image {
	return &Image{W: w, H: h, Pix: make([]uint16, w*h)}
}

func (im *Image) Fill(c uint16) {
	for i := range im.Pix {
		im.Pix[i] = c
	}
}

func (im *Image) Set(x, y int, c uint16) {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return
	}
	im.Pix[y*im.W+x] = c
}

func (im *Image) Rect(x, y, w, h int, c uint16) {
	if w <= 0 || h <= 0 {
		return
	}
	for yy := y; yy < y+h; yy++ {
		if yy < 0 || yy >= im.H {
			continue
		}
		row := im.Pix[yy*im.W : (yy+1)*im.W]
		x0 := x
		if x0 < 0 {
			x0 = 0
		}
		x1 := x + w
		if x1 > im.W {
			x1 = im.W
		}
		for xx := x0; xx < x1; xx++ {
			row[xx] = c
		}
	}
}

func (im *Image) HLine(x, y, w int, c uint16) { im.Rect(x, y, w, 1, c) }
func (im *Image) VLine(x, y, h int, c uint16) { im.Rect(x, y, 1, h, c) }

func (im *Image) Border(x, y, w, h int, c uint16) {
	im.HLine(x, y, w, c)
	im.HLine(x, y+h-1, w, c)
	im.VLine(x, y, h, c)
	im.VLine(x+w-1, y, h, c)
}

// fontFor returns the bitmap font map for a logical size (10/12/16/20).
func fontFor(size int) map[rune]Glyph {
	switch {
	case size >= 20:
		return font20
	case size >= 16:
		return font16
	case size >= 12:
		return font12
	default:
		return font10
	}
}

func glyphAdvance(size int) int {
	return size/2 + 1 // ASCII advance; CJK glyphs override via their width
}

func runeGlyph(r rune, size int) (Glyph, bool) {
	f := fontFor(size)
	g, ok := f[r]
	if ok {
		return g, true
	}
	g, ok = f[' ']
	return g, ok
}

// TextWidth returns the pixel width of s at the given font size.
func TextWidth(s string, size int) int {
	w := 0
	for _, r := range s {
		g, ok := runeGlyph(r, size)
		if !ok {
			continue
		}
		w += int(g.W) + 1
	}
	return w
}

// Text draws s top-left at (x, y). Returns the advance width.
func (im *Image) Text(x, y int, s string, c uint16, size int) int {
	cx := x
	for _, r := range s {
		g, ok := runeGlyph(r, size)
		if !ok {
			continue
		}
		yy := y + int(g.Off)
		for row := 0; row < int(g.H); row++ {
			bits := g.Rows[row]
			for col := 0; col < int(g.W); col++ {
				if bits&(1<<(int(g.W)-1-col)) != 0 {
					im.Set(cx+col, yy+row, c)
				}
			}
		}
		cx += int(g.W) + 1
	}
	return cx - x
}

// TextCenter draws s horizontally centered.
func (im *Image) TextCenter(y int, s string, c uint16, size int) {
	x := (im.W - TextWidth(s, size)) / 2
	if x < 0 {
		x = 0
	}
	im.Text(x, y, s, c, size)
}

// Bar draws a percentage bar with a frame.
func (im *Image) Bar(x, y, w, h, pct int, fg, bg, border uint16) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	im.Rect(x, y, w, h, bg)
	inner := w - 2
	if inner < 0 {
		inner = 0
	}
	fill := inner * pct / 100
	im.Rect(x+1, y+1, fill, h-2, fg)
	im.Border(x, y, w, h, border)
}

// BufToRGB565 converts an RGB image buffer byte slice (r,g,b per pixel) at
// (x, y) - reserved for future logo/icon support.
func BufToRGB565(pix []byte) uint16 {
	if len(pix) < 3 {
		return 0
	}
	r := uint16(pix[0]) >> 3
	g := uint16(pix[1]) >> 2
	b := uint16(pix[2]) >> 3
	return (r << 11) | (g << 5) | b
}
