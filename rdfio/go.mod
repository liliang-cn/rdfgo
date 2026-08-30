// rdfio is a separate module, not just a separate package, and the reason is
// the one line this file has that ../go.mod does not: the parser dependency.
//
// A package in the same module would have kept the root go.mod's require list
// clean of nothing — the requirement would sit in it, in every consumer's
// module graph and go.sum, for a package most of them never build. Splitting
// the module is what makes "rdfgo has no dependencies" a fact you can check by
// reading one file rather than a claim about which packages happen to get
// linked.
module github.com/liliang-cn/rdfgo/rdfio

go 1.25

require (
	github.com/0x51-dev/rdf v0.1.0
	github.com/liliang-cn/rdfgo v0.0.0
)

require (
	github.com/0x51-dev/rids v0.1.0 // indirect
	github.com/0x51-dev/upeg v0.1.0 // indirect
)

// Until rdfgo is tagged, the parent is next door. A replace is ignored by
// anyone who depends on this module, so it costs them nothing.
replace github.com/liliang-cn/rdfgo => ../
