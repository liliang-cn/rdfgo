// Package rdfio parses and writes RDF documents for the rdfgo engine.
//
// It is a separate package from rdfgo for one reason: it needs a parser and
// rdfgo does not. Turtle and TriG are real grammars, and the library that
// reads them (github.com/0x51-dev/rdf) drags in its own parser-combinator
// dependencies. A caller who wants to run a SPARQL query against triples they
// already hold should not compile any of that, and if the two lived in one
// package they would have no choice — Go's unit of dependency is the package.
// So rdfgo's go.mod has no requires and this package's does the work.
//
// Everything here is a pure conversion between a document and
// []rdfgo.RDFTriple. Nothing in this package writes to a store, and that is
// also on purpose: importing is "parse, then decide what to do with the
// triples", and the deciding — batching, deduplicating, refusing — belongs to
// whoever owns the store rather than to the thing that read the file.
//
// # What came across and what did not
//
// This is the original external-format file with its storage half removed.
// There, importTurtle parsed and then called UpsertTriplesBatch on the SQL
// store in the same function; here ParseTurtle stops at the triples. The
// N-Triples and N-Quads line formats are not here at all: the original reads those
// with a hand-rolled scanner in rdf.go rather than with this library, and that
// scanner is part of the SQL store this extraction left behind.
package rdfio

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	rdfnq "github.com/0x51-dev/rdf/nquads"
	rdfnt "github.com/0x51-dev/rdf/ntriples"
	rdftrig "github.com/0x51-dev/rdf/trig"
	rdfttl "github.com/0x51-dev/rdf/turtle"

	"github.com/liliang-cn/rdfgo"
)

// ParseTurtle reads a Turtle document and returns its triples.
//
// baseIRI resolves the document's relative IRIs. It is a parameter rather than
// something derived here because only the caller knows where the document came
// from: the original used the directory of its database file, which is a sensible
// default for a store that reads files off disk and a meaningless one for a
// document that arrived over HTTP. See BaseIRIForPath for that default.
func ParseTurtle(r io.Reader, baseIRI string) ([]rdfgo.RDFTriple, error) {
	payload, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read turtle payload: %w", err)
	}
	doc, err := rdfttl.ParseDocument(string(payload))
	if err != nil {
		return nil, fmt.Errorf("parse turtle document: %w", err)
	}
	triples, err := rdfttl.EvaluateDocument(doc, baseIRI)
	if err != nil {
		return nil, fmt.Errorf("evaluate turtle document: %w", err)
	}
	return convertTriples(triples)
}

// ParseTriG reads a TriG document and returns its quads, each as an RDFTriple
// with Graph set for the named-graph blocks.
func ParseTriG(r io.Reader) ([]rdfgo.RDFTriple, error) {
	payload, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read trig payload: %w", err)
	}
	doc, err := rdftrig.ParseDocument(string(payload))
	if err != nil {
		return nil, fmt.Errorf("parse trig document: %w", err)
	}
	quads, err := rdftrig.EvaluateDocument(doc)
	if err != nil {
		return nil, fmt.Errorf("evaluate trig document: %w", err)
	}
	return convertQuads(quads)
}

// ParseNTriples reads an N-Triples document.
//
// It is here because a conformance suite is written in it. The W3C SPARQL and
// SHACL test suites carry their data and their expected graphs as .nt files
// as often as .ttl, and a format the harness cannot read is a test that can
// never be reported as anything but unrun -- which is the one result that
// looks like a pass and is not one. The conversion is convertTriples', which
// the Turtle path already goes through, so this adds a reader and no new way
// for a term to be wrong.
func ParseNTriples(r io.Reader) ([]rdfgo.RDFTriple, error) {
	payload, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read n-triples payload: %w", err)
	}
	doc, err := rdfnt.ParseDocument(string(payload))
	if err != nil {
		return nil, fmt.Errorf("parse n-triples document: %w", err)
	}
	return convertTriples(doc)
}

// ParseNQuads reads an N-Quads document, keeping the graph label on each
// triple the way ParseTriG does.
func ParseNQuads(r io.Reader) ([]rdfgo.RDFTriple, error) {
	payload, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read n-quads payload: %w", err)
	}
	doc, err := rdfnq.ParseDocument(string(payload))
	if err != nil {
		return nil, fmt.Errorf("parse n-quads document: %w", err)
	}
	return convertQuads(doc)
}

