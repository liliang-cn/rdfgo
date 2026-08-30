# rdfgo

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/rdfgo.svg)](https://pkg.go.dev/github.com/liliang-cn/rdfgo)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/rdfgo)](https://goreportcard.com/report/github.com/liliang-cn/rdfgo)
[![MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A SPARQL engine and a SHACL validator in Go, over a triple store you supply.
**No dependencies** — `go list -m all` is one line.

> **Conformance, measured.** Against the W3C suites, on 2026-08-30:
> **SPARQL 1.1 — 214 of 564 attempted (37.9%). SHACL Core — 6 of 98 (6.1%),
> and 2 of those 6 pass vacuously, so 4.** 66 further tests were not run (HTTP
> protocol, `SERVICE`, CSV/TSV serialisation) and are excluded from both the
> numerator and the denominator rather than counted as passes.
>
> Three things about how it was measured tilt the number **upward**: syntax
> tests are decided by whether execution errors, `SELECT` comparison is
> bag-based and blank-node-blind so `ORDER BY` is unchecked, and SHACL results
> are compared on conforms plus focus/path/value only. The true figure is at or
> below 33.2%.
>
> Use it where a 37% SPARQL engine is enough — and read
> [CONFORMANCE.md](CONFORMANCE.md) first, which lists what is missing and, more
> importantly, the ten places where it accepts a query and returns a **wrong
> answer**. That list matters more than the percentage.

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

## What it implements

SELECT, ASK, CONSTRUCT, DESCRIBE. OPTIONAL, UNION, MINUS, VALUES, BIND, FILTER,
EXISTS, sub-queries, property paths, aggregates, solution modifiers. INSERT
DATA, DELETE DATA, DELETE WHERE, and INSERT/DELETE ... WHERE. SHACL node and
property shapes: datatype, cardinality, value range, pattern, `sh:in`, node
kind and class constraints, returned as a conformance report rather than an
error.

## Tests

17 in the core and 2 in `rdfio`, plus a fuzz target: 1.3 million executions, no
crashes. See the conformance note at the top — these are the author's tests,
not the W3C suites.

MIT.
