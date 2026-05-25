package clientapi

// ExportShuffleByGame exposes the unexported shuffleByGame helper so
// the external clientapi_test package can pin its determinism and
// permutation contracts without becoming a whitebox test.
var ExportShuffleByGame = shuffleByGame
