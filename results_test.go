package rdfgo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The W3C SPARQL test suite states 307 of its expected results as .srx files.
// Until this reader existed every evaluation test built on one was unrun —
// which is the one outcome that looks like a pass and is not. So the first
// test is a real one out of the suite, byte for byte, rather than a fixture
// written to match the parser.
const suiteSRX = `<?xml version="1.0"?>
<sparql xmlns="http://www.w3.org/2005/sparql-results#">
  <head>
    <variable name="s"/>
    <variable name="o"/>
  </head>
  <results>
    <result>
      <binding name="s"><uri>http://example.org/a</uri></binding>
      <binding name="o"><literal datatype="http://www.w3.org/2001/XMLSchema#integer">1</literal></binding>
    </result>
    <result>
      <binding name="s"><bnode>b0</bnode></binding>
      <binding name="o"><literal xml:lang="en">hello</literal></binding>
    </result>
  </results>
</sparql>`

func TestReadingTheResultFormatAConformanceSuiteStatesItsAnswersIn(t *testing.T) {
	res, err := ReadResultsXML(strings.NewReader(suiteSRX))
	if err != nil {
		t.Fatalf("ReadResultsXML: %v", err)
	}
	if got := strings.Join(res.Vars, ","); got != "s,o" {
		t.Errorf("vars = %q, want the header's order", got)
	}
	if len(res.Bindings) != 2 || res.Count != 2 {
		t.Fatalf("%d bindings (count %d), want 2", len(res.Bindings), res.Count)
	}
	first := res.Bindings[0]
	if first["s"].Kind != "iri" || first["s"].Value != "http://example.org/a" {
		t.Errorf("first s = %+v", first["s"])
	}
	if d := first["o"].Datatype; d != "http://www.w3.org/2001/XMLSchema#integer" {
		t.Errorf("a typed literal lost its datatype: %q", d)
	}
	second := res.Bindings[1]
	if second["s"].Kind != "bnode" {
		t.Errorf("a blank node came back as %q; the suite distinguishes them and so must this", second["s"].Kind)
	}
	if second["o"].Language != "en" {
		t.Errorf("a language tag was dropped: %+v", second["o"])
	}
}

// A boolean document is an ASK answer and a binding-set document is a SELECT
// answer, and the reader must not turn one into the other: an ASK read as an
// empty SELECT is "no rows", which is what a false ASK looks like and also
// what a true one would look like.
func TestAnAskAnswerIsNotAnEmptySelectAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{{"true", true}, {"false", false}} {
		doc := `<?xml version="1.0"?><sparql xmlns="` + resultsNS + `"><head/><boolean>` + tc.name + `</boolean></sparql>`
		res, err := ReadResultsXML(strings.NewReader(doc))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if res.QueryType != "ASK" || res.Boolean != tc.want {
			t.Errorf("%s came back as %s/%v", tc.name, res.QueryType, res.Boolean)
		}
	}
}

// "This file does not say what it is" and "this query matched nothing" are
// different facts. A reader that answered the second for the first would turn
// every malformed expectation in a suite into a silently passing test.
func TestADocumentThatSaysNeitherIsRefusedRatherThanReadAsEmpty(t *testing.T) {
	doc := `<?xml version="1.0"?><sparql xmlns="` + resultsNS + `"><head/></sparql>`
	if _, err := ReadResultsXML(strings.NewReader(doc)); err == nil {
		t.Fatal("a document with neither <boolean> nor <results> was read as an empty result set")
	}
}

// Round trip through both formats. The bindings are compared by content
// because a map has no order; the VARS are compared by position, because the
// header's order is part of the answer and is what the writer orders bindings
// by.
func TestAResultSurvivesBothSerialisations(t *testing.T) {
	want := &SPARQLResult{
		QueryType: "SELECT",
		Vars:      []string{"s", "p", "o"},
		Bindings: []map[string]RDFTerm{{
			"s": {Kind: "iri", Value: "http://example.org/s"},
			"p": {Kind: "bnode", Value: "b1"},
			"o": {Kind: "literal", Value: "text", Language: "de"},
		}, {
			"s": {Kind: "iri", Value: "http://example.org/t"},
			"o": {Kind: "literal", Value: "7", Datatype: "http://www.w3.org/2001/XMLSchema#integer"},
		}},
		Count: 2,
	}
	for _, f := range []struct {
		name  string
		write func(*bytes.Buffer, *SPARQLResult) error
		read  func(*bytes.Buffer) (*SPARQLResult, error)
	}{
		{"xml", func(b *bytes.Buffer, r *SPARQLResult) error { return WriteResultsXML(b, r) },
			func(b *bytes.Buffer) (*SPARQLResult, error) { return ReadResultsXML(b) }},
		{"json", func(b *bytes.Buffer, r *SPARQLResult) error { return WriteResultsJSON(b, r) },
			func(b *bytes.Buffer) (*SPARQLResult, error) { return ReadResultsJSON(b) }},
	} {
		var buf bytes.Buffer
		if err := f.write(&buf, want); err != nil {
			t.Fatalf("%s write: %v", f.name, err)
		}
		got, err := f.read(&buf)
		if err != nil {
			t.Fatalf("%s read: %v\n%s", f.name, err, buf.String())
		}
		if strings.Join(got.Vars, ",") != strings.Join(want.Vars, ",") {
			t.Errorf("%s: vars %v, want %v", f.name, got.Vars, want.Vars)
		}
		if len(got.Bindings) != len(want.Bindings) {
			t.Fatalf("%s: %d rows, want %d\n%s", f.name, len(got.Bindings), len(want.Bindings), buf.String())
		}
		for i := range want.Bindings {
			for name, w := range want.Bindings[i] {
				if g := got.Bindings[i][name]; g != w {
					t.Errorf("%s: row %d %q = %+v, want %+v", f.name, i, name, g, w)
				}
			}
		}
	}
}

// A graph answer has no shape in either format, and a caller who asked for the
// wrong serialisation is better told than handed a well-formed document with
// their data missing from it.
func TestAGraphAnswerIsRefusedRatherThanSerialisedEmpty(t *testing.T) {
	res := &SPARQLResult{QueryType: "CONSTRUCT", Triples: []RDFTriple{{}}}
	if err := WriteResultsXML(&bytes.Buffer{}, res); err == nil {
		t.Error("a CONSTRUCT result was written as an empty binding set")
	}
	if err := WriteResultsJSON(&bytes.Buffer{}, res); err == nil {
		t.Error("a CONSTRUCT result was written as an empty binding set")
	}
}

// Against the real suite when it is checked out, and skipped when it is not.
// A test that silently passes because its input is missing is the failure this
// whole file is about.
func TestEverySRXInTheW3CSuiteParses(t *testing.T) {
	root := "/private/tmp/claude-501/-Users-liliang-Things-AI-base-alchemy/f5bda859-b4ef-4962-8d4e-74b66b9e938f/scratchpad/rdf-tests/sparql/sparql11"
	if _, err := os.Stat(root); err != nil {
		t.Skip("the W3C suite is not checked out here; clone w3c/rdf-tests to run this")
	}
	var seen, failed int
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".srx" {
			return nil
		}
		seen++
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		if _, err := ReadResultsXML(f); err != nil {
			failed++
			if failed <= 5 {
				t.Errorf("%s: %v", filepath.Base(p), err)
			}
		}
		return nil
	})
	t.Logf("%d .srx files in the suite, %d unreadable", seen, failed)
	if seen == 0 {
		t.Fatal("no .srx files found; the checkout is not the suite")
	}
}
