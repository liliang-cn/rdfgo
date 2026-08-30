# Conformance

Measured 2026-08-30 against [w3c/rdf-tests](https://github.com/w3c/rdf-tests)
`sparql/sparql11` and [w3c/data-shapes](https://github.com/w3c/data-shapes)
`core`.

```
SPARQL 1.1   214 / 564 attempted   37.9%
SHACL Core     6 /  98 attempted    6.1%   (2 of the 6 pass vacuously — really 4)
             220 / 662              33.2%
unrun         66   HTTP protocol, SERVICE, CSV/TSV — excluded from both sides
```

Three measurement choices tilt this **upward**: syntax tests are decided by
whether execution errors (there is no parse-only entry point), `SELECT`
comparison is bag-based and blank-node-blind so `ORDER BY` is unchecked, and
SHACL comparison ignores `sh:sourceConstraintComponent`. **At or below 33.2%.**

## Accepts the query, returns a wrong answer

This list matters more than the percentage. An error is recoverable; a wrong
answer that looks right is not.

| # | Behaviour | Reproducer |
|---|---|---|
| 1 | Every arithmetic result is `xsd:decimal` whatever the operands were | `bind/bind01` |
| 2 | `xsd:decimal` arithmetic runs in float64 — `1.1+2.2` → `3.3000000000000003` | `aggregates/agg-sum-01` |
| 3 | `LCASE` discards the language tag | `functions/lcase01` |
| 4 | `BIND` results are invisible to later triple patterns | `bind/bind03` |
| 5 | `BIND` scope leaks into a nested group | `bind/bind10` |
| 6 | A sub-query ignores the enclosing `GRAPH` | `subquery/subquery01` |
| 7 | `AVG` over an empty group returns unbound, not 0 | `aggregates/agg-avg-03` |
| 8 | A prefixed-name sequence path (`ex:a/ex:b`) returns **0 rows with no error** | `property-path/pp01` |
| 9 | 21 syntactically invalid queries are accepted | `aggregates/agg08` |
| 10 | `DELETE … INSERT … WHERE` does not snapshot its solutions | `delete-insert/delete-insert-halloween-problem` |

## Not implemented

**SHACL.** Node-level constraints are never evaluated: `ValidateSHACL` walks
only `shape.Properties`, so a `sh:NodeShape` carrying `sh:class`, `sh:datatype`,
`sh:nodeKind`, `sh:pattern` or `sh:in` directly reports conformance. That is 80
of the 92 SHACL failures. Non-IRI `sh:path` (sequence, inverse, alternative) is
also absent, and the report carries no `sh:sourceConstraintComponent`.

**SPARQL.** `LOAD`, `CLEAR`, `DROP`, `CREATE`, `ADD`, `COPY`, `MOVE`. About 35
built-ins including `CONCAT`, `SUBSTR`, `REPLACE`, `UCASE`, `CONTAINS`,
`STRSTARTS`, `COALESCE`, `IN`, the hash and date functions. Sequence, `?`,
negated and grouped property paths. `SERVICE`, `FROM`/`FROM NAMED`, a base IRI
on `ExecuteSPARQL`, `VALUES` after `WHERE`, `CONSTRUCT WHERE`, `;`-separated
update sequences, `[ ]` and `( )` in query bodies, `_:` labels.

**Entailment.** 57 tests, as the package doc says. 13 pass because their answer
is derivable by simple entailment.

## A bug in the Turtle parser, upstream

`rdfio.ParseTurtle` cannot read most real Turtle. Of the suites' 395 `.ttl`
files it rejects 48 and misreads 140 more.

The cause is in `github.com/0x51-dev/rdf@v0.1.0/turtle`: `parseDocument` ends
with `sort.Sort(document)`, and `Document.Less` returns `false` both ways when
comparing a `Prefix` directive to a `Triple`. `sort.Sort` is unstable above 12
elements, so **any document with more than 12 statements can have its `@prefix`
lines shuffled after the triples that use them**. Twelve triples parse;
thirteen do not. `<>` also resolves to the *directory* of the base IRI rather
than to the document.

`ParseNTriples` and `ParseNQuads` are correct and were used for every byte of
data the engine saw during this measurement.

## Re-running it

The harness is not in this repository — it needs a 200MB checkout and a Python
virtualenv, and a zero-dependency library has no business carrying either. The
steps are in the report that produced this file: clone both suites, convert
every RDF file to N-Triples with rdflib, then run each manifest entry through
`ExecuteSPARQL` / `ValidateSHACL` and compare.
