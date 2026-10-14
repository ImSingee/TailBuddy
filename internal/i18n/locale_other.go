//go:build !darwin

package i18n

// PreferredLocale is English on non-macOS development hosts.
func PreferredLocale() string { return "en" }
