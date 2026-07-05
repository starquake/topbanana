package auth_test

import (
	"errors"
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

func TestCreateWithPetnameFallback_FirstChoiceSucceeds(t *testing.T) {
	t.Parallel()

	var names []string
	player, err := CreateWithPetnameFallback("Chosen-Name-Ace", func(name string) (*Player, error) {
		names = append(names, name)

		return &Player{DisplayName: name}, nil
	})
	if err != nil {
		t.Fatalf("CreateWithPetnameFallback err = %v, want nil", err)
	}
	if got, want := player.DisplayName, "Chosen-Name-Ace"; got != want {
		t.Errorf("player.DisplayName = %q, want %q", got, want)
	}
	if got, want := len(names), 1; got != want {
		t.Errorf("create call count = %d, want %d", got, want)
	}
}

func TestCreateWithPetnameFallback_RetriesOnCollision(t *testing.T) {
	t.Parallel()

	calls := 0
	player, err := CreateWithPetnameFallback("Taken-Name-Owl", func(name string) (*Player, error) {
		calls++
		if calls <= 2 {
			return nil, ErrDisplayNameTaken
		}

		return &Player{DisplayName: name}, nil
	})
	if err != nil {
		t.Fatalf("CreateWithPetnameFallback err = %v, want nil", err)
	}
	if player == nil {
		t.Fatal("CreateWithPetnameFallback returned nil player after retries")
	}
	if got, want := calls, 3; got != want {
		t.Errorf("create call count = %d, want %d", got, want)
	}
}

// TestCreateWithPetnameFallback_Exhausts pins the bounded retry: the first
// choice plus five re-rolls is six create attempts, after which the collision
// sentinel is returned unwrapped.
func TestCreateWithPetnameFallback_Exhausts(t *testing.T) {
	t.Parallel()

	calls := 0
	_, err := CreateWithPetnameFallback("Taken-Name-Owl", func(string) (*Player, error) {
		calls++

		return nil, ErrDisplayNameTaken
	})
	if got, want := err, ErrDisplayNameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
	if got, want := calls, 6; got != want {
		t.Errorf("create call count = %d, want %d", got, want)
	}
}

func TestCreateWithPetnameFallback_NonCollisionErrorStops(t *testing.T) {
	t.Parallel()

	calls := 0
	_, err := CreateWithPetnameFallback("Chosen-Name-Ace", func(string) (*Player, error) {
		calls++

		return nil, errors.ErrUnsupported
	})
	if got, want := err, errors.ErrUnsupported; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
	if got, want := calls, 1; got != want {
		t.Errorf("create call count = %d, want %d", got, want)
	}
}
