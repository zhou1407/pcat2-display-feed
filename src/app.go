package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBrightness = 50
	animationFrames   = 15
	animationDuration = 500 * time.Millisecond
	movementPoll      = 100 * time.Millisecond
	movementPath      = "/sys/kernel/photonicat-pm/movement_trigger"
)

// App is the screen daemon: display state machine + input + control socket +
// device control (fan / charge limit / LED / beeper).
type App struct {
	disp   *Display
	sensor *Sensor
	keys   *KeyReader
	dev    *DevCtl
	socket string

	noApply bool // skip device writes at startup (debug/probe phase)

	mu            sync.Mutex
	awake         bool
	page          int
	brightness    int
	lastActivity  time.Time
	keyPresses    int
	info          SysInfo
	cfg           *Config
	mcuErr        string
	chargeActual  int
	fanCooling    int

	stop chan struct{}
}

func NewApp(disp *Display, socket string, noApply bool) *App {
	a := &App{
		disp:    disp,
		sensor:  NewSensor(),
		dev:     NewDevCtl(),
		socket:  socket,
		noApply: noApply,
		cfg:     LoadConfig(),
		stop:    make(chan struct{}),
	}
	if kr, err := OpenPWRKey(true); err != nil {
		log.Printf("pcat2d: no pwrkey device: %v", err)
	} else {
		a.keys = kr
	}
	return a
}

// Run is the daemon main loop.
func (a *App) Run() error {
	log.Printf("pcat2d: start (socket %s)", a.socket)
	a.mu.Lock()
	a.awake = true
	a.page = 0
	if a.cfg.Brightness < 0 || a.cfg.Brightness > 100 {
		a.cfg.Brightness = defaultBrightness
	}
	a.brightness = a.cfg.Brightness
	a.lastActivity = time.Now()
	a.mu.Unlock()

	a.applyDeviceConfigOnStart()

	if err := a.disp.SetBrightnessPct(a.brightness); err != nil {
		log.Printf("pcat2d: backlight: %v", err)
	}
	a.info = a.sensor.Sample()
	a.redraw()

	go a.serveSocket()
	if a.keys != nil {
		go a.keyLoop()
	}
	go a.movementLoop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.tick()
		case <-a.stop:
			a.disp.SetBrightnessPct(0)
			log.Printf("pcat2d: stopped")
			return nil
		}
	}
}

// applyDeviceConfigOnStart applies persisted fan/LED/beeper settings and reads
// the current charge limit. Writes are skipped with -noapply.
func (a *App) applyDeviceConfigOnStart() {
	if a.noApply {
		log.Printf("pcat2d: -noapply: skipping fan/output writes at startup")
	}
	var errs []string
	if !a.noApply {
		if err := a.dev.ApplyFan(a.cfg.FanMode, a.cfg.FanLevel); err != nil {
			log.Printf("pcat2d: apply fan: %v", err)
			errs = append(errs, "fan: "+err.Error())
		}
	}
	if v, err := a.dev.ReadChargeLimit(); err != nil {
		log.Printf("pcat2d: read charge limit: %v", err)
		errs = append(errs, "charge: "+err.Error())
	} else {
		log.Printf("pcat2d: mcu charge limit %d%%", v)
		a.chargeActual = v
	}
	if !a.noApply && configExists() {
		if err := a.dev.ApplyOutputs(a.cfg.LED, a.cfg.Beeper); err != nil {
			log.Printf("pcat2d: apply led/beeper: %v", err)
			errs = append(errs, "outputs: "+err.Error())
		}
	}
	if len(errs) > 0 {
		a.mu.Lock()
		a.mcuErr = strings.Join(errs, "; ")
		a.mu.Unlock()
	}
}

func (a *App) tick() {
	info := a.sensor.Sample()

	a.mu.Lock()
	a.info = info
	awake := a.awake
	page := a.page
	last := a.lastActivity
	cfg := a.cfg
	a.mu.Unlock()

	if !awake {
		return
	}
	timeout := time.Duration(cfg.DimBatterySec) * time.Second
	if info.ChargerOnline {
		timeout = time.Duration(cfg.DimChargerSec) * time.Second
	}
	if timeout > 0 && time.Since(last) > timeout {
		a.mu.Lock()
		a.awake = false
				a.mu.Unlock()
		a.disp.SetBrightnessPct(0)
		log.Printf("pcat2d: screen off (idle %s)", time.Since(last).Round(time.Second))
		return
	}
	if _, err := a.disp.ShowDiff(a.renderPageIdx(page, info)); err != nil {
		log.Printf("pcat2d: flush: %v", err)
	}
}

