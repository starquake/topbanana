// Package store provides the application's data stores.
package store

import "github.com/starquake/topbanana/internal/quiz"

// Stores is a collection of stores for the application.
type Stores struct {
	Quizzes quiz.Store
}
