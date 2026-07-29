package main

import (
	"bytes"
	"fmt"
	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/patchtest"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

type scenario struct {
	name    string
	initial map[string]string
	script  string
	patch   string
}

func main() {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		fatalf("loading GPT-5 tokenizer: %v", err)
	}

	fmt.Printf("GPT-5 encoding: %s\n\n", codec.GetName())
	fmt.Printf("%-28s %8s %12s %8s %11s\n", "scenario", "hpatch", "apply_patch", "saved", "reduction")

	var totalHPatch, totalApplyPatch int
	for _, scenario := range scenarios() {
		hpatchTree, err := runHPatch(scenario)
		if err != nil {
			fatalf("%s: %v", scenario.name, err)
		}
		patchTree, err := patchtest.Apply(scenario.initial, scenario.patch)
		if err != nil {
			fatalf("%s apply_patch input: %v", scenario.name, err)
		}
		if !reflect.DeepEqual(hpatchTree, patchTree) {
			fatalf("%s representations differ:\nhpatch: %#v\napply_patch: %#v", scenario.name, hpatchTree, patchTree)
		}

		hpatchTokens, err := codec.Count(scenario.script)
		if err != nil {
			fatalf("tokenizing %s hpatch input: %v", scenario.name, err)
		}
		patchTokens, err := codec.Count(scenario.patch)
		if err != nil {
			fatalf("tokenizing %s apply_patch input: %v", scenario.name, err)
		}
		totalHPatch += hpatchTokens
		totalApplyPatch += patchTokens
		printRow(scenario.name, hpatchTokens, patchTokens)
	}

	fmt.Println()
	printRow("total", totalHPatch, totalApplyPatch)
}

func scenarios() []scenario {
	return []scenario{
		{
			name: "long-line replacement",
			initial: map[string]string{
				"calc.go": "package calc\n\nfunc total(subtotal, tax int) int { return subtotal + tax + adjustmentForRegion(subtotal, tax) }\n",
			},
			script: "in calc.go\ntsel 3 \"subtotal + tax\"\ntype \"subtotal - discount + tax\"\n",
			patch:  "*** Begin Patch\n*** Update File: calc.go\n@@\n-func total(subtotal, tax int) int { return subtotal + tax + adjustmentForRegion(subtotal, tax) }\n+func total(subtotal, tax int) int { return subtotal - discount + tax + adjustmentForRegion(subtotal, tax) }\n*** End Patch\n",
		},
		{
			name:    "last occurrence delete",
			initial: map[string]string{"logs.txt": "debug info debug\n"},
			script:  "in logs.txt\ntsel 1 \" debug\"\ndel\n",
			patch:   "*** Begin Patch\n*** Update File: logs.txt\n@@\n-debug info debug\n+debug info\n*** End Patch\n",
		},
		{
			name: "block duplication",
			initial: map[string]string{
				"service.go": "func run() {\n\tprepare()\n\texecute()\n}\n",
			},
			script: "in service.go\nrsel 2:3\ncopy\npaste\n",
			patch:  "*** Begin Patch\n*** Update File: service.go\n@@\n \tprepare()\n \texecute()\n+\tprepare()\n+\texecute()\n*** End Patch\n",
		},
		{
			name:    "stable baseline line numbers",
			initial: map[string]string{"config.txt": "name=old\nmode=slow\n"},
			script:  "in config.txt\ntsel 1 \"old\"\ntype \"new\\nextra=yes\"\ntsel 2 \"slow\"\ntype \"fast\"\n",
			patch:   "*** Begin Patch\n*** Update File: config.txt\n@@\n-name=old\n-mode=slow\n+name=new\n+extra=yes\n+mode=fast\n*** End Patch\n",
		},
		{
			name:    "new file typing",
			initial: map[string]string{},
			script:  "new note.txt\ntype \"foo bar\\n\"\n",
			patch:   "*** Begin Patch\n*** Add File: note.txt\n+foo bar\n+\n*** End Patch\n",
		},
		{
			name: "edit move and delete",
			initial: map[string]string{
				"old.txt":      "hello old\n",
				"obsolete.txt": "unused\n",
			},
			script: "in old.txt\ntsel 1 \"old\"\ntype \"new\"\nmv moved.txt\nin obsolete.txt\nrm\n",
			patch:  "*** Begin Patch\n*** Update File: old.txt\n*** Move to: moved.txt\n@@\n-hello old\n+hello new\n*** Delete File: obsolete.txt\n*** End Patch\n",
		},
	}
}

func runHPatch(scenario scenario) (map[string]string, error) {
	root, err := os.MkdirTemp("", "hpatch-compare-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	for path, content := range scenario.initial {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	var stdout, stderr bytes.Buffer
	if exitCode := hpatch.Run(nil, strings.NewReader(scenario.script), &stdout, &stderr, root, ""); exitCode != 0 {
		return nil, fmt.Errorf("hpatch exited %d: %s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		return nil, fmt.Errorf("unexpected stdout %q", stdout.String())
	}
	if stderr.Len() == 0 {
		return nil, fmt.Errorf("missing final-state report")
	}
	return readTree(root)
}

func readTree(root string) (map[string]string, error) {
	tree := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree[relative] = string(content)
		return nil
	})
	return tree, err
}

func printRow(name string, hpatchTokens, patchTokens int) {
	saved := patchTokens - hpatchTokens
	reduction := 0.0
	if patchTokens != 0 {
		reduction = float64(saved) / float64(patchTokens) * 100
	}
	fmt.Printf("%-28s %8d %12d %8d %10.1f%%\n", name, hpatchTokens, patchTokens, saved, reduction)
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "compare: "+format+"\n", arguments...)
	os.Exit(1)
}
