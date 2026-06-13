// Package unauthorized is a fixture representing an arbitrary package
// that should NOT be allowed to import wrkfrpc.
// This fixture covers test 4 (fail case): unauthorized→wrkfrpc.
package unauthorized

import _ "fixture/wrkfrpc"