func (a *App) redraw() {
	a.mu.Lock()
	page := a.page
	info := a.info
	a.mu.Unlock()
	if err := a.disp.ShowFull(a.renderPageIdx(page, info)); err != nil {
		log.Printf("pcat2d: flush: %v", err)
	}
}

func (a *App) keyLoop() {
	for pressed := range a.keys.ch {
		if pressed {
			a.onKey()
		}
	}
}

func (a *App) onKey() {
	a.mu.Lock()
	a.keyPresses++
	n := a.keyPresses
	a.lastActivity = time.Now()
	wasAwake := a.awake
	a.awake = true
	brightness := a.brightness
	a.mu.Unlock()

	log.Printf("pcat2d: KEY_POWER press #%d", n)
	if !wasAwake {
		a.disp.SetBrightnessPct(brightness)
		a.redraw()
		return
	}
	a.nextPage()
}

// nextPage switches to the next page with a top-down wipe. Each animation step
// writes only the newly revealed band, so no single SPI burst exceeds one panel
// scan-out and the transition has no full-frame tearing.
func (a *App) nextPage() {
	a.mu.Lock()
	to := (a.page + 1) % pageCount
	a.page = to
	info := a.info
	a.mu.Unlock()

	newImg := a.renderPageIdx(to, info)
	if err := a.disp.WipeTo(newImg, animationFrames, animationDuration); err != nil {
		log.Printf("pcat2d: wipe: %v", err)
	}
}

func (a *App) movementLoop() {
	t := time.NewTicker(movementPoll)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			a.mu.Lock()
			enabled := a.cfg.Movement
			a.mu.Unlock()
			if !enabled {
				continue
			}
			b, err := os.ReadFile(movementPath)
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(b)) != "1" {
				continue
			}
			a.mu.Lock()
			a.lastActivity = time.Now()
			was := a.awake
			a.awake = true
			brightness := a.brightness
			a.mu.Unlock()
			if !was {
				log.Printf("pcat2d: movement wake")
				a.disp.SetBrightnessPct(brightness)
				a.redraw()
			}
		case <-a.stop:
			return
		}
	}
}

// uiState returns the display state used by page rendering.
func (a *App) uiState() (awake bool, page, brightness, keyPresses int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.awake, a.page, a.brightness, a.keyPresses
}
func (a *App) status() statusOut {
	a.mu.Lock()
	info := a.info
	memPct := 0
	if info.MemTotalKB > 0 {
		memPct = 100 * (info.MemTotalKB - info.MemAvailableKB) / info.MemTotalKB
	}
	cfg := a.cfg
	awake, page, brightness := a.awake, a.page, a.brightness
	keyPresses, mcuErr, chargeActual, fanCooling := a.keyPresses, a.mcuErr, a.chargeActual, a.fanCooling
	a.mu.Unlock()

	return statusOut{
		Awake:         awake,
		Page:          page,
		Pages:         pageCount,
		Brightness:    brightness,
		KeyPresses:    keyPresses,
		BatteryPct:    info.BatteryPct,
		BatteryMV:     info.BatteryMV,
		BatteryMA:     info.BatteryMA,
		BatteryStatus: info.BatteryStatus,
		ChargerOnline: info.ChargerOnline,
		BoardTempC:    info.BoardTempC,
		FanRPM:        info.FanRPM,
		CPU:           info.CPU,
		MemPct:        memPct,
		UptimeSec:     info.UptimeSec,
		OSVersion:     info.OSVersion,
		MaxSpeedHz:    a.disp.maxSpeed,

		FanMode:       cfg.FanMode,
		FanLevel:      cfg.FanLevel,
		FanCooling:    fanCooling,
		ChargeLimit:   cfg.ChargeLimit,
		ChargeLimitActual: chargeActual,
		LEDMode:       cfg.LED.Mode,
		LEDStart:      cfg.LED.Start,
		LEDEnd:        cfg.LED.End,
		BeeperMode:    cfg.Beeper.Mode,
		BeeperStart:   cfg.Beeper.Start,
		BeeperEnd:     cfg.Beeper.End,
		DimBatterySec: cfg.DimBatterySec,
		DimChargerSec: cfg.DimChargerSec,
		Movement:      cfg.Movement,
		MCUErr:        mcuErr,
	}
}

func (a *App) serveSocket() {
	path := a.socket
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("pcat2d: control socket %s: %v", path, err)
		return
	}
	os.Chmod(path, 0660)
	log.Printf("pcat2d: control socket ready")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go a.handleConn(conn)
	}
}

func (a *App) handleConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	dec := json.NewDecoder(bufio.NewReader(conn))
	var req ctlRequest
	if err := dec.Decode(&req); err != nil {
		return
	}
	resp := a.execCtl(req)
	json.NewEncoder(conn).Encode(resp)
}

