package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/higress-group/proxy-wasm-go-sdk/proxywasm/types"
	"github.com/higress-group/wasm-go/pkg/iface"
	"github.com/higress-group/wasm-go/pkg/wrapper"
)

// captureBackend records every BatchEval entry so release/acquire flows can
// be asserted without a proxy-wasm host.
type captureBackend struct {
	batches [][]LimitEntry
}

func (b *captureBackend) BatchEval(_ int64, entries []LimitEntry, callback func(EvalResult)) (types.Action, error) {
	b.batches = append(b.batches, entries)
	return 0, nil
}

// testHttpContext is the minimal HttpContext fake: only the context map the
// stream-done path reads is real; every host-bound call is a no-op panic
// guard that must never be reached by the code under test.
type testHttpContext struct {
	values map[string]interface{}
}

func newTestHttpContext() *testHttpContext {
	return &testHttpContext{values: map[string]interface{}{}}
}

func (c *testHttpContext) SetContext(key string, value interface{}) { c.values[key] = value }
func (c *testHttpContext) GetContext(key string) interface{}        { return c.values[key] }
func (c *testHttpContext) GetStringContext(key, defaultValue string) string {
	if v, ok := c.values[key].(string); ok {
		return v
	}
	return defaultValue
}

func (c *testHttpContext) Scheme() string                              { panic("not implemented") }
func (c *testHttpContext) Host() string                                { panic("not implemented") }
func (c *testHttpContext) Path() string                                { panic("not implemented") }
func (c *testHttpContext) Method() string                              { panic("not implemented") }
func (c *testHttpContext) GetBoolContext(string, bool) bool            { panic("not implemented") }
func (c *testHttpContext) GetByteSliceContext(string, []byte) []byte   { panic("not implemented") }
func (c *testHttpContext) GetUserAttribute(string) interface{}         { panic("not implemented") }
func (c *testHttpContext) SetUserAttribute(string, interface{})        { panic("not implemented") }
func (c *testHttpContext) SetUserAttributeMap(map[string]interface{})  { panic("not implemented") }
func (c *testHttpContext) GetUserAttributeMap() map[string]interface{} { panic("not implemented") }
func (c *testHttpContext) WriteUserAttributeToLog() error              { panic("not implemented") }
func (c *testHttpContext) WriteUserAttributeToLogWithKey(string) error { panic("not implemented") }
func (c *testHttpContext) WriteUserAttributeToTrace() error            { panic("not implemented") }
func (c *testHttpContext) DontReadRequestBody()                        { panic("not implemented") }
func (c *testHttpContext) DontReadResponseBody()                       { panic("not implemented") }
func (c *testHttpContext) BufferRequestBody()                          { panic("not implemented") }
func (c *testHttpContext) BufferResponseBody()                         { panic("not implemented") }
func (c *testHttpContext) NeedPauseStreamingResponse()                 { panic("not implemented") }
func (c *testHttpContext) PushBuffer([]byte)                           { panic("not implemented") }
func (c *testHttpContext) PopBuffer() []byte                           { panic("not implemented") }
func (c *testHttpContext) BufferQueueSize() int                        { panic("not implemented") }
func (c *testHttpContext) DisableReroute()                             { panic("not implemented") }
func (c *testHttpContext) SetRequestBodyBufferLimit(uint32)            { panic("not implemented") }
func (c *testHttpContext) SetResponseBodyBufferLimit(uint32)           { panic("not implemented") }
func (c *testHttpContext) RouteCall(string, string, [][2]string, []byte, iface.RouteResponseCallback) error {
	panic("not implemented")
}
func (c *testHttpContext) GetExecutionPhase() iface.HTTPExecutionPhase { panic("not implemented") }
func (c *testHttpContext) HasRequestBody() bool                        { panic("not implemented") }
func (c *testHttpContext) HasResponseBody() bool                       { panic("not implemented") }
func (c *testHttpContext) IsWebsocket() bool                           { panic("not implemented") }
func (c *testHttpContext) IsBinaryRequestBody() bool                   { panic("not implemented") }
func (c *testHttpContext) IsBinaryResponseBody() bool                  { panic("not implemented") }

