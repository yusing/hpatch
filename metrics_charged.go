package hpatch

import "os"

// chargedScriptVariable names the text to account for as model output when the
// evaluated script is not what the model wrote. A caller that repairs a rejected
// script from a short correction rebuilds the complete script before invoking
// hpatch; charging the rebuilt script would report the repair as costing as much
// as the full retry it replaces, inverting the comparison gain measures.
const chargedScriptVariable = "HPATCH_CHARGED_SCRIPT"

// chargedScript is the text whose tokens count as this invocation's model
// output. It defaults to the evaluated script, because ordinarily the model
// wrote exactly what hpatch received. Only output accounting uses it; evaluation
// always reads stdin.
func chargedScript(evaluated string) string {
	charged, ok := os.LookupEnv(chargedScriptVariable)
	if !ok || charged == "" {
		return evaluated
	}
	return charged
}
