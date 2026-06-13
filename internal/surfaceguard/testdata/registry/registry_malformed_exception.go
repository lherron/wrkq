// Fixture: new method with a MALFORMED ARCH-EXCEPTION (empty reason after colon).
// The annotation token is present but the reason is empty — not a valid exception.
// Check must flag wrkf.test.uncovered as a violation (malformed exception is not exempt).
package fixtureregistry

import "context"

func RegisterMalformedException(s *Server) {
	s.Register("wrkf.initialize", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.next", handlerFunc(func(ctx context.Context) {}))
	// ARCH-EXCEPTION(T-04369):
	s.Register("wrkf.test.uncovered", handlerFunc(func(ctx context.Context) {}))
}
