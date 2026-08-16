package i18n

import "embed"

//go:embed locales/*.yaml
var embedded embed.FS

// NewBundle loads the locales compiled into the binary. It can only fail on
// a malformed embedded file, which is a build-time defect, not a runtime
// condition — hence the panic-free MustNewBundle used by callers that cannot
// proceed without messages.
func NewBundle() (*Bundle, error) {
	return New(embedded)
}

// MustNewBundle is NewBundle for wiring at startup.
func MustNewBundle() *Bundle {
	b, err := NewBundle()
	if err != nil {
		panic(err)
	}
	return b
}
