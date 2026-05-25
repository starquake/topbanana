package auth_test

import (
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
)

func TestGeneratePetname_Shape(t *testing.T) {
	t.Parallel()

	for range 100 {
		name := GeneratePetname()
		if name == "" {
			t.Fatal("GeneratePetname() = empty string, want non-empty")
		}
		parts := strings.Split(name, "-")
		if got, want := len(parts), 3; got != want {
			t.Errorf("GeneratePetname() = %q, want %d hyphen-separated segments", name, want)
		}
		for i, p := range parts {
			if p == "" {
				t.Errorf("GeneratePetname() = %q, segment %d is empty", name, i)
			}
		}
	}
}

// TestGeneratePetname_Distinct sanity-checks that the pool is wide enough to
// produce a healthy spread of distinct names over 1000 calls. The exact pool
// size isn't asserted (that would couple the test to the word-list contents),
// only that the generator isn't degenerate.
func TestGeneratePetname_Distinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		seen[GeneratePetname()] = struct{}{}
	}
	// With ~15M combinations, the birthday-problem expectation on 1000 draws
	// is ~999 distinct outcomes. Allow generous slack to keep the test stable
	// while still failing loudly if the generator collapses to a tiny pool.
	if got, want := len(seen), 900; got < want {
		t.Errorf("len(distinct petnames over 1000 calls) = %d, want >= %d", got, want)
	}
}
