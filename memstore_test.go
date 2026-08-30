package rdfgo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// memStore is the Store the tests run against.
//
// It exists because the engine's tests used to reach a SQLite file, and every
// one of them spent its first twenty lines creating a database, initialising a
// schema, and arranging for the .db, .db-wal and .db-shm to be removed
// afterwards — none of which was ever the thing under test. A map is a better
// fixture for a query evaluator than a database is, and it is also the proof
// the extraction was worth doing: if the four methods below are enough to run
// the whole SPARQL and SHACL suite, then Store is not an abbreviation of a
// SQL store, it is the actual contract.
//
// It deliberately reproduces the parts of the SQL store's behaviour the engine
// depends on, and no more:
//
//   - Terms are normalised on the way in and on the way through a pattern, so
//     a query written with ex:alice finds a triple written with the expanded
//     IRI. Skipping this would make the fixture pass queries the real store
//     fails and vice versa.
//   - A triple's identity is its content, so writing the same triple twice is
//     one triple. SPARQL defines INSERT DATA of an existing triple as a no-op
//     and the engine does not check first.
//   - A nil pattern field is a wildcard, including Graph: a pattern with no
//     graph term means "any graph", not "the default graph".
type memStore struct {
	mu         sync.Mutex
	byID       map[string]RDFTriple
	order      []string
	namespaces map[string]string
}

func newMemStore() *memStore {
	return &memStore{
		byID:       make(map[string]RDFTriple),
		namespaces: make(map[string]string),
	}
}

// UpsertNamespace registers a prefix binding, mirroring the method the SQL
// store offers outside the Store interface. Namespaces are storage state, not
// engine state, which is why registering one is not on Store: the engine only
// ever reads them.
func (m *memStore) UpsertNamespace(_ context.Context, ns Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.namespaces[ns.Prefix] = ns.URI
	return nil
}

// UpsertTriplesBatch is the batch helper the ported tests used on the SQL
// store. It is a loop over UpsertTriple, because a batch is an optimisation
// and not a different meaning, and it returns the count so a test can assert
// on it the way it used to assert on BatchResult.SuccessCount.
func (m *memStore) UpsertTriplesBatch(ctx context.Context, triples []*RDFTriple) (int, error) {
	for i, triple := range triples {
		if err := m.UpsertTriple(ctx, triple); err != nil {
			return i, err
		}
	}
	return len(triples), nil
}

