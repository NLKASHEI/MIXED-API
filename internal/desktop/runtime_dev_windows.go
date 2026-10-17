//go:build windows && !bundled

package desktop

// Development builds may use the locally installed renderer. Release builds
// always use -tags bundled and must embed the pinned, offline runtime.
func rendererPath() (string, error) { return "", nil }
