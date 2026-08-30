# rdfgo

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/rdfgo.svg)](https://pkg.go.dev/github.com/liliang-cn/rdfgo)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/rdfgo)](https://goreportcard.com/report/github.com/liliang-cn/rdfgo)
[![MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A SPARQL 1.1 engine and a SHACL validator in Go, over a triple store you
supply. **No dependencies** — `go list -m all` is one line.

```bash
go get github.com/liliang-cn/rdfgo
```

```go
engine := rdfgo.New(myStore)

rows, err := engine.ExecuteSPARQL(ctx,
    `SELECT ?s WHERE { ?s a <http://example.org/Person> }`)

report, err := engine.ValidateSHACL(ctx, shapesGraph)
```

## Your store is four methods

```go
type Store interface {
    FindTriples(ctx context.Context, pattern TriplePattern) ([]RDFTriple, error)
    UpsertTriple(ctx context.Context, triple *RDFTriple) error
    DeleteTriple(ctx context.Context, triple RDFTriple) error
    ListNamespaces(ctx context.Context) ([]Namespace, error)
}
```

That is the whole coupling: 13 call sites, against 49 types and 173 functions
the engine owns. The interface was not designed — it was measured off working
code: it ran against a SQL-backed store for a year before it was lifted out. The test fixture is an in-memory `Store`, which is
also the proof that four methods are enough.

## Parsing is a separate module

```bash
go get github.com/liliang-cn/rdfgo/rdfio   # Turtle and TriG
```

Split so the parser's dependency stays out of your `go.sum` if you only want to
query.

## What it does not do

**No OWL reasoning** — there is no reasoner here, and none worth the name in
Go. Materialise entailments before you query. **No RDFS forward chaining** —
that rule engine is a writer and stayed behind.

Two behaviours worth knowing, both measured in the original and neither
introduced here: `parseSPARQL` calls `ListNamespaces` once per query, so a SQL
store pays a round trip per query; and `sh:class` is a direct `rdf:type` lookup
with no subclass closure.

## Tests

17 in the core and 2 in `rdfio`, ported with their assertions unchanged, plus a
fuzz target: 1.3 million executions, no crashes.

MIT.
