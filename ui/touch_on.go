//go:build ios

package ui

// iOS's backend sends the pointer events of fingers.
func init() { fingers = fingerEvent }
