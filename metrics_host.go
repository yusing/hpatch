package hpatch

import (
	"fmt"
	"io"
)

const hostRejectionCommand = "account-rejection"

// recordHostRejection records a correction rejected by the host before script
// evaluation. Standard input is the exact diagnostic restored to the model;
// the exact model-emitted payload and definition context come from accounting.
func recordHostRejection(stdin io.Reader, stderr io.Writer, dataDirectory string) int {
	accounting, err := loadMetricAccounting()
	if err != nil {
		return fail(stderr, err.Error())
	}
	if accounting.ChargedScript == "" {
		return fail(stderr, "host rejection accounting requires a charged script")
	}
	diagnostic, err := io.ReadAll(io.LimitReader(stdin, maxAccountingFileBytes+1))
	if err != nil {
		return fail(stderr, fmt.Sprintf("reading host rejection diagnostic: %v", err))
	}
	if len(diagnostic) > maxAccountingFileBytes {
		return fail(stderr, fmt.Sprintf("host rejection diagnostic exceeds %d bytes", maxAccountingFileBytes))
	}
	if len(diagnostic) == 0 {
		return fail(stderr, "host rejection accounting requires a diagnostic")
	}
	if dataDirectory != "" {
		if err := recordIneffectiveMetrics(dataDirectory, accounting.ChargedScript, string(diagnostic), invocationMetrics{}, accounting); err != nil {
			return fail(stderr, err.Error())
		}
	}
	return 0
}
