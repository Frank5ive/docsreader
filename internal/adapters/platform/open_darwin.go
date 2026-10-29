//go:build darwin

package platform

import "os/exec"

// OpenBrowser opens url in the default browser.
func OpenBrowser(url string) error { return exec.Command("open", url).Start() }
