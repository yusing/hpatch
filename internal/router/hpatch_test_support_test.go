package router

import "context"

// Source: hpatch_correction_flow_test.go:15:31 metricsObservingTranslator.
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