func (a *App) execCtl(req ctlRequest) ctlResponse {
	switch req.Cmd {
	case "status":
		st := a.status()
		return ctlResponse{OK: true, Status: &st}

	case "set":
		return a.handleSet(req.Arg)

	case "screen":
		switch req.Arg {
		case "on":
			a.mu.Lock()
			a.awake = true
			a.lastActivity = time.Now()
			brightness := a.brightness
			a.mu.Unlock()
			a.disp.SetBrightnessPct(brightness)
			a.redraw()
			return ctlResponse{OK: true, Msg: "screen on"}
		case "off":
			a.mu.Lock()
			a.awake = false
			a.mu.Unlock()
			a.disp.SetBrightnessPct(0)
			return ctlResponse{OK: true, Msg: "screen off"}
		}
		return ctlResponse{OK: false, Err: "usage: screen on|off"}

	case "brightness":
		v, err := strconv.Atoi(req.Arg)
		if err != nil || v < 0 || v > 100 {
			return ctlResponse{OK: false, Err: "usage: brightness 0..100"}
		}
		a.mu.Lock()
		a.brightness = v
		a.cfg.Brightness = v
		awake := a.awake
		cfg := a.cfg
		a.mu.Unlock()
		cfg.Save()
		if awake {
			a.disp.SetBrightnessPct(v)
		}
		return ctlResponse{OK: true, Msg: fmt.Sprintf("brightness %d%%", v)}

	case "next":
		a.mu.Lock()
		a.lastActivity = time.Now()
		awake := a.awake
		a.mu.Unlock()
		if awake {
			a.nextPage()
		}
		return ctlResponse{OK: true, Msg: "next page"}

	case "page":
		v, err := strconv.Atoi(req.Arg)
		if err != nil || v < 0 || v >= pageCount {
			return ctlResponse{OK: false, Err: fmt.Sprintf("usage: page 0..%d", pageCount-1)}
		}
		a.mu.Lock()
		a.page = v
		a.awake = true
		a.lastActivity = time.Now()
		brightness := a.brightness
		a.mu.Unlock()
		a.disp.SetBrightnessPct(brightness)
		a.redraw()
		return ctlResponse{OK: true, Msg: fmt.Sprintf("page %d", v)}

	case "reload":
		a.redraw()
		return ctlResponse{OK: true, Msg: "reloaded"}
	}
	return ctlResponse{OK: false, Err: "unknown command: " + req.Cmd}
}

// setReq is the JSON payload accepted by the `set` command (and thus by the
// rpcd/LuCI bridge). Every field is optional; only provided fields change.
type setReq struct {
	FanMode       *string `json:"fan_mode"`
	FanLevel      *int    `json:"fan_level"`
	ChargeLimit   *int    `json:"charge_limit"`
	LEDMode       *string `json:"led_mode"`
	LEDStart      *string `json:"led_start"`
	LEDEnd        *string `json:"led_end"`
	BeeperMode    *string `json:"beeper_mode"`
	BeeperStart   *string `json:"beeper_start"`
	BeeperEnd     *string `json:"beeper_end"`
	Brightness    *int    `json:"brightness"`
	DimBatterySec *int    `json:"dim_battery_sec"`
	DimChargerSec *int    `json:"dim_charger_sec"`
	Movement      *bool   `json:"movement"`
}

// unwrapData accepts both a flat object and the {"data": {...}} wrapper that
// rpcd produces when a LuCI rpc.declare call uses params: ['data'].
func unwrapData(raw string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	if d, ok := m["data"]; ok {
		var s string
		if json.Unmarshal(d, &s) == nil {
			return s
		}
		return string(d)
	}
	return raw
}

func validSchedule(s OutputSchedule) error {
	switch s.Mode {
	case "off", "on":
		return nil
	case "timed":
		if _, _, ok := parseClock(s.Start); !ok {
			return fmt.Errorf("bad start time %q", s.Start)
		}
		if _, _, ok := parseClock(s.End); !ok {
			return fmt.Errorf("bad end time %q", s.End)
		}
		return nil
	}
	return fmt.Errorf("bad mode %q", s.Mode)
}

