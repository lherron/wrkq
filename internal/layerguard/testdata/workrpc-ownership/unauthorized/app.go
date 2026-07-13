// Package unauthorized is a fixture representing an arbitrary package
// that should not be allowed to import workrpc.
package unauthorized

import _ "fixture/workrpc"
