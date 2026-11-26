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
	"fmt"
	"slices"
	"strings"
)

type adjacency map[string][]string

// importGraph tracks the import relationships between files and snippets while
// a Caddyfile is parsed. Nodes identify a physical file, or a snippet scoped to
// a file ("file:snippet"); edges point from an importing node to the nodes it
// imports.
//
// The graph is append-only: nodes and edges are never removed. Cycle detection
// does not rely on reconstructing reachability from a graph that may be rolled
// back, which previously let shared (diamond) imports corrupt the graph and
// disable detection, turning a cyclic import into unbounded recursion and a
// stack overflow. An edge that would close a cycle is rejected before it is
// recorded, leaving prior edges intact, so a later expansion site can still
// reuse the same nodes safely. A separate expansion-count budget in the parser
// (defaultMaxImportExpansions) bounds total work even if detection were
// bypassed.
type importGraph struct {
	nodes map[string]struct{}
	edges adjacency
}

// addNode records name as a graph node. It is a no-op if name already exists.
func (i *importGraph) addNode(name string) {
	if i.nodes == nil {
		i.nodes = make(map[string]struct{})
	}
	i.nodes[name] = struct{}{}
}

// addNodes records every name as a graph node.
func (i *importGraph) addNodes(names []string) {
	for _, name := range names {
		i.addNode(name)
	}
}

// addEdge records a directed edge from -> to. Both nodes must already be
// registered. A duplicate edge is ignored. An edge that would close a cycle
// (to is reachable from from, including from == to) is rejected with an error
// and the graph is left untouched, so callers never need to roll back.
func (i *importGraph) addEdge(from, to string) error {
	if !i.exists(from) || !i.exists(to) {
		return fmt.Errorf("one of the nodes does not exist")
	}

	if i.areConnected(from, to) {
		// already recorded; nothing to do
		return nil
	}

	if path, cycles := i.reaches(to, from); cycles {
		return fmt.Errorf("a cycle of imports exists: %s", strings.Join(append(path, to), " -> "))
	}

	if i.edges == nil {
		i.edges = make(adjacency)
	}
	i.edges[from] = append(i.edges[from], to)
	return nil
}

// addEdges records directed edges from each entry in tos back to from. If any
// edge would create a cycle, the error is returned and the graph retains only
// the edges recorded before the failing one; nothing is deleted.
func (i *importGraph) addEdges(from string, tos []string) error {
	for _, to := range tos {
		if err := i.addEdge(from, to); err != nil {
			return err
		}
	}
	return nil
}

// areConnected reports whether a direct edge from -> to already exists.
func (i *importGraph) areConnected(from, to string) bool {
	return slices.Contains(i.edges[from], to)
}

// reaches performs a depth-first search starting at from and reports whether
// target is reachable. On a hit it also returns the node path from from to
// target (inclusive), useful for rendering a readable cycle. The search is
// iterative so a long acyclic import chain cannot itself overflow the stack.
func (i *importGraph) reaches(from, target string) ([]string, bool) {
	if from == target {
		return []string{from}, true
	}
	type frame struct {
		node string
		next int
	}
	visited := map[string]bool{from: true}
	stack := []frame{{node: from}}
	path := []string{from}

	for len(stack) > 0 {
		fr := &stack[len(stack)-1]
		neighbors := i.edges[fr.node]
		if fr.next >= len(neighbors) {
			stack = stack[:len(stack)-1]
			path = path[:len(path)-1]
			continue
		}
		neighbor := neighbors[fr.next]
		fr.next++
		if visited[neighbor] {
			continue
		}
		if neighbor == target {
			return append(path, target), true
		}
		visited[neighbor] = true
		stack = append(stack, frame{node: neighbor})
		path = append(path, neighbor)
	}
	return nil, false
}

// exists reports whether key is a registered node.
func (i *importGraph) exists(key string) bool {
	_, exists := i.nodes[key]
	return exists
}
