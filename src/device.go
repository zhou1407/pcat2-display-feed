package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The photonicat-pm kernel driver owns the MCU UART on /dev/pcat-pm-ctl. It
// accepts complete frames from userspace, rewrites seq and CRC, filters a set
// of kernel-exclusive commands (heartbeat/status/shutdown/watchdog/fan) and
// queues all other MCU->host frames for userspace to read.
//
// Frame: A5 | src(01) | dst(81) | seq LE16 | len LE16 (=payload+3) | cmd LE16 |
//        payload | needAck | CRC16-MODBUS LE | 5A
// CRC covers bytes src..needAck (i.e. frame[1:9+len(payload)]).
const (
	mcuCtlDev = "/dev/pcat-pm-ctl"

	cmdHWVersion    = 0x03
	cmdFWVersion    = 0x05
	cmdFanPIDTarget = 0x97 // MCU PID target temperature, 1 byte Celsius
	cmdChargeLimit  = 0xA5 // set charge threshold, 1 byte 50..100
	cmdChargeGet    = 0xA7 // get charge threshold, reply 1 byte
	cmdLEDBeeper    = 0xAF // 10 bytes: LED[5] + beeper[5]
)

var errMCUTimeout = errors.New("mcu: reply timeout")

func crc16Modbus(b []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, c := range b {
		crc ^= uint16(c)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func buildMCUFrame(cmd uint16, payload []byte, needAck bool) []byte {
	ln := len(payload) + 3
	f := make([]byte, 0, 10+len(payload))
	f = append(f, 0xA5, 0x01, 0x81, 0x00, 0x00, byte(ln), byte(ln>>8), byte(cmd), byte(cmd>>8))
	f = append(f, payload...)
	if needAck {
		f = append(f, 1)
	} else {
		f = append(f, 0)
	}
	crc := crc16Modbus(f[1:])
	return append(f, byte(crc), byte(crc>>8), 0x5A)
}

type mcuFrame struct {
	cmd     uint16
	seq     uint16
	payload []byte
}

// parseMCUFrames returns complete frames plus an unconsumed tail.
func parseMCUFrames(buf []byte) ([]mcuFrame, []byte) {
	var out []mcuFrame
	i := 0
	for {
		for i < len(buf) && buf[i] != 0xA5 {
			i++
		}
		if i >= len(buf) || len(buf)-i < 13 {
			return out, buf[i:]
		}
		ln := int(binary.LittleEndian.Uint16(buf[i+5:]))
		if ln < 3 {
			i++
			continue
		}
		total := ln + 10
		if len(buf)-i < total {
			return out, buf[i:]
		}
		fr := buf[i : i+total]
		if fr[total-1] != 0x5A {
			i++
			continue
		}
		cmd := binary.LittleEndian.Uint16(fr[7:])
		seq := binary.LittleEndian.Uint16(fr[3:])
		payload := append([]byte(nil), fr[9:9+ln-3]...)
		out = append(out, mcuFrame{cmd: cmd, seq: seq, payload: payload})
		i += total
	}
}

// MCU is a serialized client for the photonicat-pm protocol.
type MCU struct {
	mu   sync.Mutex
	file *os.File
}

func NewMCU() *MCU { return &MCU{} }

func (m *MCU) openLocked() error {
	if m.file != nil {
		return nil
	}
	f, err := os.OpenFile(mcuCtlDev, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	m.file = f
	return nil
}

func (m *MCU) closeLocked() {
	if m.file != nil {
		m.file.Close()
		m.file = nil
	}
}

// Transact sends one command and waits for the reply whose command id is cmd+1.
func (m *MCU) Transact(cmd uint16, payload []byte, timeout time.Duration) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.openLocked(); err != nil {
		return nil, err
	}
	if _, err := m.file.Write(buildMCUFrame(cmd, payload, true)); err != nil {
		m.closeLocked()
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, 512)
	chunk := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := m.file.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) {
			m.closeLocked()
			return nil, err
		}
		if len(buf) > 0 {
			frames, rest := parseMCUFrames(buf)
			buf = rest
			for _, f := range frames {
				if f.cmd == cmd+1 {
					return f.payload, nil
				}
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return nil, errMCUTimeout
}

// Close releases the ctl device.
func (m *MCU) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
}

// OutputSchedule is one MCU output channel (power LED or beeper).
type OutputSchedule struct {
	Mode  string `json:"mode"`            // off | on | timed
	Start string `json:"start,omitempty"` // "HH:MM" local time
	End   string `json:"end,omitempty"`   // "HH:MM" local time
}

func parseClock(s string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func localToUTC(hh, mm int) (int, int) {
	now := time.Now()
	t := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, time.Local)
	u := t.UTC()
	return u.Hour(), u.Minute()
}

// scheduleChannel encodes [mode, start_h, start_m, end_h, end_m] in UTC, which
// is the format the MCU expects (same as the vendor userland implementation).
func scheduleChannel(s OutputSchedule) []byte {
	switch s.Mode {
	case "on":
		return []byte{1, 0, 0, 0, 0}
	case "timed":
		sh, sm, ok1 := parseClock(s.Start)
		eh, em, ok2 := parseClock(s.End)
		if !ok1 || !ok2 {
			return []byte{0, 0, 0, 0, 0}
		}
		uh, um := localToUTC(sh, sm)
		vh, vm := localToUTC(eh, em)
		return []byte{2, byte(uh), byte(um), byte(vh), byte(vm)}
	}
	return []byte{0, 0, 0, 0, 0}
}

// rtcTime mirrors Linux struct rtc_time.
type rtcTime struct {
	Sec, Min, Hour, Mday, Mon, Year, Wday, Yday, Isdst int32
}

const rtcSetTime = 0x4024700A // _IOW('p', 0x0a, struct rtc_time)

// SyncMCURTC sets the MCU RTC through /dev/rtc0. The kernel photonicat-pm
// driver owns the DATE_TIME_SYNC command, so writing 0x09 to the ctl device is
// filtered; setting the RTC is the supported path.
func SyncMCURTC() error {
	paths := []string{"/dev/rtc0", "/dev/rtc1"}
	var f *os.File
	var err error
	for _, p := range paths {
		if f, err = os.OpenFile(p, os.O_RDONLY, 0); err == nil {
			break
		}
	}
	if f == nil {
		return fmt.Errorf("no rtc device: %v", err)
	}
	defer f.Close()
	now := time.Now().UTC()
	rt := rtcTime{
		Sec:   int32(now.Second()),
		Min:   int32(now.Minute()),
		Hour:  int32(now.Hour()),
		Mday:  int32(now.Day()),
		Mon:   int32(now.Month() - 1),
		Year:  int32(now.Year() - 1900),
		Wday:  int32(now.Weekday()),
		Yday:  int32(now.YearDay() - 1),
		Isdst: 0,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), rtcSetTime, uintptr(unsafe.Pointer(&rt)))
	if errno != 0 {
		return errno
	}
	return nil
}

// DevCtl is the device-control layer: fan (thermal cooling), charge limit and
// power LED / beeper schedules (MCU).
type DevCtl struct {
	mcu         *MCU
	coolingPath string
}

func NewDevCtl() *DevCtl {
	d := &DevCtl{mcu: NewMCU()}
	d.locateCooling()
	return d
}

func (d *DevCtl) locateCooling() {
	ents, err := filepath.Glob("/sys/class/thermal/cooling_device*")
	if err != nil {
		return
	}
	for _, p := range ents {
		if readFileTrim(p+"/type") == "pcat-pm-fan" {
			d.coolingPath = p
			return
		}
	}
}

// CoolingState returns the current thermal cooling state (0 = MCU PID/auto).
func (d *DevCtl) CoolingState() (int, error) {
	if d.coolingPath == "" {
		return 0, errors.New("pcat-pm-fan cooling device not found")
	}
	return strconv.Atoi(readFileTrim(d.coolingPath + "/cur_state"))
}

// SetFanCooling writes the pcat-pm-fan thermal cooling level (0..9).
func (d *DevCtl) SetFanCooling(level int) error {
	return d.setCooling(level)
}

func (d *DevCtl) setCooling(level int) error {
	if d.coolingPath == "" {
		return errors.New("pcat-pm-fan cooling device not found")
	}
	if level < 0 {
		level = 0
	}
	if level > 9 {
		level = 9
	}
	return os.WriteFile(d.coolingPath+"/cur_state", []byte(strconv.Itoa(level)), 0644)
}

// ApplyFan applies manual level (1..9) or the MCU PID mode (state 0 + target
// temperature: smart_cool = 43 C, smart_quiet = 50 C).
func (d *DevCtl) ApplyFan(mode string, level int) error {
	switch mode {
	case "manual":
		if level < 1 {
			level = 1
		}
		if level > 9 {
			level = 9
		}
		return d.setCooling(level)
	case "smart_cool", "smart_quiet":
		target := 43
		if mode == "smart_quiet" {
			target = 50
		}
		if err := d.setCooling(0); err != nil {
			return err
		}
		_, err := d.mcu.Transact(cmdFanPIDTarget, []byte{byte(target)}, 800*time.Millisecond)
		return err
	}
	return fmt.Errorf("unknown fan mode %q", mode)
}

// ReadChargeLimit reads the MCU charge threshold (0xA7).
func (d *DevCtl) ReadChargeLimit() (int, error) {
	p, err := d.mcu.Transact(cmdChargeGet, nil, 800*time.Millisecond)
	if err != nil {
		return 0, err
	}
	if len(p) < 1 {
		return 0, errors.New("empty charge-limit reply")
	}
	return int(p[0]), nil
}

// SetChargeLimit writes the charge threshold (0xA5, 50..100) and reads it back.
func (d *DevCtl) SetChargeLimit(v int) (int, error) {
	if v < 50 {
		v = 50
	}
	if v > 100 {
		v = 100
	}
	if _, err := d.mcu.Transact(cmdChargeLimit, []byte{byte(v)}, 800*time.Millisecond); err != nil {
		return 0, err
	}
	time.Sleep(120 * time.Millisecond)
	if got, err := d.ReadChargeLimit(); err == nil {
		return got, nil
	}
	return v, nil
}

// ApplyOutputs writes the LED + beeper schedules (0xAF) after syncing the MCU RTC.
func (d *DevCtl) ApplyOutputs(led, beeper OutputSchedule) error {
	payload := append(scheduleChannel(led), scheduleChannel(beeper)...)
	if err := SyncMCURTC(); err != nil {
		log.Printf("pcat2d: mcu rtc sync: %v", err)
	}
	_, err := d.mcu.Transact(cmdLEDBeeper, payload, 1200*time.Millisecond)
	return err
}

// ProbeMCU runs the read-only version/limit probes used by `pcat2d probe`.
func ProbeMCU() {
	m := NewMCU()
	defer m.Close()
	probe := func(name string, cmd uint16) {
		p, err := m.Transact(cmd, nil, 900*time.Millisecond)
		if err != nil {
			fmt.Printf("%-18s cmd=0x%02X  ERROR: %v\n", name, cmd, err)
			return
		}
		fmt.Printf("%-18s cmd=0x%02X  reply=% X  (%d bytes)\n", name, cmd, p, len(p))
	}
	fmt.Println("MCU read-only probe via", mcuCtlDev)
	probe("hw version", cmdHWVersion)
	probe("fw version", cmdFWVersion)
	probe("charge limit", cmdChargeGet)
}
