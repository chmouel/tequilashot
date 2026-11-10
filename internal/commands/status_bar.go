package commands

import "github.com/chmouel/tequilashot/internal/waybar"

// statusBarHider is a claim on the hidden status bar.
type statusBarHider interface{ Restore() }

// noStatusBar is the claim of a capture that leaves the bar alone.
type noStatusBar struct{}

func (noStatusBar) Restore() {}

// hideStatusBar is a variable so that tests can observe the claims. A nil
// *waybar.Hider is a valid claim.
var hideStatusBar = func() statusBarHider { return waybar.ForScreen() }

// holdStatusBar hides the bar for a capture of a whole screen; a window or
// region capture leaves it alone.
func holdStatusBar(wholeScreen bool) statusBarHider {
	if !wholeScreen {
		return noStatusBar{}
	}
	return hideStatusBar()
}
