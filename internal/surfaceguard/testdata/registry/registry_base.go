// Fixture: a mini api_registry-like source with only the baseline methods.
// Not compiled as part of any package — lives under testdata/ for go build exclusion.
// ExtractRegistrations should parse this and return exactly 4 registrations.
package fixtureregistry

import "context"

func RegisterBase(s *Server) {
	s.Register("wrkf.initialize", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.next", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.task.attach", handlerFunc(func(ctx context.Context) {}))
	s.Register("wrkf.task.inspect", handlerFunc(func(ctx context.Context) {}))
}
