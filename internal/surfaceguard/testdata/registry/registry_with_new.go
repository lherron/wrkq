// Fixture: baseline methods + a new unevidenced method "wrkf.test.uncovered".
// No ARCH-EXCEPTION and no test evidence exist for the new method.
// ExtractRegistrations should return 5 registrations; Check must flag wrkf.test.uncovered.
package fixtureregistry

import "context"

func RegisterWithNew(s *Server) {
	s.Register("wrkf.initialize", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.next", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.task.attach", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.task.inspect", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.test.uncovered", handlerFunc(func(ctx context.Context) {}))
}
