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
	"fmt"
	"strings"
)

// ErrorKind classifies a Caddyfile adaptation error so callers can react
// programmatically instead of matching message text.
type ErrorKind string

const (
	// KindSyntax is a structural/lexical error: unbalanced braces, an
	// unexpected token or end of file, and similar grammar problems.
	KindSyntax ErrorKind = "syntax"

	// KindUnknownDirective means a directive name was not registered for the
	// active server type. This is distinct from KindArgument so that tooling
	// can suggest installing a module or correcting the directive's spelling.
	KindUnknownDirective ErrorKind = "unknown_directive"

	// KindArgument means the directive or import itself was recognized, but
	// its arguments were wrong: a missing or unexpected argument, a bad
	// value, or an unsupported subdirective.
	KindArgument ErrorKind = "argument"

	// KindImport means an import could not be resolved or expanded: a
	// missing file, an unreadable/glob pattern, or an import cycle.
	KindImport ErrorKind = "import"
)

// ParseError is a Caddyfile parse or adaptation error annotated with the
// source position where it occurred. The position refers to the token being
// processed when the error arose; when that token came from an imported file,
// ImportChain records each import site (file:line) from the root down.
type ParseError struct {
	// Kind classifies the error.
	Kind ErrorKind

	// File and Line locate the offending token in its originating file.
	File string
	Line int

	// Directive is the recognized directive whose arguments were invalid, for
	// KindArgument and KindUnknownDirective. It is empty for other kinds.
	Directive string

	// ImportChain lists the import sites traversed to reach File, outermost
	// first, as "file:line (import)" strings. Empty when File is the root.
	ImportChain []string

	// Err is the underlying error carrying the human-readable message.
	Err error
}

// Error renders the message with its file:line position and import chain.
func (e *ParseError) Error() string {
	var b strings.Builder
	b.WriteString(e.Err.Error())
	// Lexer failures carry their line inside the message itself but have no
	// token, so Line can be zero; render just the file rather than a ":0".
	if e.File != "" && e.Line > 0 {
		fmt.Fprintf(&b, ", at %s:%d", e.File, e.Line)
	} else if e.File != "" {
		fmt.Fprintf(&b, ", at %s", e.File)
	}
	if len(e.ImportChain) > 0 {
		fmt.Fprintf(&b, " import chain: ['%s']", strings.Join(e.ImportChain, "','"))
	}
	return b.String()
}

// Unwrap exposes the underlying error for errors.Is/errors.As.
func (e *ParseError) Unwrap() error { return e.Err }

// NewParseError wraps err as a ParseError of kind at the given token's
// position, carrying its import chain.
func NewParseError(kind ErrorKind, tkn Token, err error) *ParseError {
	return &ParseError{
		Kind:        kind,
		File:        tkn.File,
		Line:        tkn.Line,
		ImportChain: append([]string{}, tkn.imports...),
		Err:         err,
	}
}

// AsParseError finds the first *ParseError in err's tree, if any.
func AsParseError(err error) (*ParseError, bool) {
	var pe *ParseError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// UnknownDirectiveError indicates that dir at tkn's position is not a
// registered directive. It is always wrapped in a ParseError of
// KindUnknownDirective; use errors.As to detect it.
type UnknownDirectiveError struct {
	Directive string
}

func (e *UnknownDirectiveError) Error() string {
	return fmt.Sprintf("unrecognized directive: %s", e.Directive)
}

// ArgumentError indicates that a recognized directive received the wrong
// arguments. It is always wrapped in a ParseError of KindArgument.
type ArgumentError struct {
	Directive string
	Reason    string
}

func (e *ArgumentError) Error() string {
	if e.Directive == "" {
		return e.Reason
	}
	return fmt.Sprintf("directive '%s': %s", e.Directive, e.Reason)
}