var _ wrapper.HttpContext = (*testHttpContext)(nil)

func mustConcurrency(t *testing.T, spec *ConcurrencySpec) *ConcurrencySpec {
	t.Helper()
	if err := spec.Compile(); err != nil {
		t.Fatalf("unexpected ConcurrencySpec.Compile error: %v", err)
	}
	return spec
}

func TestConcurrencySpecCompile(t *testing.T) {
	cases := []struct {
		name    string
		spec    *ConcurrencySpec
		wantErr bool
	}{
		{"default only", &ConcurrencySpec{Default: intPtr(1)}, false},
		{"models only", &ConcurrencySpec{Models: map[string]int{"glm": 2}}, false},
		{"zero means unlimited", &ConcurrencySpec{Models: map[string]int{"glm": 0}}, false},
		{"negative default", &ConcurrencySpec{Default: intPtr(-1)}, true},
		{"negative model quota", &ConcurrencySpec{Models: map[string]int{"glm": -2}}, true},
		{"empty model name", &ConcurrencySpec{Models: map[string]int{"": 1}}, true},
		{"oversized model name", &ConcurrencySpec{Models: map[string]int{strings.Repeat("m", concurrencyMaxModelNameLen+1): 1}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Compile()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	over := &ConcurrencySpec{Models: make(map[string]int, concurrencyMaxModelEntries+1)}
	for i := 0; i <= concurrencyMaxModelEntries; i++ {
		over.Models[strings.Repeat("m", i+1)] = 1
	}
	if err := over.Compile(); err == nil {
		t.Fatalf("expected entry-count error, got nil")
	}
}

func TestConcurrencySpecCompileIsIdempotent(t *testing.T) {
	spec := mustConcurrency(t, &ConcurrencySpec{Default: intPtr(2), Models: map[string]int{"glm": 3}})
	// Higress may re-Compile a shared spec on every rule-level override;
	// Resolve must not change.
	for range 3 {
		if err := spec.Compile(); err != nil {
			t.Fatalf("re-Compile: %v", err)
		}
	}
	if got, _ := spec.Resolve("glm"); got != 3 {
		t.Fatalf("Resolve(glm)=%d, want 3", got)
	}
	if got, _ := spec.Resolve("other"); got != 2 {
		t.Fatalf("Resolve(other)=%d, want 2", got)
	}
}

func TestConcurrencySpecResolve(t *testing.T) {
	spec := mustConcurrency(t, &ConcurrencySpec{
		Default: intPtr(1),
		Models:  map[string]int{"glm-5.3-flash": 3, "unlimited-model": 0},
	})

	if got, ok := spec.Resolve("glm-5.3-flash"); !ok || got != 3 {
		t.Fatalf("Resolve(override)=(-%d,%v), want (3,true)", got, ok)
	}
	// Explicit 0 is "unlimited", distinct from "not capped by this spec".
	if got, ok := spec.Resolve("unlimited-model"); !ok || got != 0 {
		t.Fatalf("Resolve(unlimited)=(%d,%v), want (0,true)", got, ok)
	}
	if got, ok := spec.Resolve("never-seen"); !ok || got != 1 {
		t.Fatalf("Resolve(default)=(%d,%v), want (1,true)", got, ok)
	}

	noDefault := mustConcurrency(t, &ConcurrencySpec{Models: map[string]int{"glm": 3}})
	if _, ok := noDefault.Resolve("other"); ok {
		t.Fatalf("model absent and no default: expected ok=false")
	}

	if _, ok := (*ConcurrencySpec)(nil).Resolve("glm"); ok {
		t.Fatalf("nil spec must not cap")
	}
	uncompiled := &ConcurrencySpec{Default: intPtr(1)}
	if _, ok := uncompiled.Resolve("glm"); ok {
		t.Fatalf("uncompiled spec must not cap")
	}
}

func TestPluginConfigValidateConcurrencyOnlyCombo(t *testing.T) {
	cfg := &PluginConfig{
		RuleName: "rule",
		LimitCombinations: []LimitCombination{
			{
				Name:              "user-6",
				Match:             []MatchRule{mustHeaderRule(t, "x-api-key", "k1")},
				ConcurrencyLimits: &ConcurrencySpec{Default: intPtr(1)},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("concurrency-only combo must be valid: %v", err)
	}

	empty := &PluginConfig{
		RuleName: "rule",
		LimitCombinations: []LimitCombination{
			{Name: "user-6", Match: []MatchRule{mustHeaderRule(t, "x-api-key", "k1")}},
		},
	}
	if err := empty.Validate(); err == nil {
		t.Fatalf("combo without any limit kind must be rejected")
	}
}

func TestPluginConfigValidateStaleSeconds(t *testing.T) {
	cfg := &PluginConfig{
		RuleName: "rule",
		LimitCombinations: []LimitCombination{
			{
				Name:              "user-6",
				Match:             []MatchRule{mustHeaderRule(t, "x-api-key", "k1")},
				ConcurrencyLimits: &ConcurrencySpec{Default: intPtr(1)},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.ConcurrencyStaleSeconds != defaultConcurrencyStaleSecs {
		t.Fatalf("stale seconds = %d, want default %d",
			cfg.ConcurrencyStaleSeconds, defaultConcurrencyStaleSecs)
	}

	cfg.ConcurrencyStaleSeconds = concurrencyMinStaleSeconds - 1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("stale seconds below minimum must be rejected")
	}
}

func TestCollectChecksConcurrencyAcquire(t *testing.T) {
	cfg := &PluginConfig{
		RuleName:                "gpustack-rate-limit",
		ConcurrencyStaleSeconds: 7200,
		LimitCombinations: []LimitCombination{
			{
				Name:  "user-6",
				Match: []MatchRule{mustHeaderRule(t, "x-mse-consumer", "gpustack-6")},
				ConcurrencyLimits: mustConcurrency(t, &ConcurrencySpec{
					Default: intPtr(1),
					Models:  map[string]int{"glm-5.3-flash": 3, "free-model": 0},
				}),
			},
		},
	}
	base := "gpustack-rate-limit|user-6|header:x-mse-consumer=gpustack-6"

	// Override model: quota 3, per-model key.
	entries, _, held := collectChecks(
		cfg,
		[][2]string{{"x-mse-consumer", "gpustack-6"}, {"x-higress-llm-model", "glm-5.3-flash"}},
		"req-1", time.Now(),
	)
	if len(entries) != 1 {
		t.Fatalf("len(entries)=%d, want 1", len(entries))
	}
	e := entries[0]
	if e.Key != base+"|c:glm-5.3-flash" {
		t.Errorf("Key=%q, want %q", e.Key, base+"|c:glm-5.3-flash")
	}
	if e.Mode != modeCheckAndAdd || e.Count != 1 || e.Quota != 3 {
		t.Errorf("acquire entry = %+v", e)
	}
	if e.Window != 7200 || e.TTL != 14400 {
		t.Errorf("stale window/ttl = %d/%d, want 7200/14400", e.Window, e.TTL)
	}
	if e.Kind != metricKindConcurrency || e.Period != "7200s" {
		t.Errorf("metric labels = kind %q period %q", e.Kind, e.Period)
	}
	if e.Bucket != "header:x-mse-consumer=gpustack-6|model=glm-5.3-flash" {
		t.Errorf("Bucket=%q", e.Bucket)
	}
	if len(held) != 1 || held[0].Key != e.Key || held[0].Member != "req-1" {
		t.Fatalf("held = %+v, want one entry for %q", held, e.Key)
	}

	// Unlisted model: falls back to the default quota, own bucket.
	entries, _, _ = collectChecks(
		cfg,
		[][2]string{{"x-mse-consumer", "gpustack-6"}, {"x-higress-llm-model", "qwen3.8-27b"}},
		"req-2", time.Now(),
	)
	if len(entries) != 1 || entries[0].Quota != 1 || entries[0].Key != base+"|c:qwen3.8-27b" {
		t.Fatalf("default-model entry = %+v", entries)
	}

	// Explicitly unlimited model: no entry, no slot.
	entries, _, held = collectChecks(
		cfg,
		[][2]string{{"x-mse-consumer", "gpustack-6"}, {"x-higress-llm-model", "free-model"}},
		"req-3", time.Now(),
	)
	if len(entries) != 0 || len(held) != 0 {
		t.Fatalf("unlimited model must not acquire, got entries=%d held=%d", len(entries), len(held))
	}

	// Missing model header: the shared "none" bucket instead of a bypass.
	entries, _, _ = collectChecks(
		cfg,
		[][2]string{{"x-mse-consumer", "gpustack-6"}},
		"req-4", time.Now(),
	)
	if len(entries) != 1 || entries[0].Key != base+"|c:none" || entries[0].Quota != 1 {
		t.Fatalf("none-bucket entry = %+v", entries)
	}
}

func TestCollectChecksConcurrencyNoBlockNoEntry(t *testing.T) {
	// The admin-exemption shape the server publishes: a user combo without a
	// concurrency block must not acquire anything.
	cfg := &PluginConfig{
		RuleName: "gpustack-rate-limit",
		LimitCombinations: []LimitCombination{
			{
				Name:        "user-1",
				Match:       []MatchRule{mustHeaderRule(t, "x-mse-consumer", "gpustack-1")},
				QueryLimits: &RateQuota{PerMinute: intPtr(10)},
			},
		},
	}
	entries, _, held := collectChecks(
		cfg,
		[][2]string{{"x-mse-consumer", "gpustack-1"}, {"x-higress-llm-model", "glm"}},
		"req-1", time.Now(),
	)
	if len(entries) != 1 || entries[0].Kind != metricKindQuery {
		t.Fatalf("entries = %+v", entries)
	}
	if len(held) != 0 {
		t.Fatalf("held = %+v, want empty", held)
	}
}

func TestConcurrencyKeyPartMatchesUsageReader(t *testing.T) {
	// The usage endpoint derives the key from the combo shape; pin the
	// plugin's key layout so the two cannot drift apart silently.
	cfg := &PluginConfig{
		RuleName: "gpustack-rate-limit",
		LimitCombinations: []LimitCombination{
			{
				Name:  "user-6",
				Match: []MatchRule{{Source: SourceConsumer, Value: "regexp_capture:^(?:[^.]+\\.)?(gpustack-.+)$"}},
				ConcurrencyLimits: mustConcurrency(t, &ConcurrencySpec{
					Default: intPtr(1),
					Models:  map[string]int{"glm": 2},
				}),
			},
		},
	}
	for i := range cfg.LimitCombinations {
		for j := range cfg.LimitCombinations[i].Match {
			if err := cfg.LimitCombinations[i].Match[j].Compile(); err != nil {
				t.Fatalf("compile match rule: %v", err)
			}
		}
	}
	entries, _, _ := collectChecks(
		cfg,
		[][2]string{{"x-mse-consumer", "gpustack-6"}, {"x-higress-llm-model", "glm"}},
		"req-1", time.Now(),
	)
	if len(entries) != 1 {
		t.Fatalf("len(entries)=%d, want 1", len(entries))
	}
	want := "gpustack-rate-limit|user-6|consumer=gpustack-6|c:glm"
	if entries[0].Key != want {
		t.Fatalf("Key=%q, want %q", entries[0].Key, want)
	}
}

func TestConcurrencyConfigJSON(t *testing.T) {
	// parseGlobalConfig dial redis and logs host-side; the tag wiring under
	// test is plain json.Unmarshal plus Validate, which is all the
	// reconciler's JSON flows through before Validate runs.
	raw := `{
		"rule_name": "gpustack-rate-limit",
		"concurrency_stale_seconds": 1800,
		"limit_combinations": [
			{
				"name": "user-6",
				"match": [{"source": "consumer", "value": "regexp_capture:^(?:[^.]+\\.)?(gpustack-.+)$"}],
				"concurrency_limits": {"default": 1, "models": {"glm-5.3-flash": 4}}
			}
		]
	}`
	var cfg PluginConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.ConcurrencyStaleSeconds != 1800 {
		t.Fatalf("stale = %d, want 1800", cfg.ConcurrencyStaleSeconds)
	}
	if len(cfg.LimitCombinations) != 1 {
		t.Fatalf("combos = %d, want 1", len(cfg.LimitCombinations))
	}
	spec := cfg.LimitCombinations[0].ConcurrencyLimits
	if spec == nil {
		t.Fatalf("concurrency_limits not parsed")
	}
	if got, _ := spec.Resolve("glm-5.3-flash"); got != 4 {
		t.Fatalf("Resolve(override)=%d, want 4", got)
	}
	if got, _ := spec.Resolve("other"); got != 1 {
		t.Fatalf("Resolve(default)=%d, want 1", got)
	}
}

func TestOnHttpStreamDoneReleasesHeldSlots(t *testing.T) {
	backend := &captureBackend{}
	cfg := PluginConfig{RuleName: "gpustack-rate-limit", Backend: backend}
	ctx := newTestHttpContext()
	ctx.SetContext(ctxKeyConcurrencyHeld, []concurrencyHeld{
		{Key: "gpustack-rate-limit|user-6|consumer=gpustack-6|c:glm", Member: "req-1"},
		{Key: "gpustack-rate-limit|user-6|consumer=gpustack-6|c:qwen", Member: "req-1"},
	})

	onHttpStreamDone(ctx, cfg)

	if len(backend.batches) != 1 {
		t.Fatalf("release batches = %d, want 1", len(backend.batches))
	}
	entries := backend.batches[0]
	if len(entries) != 2 {
		t.Fatalf("release entries = %d, want 2", len(entries))
	}
	for i, want := range []string{
		"gpustack-rate-limit|user-6|consumer=gpustack-6|c:glm",
		"gpustack-rate-limit|user-6|consumer=gpustack-6|c:qwen",
	} {
		if entries[i].Key != want || entries[i].Mode != modeRelease || entries[i].Member != "req-1" {
			t.Errorf("release[%d] = %+v, want key %q mode %q", i, entries[i], want, modeRelease)
		}
		// Pin the two invariants the lua script depends on: Window=stale
		// keeps the pass-1 prune from evicting the whole ZSET (now-0 would
		// wipe every slot), and Count=1 makes ZREM target the encoded
		// "<member>|<count>" form the acquire wrote.
		if entries[i].Window != defaultConcurrencyStaleSecs || entries[i].Count != 1 {
			t.Errorf("release[%d] window=%d count=%d, want %d/1",
				i, entries[i].Window, entries[i].Count, defaultConcurrencyStaleSecs)
		}
	}
}

func TestOnHttpStreamDoneNoHeldNoRelease(t *testing.T) {
	backend := &captureBackend{}
	cfg := PluginConfig{RuleName: "gpustack-rate-limit", Backend: backend}
	onHttpStreamDone(newTestHttpContext(), cfg)
	if len(backend.batches) != 0 {
		t.Fatalf("unexpected batches: %+v", backend.batches)
	}
}
