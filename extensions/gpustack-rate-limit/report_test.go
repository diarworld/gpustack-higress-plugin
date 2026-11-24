package main

import (
	"encoding/json"
	"testing"

	"github.com/higress-group/wasm-go/pkg/wrapper"
)

// fakeReportClient captures the report POST so the dispatch path can be
// asserted without a proxywasm host. Only Post is overridden; the code under
// test never touches the other verbs.
type fakeReportClient struct {
	wrapper.HttpClient
	postPath   string
	postBody   []byte
	postCalled bool
}

func (f *fakeReportClient) Post(
	path string, _ [][2]string, body []byte, cb wrapper.ResponseCallback, _ ...uint32,
) error {
	f.postCalled = true
	f.postPath = path
	f.postBody = body
	if cb != nil {
		cb(200, nil, nil)
	}
	return nil
}

func TestReportLimitEventDispatchesPayload(t *testing.T) {
	fake := &fakeReportClient{}
	config := PluginConfig{
		Endpoint: &ReportEndpointConfig{
			ServiceName: "gpustack.static",
			Path:        "/v2/usage/gateway-limit-events",
			TimeoutMs:   1000,
		},
		HeaderAdd:    map[string]string{"X-GPUStack-Auth-Token": "tok"},
		ReportClient: fake,
	}
	limited := &LimitEntry{Combo: "user-3970", Kind: "token_calendar", Period: "each_month"}

	reportLimitEvent(config, aiLabels{Consumer: "gpustack-3970", Model: "qwen3-32b", Route: "r1"}, limited)

	if !fake.postCalled {
		t.Fatal("reportLimitEvent did not dispatch a POST")
	}
	if fake.postPath != "/v2/usage/gateway-limit-events" {
		t.Errorf("post path = %q, want the configured report path", fake.postPath)
	}
	var env limitEventReportEnvelope
	if err := json.Unmarshal(fake.postBody, &env); err != nil {
		t.Fatalf("body is not a limitEventReportEnvelope: %v", err)
	}
	if len(env.Events) != 1 {
		t.Fatalf("envelope carries %d events, want 1", len(env.Events))
	}
	payload := env.Events[0]
	if payload.Consumer != "gpustack-3970" || payload.Model != "qwen3-32b" || payload.Route != "r1" {
		t.Errorf("payload labels = %+v, want the ai labels verbatim", payload)
	}
	if payload.Combo != "user-3970" || payload.Kind != "token_calendar" || payload.Period != "each_month" {
		t.Errorf("payload limit = %+v, want the tripped entry verbatim", payload)
	}
	if payload.OccurredAt == "" {
		t.Error("payload carried no occurred_at")
	}
}

func TestReportLimitEventWithoutEntryStillAttributesConsumer(t *testing.T) {
	// A rejection whose tripped entry cannot be located (the lua contract
	// makes this unreachable) must still report the consumer -- the axis
	// degrades server-side to the generic limit_event.
	fake := &fakeReportClient{}
	config := PluginConfig{
		Endpoint:     &ReportEndpointConfig{ServiceName: "gpustack.static", Path: "/p"},
		ReportClient: fake,
	}

	reportLimitEvent(config, aiLabels{Consumer: "gpustack-42"}, nil)

	if !fake.postCalled {
		t.Fatal("reportLimitEvent did not dispatch a POST")
	}
	var env limitEventReportEnvelope
	if err := json.Unmarshal(fake.postBody, &env); err != nil {
		t.Fatalf("body is not a limitEventReportEnvelope: %v", err)
	}
	if len(env.Events) != 1 {
		t.Fatalf("envelope carries %d events, want 1", len(env.Events))
	}
	payload := env.Events[0]
	if payload.Consumer != "gpustack-42" {
		t.Errorf("consumer = %q, want gpustack-42", payload.Consumer)
	}
	if payload.Combo != "" || payload.Kind != "" || payload.Period != "" {
		t.Errorf("payload = %+v, want the limit fields absent", payload)
	}
}

func TestReportLimitEventNoEndpointIsSilent(t *testing.T) {
	// No endpoint configured (feature off, or an older CR) -- reporting must
	// be a no-op, not an error and not a dispatch.
	fake := &fakeReportClient{}
	config := PluginConfig{ReportClient: fake}

	reportLimitEvent(config, aiLabels{Consumer: "gpustack-42"}, nil)

	if fake.postCalled {
		t.Fatal("reportLimitEvent dispatched without an endpoint configured")
	}
}

func TestInitReportClientDefaultsAndSkips(t *testing.T) {
	config := PluginConfig{Endpoint: &ReportEndpointConfig{ServiceName: "gpustack.static"}}
	initReportClient(&config)
	if config.ReportClient == nil {
		t.Fatal("endpoint with a service name produced no client")
	}
	if config.Endpoint.TimeoutMs != 5000 {
		t.Errorf("timeout = %d, want the 5000 default", config.Endpoint.TimeoutMs)
	}

	empty := PluginConfig{}
	initReportClient(&empty)
	if empty.ReportClient != nil {
		t.Fatal("config without an endpoint built a client")
	}

	nameless := PluginConfig{Endpoint: &ReportEndpointConfig{Path: "/p"}}
	initReportClient(&nameless)
	if nameless.ReportClient != nil {
		t.Fatal("endpoint without a service name built a client")
	}
}
