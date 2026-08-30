package rdfgo

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// RDFTermIRI represents an IRI/resource term.
	RDFTermIRI = "iri"
	// RDFTermBlankNode represents a blank node term.
	RDFTermBlankNode = "blank_node"
	// RDFTermLiteral represents a literal term.
	RDFTermLiteral = "literal"
)

var builtinNamespaces = map[string]string{
	"rdf":    "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
	"rdfs":   "http://www.w3.org/2000/01/rdf-schema#",
	"xsd":    "http://www.w3.org/2001/XMLSchema#",
	"owl":    "http://www.w3.org/2002/07/owl#",
	"schema": "https://schema.org/",
	"foaf":   "http://xmlns.com/foaf/0.1/",
	"skos":   "http://www.w3.org/2004/02/skos/core#",
}

// Namespace represents a prefix to IRI mapping.
type Namespace struct {
	Prefix string `json:"prefix"`
	URI    string `json:"uri"`
}

// RDFTerm represents one RDF term.
type RDFTerm struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Datatype string `json:"datatype,omitempty"`
	Language string `json:"language,omitempty"`
}

// RDFTriple represents one RDF triple or quad when Graph is set.
type RDFTriple struct {
	ID         string   `json:"id,omitempty"`
	Subject    RDFTerm  `json:"subject"`
	Predicate  RDFTerm  `json:"predicate"`
	Object     RDFTerm  `json:"object"`
	Graph      *RDFTerm `json:"graph,omitempty"`
	Inferred   bool     `json:"inferred,omitempty"`
	Rule       string   `json:"rule,omitempty"`
	SupportIDs []string `json:"support_ids,omitempty"`
}

// TriplePattern filters triple lookup operations. Nil fields behave as wildcards.
type TriplePattern struct {
	Subject   *RDFTerm `json:"subject,omitempty"`
	Predicate *RDFTerm `json:"predicate,omitempty"`
	Object    *RDFTerm `json:"object,omitempty"`
	Graph     *RDFTerm `json:"graph,omitempty"`
	Inferred  *bool    `json:"inferred,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

// NewIRI creates an IRI term.
func NewIRI(value string) RDFTerm {
	return RDFTerm{
		Kind:  RDFTermIRI,
		Value: strings.TrimSpace(strings.Trim(value, "<>")),
	}
}

// NewBlankNode creates a blank node term.
func NewBlankNode(value string) RDFTerm {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "_:")
	return RDFTerm{
		Kind:  RDFTermBlankNode,
		Value: value,
	}
}

// NewLiteral creates a plain literal term.
func NewLiteral(value string) RDFTerm {
	return RDFTerm{
		Kind:  RDFTermLiteral,
		Value: value,
	}
}

// NewLangLiteral creates a language-tagged literal term.
func NewLangLiteral(value, language string) RDFTerm {
	return RDFTerm{
		Kind:     RDFTermLiteral,
		Value:    value,
		Language: strings.ToLower(strings.TrimSpace(language)),
	}
}

// NewTypedLiteral creates a typed literal term.
func NewTypedLiteral(value, datatype string) RDFTerm {
	return RDFTerm{
		Kind:     RDFTermLiteral,
		Value:    value,
		Datatype: strings.TrimSpace(strings.Trim(datatype, "<>")),
	}
}

// String renders the term using RDF-compatible syntax.
func (t RDFTerm) String() string {
	switch t.Kind {
	case RDFTermIRI:
		return "<" + escapeIRI(t.Value) + ">"
	case RDFTermBlankNode:
		return "_:" + t.Value
	case RDFTermLiteral:
		out := strconv.Quote(t.Value)
		if t.Language != "" {
			return out + "@" + t.Language
		}
		if t.Datatype != "" {
			return out + "^^<" + escapeIRI(t.Datatype) + ">"
		}
		return out
	default:
		return t.Value
	}
}

// String renders the triple/quad using RDF syntax.
func (t RDFTriple) String() string {
	if t.Graph != nil {
		return fmt.Sprintf("%s %s %s %s .", t.Subject.String(), t.Predicate.String(), t.Object.String(), t.Graph.String())
	}
	return fmt.Sprintf("%s %s %s .", t.Subject.String(), t.Predicate.String(), t.Object.String())
}

func looksLikeAbsoluteIRI(value string) bool {
	if value == "" {
		return false
	}
	if strings.Contains(value, "://") {
		return true
	}
	for _, prefix := range []string{"urn:", "mailto:", "did:", "tag:"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func expandIRIWithNamespaces(value string, namespaces []Namespace) string {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" || looksLikeAbsoluteIRI(value) {
		return value
	}

	colon := strings.IndexByte(value, ':')
	if colon <= 0 {
		return value
	}
	prefix := value[:colon]
	local := value[colon+1:]
	if strings.Contains(prefix, "/") {
		return value
	}

	for _, ns := range namespaces {
		if ns.Prefix == prefix {
			return ns.URI + local
		}
	}
	return value
}

func compactIRIWithNamespaces(value string, namespaces []Namespace) string {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" {
		return ""
	}
	best := value
	bestLen := 0
	for _, ns := range namespaces {
		if strings.HasPrefix(value, ns.URI) && len(ns.URI) > bestLen {
			best = ns.Prefix + ":" + strings.TrimPrefix(value, ns.URI)
			bestLen = len(ns.URI)
		}
	}
	return best
}

func escapeIRI(value string) string {
	return strings.ReplaceAll(value, ">", "%3E")
}

// inferenceTermKey and cloneGraphTerm come from the RDFS inference
// code, which is not part of this package, but the SPARQL property-path
// evaluator uses both: it keys visited nodes by term identity while walking a
// zero-or-more path, and it copies the graph term onto every triple it
// derives. They are here because they are term operations and not inference —
// nothing about either one knows what a rule is.
func inferenceTermKey(term RDFTerm) string {
	return term.Kind + "|" + term.Value + "|" + term.Datatype + "|" + term.Language
}

func cloneGraphTerm(term *RDFTerm) *RDFTerm {
	if term == nil {
		return nil
	}
	out := *term
	return &out
}
