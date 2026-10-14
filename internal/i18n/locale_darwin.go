//go:build darwin

package i18n

import (
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// PreferredLocale asks NSBundle to negotiate the bundle's declared languages
// against macOS preferences, including the per-application language override.
// Call on the UI main thread after Foundation has been initialized.
func PreferredLocale() string {
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return "en"
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	bundle := objc.ID(objc.GetClass("NSBundle")).Send(objc.RegisterName("mainBundle"))
	languages := bundle.Send(objc.RegisterName("preferredLocalizations"))
	if languages == 0 || languages.Send(objc.RegisterName("count")) == 0 {
		return "en"
	}
	first := languages.Send(objc.RegisterName("objectAtIndex:"), uintptr(0))
	return objc.Send[string](first, objc.RegisterName("UTF8String"))
}
