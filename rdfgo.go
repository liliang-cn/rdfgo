// Package rdfgo is a SPARQL 1.1 query/update engine and a SHACL validator that
// run over any triple store you can express in four methods.
//
// It was extracted from a graph database, where the same engine was a set
// of methods on a SQLite-backed GraphStore. Nothing in the query evaluator
// wanted SQLite; it wanted triples. The extraction is what proves it: across
// 3800 lines of SPARQL and 500 of SHACL, the number of distinct things the
// engine asks its storage for is four —
//
//	sparql.go   FindTriples x4   UpsertTriple x2   DeleteTriple x3   ListNamespaces x1
//	shacl.go    FindTriples x3
//
// — and everything else those files call, they define themselves. That count is
// the whole reason this package exists as a package: an engine coupled to
// storage at four call sites is an engine that can be lifted off it, and one
// coupled at forty is not.
//
// # What it does
//
// SELECT, ASK, CONSTRUCT and DESCRIBE, with OPTIONAL, UNION, MINUS, VALUES,
// BIND, FILTER, sub-queries, property paths, aggregates and solution
// modifiers; and the updates INSERT DATA, DELETE DATA, DELETE WHERE, and the
// INSERT/DELETE ... WHERE modify form. Plus SHACL node and property shapes:
// datatype, cardinality, value range, pattern, sh:in, node kind, and class
// constraints, reported as a conformance report rather than an error.
//
// # What it does not do
//
// There is no OWL reasoning here, and no RDFS entailment either. This package
// answers queries against the triples the store actually holds; it does not
// materialise or infer new ones. The RDFS forward-chainer it grew up beside
// stayed where it was, because it is a writer — it derives triples and puts
// them back —
// and a writer is a policy about your graph rather than a way of reading it.
// If you want entailment, infer into the store and query the result; the
// engine will see the inferred triples like any others.
//
// Nor does it parse or serialise RDF documents. That is deliberate and it is
// why this package's go.mod has no requires: a caller who wants to run a
// SPARQL query should not have to compile a Turtle parser to do it. Parsing
// lives in the separate rdfio package, which depends on one, and which you can
// leave out of your build entirely.
//
// # The store is yours
//
// Implement Store over whatever you keep triples in — a SQL table, a map, a
// remote service — and hand it to New. The interface is the four methods
// above and no more. It is stated narrowly on purpose: every method added to
// it is a thing every future store has to implement, and the only defensible
// reason to add one is that the engine cannot answer a query without it.
package rdfgo

import "context"

// Store is the triple store the engine runs over.
//
// These four methods are not a design; they are a measurement. They are
// exactly what sparql.go and shacl.go call on their storage and nothing else,
// which is what makes the interface implementable by a store that is a map in
// a test and by one that is a database in production without either of them
// pretending.
//
// A pattern with nil fields is a wildcard in those positions, so FindTriples
// with an empty TriplePattern is "everything" — an implementation that treated
// nil as "match the zero term" would silently return nothing and every query
// would come back empty rather than failing.
type Store interface {
	// FindTriples returns every triple matching the pattern. Nil pattern
	// fields match anything. It is the engine's only read path: a basic graph
	// pattern, a property path step, a DESCRIBE expansion and a SHACL target
	// search are all this call with a different pattern.
	FindTriples(ctx context.Context, pattern TriplePattern) ([]RDFTriple, error)
	// UpsertTriple writes one triple, which must be idempotent: SPARQL's
	// INSERT DATA on a triple already present is defined as a no-op, not a
	// duplicate, and the engine relies on the store for that rather than
	// reading before every write.
	//
	// It takes a pointer because a store may fill in RDFTriple.ID and the
	// caller of an update may want to see it.
	UpsertTriple(ctx context.Context, triple *RDFTriple) error
	// DeleteTriple removes one triple by its content. Deleting a triple that
	// is not there must succeed: DELETE WHERE computes its deletions from a
	// pattern match and the same triple can legitimately be named twice by two
	// solutions, so a store that errored on a miss would fail ordinary
	// updates.
	DeleteTriple(ctx context.Context, triple RDFTriple) error
	// ListNamespaces returns the prefix bindings the store knows. The parser
	// asks once per query, so that a query may use a prefix the store has
	// registered without repeating it in a PREFIX clause. A store with no
	// notion of namespaces returns nil, and then only in-query PREFIX
	// declarations resolve.
	ListNamespaces(ctx context.Context) ([]Namespace, error)
}

// Engine executes SPARQL queries and SHACL validation against a Store.
//
// It holds no state of its own beyond the store, so one Engine is safe to
// share for concurrent queries exactly as far as the underlying Store is.
// Everything a query needs while it runs — bindings, prefixes, execution
// options — lives on the stack of the call that is running it.
type Engine struct {
	store Store
}

// New returns an Engine reading and writing through s.
//
// The store is not validated here and cannot be: whether it can answer is a
// thing you find out by asking it, and the first query will say so. Passing
// nil is a programming error and will panic on the first call rather than
// return a query result that quietly means "your graph is empty".
func New(s Store) *Engine {
	return &Engine{store: s}
}

// Store returns the store this engine was built over.
//
// It is here because a caller who has an Engine usually also wants to write
// triples into the thing it queries, and forcing them to carry both values
// around is how the two drift apart and a query ends up running against a
// different graph than the one that was just written.
func (e *Engine) Store() Store {
	return e.store
}
