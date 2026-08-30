package rdfgo

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// SPARQL Results XML and JSON, the two formats a SELECT or ASK answer is
// exchanged in.
//
// They are in the core rather than in rdfio, and the reason is what they are.
// Turtle and TriG are ways of writing a GRAPH, which is somebody else's data
// arriving in somebody else's syntax, and parsing them needs a grammar this
// module deliberately does not carry. These are ways of writing THIS package's
// own answer. They are the shape a SPARQL result has when it leaves a process,
// they are W3C Recommendations in their own right, and a SPARQL engine that
// can only hand its answer to Go code is an engine you cannot put behind an
// endpoint. encoding/xml and encoding/json are enough for both, so the core
// stays at zero dependencies.
//
// Reading them matters as much as writing them, and for a reason that is not
// obvious: the W3C SPARQL test suite states 307 of its expected results as
// .srx files. Without a reader, every evaluation test in that suite is unrun —
// which is the one outcome that looks like a pass and is not.

// resultsNS is the namespace both formats are defined under.
const resultsNS = "http://www.w3.org/2005/sparql-results#"

type xmlResults struct {
	XMLName xml.Name    `xml:"sparql"`
	Head    xmlHead     `xml:"head"`
	Boolean *bool       `xml:"boolean"`
	Results *xmlBindSet `xml:"results"`
}

type xmlHead struct {
	// A variable's name is an ATTRIBUTE, not the element's text. Read as
	// []string this decodes every variable to the empty string and the header
	// silently comes back empty -- which does not fail, because a result with
	// no declared variables is a legal thing to write.
	Vars []headVar `xml:"variable"`
}

// names is the header as this package's Vars, in document order. The order is
// part of the answer: it is what the writer orders each row's bindings by, and
// what a suite compares against.
func (h xmlHead) names() []string {
	if len(h.Vars) == 0 {
		return nil
	}
	out := make([]string, 0, len(h.Vars))
	for _, v := range h.Vars {
		out = append(out, v.Name)
	}
	return out
}

type xmlBindSet struct {
	Results []xmlResult `xml:"result"`
}

type xmlResult struct {
	Bindings []xmlBinding `xml:"binding"`
}

type xmlBinding struct {
	Name    string      `xml:"name,attr"`
	IRI     *string     `xml:"uri"`
	Literal *xmlLiteral `xml:"literal"`
	BNode   *string     `xml:"bnode"`
}

type xmlLiteral struct {
	Datatype string `xml:"datatype,attr"`
	Lang     string `xml:"lang,attr"`
	Value    string `xml:",chardata"`
}

// headVar is the write side of xmlHead. It is separate because a variable is
// an attribute on the way out and chardata-free, and reusing one struct for
// both directions would make the reader accept documents the spec does not
// define.
type headVar struct {
	Name string `xml:"name,attr"`
}

// ReadResultsXML parses a SPARQL Results XML document.
//
// A document that is neither a boolean nor a binding set is refused rather
// than returned empty: "this file does not say what it is" and "this query
// matched nothing" are different facts, and a reader that answered the second
// for the first would turn every malformed expectation in a test suite into a
// silently passing test.
func ReadResultsXML(r io.Reader) (*SPARQLResult, error) {
	var doc xmlResults
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("sparql results xml: %w", err)
	}
	switch {
	case doc.Boolean != nil:
		return &SPARQLResult{QueryType: "ASK", Boolean: *doc.Boolean, Count: 1}, nil
	case doc.Results != nil:
		out := &SPARQLResult{QueryType: "SELECT", Vars: doc.Head.names()}
		for _, row := range doc.Results.Results {
			binding := make(map[string]RDFTerm, len(row.Bindings))
			for _, b := range row.Bindings {
				term, err := b.term()
				if err != nil {
					return nil, err
				}
				binding[b.Name] = term
			}
			out.Bindings = append(out.Bindings, binding)
		}
		out.Count = len(out.Bindings)
		return out, nil
	}
	return nil, fmt.Errorf("sparql results xml: document has neither <boolean> nor <results>")
}

func (b xmlBinding) term() (RDFTerm, error) {
	switch {
	case b.IRI != nil:
		return RDFTerm{Kind: "iri", Value: *b.IRI}, nil
	case b.BNode != nil:
		return RDFTerm{Kind: "bnode", Value: *b.BNode}, nil
	case b.Literal != nil:
		return RDFTerm{
			Kind:     "literal",
			Value:    b.Literal.Value,
			Datatype: b.Literal.Datatype,
			Language: b.Literal.Lang,
		}, nil
	}
	return RDFTerm{}, fmt.Errorf("sparql results xml: binding %q names no term", b.Name)
}

