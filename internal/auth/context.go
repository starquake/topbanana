package auth

import "context"

// playerCtxKey is the unexported context-key type used for stashing the
// authenticated player on a request context. It's a struct, not a string,
// so external packages can't accidentally collide with it.
type playerCtxKey struct{}

// WithPlayer returns a copy of ctx that carries p. Use it in middleware to
// expose the authenticated player to downstream handlers.
func WithPlayer(ctx context.Context, p *Player) context.Context {
	return context.WithValue(ctx, playerCtxKey{}, p)
}

// PlayerFromContext returns the player stored on ctx by WithPlayer, plus a
// boolean reporting whether one was present. Returns (nil, false) for
// contexts that have not been annotated.
func PlayerFromContext(ctx context.Context) (*Player, bool) {
	p, ok := ctx.Value(playerCtxKey{}).(*Player)
	if !ok || p == nil {
		return nil, false
	}

	return p, true
}
