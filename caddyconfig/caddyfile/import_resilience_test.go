// Copyright 2015 Matthew Holt and The Caddy Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package caddyfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeImportFiles creates a set of named fragments under a fresh temporary
// directory and returns the directory plus the root Caddyfile path.
func writeImportFiles(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	var root string
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if name == "Caddyfile" {
			root = path
		}
	}
	return dir, root
}

// TestImportCycleAcrossMutualFiles ensures two fragments that import each
// other are rejected with a positioned, typed error instead of overflowing the
// stack. Previously a failed edge could roll back shared graph nodes and
// disable detection, letting doImport recurse without bound.
func TestImportCycleAcrossMutualFiles(t *testing.T) {
	_, root := writeImportFiles(t, map[string]string{
		"Caddyfile": "import a.caddy\n",
		"a.caddy":   "import b.caddy\n",
		"b.caddy":   "import a.caddy\n",
	})
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Parse(root, body)
	if err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
	pe, ok := AsParseError(err)
	if !ok {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if pe.Kind != KindImport {
		t.Errorf("expected KindImport, got %q", pe.Kind)
	}
	if pe.File == "" || pe.Line == 0 {
		t.Errorf("cycle error must carry a source position, got file=%q line=%d", pe.File, pe.Line)
	}
}

// TestImportCycleDiamondSharedFragment reproduces the rollback-corruption
// shape: a shared fragment C is imported by both A and B and closes a loop.
// Detection must hold even though C is reached through two separate paths.
func TestImportCycleDiamondSharedFragment(t *testing.T) {
	_, root := writeImportFiles(t, map[string]string{
		"Caddyfile": "import a.caddy\nimport b.caddy\n",
		"a.caddy":   "import c.caddy\n",
		"b.caddy":   "import c.caddy\n",
		"c.caddy":   "import a.caddy\n",
	})
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Parse(root, body)
	if err == nil {
		t.Fatal("expected a cycle error through the shared fragment, got nil")
	}
	if pe, ok := AsParseError(err); !ok || pe.Kind != KindImport {
		t.Fatalf("expected a typed import error, got: %v", err)
	}
}

// TestRepeatedImportIsLegalAndStable confirms that importing the same fragment
// from two sites (a diamond without a cycle) expands both times and yields
// identical results on repeated adaptation.
func TestRepeatedImportIsLegalAndStable(t *testing.T) {
	_, root := writeImportFiles(t, map[string]string{
		"Caddyfile":    "import a.caddy\nimport b.caddy\n",
		"a.caddy":      "import shared.caddy\n",
		"b.caddy":      "import shared.caddy\n",
		"shared.caddy": "localhost:1\n\trespond 1\n",
	})
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}

	first, err := Parse(root, body)
	if err != nil {
		t.Fatalf("first parse failed: %v", err)
	}
	second, err := Parse(root, body)
	if err != nil {
		t.Fatalf("second parse failed: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("block count differs across runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if len(first[i].Segments) != len(second[i].Segments) {
			t.Fatalf("segment count in block %d differs across runs", i)
		}
	}
}

// TestParseFailureReturnsNoPartialResults enforces all-or-nothing adaptation:
// any error must come back with nil server blocks so callers cannot consume a
// half-expanded configuration.
func TestParseFailureReturnsNoPartialResults(t *testing.T) {
	_, root := writeImportFiles(t, map[string]string{
		"Caddyfile": "localhost:1 {\n\trespond 1\n}\nimport does-not-exist.caddy\n",
	})
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}

	blocks, err := Parse(root, body)
	if err == nil {
		t.Fatal("expected an import error, got nil")
	}
	if blocks != nil {
		t.Errorf("expected nil blocks on error, got %d blocks", len(blocks))
	}
}

