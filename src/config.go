package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// configPath is the persistent daemon configuration. It is a plain uci file so
// that both the daemon and LuCI (via uci) can read/write it without any extra
// service glue. The daemon parses the file directly to avoid a uci CLI runtime
// dependency.
const configPath = "/etc/config/pcat2d"

// Config holds all persistent settings.
type Config struct {
	FanMode       string         `json:"fan_mode"`       // smart_cool | smart_quiet | manual
	FanLevel      int            `json:"fan_level"`      // 1..9 (manual)
	ChargeLimit   int            `json:"charge_limit"`   // 50..100 %
	LED           OutputSchedule `json:"led"`            // power LED
	Beeper        OutputSchedule `json:"beeper"`         // beeper
	Brightness    int            `json:"brightness"`     // 0..100 %
	DimBatterySec int            `json:"dim_battery_sec"` // screen-off timeout on battery
	DimChargerSec int            `json:"dim_charger_sec"` // screen-off timeout while charging
	Movement      bool           `json:"movement"`        // movement wake enabled
	MonthlyLimitGB int           `json:"monthly_limit_gb"` // 5G monthly traffic cap (GB)
}

func defaultConfig() *Config {
	return &Config{
		FanMode:       "smart_cool",
		FanLevel:      4,
		ChargeLimit:   100,
		LED:           OutputSchedule{Mode: "on", Start: "08:00", End: "22:00"},
		Beeper:        OutputSchedule{Mode: "on", Start: "08:00", End: "22:00"},
		Brightness:    50,
		DimBatterySec: 60,
		DimChargerSec: 300,
		Movement:      true,
		MonthlyLimitGB: 9999,
	}
}

// LoadConfig reads /etc/config/pcat2d. Missing file or invalid values keep the
// defaults, so the daemon always starts with a usable configuration.
func LoadConfig() *Config {
	c := defaultConfig()
	f, err := os.Open(configPath)
	if err != nil {
		return c
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "option ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "option "))
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
		switch key {
		case "fan_mode":
			switch val {
			case "smart_cool", "smart_quiet", "manual":
				c.FanMode = val
			}
		case "fan_level":
			if v, err := strconv.Atoi(val); err == nil && v >= 1 && v <= 9 {
				c.FanLevel = v
			}
		case "charge_limit":
			if v, err := strconv.Atoi(val); err == nil && v >= 50 && v <= 100 {
				c.ChargeLimit = v
			}
		case "led_mode", "led_start", "led_end":
			setScheduleField(&c.LED, key, val)
		case "beeper_mode", "beeper_start", "beeper_end":
			setScheduleField(&c.Beeper, key, val)
		case "brightness":
			if v, err := strconv.Atoi(val); err == nil && v >= 0 && v <= 100 {
				c.Brightness = v
			}
		case "dim_battery_sec":
			if v, err := strconv.Atoi(val); err == nil && v >= 0 {
				c.DimBatterySec = v
			}
		case "dim_charger_sec":
			if v, err := strconv.Atoi(val); err == nil && v >= 0 {
				c.DimChargerSec = v
			}
		case "movement":
			c.Movement = val == "1" || val == "true" || val == "on"
		case "monthly_limit_gb":
			if v, err := strconv.Atoi(val); err == nil && v >= 1 && v <= 9999 {
				c.MonthlyLimitGB = v
			}
		}
	}
	return c
}

func setScheduleField(s *OutputSchedule, key, val string) {
	switch key {
	case "led_mode", "beeper_mode":
		switch val {
		case "off", "on", "timed":
			s.Mode = val
		}
	case "led_start", "beeper_start":
		s.Start = val
	case "led_end", "beeper_end":
		s.End = val
	}
}

// Save writes the configuration atomically to configPath.
func (c *Config) Save() error {
	var b strings.Builder
	b.WriteString("config pcat2d 'global'\n")
	fmt.Fprintf(&b, "\toption fan_mode '%s'\n", c.FanMode)
	fmt.Fprintf(&b, "\toption fan_level '%d'\n", c.FanLevel)
	fmt.Fprintf(&b, "\toption charge_limit '%d'\n", c.ChargeLimit)
	fmt.Fprintf(&b, "\toption led_mode '%s'\n", c.LED.Mode)
	fmt.Fprintf(&b, "\toption led_start '%s'\n", c.LED.Start)
	fmt.Fprintf(&b, "\toption led_end '%s'\n", c.LED.End)
	fmt.Fprintf(&b, "\toption beeper_mode '%s'\n", c.Beeper.Mode)
	fmt.Fprintf(&b, "\toption beeper_start '%s'\n", c.Beeper.Start)
	fmt.Fprintf(&b, "\toption beeper_end '%s'\n", c.Beeper.End)
	fmt.Fprintf(&b, "\toption brightness '%d'\n", c.Brightness)
	fmt.Fprintf(&b, "\toption dim_battery_sec '%d'\n", c.DimBatterySec)
	fmt.Fprintf(&b, "\toption dim_charger_sec '%d'\n", c.DimChargerSec)
	if c.Movement {
		b.WriteString("\toption movement '1'\n")
	} else {
		b.WriteString("\toption movement '0'\n")
	}
	fmt.Fprintf(&b, "\toption monthly_limit_gb '%d'\n", c.MonthlyLimitGB)

	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

func (c *Config) clone() *Config {
	cp := *c
	return &cp
}

func configExists() bool {
	_, err := os.Stat(configPath)
	return err == nil
}
