package rdfgo

import "testing"

// memStore has to satisfy Store, and a compile-time assertion says so at the
// point of definition rather than at the first call site. Without it, an
// accidental widening of Store would be reported as "cannot use store as
// Store" somewhere in the middle of a query test, which is a much worse place
// to read the news.
var _ Store = (*memStore)(nil)

// The original suite covered these value types only incidentally, through
// a SQLite round trip that also tested the schema, the property-graph mirror
// and the batch accounting. None of that came with the extraction, so the term
// constructors and their rendering arrived here with no coverage at all — and
// they are load-bearing: RDFTerm.String is the identity a store keys triples
// by, so a change to how a literal renders silently changes what counts as the
// same triple.
func TestTermConstructorsAndRendering(t *testing.T) {
	cases := []struct {
		name string
		term RDFTerm
		kind string
		want string
	}{
		{"iri strips angle brackets", NewIRI("<https://example.com/alice>"), RDFTermIRI, "<https://example.com/alice>"},
		{"blank node strips prefix", NewBlankNode("_:b0"), RDFTermBlankNode, "_:b0"},
		{"plain literal quotes", NewLiteral("Alice"), RDFTermLiteral, `"Alice"`},
		{"lang literal tags", NewLangLiteral("Alice", "en"), RDFTermLiteral, `"Alice"@en`},
		{"typed literal appends datatype", NewTypedLiteral("25", "http://www.w3.org/2001/XMLSchema#integer"), RDFTermLiteral, `"25"^^<http://www.w3.org/2001/XMLSchema#integer>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.term.Kind != tc.kind {
				t.Fatalf("kind: got %q want %q", tc.term.Kind, tc.kind)
			}
			if got := tc.term.String(); got != tc.want {
				t.Fatalf("String(): got %q want %q", got, tc.want)
			}
		})
	}
}

// A quad renders with its graph and a triple does not, which is the difference
// between N-Triples and N-Quads and also the difference between two triples
// that a store must not conflate.
func TestTripleRendering(t *testing.T) {
	triple := RDFTriple{
		Subject:   NewIRI("https://example.com/alice"),
		Predicate: NewIRI("https://schema.org/name"),
		Object:    NewLiteral("Alice"),
	}
	want := `<https://example.com/alice> <https://schema.org/name> "Alice" .`
	if got := triple.String(); got != want {
		t.Fatalf("triple: got %q want %q", got, want)
	}

	graphTerm := NewIRI("https://example.com/people")
	quad := triple
	quad.Graph = &graphTerm
	wantQuad := `<https://example.com/alice> <https://schema.org/name> "Alice" <https://example.com/people> .`
	if got := quad.String(); got != wantQuad {
		t.Fatalf("quad: got %q want %q", got, wantQuad)
	}
	if quad.String() == triple.String() {
		t.Fatal("a quad and a triple with the same terms must not render identically")
	}
}

// Prefix expansion is the one piece of namespace handling the engine itself
// performs — the SPARQL parser expands PREFIX-declared and store-declared
// names before it ever builds a pattern — so its edge cases belong to this
// package rather than to whatever store is underneath.
func TestExpandIRIWithNamespaces(t *testing.T) {
	namespaces := []Namespace{
		{Prefix: "ex", URI: "https://example.com/"},
		{Prefix: "schema", URI: "https://schema.org/"},
	}
	cases := []struct {
		in   string
		want string
	}{
		{"ex:alice", "https://example.com/alice"},
		{"schema:name", "https://schema.org/name"},
		{"<ex:alice>", "https://example.com/alice"},
		// An absolute IRI is already expanded and must be left alone, or a
		// query naming http://... would be mangled by any prefix called http.
		{"https://other.example/thing", "https://other.example/thing"},
		{"urn:uuid:1234", "urn:uuid:1234"},
		// An unknown prefix stays as it is rather than becoming an error: the
		// store may not be the only thing that knows what it means.
		{"nope:thing", "nope:thing"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := expandIRIWithNamespaces(tc.in, namespaces); got != tc.want {
			t.Errorf("expand(%q): got %q want %q", tc.in, got, tc.want)
		}
	}
}
