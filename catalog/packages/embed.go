// Package packages contains reviewed, versioned vendor package metadata.
package packages

import _ "embed"

// BrotherY14 is the source record for the verified Brother package.
//
//go:embed brother-y14a-c1.json
var BrotherY14 string