func (a *App) handleSet(raw string) ctlResponse {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ctlResponse{OK: false, Err: `usage: set '{"...":...}'`}
	}
	raw = unwrapData(raw)
	var p setReq
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return ctlResponse{OK: false, Err: "bad json: " + err.Error()}
	}

	a.mu.Lock()
	cfg := a.cfg.clone()
	a.mu.Unlock()

	if p.FanMode != nil {
		switch *p.FanMode {
		case "smart_cool", "smart_quiet", "manual":
			cfg.FanMode = *p.FanMode
		default:
			return ctlResponse{OK: false, Err: "fan_mode must be smart_cool|smart_quiet|manual"}
		}
	}
	if p.FanLevel != nil {
		if *p.FanLevel < 1 || *p.FanLevel > 9 {
			return ctlResponse{OK: false, Err: "fan_level must be 1..9"}
		}
		cfg.FanLevel = *p.FanLevel
	}
	if p.ChargeLimit != nil {
		if *p.ChargeLimit < 50 || *p.ChargeLimit > 100 {
			return ctlResponse{OK: false, Err: "charge_limit must be 50..100"}
		}
		cfg.ChargeLimit = *p.ChargeLimit
	}
	if p.Brightness != nil {
		if *p.Brightness < 0 || *p.Brightness > 100 {
			return ctlResponse{OK: false, Err: "brightness must be 0..100"}
		}
		cfg.Brightness = *p.Brightness
	}
	if p.DimBatterySec != nil {
		if *p.DimBatterySec < 0 || *p.DimBatterySec > 86400 {
			return ctlResponse{OK: false, Err: "dim_battery_sec must be 0..86400"}
		}
		cfg.DimBatterySec = *p.DimBatterySec
	}
	if p.DimChargerSec != nil {
		if *p.DimChargerSec < 0 || *p.DimChargerSec > 86400 {
			return ctlResponse{OK: false, Err: "dim_charger_sec must be 0..86400"}
		}
		cfg.DimChargerSec = *p.DimChargerSec
	}
	if p.Movement != nil {
		cfg.Movement = *p.Movement
	}
	if p.LEDMode != nil {
		cfg.LED.Mode = *p.LEDMode
	}
	if p.LEDStart != nil {
		cfg.LED.Start = *p.LEDStart
	}
	if p.LEDEnd != nil {
		cfg.LED.End = *p.LEDEnd
	}
	if p.BeeperMode != nil {
		cfg.Beeper.Mode = *p.BeeperMode
	}
	if p.BeeperStart != nil {
		cfg.Beeper.Start = *p.BeeperStart
	}
	if p.BeeperEnd != nil {
		cfg.Beeper.End = *p.BeeperEnd
	}
	if err := validSchedule(cfg.LED); err != nil {
		return ctlResponse{OK: false, Err: "led: " + err.Error()}
	}
	if err := validSchedule(cfg.Beeper); err != nil {
		return ctlResponse{OK: false, Err: "beeper: " + err.Error()}
	}

	if err := cfg.Save(); err != nil {
		return ctlResponse{OK: false, Err: "save: " + err.Error()}
	}

	fanChanged := p.FanMode != nil || p.FanLevel != nil
	outputsChanged := p.LEDMode != nil || p.LEDStart != nil || p.LEDEnd != nil ||
		p.BeeperMode != nil || p.BeeperStart != nil || p.BeeperEnd != nil
	brightnessChanged := p.Brightness != nil

	a.mu.Lock()
	a.cfg = cfg
	if brightnessChanged {
		a.brightness = cfg.Brightness
	}
	awake := a.awake
	a.mu.Unlock()

	var errs []string
	if brightnessChanged && awake {
		if err := a.disp.SetBrightnessPct(cfg.Brightness); err != nil {
			errs = append(errs, "backlight: "+err.Error())
		}
	}
	if fanChanged {
		if err := a.dev.ApplyFan(cfg.FanMode, cfg.FanLevel); err != nil {
			errs = append(errs, "fan: "+err.Error())
		} else if cs, err := a.dev.CoolingState(); err == nil {
			a.mu.Lock()
			a.fanCooling = cs
			a.mu.Unlock()
		}
	}
	if outputsChanged {
		if err := a.dev.ApplyOutputs(cfg.LED, cfg.Beeper); err != nil {
			errs = append(errs, "outputs: "+err.Error())
		}
	}
	if p.ChargeLimit != nil {
		if actual, err := a.dev.SetChargeLimit(cfg.ChargeLimit); err != nil {
			errs = append(errs, "charge: "+err.Error())
		} else {
			a.mu.Lock()
			a.chargeActual = actual
			a.mu.Unlock()
		}
	}

	msg := "saved"
	if len(errs) > 0 {
		a.mu.Lock()
		a.mcuErr = strings.Join(errs, "; ")
		a.mu.Unlock()
		msg = "saved with device warnings: " + strings.Join(errs, "; ")
	}

	st := a.status()
	return ctlResponse{OK: true, Msg: msg, Status: &st}
}