func (m *memStore) ListNamespaces(_ context.Context) ([]Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	merged := make(map[string]string, len(builtinNamespaces)+len(m.namespaces))
	for prefix, uri := range builtinNamespaces {
		merged[prefix] = uri
	}
	for prefix, uri := range m.namespaces {
		merged[prefix] = uri
	}
	out := make([]Namespace, 0, len(merged))
	for prefix, uri := range merged {
		out = append(out, Namespace{Prefix: prefix, URI: uri})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return out, nil
}

func (m *memStore) UpsertTriple(ctx context.Context, triple *RDFTriple) error {
	if triple == nil {
		return fmt.Errorf("triple is required")
	}
	namespaces, err := m.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	normalized, err := normalizeTestTriple(*triple, namespaces)
	if err != nil {
		return err
	}
	if normalized.ID == "" {
		normalized.ID = normalized.String()
	}
	triple.ID = normalized.ID

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[normalized.ID]; !ok {
		m.order = append(m.order, normalized.ID)
	}
	m.byID[normalized.ID] = normalized
	return nil
}

func (m *memStore) DeleteTriple(ctx context.Context, triple RDFTriple) error {
	namespaces, err := m.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	normalized, err := normalizeTestTriple(triple, namespaces)
	if err != nil {
		return err
	}
	id := normalized.ID
	if id == "" {
		id = normalized.String()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Deleting something that is not there succeeds: DELETE WHERE can name the
	// same triple from two solutions, and an error here would break ordinary
	// updates rather than reveal a problem.
	if _, ok := m.byID[id]; !ok {
		return nil
	}
	delete(m.byID, id)
	for i, existing := range m.order {
		if existing == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return nil
}

func (m *memStore) FindTriples(ctx context.Context, pattern TriplePattern) ([]RDFTriple, error) {
	namespaces, err := m.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}

	var subject, predicate, object, graphTerm *RDFTerm
	if pattern.Subject != nil {
		term, err := normalizeTestTerm(*pattern.Subject, testPositionSubject, namespaces)
		if err != nil {
			return nil, err
		}
		subject = &term
	}
	if pattern.Predicate != nil {
		term, err := normalizeTestTerm(*pattern.Predicate, testPositionPredicate, namespaces)
		if err != nil {
			return nil, err
		}
		predicate = &term
	}
	if pattern.Object != nil {
		term, err := normalizeTestTerm(*pattern.Object, testPositionObject, namespaces)
		if err != nil {
			return nil, err
		}
		object = &term
	}
	if pattern.Graph != nil {
		term, err := normalizeTestTerm(*pattern.Graph, testPositionGraph, namespaces)
		if err != nil {
			return nil, err
		}
		graphTerm = &term
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RDFTriple, 0)
	for _, id := range m.order {
		triple := m.byID[id]
		if subject != nil && (triple.Subject.Kind != subject.Kind || triple.Subject.Value != subject.Value) {
			continue
		}
		// The SQL store matches a predicate on its value alone, not its kind.
		if predicate != nil && triple.Predicate.Value != predicate.Value {
			continue
		}
		if object != nil {
			if triple.Object.Kind != object.Kind || triple.Object.Value != object.Value {
				continue
			}
			// Datatype and language narrow the match only when the pattern
			// gives them, so an untyped pattern object matches a typed literal.
			if object.Kind == RDFTermLiteral {
				if object.Datatype != "" && triple.Object.Datatype != object.Datatype {
					continue
				}
				if object.Language != "" && triple.Object.Language != object.Language {
					continue
				}
			}
		}
		if graphTerm != nil {
			if triple.Graph == nil {
				continue
			}
			if triple.Graph.Kind != graphTerm.Kind || triple.Graph.Value != graphTerm.Value {
				continue
			}
		}
		if pattern.Inferred != nil && triple.Inferred != *pattern.Inferred {
			continue
		}
		out = append(out, triple)
		if pattern.Limit > 0 && len(out) >= pattern.Limit {
			break
		}
	}
	return out, nil
}

// The normalisation below is copied from the original SQL store rather than
// exported from this package, because it is the store's job and not the
// engine's: a store that keeps expanded IRIs has to expand on the way in, and
// one that keeps prefixed forms has to do something else entirely. Writing it
// here is what an implementer of Store would have to write, so the fixture
// pays the same cost a real adopter does.
type testPosition string

const (
	testPositionSubject   testPosition = "subject"
	testPositionPredicate testPosition = "predicate"
	testPositionObject    testPosition = "object"
	testPositionGraph     testPosition = "graph"
)

func normalizeTestTriple(triple RDFTriple, namespaces []Namespace) (RDFTriple, error) {
	subject, err := normalizeTestTerm(triple.Subject, testPositionSubject, namespaces)
	if err != nil {
		return RDFTriple{}, err
	}
	predicate, err := normalizeTestTerm(triple.Predicate, testPositionPredicate, namespaces)
	if err != nil {
		return RDFTriple{}, err
	}
	object, err := normalizeTestTerm(triple.Object, testPositionObject, namespaces)
	if err != nil {
		return RDFTriple{}, err
	}
	var graphTerm *RDFTerm
	if triple.Graph != nil {
		normalizedGraph, err := normalizeTestTerm(*triple.Graph, testPositionGraph, namespaces)
		if err != nil {
			return RDFTriple{}, err
		}
		graphTerm = &normalizedGraph
	}
	return RDFTriple{
		ID:         triple.ID,
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		Graph:      graphTerm,
		Inferred:   triple.Inferred,
		Rule:       strings.TrimSpace(triple.Rule),
		SupportIDs: append([]string(nil), triple.SupportIDs...),
	}, nil
}

func normalizeTestTerm(term RDFTerm, position testPosition, namespaces []Namespace) (RDFTerm, error) {
	term.Kind = strings.TrimSpace(term.Kind)
	term.Value = strings.TrimSpace(term.Value)
	term.Datatype = strings.TrimSpace(strings.Trim(term.Datatype, "<>"))
	term.Language = strings.ToLower(strings.TrimSpace(term.Language))

	switch position {
	case testPositionSubject:
		if term.Kind != RDFTermIRI && term.Kind != RDFTermBlankNode {
			return RDFTerm{}, fmt.Errorf("rdf subject must be iri or blank node")
		}
	case testPositionPredicate:
		if term.Kind == "" {
			term.Kind = RDFTermIRI
		}
		if term.Kind != RDFTermIRI {
			return RDFTerm{}, fmt.Errorf("rdf predicate must be iri")
		}
	case testPositionGraph:
		if term.Kind != RDFTermIRI && term.Kind != RDFTermBlankNode {
			return RDFTerm{}, fmt.Errorf("rdf graph must be iri or blank node")
		}
	case testPositionObject:
		if term.Kind != RDFTermIRI && term.Kind != RDFTermBlankNode && term.Kind != RDFTermLiteral {
			return RDFTerm{}, fmt.Errorf("rdf object must be iri, blank node, or literal")
		}
	}

	if term.Kind == "" {
		return RDFTerm{}, fmt.Errorf("rdf term kind is required")
	}
	if term.Value == "" {
		return RDFTerm{}, fmt.Errorf("rdf term value is required")
	}
	if term.Kind == RDFTermIRI {
		term.Value = expandIRIWithNamespaces(term.Value, namespaces)
	}
	if term.Kind == RDFTermBlankNode {
		term.Value = strings.TrimPrefix(term.Value, "_:")
	}
	if term.Kind == RDFTermLiteral && term.Datatype != "" {
		term.Datatype = expandIRIWithNamespaces(term.Datatype, namespaces)
	}
	return term, nil
}