// WriteResultsXML serialises a SELECT or ASK result.
//
// A CONSTRUCT or DESCRIBE result is refused rather than rendered as an empty
// binding set: those answer with a graph, the format has no way to carry one,
// and a caller who asked for the wrong serialisation is better told than given
// a well-formed document with their data missing from it.
func WriteResultsXML(w io.Writer, res *SPARQLResult) error {
	if res == nil {
		return fmt.Errorf("sparql results xml: no result")
	}
	if len(res.Triples) > 0 || strings.EqualFold(res.QueryType, "CONSTRUCT") || strings.EqualFold(res.QueryType, "DESCRIBE") {
		return fmt.Errorf("sparql results xml: %s answers with a graph, which this format cannot carry; serialise it as Turtle", res.QueryType)
	}

	doc := struct {
		XMLName xml.Name `xml:"http://www.w3.org/2005/sparql-results# sparql"`
		Head    struct {
			Vars []headVar `xml:"variable"`
		} `xml:"head"`
		Boolean *bool       `xml:"boolean,omitempty"`
		Results *xmlBindSet `xml:"results,omitempty"`
	}{}
	for _, v := range res.Vars {
		doc.Head.Vars = append(doc.Head.Vars, headVar{Name: v})
	}
	if strings.EqualFold(res.QueryType, "ASK") {
		b := res.Boolean
		doc.Boolean = &b
	} else {
		set := &xmlBindSet{}
		for _, row := range res.Bindings {
			var out xmlResult
			// Ordered by the header rather than by map iteration, so two runs
			// of one query produce the same bytes. A results document that
			// differs run to run cannot be diffed, cached or compared against
			// an expectation.
			for _, name := range res.Vars {
				term, ok := row[name]
				if !ok {
					continue
				}
				out.Bindings = append(out.Bindings, bindingOf(name, term))
			}
			set.Results = append(set.Results, out)
		}
		doc.Results = set
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("sparql results xml: %w", err)
	}
	return enc.Flush()
}

func bindingOf(name string, t RDFTerm) xmlBinding {
	b := xmlBinding{Name: name}
	switch t.Kind {
	case "iri":
		v := t.Value
		b.IRI = &v
	case "bnode":
		v := t.Value
		b.BNode = &v
	default:
		b.Literal = &xmlLiteral{Datatype: t.Datatype, Lang: t.Language, Value: t.Value}
	}
	return b
}

// jsonResults is the SPARQL 1.1 Query Results JSON shape.
type jsonResults struct {
	Head struct {
		Vars []string `json:"vars,omitempty"`
	} `json:"head"`
	Boolean *bool `json:"boolean,omitempty"`
	Results *struct {
		Bindings []map[string]jsonTerm `json:"bindings"`
	} `json:"results,omitempty"`
}

type jsonTerm struct {
	Type     string `json:"type"`
	Value    string `json:"value"`
	Datatype string `json:"datatype,omitempty"`
	Lang     string `json:"xml:lang,omitempty"`
}

// ReadResultsJSON parses a SPARQL Results JSON document.
func ReadResultsJSON(r io.Reader) (*SPARQLResult, error) {
	var doc jsonResults
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("sparql results json: %w", err)
	}
	switch {
	case doc.Boolean != nil:
		return &SPARQLResult{QueryType: "ASK", Boolean: *doc.Boolean, Count: 1}, nil
	case doc.Results != nil:
		out := &SPARQLResult{QueryType: "SELECT", Vars: doc.Head.Vars}
		for _, row := range doc.Results.Bindings {
			binding := make(map[string]RDFTerm, len(row))
			for name, t := range row {
				binding[name] = RDFTerm{
					Kind: kindOfJSONType(t.Type), Value: t.Value,
					Datatype: t.Datatype, Language: t.Lang,
				}
			}
			out.Bindings = append(out.Bindings, binding)
		}
		out.Count = len(out.Bindings)
		return out, nil
	}
	return nil, fmt.Errorf("sparql results json: document has neither boolean nor results")
}

// kindOfJSONType maps the format's term types onto this package's.
//
// "typed-literal" is accepted alongside "literal" because the 2008 form of
// this format used it and documents written then are still in circulation,
// including in test suites. Refusing them would be refusing valid history.
func kindOfJSONType(t string) string {
	switch t {
	case "uri":
		return "iri"
	case "bnode":
		return "bnode"
	case "typed-literal":
		return "literal"
	default:
		return t
	}
}

// WriteResultsJSON serialises a SELECT or ASK result.
func WriteResultsJSON(w io.Writer, res *SPARQLResult) error {
	if res == nil {
		return fmt.Errorf("sparql results json: no result")
	}
	if len(res.Triples) > 0 || strings.EqualFold(res.QueryType, "CONSTRUCT") || strings.EqualFold(res.QueryType, "DESCRIBE") {
		return fmt.Errorf("sparql results json: %s answers with a graph, which this format cannot carry; serialise it as Turtle", res.QueryType)
	}
	var doc jsonResults
	doc.Head.Vars = res.Vars
	if strings.EqualFold(res.QueryType, "ASK") {
		b := res.Boolean
		doc.Boolean = &b
	} else {
		rows := make([]map[string]jsonTerm, 0, len(res.Bindings))
		for _, row := range res.Bindings {
			out := make(map[string]jsonTerm, len(row))
			for name, t := range row {
				j := jsonTerm{Value: t.Value, Datatype: t.Datatype, Lang: t.Language}
				switch t.Kind {
				case "iri":
					j.Type = "uri"
				case "bnode":
					j.Type = "bnode"
				default:
					j.Type = "literal"
				}
				out[name] = j
			}
			rows = append(rows, out)
		}
		doc.Results = &struct {
			Bindings []map[string]jsonTerm `json:"bindings"`
		}{Bindings: rows}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
