package somepkg

import "fmt"

// VendorBareNolint has a bare //nolint — but lives under vendor/.
// Expected: IGNORED when Scan is called with excludeSegments containing "vendor".
// Expected: FINDING when Scan is called with no exclusions.
func VendorBareNolint() {
	fmt.Println("in vendor") //nolint
}
