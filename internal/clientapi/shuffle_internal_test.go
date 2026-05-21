package clientapi

import (
	"fmt"
	"slices"
	"testing"
)

// TestShuffleByGame_Deterministic pins the contract the player client
// relies on (#297): a reload of the same question in the same game
// must produce the same option layout. The test calls the helper
// twice with the same inputs and asserts byte-equal output orders.
func TestShuffleByGame_Deterministic(t *testing.T) {
	t.Parallel()

	first := []int{1, 2, 3, 4}
	shuffleByGame("game-abc", 42, len(first), func(i, j int) {
		first[i], first[j] = first[j], first[i]
	})

	second := []int{1, 2, 3, 4}
	shuffleByGame("game-abc", 42, len(second), func(i, j int) {
		second[i], second[j] = second[j], second[i]
	})

	if !slices.Equal(first, second) {
		t.Errorf("shuffleByGame(\"game-abc\", 42) produced %v then %v — want stable order", first, second)
	}
}

// TestShuffleByGame_PreservesElements guards against the swap function
// dropping or duplicating items. Returns a permutation, not a sample.
func TestShuffleByGame_PreservesElements(t *testing.T) {
	t.Parallel()

	got := []int{1, 2, 3, 4, 5, 6, 7, 8}
	shuffleByGame("any-game", 1, len(got), func(i, j int) {
		got[i], got[j] = got[j], got[i]
	})

	slices.Sort(got)
	want := []int{1, 2, 3, 4, 5, 6, 7, 8}
	if !slices.Equal(got, want) {
		t.Errorf("sorted shuffleByGame output = %v, want %v", got, want)
	}
}

// TestShuffleByGame_DifferentGamesDiffer is the headline anti-cheat
// property the ticket asks for: a screenshot from one player's game
// doesn't tell another player where the right answer sits. Calls the
// helper across N distinct gameIDs on the same question and asserts
// at least two of them produce different orders. With 4 options
// (24 permutations) and 8 trials the false-fail probability is below
// 1e-9.
func TestShuffleByGame_DifferentGamesDiffer(t *testing.T) {
	t.Parallel()

	const trials = 8
	seen := make(map[string]struct{}, trials)
	for i := range trials {
		opts := []int{1, 2, 3, 4}
		gameID := "game-" + string(rune('a'+i))
		shuffleByGame(gameID, 1, len(opts), func(a, b int) {
			opts[a], opts[b] = opts[b], opts[a]
		})
		seen[fmt.Sprintf("%v", opts)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Errorf("shuffleByGame across %d gameIDs produced only %d distinct orders, want >= 2", trials, len(seen))
	}
}