// WriteTriG serialises triples as TriG, grouping the ones that name a graph
// into blocks and leaving the rest in the default graph.
//
// namespaces are written as @prefix headers and used to shorten IRIs. Passing
// none is legal and produces a document of fully expanded IRIs, which is
// correct TriG and merely verbose.
func WriteTriG(w io.Writer, triples []rdfgo.RDFTriple, namespaces []rdfgo.Namespace) error {
	buffered := bufio.NewWriter(w)
	defer func() { _ = buffered.Flush() }()

	for _, ns := range namespaces {
		if _, err := fmt.Fprintf(buffered, "@prefix %s: <%s> .\n", ns.Prefix, ns.URI); err != nil {
			return err
		}
	}
	if len(namespaces) > 0 && len(triples) > 0 {
		if _, err := fmt.Fprintln(buffered); err != nil {
			return err
		}
	}

	defaultGraph := make([]rdfgo.RDFTriple, 0)
	graphOrder := make([]string, 0)
	graphBuckets := make(map[string][]rdfgo.RDFTriple)
	graphTerms := make(map[string]rdfgo.RDFTerm)
	for _, triple := range triples {
		if triple.Graph == nil {
			defaultGraph = append(defaultGraph, triple)
			continue
		}
		key := triple.Graph.Kind + "|" + triple.Graph.Value
		if _, ok := graphBuckets[key]; !ok {
			graphOrder = append(graphOrder, key)
			graphTerms[key] = *triple.Graph
		}
		graphBuckets[key] = append(graphBuckets[key], triple)
	}

	if err := writeTriGStatements(buffered, defaultGraph, "", namespaces); err != nil {
		return err
	}
	if len(defaultGraph) > 0 && len(graphOrder) > 0 {
		if _, err := fmt.Fprintln(buffered); err != nil {
			return err
		}
	}
	for i, key := range graphOrder {
		if _, err := fmt.Fprintf(buffered, "%s {\n", compactTerm(graphTerms[key], namespaces)); err != nil {
			return err
		}
		if err := writeTriGStatements(buffered, graphBuckets[key], "\t", namespaces); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(buffered, "}"); err != nil {
			return err
		}
		if i+1 < len(graphOrder) {
			if _, err := fmt.Fprintln(buffered); err != nil {
				return err
			}
		}
	}
	return buffered.Flush()
}

func writeTriGStatements(w io.Writer, triples []rdfgo.RDFTriple, indent string, namespaces []rdfgo.Namespace) error {
	for _, triple := range triples {
		if _, err := fmt.Fprintf(w, "%s%s %s %s .\n",
			indent,
			compactTerm(triple.Subject, namespaces),
			compactTerm(triple.Predicate, namespaces),
			compactTerm(triple.Object, namespaces),
		); err != nil {
			return err
		}
	}
	return nil
}

// BaseIRIForPath is the base IRI the original used when importing Turtle: the
// directory holding path, as a file: URI.
//
// It is exported rather than applied automatically because a base IRI that is
// wrong is worse than one that is absent — it silently produces triples about
// the wrong subjects — so the caller has to say it means this one.
func BaseIRIForPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "file:///"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	info, err := os.Stat(absPath)
	if err == nil && info.IsDir() {
		return fileURI(absPath) + "/"
	}
	dir := filepath.Dir(absPath)
	return fileURI(dir) + "/"
}

func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if strings.HasPrefix(slashed, "/") {
		return "file://" + slashed
	}
	return "file:///" + slashed
}

func convertTriples(doc rdfnt.Document) ([]rdfgo.RDFTriple, error) {
	triples := make([]rdfgo.RDFTriple, 0, len(doc))
	for _, triple := range doc {
		converted, err := externalTripleToRDFTriple(triple)
		if err != nil {
			return nil, err
		}
		triples = append(triples, *converted)
	}
	return triples, nil
}

func convertQuads(doc rdfnq.Document) ([]rdfgo.RDFTriple, error) {
	triples := make([]rdfgo.RDFTriple, 0, len(doc))
	for _, quad := range doc {
		converted, err := externalQuadToRDFTriple(quad)
		if err != nil {
			return nil, err
		}
		triples = append(triples, *converted)
	}
	return triples, nil
}

func externalTripleToRDFTriple(triple rdfnt.Triple) (*rdfgo.RDFTriple, error) {
	subject, err := externalSubjectToTerm(triple.Subject)
	if err != nil {
		return nil, err
	}
	predicate, err := externalPredicateToTerm(triple.Predicate)
	if err != nil {
		return nil, err
	}
	object, err := externalObjectToTerm(triple.Object)
	if err != nil {
		return nil, err
	}
	return &rdfgo.RDFTriple{
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}, nil
}

