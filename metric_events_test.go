package hpatch

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestHPatch2InvocationMetricsCountCommandsTargetsAndReasons(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\n", 0o644)
	script := "in file.txt\n" +
		"type " + row(1, "alpha") + " \"A\"\n" +
		"type+ " + row(2, "beta") + " \"B\"\n" +
		"type 9:0000 \"\"\n"

	invocation, err := evaluateInvocationForTest(t, root, script)
	if err == nil {
		t.Fatal("missing row unexpectedly succeeded")
	}
	for _, operation := range []string{"in", "type", "type+"} {
		entry := invocation.Commands[commandOperationIndex(operation)]
		wantInvocations := uint64(1)
		if operation == "type" {
			wantInvocations = 2
		}
		if entry.Invocations != wantInvocations {
			t.Fatalf("%s metrics = %+v", operation, entry)
		}
	}
	if invocation.Commands[commandOperationIndex("type")].Errors != 1 {
		t.Fatalf("type metrics = %+v", invocation.Commands[commandOperationIndex("type")])
	}
	if invocation.Targets[targetVariantLine-1] != (commandMetric{Invocations: 3, Errors: 1}) {
		t.Fatalf("line targets = %+v", invocation.Targets)
	}
	if invocation.Reasons[reasonRowMissing] != 1 {
		t.Fatalf("reasons = %+v", invocation.Reasons)
	}
}

func TestHPatch2TextTargetMetricsDistinguishMultiplicity(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "x x\n", 0o644)
	script := "in file.txt\n" +
		"type " + row(1, "x x") + " \"x\" \"y\"\n" +
		"type+ \"x\" 2 \"!\""
	invocation, err := evaluateInvocationForTest(t, root, script)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Targets[targetVariantTextSingle-1].Invocations != 1 ||
		invocation.Targets[targetVariantTextMultiple-1].Invocations != 1 {
		t.Fatalf("targets = %+v", invocation.Targets)
	}
}

func TestHPatch2MetricsSlotRoundTrip(t *testing.T) {
	value := metrics{}
	value.Commands[commandOperationIndex("type+")] = commandMetric{Invocations: 3, Errors: 1}
	value.Targets[targetVariantRange-1] = commandMetric{Invocations: 3, Errors: 1}
	value.Reasons[reasonEditConflict] = 1
	value.CommandReasons[commandOperationIndex("type+")][reasonEditConflict] = 1
	encoded := encodeMetricsSlot(value, 7)
	got, generation, ok := decodeMetricsSlot(encoded)
	if !ok || generation != 7 || !reflect.DeepEqual(got, value) {
		t.Fatalf("decode = %+v, %d, %v; want %+v, 7, true", got, generation, ok, value)
	}
}

func TestHPatch2StructuredGainNamesTargets(t *testing.T) {
	value := metrics{}
	value.Commands[commandOperationIndex("type")] = commandMetric{Invocations: 1}
	value.Targets[targetVariantLine-1] = commandMetric{Invocations: 1}
	gain := value.gainMetrics()
	if gain.Commands[commandOperationIndex("type")].Name != "type" ||
		gain.Targets[targetVariantLine-1].Name != "line" ||
		gain.Targets[targetVariantTextSingle-1].Name != "text-single" ||
		gain.Commands[commandOperationIndex("type+")].Name != "type+" {
		t.Fatalf("structured gain names = commands %+v, targets %+v", gain.Commands, gain.Targets)
	}
}

func evaluateInvocationForTest(t *testing.T, rootPath, script string) (invocationMetrics, error) {
	t.Helper()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("closing workspace root: %v", err)
		}
	}()
	_, _, invocation, _, _, err := evaluateScript(context.Background(), Workspace{Root: root, CWD: "."}, script)
	return invocation, err
}
