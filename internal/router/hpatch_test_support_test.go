package router

import "context"

// Source: hpatch_metrics_test.go metricsObservingTranslator.
type metricsObservingTranslator struct {
	translate func(context.Context, string, string) ([]byte, error)
	record    func(context.Context, hpatchMetricRecord) error
}

func (t metricsObservingTranslator) Translate(ctx context.Context, workspace string, script string) (hpatchTranslationResult, error) {
	patch, err := t.translate(ctx, workspace, script)
	return hpatchTranslationResult{patch: patch, report: testHPatchReport}, err
}

func (t metricsObservingTranslator) RecordMetrics(ctx context.Context, record hpatchMetricRecord) error {
	return t.record(ctx, record)
}

func (metricsObservingTranslator) ToolDescription() string {
	return testHPatchToolDescription
}

// hpatchResultObservingTranslator is metricsObservingTranslator for tests that
// need evaluator rejections in the translation result as well as the recorded
// metrics, so a rejection and its following recovery can both be asserted.
type hpatchResultObservingTranslator struct {
	translate func(context.Context, string, string) (hpatchTranslationResult, error)
	record    func(context.Context, hpatchMetricRecord) error
}

func (t hpatchResultObservingTranslator) Translate(ctx context.Context, workspace string, script string) (hpatchTranslationResult, error) {
	return t.translate(ctx, workspace, script)
}

func (t hpatchResultObservingTranslator) RecordMetrics(ctx context.Context, record hpatchMetricRecord) error {
	return t.record(ctx, record)
}

func (hpatchResultObservingTranslator) ToolDescription() string {
	return testHPatchToolDescription
}
