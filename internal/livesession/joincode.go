package livesession

import (
	"crypto/rand"
	"math/big"
)

// joinCodeAlphabet is the ambiguity-free alphabet for room codes: no 0/O,
// no 1/I/L, so a code read off a TV or spoken aloud cannot be mistyped
// between visually or phonetically confusable characters. 31 symbols
// (A-Z minus I,L,O plus 2-9) keeps every character unambiguous.
const joinCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// joinCodeLength is the number of characters in a room code. Six chars
// over the 31-symbol alphabet give ~887 million combinations - vast
// relative to a quiz-night room, so collisions are rare and the
// collision-checked generator regenerates the occasional clash.
const joinCodeLength = 6

// GenerateJoinCode returns a random room code over the ambiguity-free
// alphabet. Not guaranteed unique - [Service.allocateJoinCode] probes it
// against the store and regenerates on collision. Uses crypto/rand so the
// code is not predictable from a prior one (a guessable code would let an
// outsider probe for live rooms).
func GenerateJoinCode() string {
	b := make([]byte, joinCodeLength)
	for i := range b {
		b[i] = joinCodeAlphabet[pickIndex(len(joinCodeAlphabet))]
	}

	return string(b)
}

// pickIndex returns a uniformly-random index into an alphabet of size n.
// Falls back to 0 on the (effectively impossible) crypto/rand error rather
// than taking down the create path; the collision check still guards
// uniqueness even in that degenerate case.
func pickIndex(n int) int64 {
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}

	return idx.Int64()
}
