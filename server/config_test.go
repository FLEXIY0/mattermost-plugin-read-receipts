package main

import "testing"

// The System Console number field does not enforce the range, so anything
// unusable has to be caught here rather than shipped to every client.
func TestConfigurationNormalized(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"in range", 14, 14},
		{"at the minimum", minTickSize, minTickSize},
		{"at the maximum", maxTickSize, maxTickSize},
		{"below the minimum", minTickSize - 1, defaultTickSize},
		{"above the maximum", maxTickSize + 1, defaultTickSize},
		{"unset", 0, defaultTickSize},
		{"negative", -20, defaultTickSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (configuration{TickSize: tc.in}).normalized().TickSize; got != tc.want {
				t.Errorf("TickSize %d normalized to %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestGetConfigurationDefaultsBeforeAnyLoad(t *testing.T) {
	p := &Plugin{}
	if got := p.getConfiguration().TickSize; got != defaultTickSize {
		t.Errorf("TickSize = %d before configuration is loaded, want %d", got, defaultTickSize)
	}
}
