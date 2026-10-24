package menubar

// Popover height bounds shared by the native hosts and the browser page.
// The page measures its own content and asks the host to resize; these clamp
// the request so a bad measurement can never collapse or overflow the popover.
const (
	// MenubarPopoverMinHeight keeps the header, at least one card and the
	// footer visible even when a provider list is almost empty.
	MenubarPopoverMinHeight = 140
	// MenubarPopoverMaxHeight is the default ceiling. Callers pass the screen's
	// visible height when it is smaller, so the popover never grows off-screen.
	MenubarPopoverMaxHeight = 720
)

// ClampPopoverHeight bounds a requested popover height. maxAllowed is the
// caller's upper bound (for example the screen's visible height minus a
// margin); values below the floor are ignored so the built-in ceiling applies.
func ClampPopoverHeight(requested, maxAllowed int) int {
	upper := MenubarPopoverMaxHeight
	if maxAllowed >= MenubarPopoverMinHeight && maxAllowed < upper {
		upper = maxAllowed
	}
	if requested < MenubarPopoverMinHeight {
		return MenubarPopoverMinHeight
	}
	if requested > upper {
		return upper
	}
	return requested
}
