package clientapi

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
)

// shuffleOptionsSeed derives a deterministic uint64 seed from a scope ID
// and question ID. The shuffle of the option buttons (#297) is stable
// per (scope, question) so a player who reloads mid-question sees the
// same order they did before - preventing both confusion and a
// deliberate "re-roll the layout" by refreshing. Different scopes on the
// same question see different orders because the scope ID dominates the
// hash, so position-memorisation across players doesn't help either.
// FNV-64a is fast, deterministic, and well-distributed enough for a small
// shuffle; no cryptographic strength is needed because the order is
// observable anyway once the question is rendered.
//
// The scope is the per-play identity that should pin the order: the solo
// game id (#297) or the live session id (#1074), so every player in one
// live session sees the same order while two sessions of the same quiz
// differ.
func shuffleOptionsSeed(scopeID string, questionID int64) uint64 {
	h := fnv.New64a()
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(scopeID))
	_, _ = h.Write([]byte{'/'})
	// binary.Write into a hash.Hash never errors either; fixed byte
	// order keeps the seed identical across hosts, and the value is
	// treated as opaque bits for seeding so sign is irrelevant.
	_ = binary.Write(h, binary.LittleEndian, questionID)

	return h.Sum64()
}

// shuffleBySeed shuffles n items in place using a PCG RNG seeded by
// [shuffleOptionsSeed] over (scopeID, questionID). Two seed words derived
// from one hash give the PCG enough entropy for the small permutation
// space (4!=24 for the usual four options) without pulling in a SHA
// family hash for what is essentially a UI concern. swap mirrors the
// signature [rand.Rand.Shuffle] expects.
func shuffleBySeed(scopeID string, questionID int64, n int, swap func(i, j int)) {
	seed := shuffleOptionsSeed(scopeID, questionID)
	// G404: deterministic-by-design - we need the same (scopeID,
	// questionID) to always yield the same permutation across reloads
	// and process restarts. crypto/rand cannot do that because it
	// doesn't accept a seed. No secret protection is at stake; the
	// player sees the resulting order anyway.
	rng := rand.New(rand.NewPCG(seed, ^seed)) //nolint:gosec // deterministic shuffle, not a security boundary
	rng.Shuffle(n, swap)
}