// TestImportErrorPositionPointsAtImportSite verifies that a missing imported
// file is reported with the file and line of the import directive, including
// through a nested import chain.
func TestImportErrorPositionPointsAtImportSite(t *testing.T) {
	dir, root := writeImportFiles(t, map[string]string{
		"Caddyfile": "import a.caddy\n",
		"a.caddy":   "import missing.caddy\n",
	})
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Parse(root, body)
	if err == nil {
		t.Fatal("expected a missing-file error, got nil")
	}
	pe, ok := AsParseError(err)
	if !ok {
		t.Fatalf("expected *ParseError, got %T", err)
	}
	wantFile := filepath.Join(dir, "a.caddy")
	if pe.File != wantFile {
		t.Errorf("expected error in %q, got %q", wantFile, pe.File)
	}
	if pe.Line != 1 {
		t.Errorf("expected error on line 1, got %d", pe.Line)
	}
	if len(pe.ImportChain) == 0 {
		t.Error("expected an import chain locating the nested import")
	}
}

// TestUnknownDirectiveVsArgumentKinds ensures the two failure classes are
// separately detectable via errors.As rather than message matching.
func TestUnknownDirectiveVsArgumentKinds(t *testing.T) {
	// An unknown directive is produced at the httpcaddyfile layer; here we only
	// verify the typed errors construct and classify correctly through the
	// caddyfile error helpers.
	unkTkn := Token{File: "Caddyfile", Line: 3}
	unk := NewParseError(KindUnknownDirective, unkTkn, &UnknownDirectiveError{Directive: "bogus"})
	var ude *UnknownDirectiveError
	if !errors.As(unk, &ude) {
		t.Fatal("UnknownDirectiveError should be discoverable with errors.As")
	}
	if ude.Directive != "bogus" {
		t.Errorf("unexpected directive name %q", ude.Directive)
	}
	if pe, _ := AsParseError(unk); pe.Kind != KindUnknownDirective {
		t.Errorf("expected KindUnknownDirective, got %q", pe.Kind)
	}

	argTkn := Token{File: "Caddyfile", Line: 7}
	arg := NewParseError(KindArgument, argTkn, &ArgumentError{
		Directive: "respond",
		Reason:    "missing argument",
	})
	var ae *ArgumentError
	if !errors.As(arg, &ae) {
		t.Fatal("ArgumentError should be discoverable with errors.As")
	}
	if pe, _ := AsParseError(arg); pe.Kind != KindArgument {
		t.Errorf("expected KindArgument, got %q", pe.Kind)
	}

	// The two kinds must never compare equal.
	if unk.Kind == arg.Kind {
		t.Error("unknown-directive and argument errors must have distinct kinds")
	}
}

// TestParseIsIdempotent runs adaptation repeatedly and confirms the structural
// output is stable, satisfying repeatable adaptation of the same input.
func TestParseIsIdempotent(t *testing.T) {
	input := []byte(`localhost:8080 {
	respond "hello"
	header X-Server caddy
}`)
	first, err := Parse("Caddyfile", input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := Parse("Caddyfile", input)
		if err != nil {
			t.Fatalf("iteration %d failed: %v", i, err)
		}
		if len(got) != len(first) || len(got[0].Segments) != len(first[0].Segments) {
			t.Fatalf("iteration %d produced a different structure", i)
		}
	}
}

// TestImportDepthLimitGuardsRunawayExpansion drives a deep import chain with a
// deliberately small depth limit and confirms a bounded, typed error is
// returned instead of a runtime stack overflow.
func TestImportDepthLimitGuardsRunawayExpansion(t *testing.T) {
	dir := t.TempDir()
	const depth = 8
	for i := 0; i < depth; i++ {
		var content string
		if i < depth-1 {
			content = "import f" + itoa(i+1) + ".caddy\n"
		} else {
			content = "respond ok\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+itoa(i)+".caddy"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, "root.caddy")
	if err := os.WriteFile(root, []byte("import f0.caddy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := allTokens(root, body)
	if err != nil {
		t.Fatal(err)
	}
	p := parser{
		Dispenser: NewDispenser(tokens),
		importGraph: importGraph{
			nodes: make(map[string]struct{}),
			edges: make(adjacency),
		},
		importExpansions:    new(int),
		maxImportExpansions: 3,
	}

	_, err = p.parseAll()
	if err == nil {
		t.Fatal("expected an import-depth error, got nil")
	}
	pe, ok := AsParseError(err)
	if !ok || pe.Kind != KindImport {
		t.Fatalf("expected a typed import error, got: %v", err)
	}
}

// itoa is a tiny dependency-free integer formatter for test file names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
