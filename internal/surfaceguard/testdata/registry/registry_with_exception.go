// Fixture: baseline methods + new method with a valid ARCH-EXCEPTION.
// The ARCH-EXCEPTION is well-formed (valid ticket T-04369, non-empty reason).
// Check must NOT flag wrkf.test.uncovered; ExceptionsByRule must increment.
package fixtureregistry

import "context"

func RegisterWithException(s *Server) {
	s.Register("wrkf.initialize", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.next", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.task.attach", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.task.inspect", handlerFunc(func(ctx context.Context) {}))
	// ARCH-EXCEPTION(T-04369): provisional surface; contract test pending smoke-wrkf-rpc.sh update
	s.Register("wrkf.test.uncovered", handlerFunc(func(ctx context.Context) {}))
}
