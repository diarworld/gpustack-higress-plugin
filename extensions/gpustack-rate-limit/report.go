// Rejection reporting: one fire-and-forget POST per rejected request to the
// GPUStack server, so every 429 lands in the resource-events UI (Usage →
// Resource Events, resource "limit"). The endpoint is the same trust channel
// the token-usage plugin uses for its metrics report -- a McpBridge registry
// service validating the derived gateway token from header_add.
//
// Reporting is best-effort by design: a report failure never changes the
// rejection outcome, and a missing endpoint disables the path entirely. The
// server folds bursts, so per-request POSTs stay proportionate even under a
// rejection storm.
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/higress-group/proxy-wasm-go-sdk/proxywasm"
)

// LimitEventReport is the JSON payload POSTed to the server for one rejected
// request. OccurredAt is the rejection instant (RFC3339, UTC). The fields
// mirror the rejected_total metric labels, so a UI row can be cross-checked
// against the gateway counters; Consumer is the raw x-mse-consumer identity
// ("gpustack-<uid>" or "<access_key>.gpustack-<uid>") -- the server resolves
// the user from it, the plugin never learns usernames.
type LimitEventReport struct {
	OccurredAt string `json:"occurred_at"`
	Consumer   string `json:"consumer,omitempty"`
	Combo      string `json:"combo,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Period     string `json:"period,omitempty"`
	Model      string `json:"model,omitempty"`
	Route      string `json:"route,omitempty"`
}

// limitEventReportEnvelope matches the server's GatewayLimitEventReport: the
// list shape leaves room for batching several rejections into one POST
// without a wire-format change.
type limitEventReportEnvelope struct {
	Events []LimitEventReport `json:"events"`
}

// reportLimitEvent dispatches the report for one rejection. Called from
// handleEvalResult AFTER SendHttpResponseWithDetail: the reply is already
// queued, so this hostcall sits in the proven-safe zone (the documented
// hazard is hostcalls between callback entry and the reply, not after it),
// and a dispatch failure must never alter the rejection.
func reportLimitEvent(config PluginConfig, ai aiLabels, limited *LimitEntry) {
	if config.ReportClient == nil || config.Endpoint == nil {
		return
	}

	report := LimitEventReport{
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Consumer:   ai.Consumer,
		Model:      ai.Model,
		Route:      ai.Route,
	}
	if limited != nil {
		report.Combo = limited.Combo
		report.Kind = limited.Kind
		report.Period = limited.Period
	}

	body, err := json.Marshal(limitEventReportEnvelope{Events: []LimitEventReport{report}})
	if err != nil {
		// The struct is flat strings; marshal failure would mean a broken
		// build, not bad input. Log and drop.
		proxywasm.LogErrorf("%s: report marshal failed: %v", pluginName, err)
		return
	}

	headers := [][2]string{{"content-type", "application/json"}}
	for k, v := range config.HeaderAdd {
		headers = append(headers, [2]string{k, v})
	}

	path := config.Endpoint.Path
	if path == "" {
		path = "/"
	}

	if err := config.ReportClient.Post(
		path,
		headers,
		body,
		func(statusCode int, _ http.Header, _ []byte) {
			if statusCode/100 != 2 {
				proxywasm.LogWarnf("%s: limit-event report got status %d", pluginName, statusCode)
			}
		},
		config.Endpoint.TimeoutMs,
	); err != nil {
		proxywasm.LogWarnf("%s: limit-event report dispatch failed: %v", pluginName, err)
	}
}
