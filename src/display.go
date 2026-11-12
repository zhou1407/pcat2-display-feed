package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Panel / board constants (verified on stock ImmortalWrt r42943, kernel 6.18.55)
const (
	PanelW = 172
	PanelH = 320

	ColOffset = 34
	RowOffset = 0

	spidevPath  = "/dev/spidev1.0"
	spiSpeedHz  = 50000000
	dcGPIO      = 121 // GPIO3_D1
	rstGPIO     = 122 // GPIO3_D2
	backlightDir = "/sys/class/backlight/backlight"
)

// Linux ioctl numbers from linux/spi/spidev.h for __u8 / __u32 arguments.
const (
	spiIOCWRMode     = 0x40016b01
	spiIOCRDMode     = 0x80016b01
	spiIOCWRBits     = 0x40016b03
	spiIOCRDBits     = 0x80016b03
	spiIOCWRMaxSpeed = 0x40046b04
	spiIOCRDMaXSpeed = 0x80046b04
)

// Display is the GC9307 SPI panel driven from userspace.
type Display struct {
	spi *os.File
	dc  *os.File
	rst *os.File

	bufsiz   int
	maxSpeed uint32
	blMax    int
	blPct    int

	fb []byte // PanelW*PanelH*2 big-endian RGB565
	last *Image // last frame pushed, for dirty-band diffing

	mu sync.Mutex
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

func gpioExport(n int) error {
	dir := fmt.Sprintf("/sys/class/gpio/gpio%d", n)
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.WriteFile("/sys/class/gpio/export", []byte(strconv.Itoa(n)), 0); err != nil {
		return fmt.Errorf("export gpio%d: %w", n, err)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(dir); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("export gpio%d: timeout", n)
}

func gpioOpen(n int, out bool) (*os.File, error) {
	if err := gpioExport(n); err != nil {
		return nil, err
	}
	dir := "in"
	if out {
		dir = "out"
	}
	if err := os.WriteFile(fmt.Sprintf("/sys/class/gpio/gpio%d/direction", n), []byte(dir), 0); err != nil {
		return nil, fmt.Errorf("gpio%d direction: %w", n, err)
	}
	f, err := os.OpenFile(fmt.Sprintf("/sys/class/gpio/gpio%d/value", n), os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("gpio%d value: %w", n, err)
	}
	return f, nil
}

func gpioWrite(f *os.File, v int) {
	if f == nil {
		return
	}
	b := []byte("0")
	if v != 0 {
		b[0] = '1'
	}
	f.Seek(0, 0)
	f.Write(b)
}

func readIntFile(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// OpenDisplay opens spidev + GPIOs, raises SPI speed, initializes the panel.
func OpenDisplay() (*Display, error) {
	f, err := os.OpenFile(spidevPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", spidevPath, err)
	}
	d := &Display{spi: f, bufsiz: 4096}

	if b, err := os.ReadFile("/sys/module/spidev/parameters/bufsiz"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
			d.bufsiz = n
		}
	}

	var cur uint32
	if err := ioctl(f.Fd(), spiIOCRDMaXSpeed, unsafe.Pointer(&cur)); err == nil {
		log.Printf("display: spi default max_speed_hz=%d", cur)
	}
	want := uint32(spiSpeedHz)
	if err := ioctl(f.Fd(), spiIOCWRMaxSpeed, unsafe.Pointer(&want)); err != nil {
		log.Printf("display: set spi speed: %v", err)
	}
	var got uint32
	if err := ioctl(f.Fd(), spiIOCRDMaXSpeed, unsafe.Pointer(&got)); err == nil {
		d.maxSpeed = got
		log.Printf("display: spi effective max_speed_hz=%d", got)
	}
	mode := uint8(0)
	if err := ioctl(f.Fd(), spiIOCWRMode, unsafe.Pointer(&mode)); err != nil {
		log.Printf("display: set mode: %v", err)
	}
	bits := uint8(8)
	if err := ioctl(f.Fd(), spiIOCWRBits, unsafe.Pointer(&bits)); err != nil {
		log.Printf("display: set bits: %v", err)
	}

	if d.dc, err = gpioOpen(dcGPIO, true); err != nil {
		return nil, err
	}
	if d.rst, err = gpioOpen(rstGPIO, true); err != nil {
		return nil, err
	}

	if b, err := os.ReadFile(backlightDir + "/max_brightness"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
			d.blMax = n
		}
	}
	if d.blMax <= 0 {
		d.blMax = 1666
	}

	d.fb = make([]byte, PanelW*PanelH*2)

	if err := d.initPanel(); err != nil {
		return nil, err
	}
	log.Printf("display: gc9307 ready (172x320, %d Hz, backlight max %d)", d.maxSpeed, d.blMax)
	return d, nil
}

func (d *Display) Close() {
	if d.dc != nil {
		d.dc.Close()
	}
	if d.rst != nil {
		d.rst.Close()
	}
	if d.spi != nil {
		d.spi.Close()
	}
}

func (d *Display) cmd(b byte) error {
	gpioWrite(d.dc, 0)
	_, err := d.spi.Write([]byte{b})
	return err
}

func (d *Display) sendData(p []byte) error {
	gpioWrite(d.dc, 1)
	for len(p) > 0 {
		n := len(p)
		if n > d.bufsiz {
			n = d.bufsiz
		}
		if _, err := d.spi.Write(p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

func (d *Display) cmdData(b byte, data []byte) error {
	if err := d.cmd(b); err != nil {
		return err
	}
	return d.sendData(data)
}

func (d *Display) setWindow(x0, y0, x1, y1 int) error {
	caset := []byte{
		byte((x0 + ColOffset) >> 8), byte(x0 + ColOffset),
		byte((x1 + ColOffset) >> 8), byte(x1 + ColOffset),
	}
	raset := []byte{
		byte((y0 + RowOffset) >> 8), byte(y0 + RowOffset),
		byte((y1 + RowOffset) >> 8), byte(y1 + RowOffset),
	}
	if err := d.cmdData(0x2A, caset); err != nil {
		return err
	}
	return d.cmdData(0x2B, raset)
}

// initPanel sequence verified on hardware (identical to the bring-up test program).
func (d *Display) initPanel() error {
	gpioWrite(d.rst, 0)
	time.Sleep(20 * time.Millisecond)
	gpioWrite(d.rst, 1)
	time.Sleep(120 * time.Millisecond)

	if err := d.cmd(0x01); err != nil { // SWRESET
		return err
	}
	time.Sleep(120 * time.Millisecond)
	if err := d.cmd(0x11); err != nil { // SLPOUT
		return err
	}
	time.Sleep(120 * time.Millisecond)

	if err := d.cmdData(0x3A, []byte{0x55}); err != nil { // COLMOD 16bpp
		return err
	}
	if err := d.cmdData(0x36, []byte{0x48}); err != nil { // MADCTL MX|BGR (verified colors)
		return err
	}
	if err := d.cmd(0x20); err != nil { // INVOFF
		return err
	}
	if err := d.cmd(0x13); err != nil { // NORON
		return err
	}
	if err := d.cmd(0x29); err != nil { // DISPON
		return err
	}
	time.Sleep(120 * time.Millisecond)
	return nil
}

// SetBrightnessPct sets the backlight percentage (0=off, 100=brightest).
//
// Hardware-verified polarity: brightness 0 disables the PWM (screen off) and
// max_brightness (1666) is the brightest level; the kernel's built-in level
// table is already CIE1931-corrected (pwm_bl default table), so a plain linear
// percentage -> level mapping gives perceptually linear brightness.
func (d *Display) SetBrightnessPct(pct int) error {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	val := d.blMax * pct / 100
	if err := os.WriteFile(backlightDir+"/brightness", []byte(strconv.Itoa(val)), 0); err != nil {
		return fmt.Errorf("backlight: %w", err)
	}
	d.blPct = pct
	return nil
}
func (d *Display) BrightnessPct() int { return d.blPct }

// bandHeight is the dirty-band granularity (rows) for periodic refreshes.
// PanelH is a multiple of it.
const bandHeight = 8

// ShowFull writes the whole image to the panel and records it as last frame.
func (d *Display) ShowFull(im *Image) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.showFullLocked(im)
}

func (d *Display) showFullLocked(im *Image) error {
	fb := d.fb
	for i, p := range im.Pix {
		fb[2*i] = byte(p >> 8)
		fb[2*i+1] = byte(p)
	}
	if err := d.setWindow(0, 0, PanelW-1, PanelH-1); err != nil {
		return err
	}
	if err := d.cmd(0x2C); err != nil { // RAMWR
		return err
	}
	if err := d.sendData(fb); err != nil {
		return err
	}
	d.saveLastLocked(im)
	return nil
}

// Flush keeps the old name for a full-frame update.
func (d *Display) Flush(im *Image) error { return d.ShowFull(im) }

func (d *Display) saveLastLocked(im *Image) {
	if d.last == nil {
		d.last = NewImage(PanelW, PanelH)
	}
	copy(d.last.Pix, im.Pix)
}

// writeRowsLocked pushes rows y0..y1 (inclusive) of im. Caller holds d.mu.
func (d *Display) writeRowsLocked(im *Image, y0, y1 int) error {
	if y1 < y0 {
		return nil
	}
	n := (y1 - y0 + 1) * PanelW
	fb := d.fb[:2*n]
	for i := 0; i < n; i++ {
		p := im.Pix[y0*PanelW+i]
		fb[2*i] = byte(p >> 8)
		fb[2*i+1] = byte(p)
	}
	if err := d.setWindow(0, y0, PanelW-1, y1); err != nil {
		return err
	}
	if err := d.cmd(0x2C); err != nil { // RAMWR
		return err
	}
	return d.sendData(fb)
}

// ShowDiff pushes only the bands of rows that differ from the last frame.
// Each 8-row band is ~2.7 KB and writes in well under one 60 Hz scan-out
// (16.7 ms), so periodic 1 Hz refreshes do not tear.
func (d *Display) ShowDiff(im *Image) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last == nil {
		return true, d.showFullLocked(im)
	}
	bands := PanelH / bandHeight
	start := -1
	changed := false
	for b := 0; b < bands; b++ {
		y0 := b * bandHeight
		diff := false
		for y := y0; y < y0+bandHeight && !diff; y++ {
			row := im.Pix[y*PanelW : (y+1)*PanelW]
			last := d.last.Pix[y*PanelW : (y+1)*PanelW]
			for x := range row {
				if row[x] != last[x] {
					diff = true
					break
				}
			}
		}
		if diff {
			changed = true
			if start < 0 {
				start = y0
			}
		} else if start >= 0 {
			if err := d.writeRowsLocked(im, start, y0-1); err != nil {
				return changed, err
			}
			start = -1
		}
	}
	if start >= 0 {
		if err := d.writeRowsLocked(im, start, PanelH-1); err != nil {
			return changed, err
		}
	}
	if changed {
		d.saveLastLocked(im)
	}
	return changed, nil
}

// WipeTo reveals im with a top-down curtain over the given total duration.
// Each step writes only the newly revealed rows, so no single SPI burst
// exceeds one panel scan-out and the transition has no full-frame tearing.
func (d *Display) WipeTo(im *Image, steps int, total time.Duration) error {
	if steps < 1 {
		steps = 1
	}
	step := total / time.Duration(steps)
	d.mu.Lock()
	defer d.mu.Unlock()
	prev := 0
	for s := 1; s <= steps; s++ {
		t0 := time.Now()
		y1 := PanelH * s / steps
		if y1 <= prev {
			continue
		}
		if err := d.writeRowsLocked(im, prev, y1-1); err != nil {
			return err
		}
		prev = y1
		if remain := step - time.Since(t0); remain > 0 {
			time.Sleep(remain)
		}
	}
	if prev < PanelH {
		if err := d.writeRowsLocked(im, prev, PanelH-1); err != nil {
			return err
		}
	}
	d.saveLastLocked(im)
	return nil
}