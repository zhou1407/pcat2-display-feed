package main

import "testing"

func TestSmartFanSpeed(t *testing.T) {
	cases := []struct {
		name     string
		mode     string
		temp     int
		charging bool
		maxLvl   int
		want     int
	}{
		// charging: voltage 8.2V, current +1.2A, SOC 50% => charging branch
		{"cool charging 43 -> 1", "smart_cool", 43, true, 4, 1},
		{"cool charging 47 -> capped 4", "smart_cool", 47, true, 4, 4},
		{"cool charging 47 user 9 -> 5", "smart_cool", 47, true, 9, 5},
		{"quiet charging 44 -> 0", "smart_quiet", 44, true, 4, 0},
		{"quiet charging 46 -> 2", "smart_quiet", 46, true, 4, 2},
		// no charger: discharging thresholds
		{"cool not-charging 47 -> 0", "smart_cool", 47, false, 4, 0},
		{"cool not-charging 48 -> 1", "smart_cool", 48, false, 4, 1},
		{"quiet not-charging 52 -> 1", "smart_quiet", 52, false, 4, 1},
		// critical override
		{"critical 61 -> 8", "smart_cool", 61, true, 4, 8},
		{"critical 61 -> 8 (manual max 1)", "smart_quiet", 61, true, 1, 8},
		// no temperature
		{"temp 0 -> 0", "smart_cool", 0, true, 4, 0},
	}
	for _, c := range cases {
		got := smartFanSpeed(c.mode, c.temp, c.charging, c.maxLvl)
		if got != c.want {
			t.Errorf("%s: smartFanSpeed(%s,%d,%v,%d)=%d want %d",
				c.name, c.mode, c.temp, c.charging, c.maxLvl, got, c.want)
		}
	}
}
