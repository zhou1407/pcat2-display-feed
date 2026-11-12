package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	evKey    = 0x01
	keyPower = 116 // KEY_POWER
	evIOCGrab = 0x40044590
)

// KeyReader delivers KEY_POWER press/release events from the rk805 pwrkey
// evdev device: ch receives true on press, false on release.
type KeyReader struct {
	f    *os.File
	ch   chan bool
	done chan struct{}
}

// findPWREvent locates /dev/input/eventN for the rk805 PMIC power key.
func findPWREvent() (string, error) {
	b, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return "", err
	}
	for _, blk := range strings.Split(string(b), "\n\n") {
		var name, handlers string
		for _, ln := range strings.Split(blk, "\n") {
			ln = strings.TrimSpace(ln)
			if strings.HasPrefix(ln, "N: Name=") {
				name = strings.Trim(strings.TrimPrefix(ln, "N: Name="), "\"")
			} else if strings.HasPrefix(ln, "H: Handlers=") {
				handlers = strings.TrimPrefix(ln, "H: Handlers=")
			}
		}
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if name != "rk805 pwrkey" && !strings.Contains(lower, "pwrkey") && !strings.Contains(lower, "power button") {
			continue
		}
		for _, h := range strings.Fields(handlers) {
			if strings.HasPrefix(h, "event") {
				return "/dev/input/" + h, nil
			}
		}
	}
	return "", fmt.Errorf("no power key input device found in /proc/bus/input/devices")
}

// OpenPWRKey opens the power key evdev device (optionally grabbing it).
func OpenPWRKey(grab bool) (*KeyReader, error) {
	path, err := findPWREvent()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	if grab {
		var one int32 = 1
		syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), evIOCGrab, uintptr(unsafe.Pointer(&one)))
	}
	kr := &KeyReader{f: f, ch: make(chan bool, 16), done: make(chan struct{})}
	go kr.loop()
	return kr, nil
}

func (kr *KeyReader) loop() {
	buf := make([]byte, 24*16)
	for {
		n, err := kr.f.Read(buf)
		if err != nil {
			close(kr.done)
			return
		}
		for off := 0; off+24 <= n; off += 24 {
			ev := buf[off : off+24]
			typ := binary.LittleEndian.Uint16(ev[16:18])
			code := binary.LittleEndian.Uint16(ev[18:20])
			val := int32(binary.LittleEndian.Uint32(ev[20:24]))
			if typ == evKey && code == keyPower {
				select {
				case kr.ch <- val == 1:
				default:
				}
			}
		}
	}
}

func (kr *KeyReader) Close() {
	if kr.f != nil {
		kr.f.Close()
	}
}
