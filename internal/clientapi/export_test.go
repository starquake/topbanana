package clientapi

// ExportShuffleBySeed exposes the unexported shuffleBySeed helper so
// the external clientapi_test package can pin its determinism and
// permutation contracts without becoming a whitebox test.
var ExportShuffleBySeed = shuffleBySeed