func externalQuadToRDFTriple(quad rdfnq.Quad) (*rdfgo.RDFTriple, error) {
	triple, err := externalTripleToRDFTriple(quad.Triple)
	if err != nil {
		return nil, err
	}
	if quad.GraphLabel != nil {
		graphTerm, err := externalSubjectToTerm(quad.GraphLabel)
		if err != nil {
			return nil, err
		}
		triple.Graph = &graphTerm
	}
	return triple, nil
}

func externalSubjectToTerm(subject rdfnt.Subject) (rdfgo.RDFTerm, error) {
	switch value := subject.(type) {
	case rdfnt.IRIReference:
		return rdfgo.NewIRI(string(value)), nil
	case *rdfnt.IRIReference:
		if value == nil {
			return rdfgo.RDFTerm{}, fmt.Errorf("nil iri subject")
		}
		return rdfgo.NewIRI(string(*value)), nil
	case rdfnt.BlankNode:
		return rdfgo.NewBlankNode(string(value)), nil
	case *rdfnt.BlankNode:
		if value == nil {
			return rdfgo.RDFTerm{}, fmt.Errorf("nil blank subject")
		}
		return rdfgo.NewBlankNode(string(*value)), nil
	default:
		return rdfgo.RDFTerm{}, fmt.Errorf("unsupported rdf subject type %T", subject)
	}
}

func externalPredicateToTerm(predicate rdfnt.IRIReference) (rdfgo.RDFTerm, error) {
	return rdfgo.NewIRI(string(predicate)), nil
}

func externalObjectToTerm(object rdfnt.Object) (rdfgo.RDFTerm, error) {
	switch value := object.(type) {
	case rdfnt.IRIReference:
		return rdfgo.NewIRI(string(value)), nil
	case *rdfnt.IRIReference:
		if value == nil {
			return rdfgo.RDFTerm{}, fmt.Errorf("nil iri object")
		}
		return rdfgo.NewIRI(string(*value)), nil
	case rdfnt.BlankNode:
		return rdfgo.NewBlankNode(string(value)), nil
	case *rdfnt.BlankNode:
		if value == nil {
			return rdfgo.RDFTerm{}, fmt.Errorf("nil blank object")
		}
		return rdfgo.NewBlankNode(string(*value)), nil
	case rdfnt.Literal:
		return externalLiteralToTerm(value), nil
	case *rdfnt.Literal:
		if value == nil {
			return rdfgo.RDFTerm{}, fmt.Errorf("nil literal object")
		}
		return externalLiteralToTerm(*value), nil
	default:
		return rdfgo.RDFTerm{}, fmt.Errorf("unsupported rdf object type %T", object)
	}
}

func externalLiteralToTerm(literal rdfnt.Literal) rdfgo.RDFTerm {
	if literal.Reference != nil {
		return rdfgo.NewTypedLiteral(literal.Value, string(*literal.Reference))
	}
	if literal.Language != "" {
		return rdfgo.NewLangLiteral(literal.Value, literal.Language)
	}
	return rdfgo.NewLiteral(literal.Value)
}

// compactTerm is the original store's compactTerm with the store removed: it
// took a context only to look the namespaces up, and here they are already in
// hand. The shortening rules are unchanged, including the one that matters —
// an IRI no prefix covers is written in full angle brackets rather than left
// bare, because a bare IRI is not TriG.
func compactTerm(term rdfgo.RDFTerm, namespaces []rdfgo.Namespace) string {
	switch term.Kind {
	case rdfgo.RDFTermIRI:
		compacted := compactIRI(term.Value, namespaces)
		if compacted == term.Value {
			return term.String()
		}
		return compacted
	case rdfgo.RDFTermBlankNode:
		return term.String()
	case rdfgo.RDFTermLiteral:
		if term.Datatype == "" {
			return term.String()
		}
		compacted := compactIRI(term.Datatype, namespaces)
		out := strconv.Quote(term.Value)
		if term.Language != "" {
			return out + "@" + term.Language
		}
		if compacted == term.Datatype {
			return out + "^^<" + strings.ReplaceAll(term.Datatype, ">", "%3E") + ">"
		}
		return out + "^^" + compacted
	default:
		return term.Value
	}
}

func compactIRI(value string, namespaces []rdfgo.Namespace) string {
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
