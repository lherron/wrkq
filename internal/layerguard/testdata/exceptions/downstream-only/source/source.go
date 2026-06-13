// Package source is a fixture governed by the layer-boundary rule.
// It imports middle WITHOUT an ARCH-EXCEPTION on this import line.
// The ARCH-EXCEPTION is downstream (on middle's import of cobra), which does NOT authorize source.
// Expected: violation reported for source→middle→cobra chain.
package source

import _ "fixture/middle"
