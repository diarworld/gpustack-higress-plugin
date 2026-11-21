package main

import (
	"testing"
	"time"
)

func TestFreeWindowValidate(t *testing.T) {
	cases := []struct {
		name    string
		fw      FreeWindowConfig
		wantErr bool
	}{
		{
			name: "ok window only",
			fw:   FreeWindowConfig{Enabled: true, Start: "15:00", End: "06:00"},
		},
		{
			name: "ok with days",
			fw:   FreeWindowConfig{Enabled: true, Start: "00:00", End: "23:59", Days: []string{"sat", "sun"}},
		},
		{
			name:    "bad start format",
			fw:      FreeWindowConfig{Enabled: true, Start: "1500", End: "06:00"},
			wantErr: true,
		},
		{
			name:    "bad end hour",
			fw:      FreeWindowConfig{Enabled: true, Start: "15:00", End: "24:00"},
			wantErr: true,
		},
		{
			name:    "start equals end",
			fw:      FreeWindowConfig{Enabled: true, Start: "06:00", End: "06:00"},
			wantErr: true,
		},
		{
			name:    "unknown day",
			fw:      FreeWindowConfig{Enabled: true, Start: "00:00", End: "01:00", Days: []string{"sat", "funday"}},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.fw.validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("validate() err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestInFreeWindow(t *testing.T) {
	mk := func(fw *FreeWindowConfig) *PluginConfig {
		return &PluginConfig{RuleName: "r", FreeWindows: fw}
	}
	// 2026-11-21 is a Saturday; 2026-11-18 is a Wednesday.
	wed := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	sat := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		cfg    *PluginConfig
		at     time.Time
		inFree bool
	}{
		{
			name:   "absent block is never free",
			cfg:    mk(nil),
			at:     sat,
			inFree: false,
		},
		{
			name:   "disabled block is never free",
			cfg:    mk(&FreeWindowConfig{Enabled: false, Start: "00:00", End: "23:59", Days: []string{"sat"}}),
			at:     sat,
			inFree: false,
		},
		{
			name: "inside a wrapping window",
			cfg: mk(func() *FreeWindowConfig {
				fw := &FreeWindowConfig{Enabled: true, Start: "15:00", End: "06:00"}
				if err := fw.validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				return fw
			}()),
			at:     time.Date(2026, 11, 18, 23, 30, 0, 0, time.UTC),
			inFree: true,
		},
		{
			name: "inside the wrapped tail after midnight",
			cfg: mk(func() *FreeWindowConfig {
				fw := &FreeWindowConfig{Enabled: true, Start: "15:00", End: "06:00"}
				if err := fw.validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				return fw
			}()),
			at:     time.Date(2026, 11, 18, 5, 59, 0, 0, time.UTC),
			inFree: true,
		},
		{
			name: "midday outside a wrapping window",
			cfg: mk(func() *FreeWindowConfig {
				fw := &FreeWindowConfig{Enabled: true, Start: "15:00", End: "06:00"}
				if err := fw.validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				return fw
			}()),
			at:     time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
			inFree: false,
		},
		{
			name: "window end is exclusive",
			cfg: mk(func() *FreeWindowConfig {
				fw := &FreeWindowConfig{Enabled: true, Start: "06:00", End: "09:00"}
				if err := fw.validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				return fw
			}()),
			at:     time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC),
			inFree: false,
		},
		{
			name: "whole free day covers any hour",
			cfg: mk(func() *FreeWindowConfig {
				fw := &FreeWindowConfig{Enabled: true, Start: "06:00", End: "07:00", Days: []string{"sat", "sun"}}
				if err := fw.validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				return fw
			}()),
			at:     sat, // 12:00, outside the 06:00-07:00 window, but a free day
			inFree: true,
		},
		{
			name: "weekday outside the window is not free",
			cfg: mk(func() *FreeWindowConfig {
				fw := &FreeWindowConfig{Enabled: true, Start: "06:00", End: "07:00", Days: []string{"sat", "sun"}}
				if err := fw.validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				return fw
			}()),
			at:     wed.Add(12 * time.Hour),
			inFree: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.InFreeWindow(c.at); got != c.inFree {
				t.Fatalf("InFreeWindow(%s)=%v, want %v", c.at, got, c.inFree)
			}
		})
	}
}

func TestCollectChecksFreeWindowSuspendsEverything(t *testing.T) {
	cfg := &PluginConfig{
		RuleName: "myrule",
		LimitCombinations: []LimitCombination{
			{
				Name:        "c1",
				Match:       []MatchRule{mustHeaderRule(t, "x-api-key", "k1")},
				QueryLimits: &RateQuota{PerMinute: intPtr(10)},
			},
		},
	}
	fw := &FreeWindowConfig{Enabled: true, Start: "00:00", End: "01:00", Days: []string{"wed"}}
	if err := fw.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	cfg.FreeWindows = fw

	headers := [][2]string{{"x-api-key", "k1"}}
	// 2026-11-18 is a Wednesday -- inside the schedule, so nothing may be
	// built: no eval entries, no pending token adds, no slots to hold.
	freeAt := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	entries, pending, held := collectChecks(cfg, headers, "rid", freeAt)
	if len(entries) != 0 || len(pending) != 0 || len(held) != 0 {
		t.Fatalf("free window must suspend all checks, got entries=%d pending=%d held=%d",
			len(entries), len(pending), len(held))
	}

	// Thursday the 19th is not a free day: checks are back.
	paidAt := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	entries, pending, held = collectChecks(cfg, headers, "rid", paidAt)
	if len(entries) == 0 {
		t.Fatal("outside the free window the combo must produce entries")
	}
	if len(pending) != 0 {
		t.Fatalf("query-only combo must not produce pending token adds, got %d", len(pending))
	}
	if len(held) != 0 {
		t.Fatalf("combo without concurrency limits must not hold slots, got %d", len(held))
	}
}
