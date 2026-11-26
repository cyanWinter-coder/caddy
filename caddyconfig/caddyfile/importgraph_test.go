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
	"strings"
	"testing"
)

func TestImportGraphSelfLoop(t *testing.T) {
	g := &importGraph{}
	g.addNode("a")

	if err := g.addEdge("a", "a"); err == nil {
		t.Error("expected error for self-loop cycle a -> a")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected a cycle error, got: %v", err)
	}
}

func TestImportGraphRejectsCycleAndKeepsGraphIntact(t *testing.T) {
	g := &importGraph{}
	g.addNodes([]string{"a", "b", "c"})

	if err := g.addEdge("a", "b"); err != nil {
		t.Fatalf("edge a->b should succeed: %v", err)
	}
	if err := g.addEdge("b", "c"); err != nil {
		t.Fatalf("edge b->c should succeed: %v", err)
	}

	// Closing the loop c->a must fail, and the failed edge must not be recorded.
	err := g.addEdge("c", "a")
	if err == nil {
		t.Fatal("expected a cycle error for c -> a")
	}
	if !strings.Contains(err.Error(), "a -> b -> c -> a") &&
		!strings.Contains(err.Error(), "cycle") {
		t.Errorf("cycle error should render the loop, got: %v", err)
	}
	if g.areConnected("c", "a") {
		t.Error("the rejected edge c -> a must not be recorded")
	}

	// Because the graph is append-only, previously recorded edges survive the
	// rejected edge. A later valid edge can still be added.
	if !g.areConnected("a", "b") || !g.areConnected("b", "c") {
		t.Error("pre-existing edges must survive a rejected edge")
	}
	if err := g.addEdge("a", "c"); err != nil {
		t.Errorf("adding an acyclic edge after a rejected cycle should work: %v", err)
	}
}

func TestImportGraphDuplicateEdgesAreIdempotent(t *testing.T) {
	g := &importGraph{}
	g.addNodes([]string{"a", "b"})

	for i := 0; i < 3; i++ {
		if err := g.addEdge("a", "b"); err != nil {
			t.Fatalf("repeated edge a -> b must stay idempotent, got: %v", err)
		}
	}
	if got := len(g.edges["a"]); got != 1 {
		t.Errorf("expected a single recorded edge, got %d", got)
	}
}

func TestImportGraphDiamondDoesNotCycle(t *testing.T) {
	// root imports A and B, both import C: a legal diamond, not a cycle.
	g := &importGraph{}
	g.addNodes([]string{"root", "a", "b", "c"})

	edges := [][2]string{
		{"root", "a"}, {"root", "b"}, {"a", "c"}, {"b", "c"},
	}
	for _, e := range edges {
		if err := g.addEdge(e[0], e[1]); err != nil {
			t.Errorf("diamond edge %s->%s must be accepted: %v", e[0], e[1], err)
		}
	}
}

func TestImportGraphReachesRendersPath(t *testing.T) {
	g := &importGraph{}
	g.addNodes([]string{"a", "b", "c", "d"})
	_ = g.addEdge("a", "b")
	_ = g.addEdge("b", "c")
	_ = g.addEdge("a", "d")

	path, ok := g.reaches("a", "c")
	if !ok {
		t.Fatal("expected a to reach c")
	}
	if got := strings.Join(path, " -> "); got != "a -> b -> c" {
		t.Errorf("unexpected reachability path: %q", got)
	}
	if _, ok := g.reaches("c", "a"); ok {
		t.Error("c must not reach a")
	}
}
