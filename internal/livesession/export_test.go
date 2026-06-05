package livesession

// ExportNewServiceWithCodeGen re-exports newServiceWithCodeGen so the
// external test package can construct a Service with an injected join-code
// generator (to force collisions deterministically) without widening the
// production API.
var ExportNewServiceWithCodeGen = newServiceWithCodeGen
