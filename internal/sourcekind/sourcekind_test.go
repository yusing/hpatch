package sourcekind

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		path       string
		language   string
		jsx        bool
		validation bool
		ok         bool
	}{
		{path: "a.go", language: "go", ok: true},
		{path: "a.d.ts", language: "typescript", validation: true, ok: true},
		{path: "a.d.mts", language: "typescript", ok: true},
		{path: "a.tsx", language: "typescript", jsx: true, ok: true},
		{path: "a.jsx", language: "javascript", jsx: true, ok: true},
		{path: "a.pyi", language: "python", ok: true},
		{path: "a.GO"},
		{path: "a.txt"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			format, ok := Classify(test.path)
			if ok != test.ok || format.Language != test.language || format.JSX != test.jsx || format.SyntaxValidation != test.validation {
				t.Fatalf("Classify(%q) = (%+v, %t)", test.path, format, ok)
			}
		})
	}
}
