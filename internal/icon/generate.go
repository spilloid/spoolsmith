package icon

// The product site's icons are rendered from the same master PNG the Windows
// executables embed, so the two can never show different artwork. Regenerate
// with "go generate ./internal/icon" after replacing assets/icon/spoolsmith.png;
// site_test.go fails if the committed favicon.ico no longer matches it.
//
//go:generate go run ./mkicon -src ../../assets/icon/spoolsmith.png -site ../../docs
