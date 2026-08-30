package rdfio_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liliang-cn/rdfgo"
	"github.com/liliang-cn/rdfgo/rdfio"
)

// TestParseTurtleAndTriGRoundTrip is the original TestRDFImportTurtleAndExportTriG
// with the database taken out. There, the counts it asserts came back from
// UpsertTriplesBatch and so were counting rows written; here they come from the
// parser and count triples read, which is what the test was really about — the
// blank node in the Turtle has to become two triples, and the TriG graph block
// has to become one triple that knows its graph.
func TestParseTurtleAndTriGRoundTrip(t *testing.T) {
	namespaces := []rdfgo.Namespace{
		{Prefix: "ex", URI: "https://example.com/"},
		{Prefix: "schema", URI: "https://schema.org/"},
	}

	turtlePayload := `
@prefix ex: <https://example.com/> .
@prefix schema: <https://schema.org/> .

ex:alice schema:knows [
	schema:name "Bob"
] .
`
	turtleTriples, err := rdfio.ParseTurtle(strings.NewReader(turtlePayload), "file:///")
	if err != nil {
		t.Fatalf("parse turtle: %v", err)
	}
	if len(turtleTriples) != 2 {
		t.Fatalf("expected turtle to yield 2 triples, got %d", len(turtleTriples))
	}

	trigPayload := `
@prefix ex: <https://example.com/> .
@prefix schema: <https://schema.org/> .

ex:people {
	ex:alice schema:memberOf ex:team .
}
`
	trigTriples, err := rdfio.ParseTriG(strings.NewReader(trigPayload))
	if err != nil {
		t.Fatalf("parse trig: %v", err)
	}
	if len(trigTriples) != 1 {
		t.Fatalf("expected trig to yield 1 quad, got %d", len(trigTriples))
	}
	if trigTriples[0].Graph == nil {
		t.Fatalf("expected trig quad to carry a graph term, got %+v", trigTriples[0])
	}

	var out bytes.Buffer
	if err := rdfio.WriteTriG(&out, append(turtleTriples, trigTriples...), namespaces); err != nil {
		t.Fatalf("write trig: %v", err)
	}
	exported := out.String()
	if !strings.Contains(exported, "ex:people {") {
		t.Fatalf("expected TriG output to contain graph block, got %q", exported)
	}
	if !strings.Contains(exported, "schema:memberOf") {
		t.Fatalf("expected TriG output to contain predicate, got %q", exported)
	}
	if !strings.Contains(exported, "@prefix ex: <https://example.com/> .") {
		t.Fatalf("expected TriG output to declare prefixes, got %q", exported)
	}
}

// TestParsedTriplesAreQueryable is the reason both packages exist in one
// repository: what the parser produces has to be what the engine reads. It
// would be possible for the two to diverge silently — a parser that emitted a
// literal where the engine expects an IRI would still round-trip through
// WriteTriG and still fail every query.
func TestParsedTriplesAreQueryable(t *testing.T) {
	triples, err := rdfio.ParseTurtle(strings.NewReader(`
@prefix ex: <https://example.com/> .
@prefix schema: <https://schema.org/> .

ex:alice schema:name "Alice" .
ex:bob schema:name "Bob" .
`), "file:///")
	if err != nil {
		t.Fatalf("parse turtle: %v", err)
	}

	store := newMemStore()
	ctx := t.Context()
	for i := range triples {
		if err := store.UpsertTriple(ctx, &triples[i]); err != nil {
			t.Fatalf("upsert parsed triple: %v", err)
		}
	}

	result, err := rdfgo.New(store).ExecuteSPARQL(ctx, `
		SELECT ?name WHERE { ?s <https://schema.org/name> ?name } ORDER BY ?name
	`)
	if err != nil {
		t.Fatalf("execute sparql: %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("expected 2 bindings, got %d: %+v", result.Count, result.Bindings)
	}
	if got := result.Bindings[0]["name"].Value; got != "Alice" {
		t.Fatalf("expected first name Alice, got %q", got)
	}
}

// memStore is the smallest thing that satisfies rdfgo.Store: enough to prove
// parsed triples are queryable, and deliberately not a reimplementation of the
// fixture in the rdfgo package. Parsed IRIs are already absolute, so this one
// does no prefix expansion at all — which is itself worth showing, because it
// means Store does not oblige an implementer to understand namespaces.
type memStore struct {
	triples []rdfgo.RDFTriple
}

func newMemStore() *memStore { return &memStore{} }

func (m *memStore) FindTriples(_ context.Context, pattern rdfgo.TriplePattern) ([]rdfgo.RDFTriple, error) {
	out := make([]rdfgo.RDFTriple, 0, len(m.triples))
	for _, triple := range m.triples {
		if pattern.Subject != nil && (triple.Subject.Kind != pattern.Subject.Kind || triple.Subject.Value != pattern.Subject.Value) {
			continue
		}
		if pattern.Predicate != nil && triple.Predicate.Value != pattern.Predicate.Value {
			continue
		}
		if pattern.Object != nil && (triple.Object.Kind != pattern.Object.Kind || triple.Object.Value != pattern.Object.Value) {
			continue
		}
		if pattern.Graph != nil && (triple.Graph == nil || triple.Graph.Value != pattern.Graph.Value) {
			continue
		}
		out = append(out, triple)
		if pattern.Limit > 0 && len(out) >= pattern.Limit {
			break
		}
	}
	return out, nil
}

func (m *memStore) UpsertTriple(_ context.Context, triple *rdfgo.RDFTriple) error {
	if triple == nil {
		return errors.New("triple is required")
	}
	triple.ID = triple.String()
	for i, existing := range m.triples {
		if existing.String() == triple.ID {
			m.triples[i] = *triple
			return nil
		}
	}
	m.triples = append(m.triples, *triple)
	return nil
}

func (m *memStore) DeleteTriple(_ context.Context, triple rdfgo.RDFTriple) error {
	key := triple.String()
	for i, existing := range m.triples {
		if existing.String() == key {
			m.triples = append(m.triples[:i], m.triples[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *memStore) ListNamespaces(_ context.Context) ([]rdfgo.Namespace, error) { return nil, nil }
