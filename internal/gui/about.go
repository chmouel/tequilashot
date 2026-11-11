//go:build !nogui

package gui

import (
	_ "embed"
	"fmt"
	"log"
	"runtime"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Version is the release version, injected by the build; local builds say dev.
var Version = "dev"

// portrait is the proprietor, glass in hand and bottle conspicuously absent.
//
//go:embed about-photo.jpg
var portrait []byte

const (
	aboutWebsite    = "https://github.com/chmouel/tequilashot"
	aboutIssueURL   = aboutWebsite + "/issues"
	aboutReadmeURL  = aboutWebsite + "#readme"
	aboutReleases   = aboutWebsite + "/releases"
	aboutSponsorURL = "https://github.com/sponsors/chmouel"
	aboutBlogURL    = "https://blog.chmouel.com"
	aboutFediverse  = "https://fosstodon.org/@chmouel"
	aboutComments   = "A discreet establishment for screenshots and recordings on Wayland, where the glass is always empty, " +
		"the salt is imaginary and the lime has been mislaid.\n\n" +
		"<span size=\"large\" weight=\"bold\"><a href=\"" + aboutSponsorURL + "\">❤ Sponsor me</a></span>"
	aboutCopyright = "© 2026 Chmouel Boudjnah"
	portraitSize   = 128
)

// aboutVersion reports the release, without the tag prefix, as the dialogue
// shows it against the application name.
func aboutVersion() string {
	v := strings.TrimPrefix(strings.TrimSpace(Version), "v")
	if v == "" {
		return "dev"
	}
	return v
}

// showAbout presents the about dialogue over the capture window.
func (a *app) showAbout() {
	dlg := newAboutDialog()
	a.aboutOpen = true
	dlg.ConnectClosed(func() { a.aboutOpen = false })
	dlg.Present(a.win)
}

// modalOpen reports whether the about or preferences dialogue covers the
// window.
func (a *app) modalOpen() bool {
	return a.aboutOpen || a.prefsOpen
}

// primaryMenu builds the header bar's main menu and registers its actions.
func (a *app) primaryMenu() *gtk.MenuButton {
	for name, activate := range map[string]func(){"open-video": a.chooseTrimVideo, "preferences": a.showPreferences, "about": a.showAbout} {
		action := gio.NewSimpleAction(name, nil)
		action.ConnectActivate(func(*glib.Variant) {
			if !a.modalOpen() {
				activate()
			}
		})
		a.gtkApp.AddAction(action)
	}
	a.gtkApp.SetAccelsForAction("app.preferences", []string{"<Control>comma"})
	a.gtkApp.SetAccelsForAction("app.open-video", []string{"<Control>o"})

	menu := gio.NewMenu()
	menu.Append("_Open Video…", "app.open-video")
	menu.Append("_Preferences", "app.preferences")
	menu.Append("_About Tequilashot", "app.about")
	b := gtk.NewMenuButton()
	b.SetIconName("open-menu-symbolic")
	b.SetTooltipText("Main Menu")
	b.SetMenuModel(menu)
	b.SetPrimary(true)
	return b
}

func newAboutDialog() *adw.AboutDialog {
	dlg := adw.NewAboutDialog()
	dlg.SetApplicationName("Tequilashot")
	dlg.SetApplicationIcon(appID)
	dlg.SetVersion(aboutVersion())
	dlg.SetDeveloperName("Chmouel Boudjnah")
	dlg.SetDevelopers([]string{"Chmouel Boudjnah https://chmouel.com"})
	dlg.SetComments(aboutComments)
	dlg.SetWebsite(aboutWebsite)
	dlg.SetIssueURL(aboutIssueURL)
	dlg.SetSupportURL(aboutReadmeURL)
	dlg.SetCopyright(aboutCopyright)
	dlg.SetLicenseType(gtk.LicenseApache20)
	dlg.SetDebugInfo(fmt.Sprintf("Tequilashot %s\nGTK %s\nlibadwaita %s\nGo %s\n%s",
		aboutVersion(), gtkVersion(), adwVersion(), runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH))
	dlg.SetDebugInfoFilename("tequilashot-debug-info.txt")
	dlg.AddLink("Sponsor me", aboutSponsorURL)
	dlg.AddLink("Documentation", aboutReadmeURL)
	dlg.AddLink("Release Notes", aboutReleases)
	dlg.AddLink("Blog", aboutBlogURL)
	dlg.AddLink("Fediverse", aboutFediverse)
	dlg.AddCreditSection("The capture machinery", []string{
		"grim https://sr.ht/~emersion/grim/",
		"slurp https://github.com/emersion/slurp",
		"GPU Screen Recorder https://git.dec05eba.com/gpu-screen-recorder/about/",
		"FFmpeg https://ffmpeg.org/",
		"RNNoise https://jmvalin.ca/demo/rnnoise/",
		"wl-clipboard https://github.com/bugaevc/wl-clipboard",
	})
	dlg.AddCreditSection("The drawing room", []string{
		"GTK https://www.gtk.org/",
		"libadwaita https://gnome.pages.gitlab.gnome.org/libadwaita/",
		"gotk4 https://github.com/diamondburned/gotk4",
	})
	dlg.AddAcknowledgementSection("Previously answering to", []string{"sway-easyshot"})

	showPortrait(dlg)
	return dlg
}

// showPortrait replaces the application mark with the proprietor's likeness,
// which libadwaita offers no property for.
func showPortrait(dlg *adw.AboutDialog) {
	mark := aboutMark(dlg.Child())
	if mark == nil {
		log.Print("about: application mark not found, keeping the plain icon")
		return
	}
	texture, err := gdk.NewTextureFromBytes(glib.NewBytes(portrait))
	if err != nil {
		log.Printf("about: portrait: %v", err)
		return
	}
	face := adw.NewAvatar(max(mark.PixelSize(), portraitSize), "", false)
	face.SetCustomImage(texture)
	face.SetHAlign(gtk.AlignCenter)
	mark.SetVisible(false)
	face.InsertAfter(mark.Parent(), mark)
}

// aboutMark finds the application icon the about dialogue draws at its head.
func aboutMark(w gtk.Widgetter) *gtk.Image {
	if w == nil {
		return nil
	}
	if img, ok := w.(*gtk.Image); ok && slices.Contains(img.CSSClasses(), "icon-dropshadow") {
		return img
	}
	base := gtk.BaseWidget(w)
	child := base.FirstChild()
	for child != nil {
		next := gtk.BaseWidget(child).NextSibling()
		if img := aboutMark(child); img != nil {
			return img
		}
		child = next
	}
	return nil
}

func gtkVersion() string {
	return fmt.Sprintf("%d.%d.%d", gtk.GetMajorVersion(), gtk.GetMinorVersion(), gtk.GetMicroVersion())
}

func adwVersion() string {
	return fmt.Sprintf("%d.%d.%d", adw.GetMajorVersion(), adw.GetMinorVersion(), adw.GetMicroVersion())
}
