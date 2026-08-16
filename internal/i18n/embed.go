package i18n

import (
	"embed"
	"io/fs"
)

//go:embed locales/*.yaml
var embedded embed.FS

// NewBundle loads the locales compiled into the binary. It can only fail on
// a malformed embedded file, which is a build-time defect, not a runtime
// condition — hence MustNewBundle for wiring at startup.
func NewBundle() (*Bundle, error) {
	dir, err := fs.Sub(embedded, "locales")
	if err != nil {
		return nil, err
	}
	return New(dir)
}

// MustNewBundle is NewBundle for wiring at startup.
func MustNewBundle() *Bundle {
	b, err := NewBundle()
	if err != nil {
		panic(err)
	}
	return b
}
