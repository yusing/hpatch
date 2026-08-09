//go:build !cgo

package hpatch

const treeSitterIndentationAvailable = false

func inferIndentationUnit(string, indentationWrapperLanguage) string {
	return ""
}

func proveWrapperMemberships(_ string, probes []indentationWrapperProbe, _ indentationWrapperLanguage) []bool {
	return make([]bool, len(probes))
}
