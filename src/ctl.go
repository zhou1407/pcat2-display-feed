package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

// ctlRequest / ctlResponse are the Unix-socket protocol (one JSON object each way).
type ctlRequest struct {
	Cmd string `json:"cmd"`
	Arg string `json:"arg,omitempty"`
}

type ctlResponse struct {
	OK     bool       `json:"ok"`
	Err    string     `json:"err,omitempty"`
	Msg    string     `json:"msg,omitempty"`
	Status *statusOut `json:"status,omitempty"`
}

// statusOut is the full daemon status, also used by the LuCI page via rpcd.
type statusOut struct {
	Awake         bool   `json:"awake"`
	Page          int    `json:"page"`
	Pages         int    `json:"pages"`
	Brightness    int    `json:"brightness"`
	KeyPresses    int    `json:"key_presses"`
	BatteryPct    int    `json:"battery_pct"`
	BatteryMV     int    `json:"battery_mv"`
	BatteryMA     int    `json:"battery_ma"`
	BatteryStatus string `json:"battery_status"`
	ChargerOnline bool   `json:"charger_online"`
	BoardTempC    int    `json:"board_temp_c"`
	FanRPM        int    `json:"fan_rpm"`
	CPU           []int  `json:"cpu"`
	MemPct        int    `json:"mem_pct"`
	UptimeSec     int64  `json:"uptime_sec"`
	OSVersion     string `json:"os_version"`
	MaxSpeedHz    uint32 `json:"spi_max_speed_hz"`

	FanMode           string `json:"fan_mode"`
	FanLevel          int    `json:"fan_level"`
	FanCooling        int    `json:"fan_cooling"`
	ChargeLimit       int    `json:"charge_limit"`
	ChargeLimitActual int    `json:"charge_limit_actual"`
	LEDMode           string `json:"led_mode"`
	LEDStart          string `json:"led_start"`
	LEDEnd            string `json:"led_end"`
	BeeperMode        string `json:"beeper_mode"`
	BeeperStart       string `json:"beeper_start"`
	BeeperEnd         string `json:"beeper_end"`
	DimBatterySec     int    `json:"dim_battery_sec"`
	DimChargerSec     int    `json:"dim_charger_sec"`
	Movement          bool   `json:"movement"`
	MCUErr            string `json:"mcu_err,omitempty"`
}

const usage = `usage: pcat2d ctl [-s socket] <command> [arg]
commands:
  status
  screen on|off
  brightness 0..100
  next
  page 0..2
  reload
  set '<json>'   (fan_mode, fan_level, charge_limit, led_*, beeper_*,
                  brightness, dim_battery_sec, dim_charger_sec, movement)`

// ctlMain runs the pcat2ctl-style client against the daemon socket.
func ctlMain(args []string) int {
	socket := "/var/run/pcat2d.sock"
	if len(args) >= 2 && args[0] == "-s" {
		socket = args[1]
		args = args[2:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	req := ctlRequest{Cmd: args[0]}
	if len(args) > 1 {
		req.Arg = strings.Join(args[1:], " ")
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pcat2d ctl: %v\n", err)
		return 1
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		fmt.Fprintf(os.Stderr, "pcat2d ctl: send: %v\n", err)
		return 1
	}
	var resp ctlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		fmt.Fprintf(os.Stderr, "pcat2d ctl: recv: %v\n", err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Err)
		return 1
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
	return 0
}
