package sway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
)

// Monitor describes an active output.
type Monitor struct {
	Name        string
	Description string
	X, Y        int
	Width       int
	Height      int
	Focused     bool
}

// Window describes a visible, mapped window and its on-screen area.
type Window struct {
	ID     string
	Title  string
	Class  string
	X, Y   int
	Width  int
	Height int
}

// ErrWindowGone reports that a window no longer exists on screen.
var ErrWindowGone = errors.New("window is no longer available")

// FindWindow returns the visible window with the given stable ID.
func FindWindow(ctx context.Context, id string) (Window, error) {
	wins, err := ListWindows(ctx)
	if err != nil {
		return Window{}, err
	}
	for _, w := range wins {
		if w.ID == id {
			return w, nil
		}
	}
	return Window{}, ErrWindowGone
}

// FormatGeometry renders an area in the slurp-style "X,Y WxH" form.
func FormatGeometry(x, y, w, h int) string {
	return fmt.Sprintf("%d,%d %dx%d", x, y, w, h)
}

// Geometry returns the window area in slurp-style form.
func (w Window) Geometry() string {
	return FormatGeometry(w.X, w.Y, w.Width, w.Height)
}

// ListMonitors returns the active outputs, ordered by position.
func ListMonitors(ctx context.Context) ([]Monitor, error) {
	if isHyprland() {
		out, err := hyprctlJSON(ctx, "-j", "monitors")
		if err != nil {
			return nil, err
		}
		return monitorsFromHypr(out)
	}
	out, err := exec.CommandContext(ctx, "swaymsg", "-t", "get_outputs", "-r").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get sway outputs: %w", err)
	}
	return monitorsFromSway(out)
}

// ListWindows returns the windows currently visible on screen.
func ListWindows(ctx context.Context) ([]Window, error) {
	if isHyprland() {
		clients, err := hyprctlJSON(ctx, "-j", "clients")
		if err != nil {
			return nil, err
		}
		monitors, err := hyprctlJSON(ctx, "-j", "monitors")
		if err != nil {
			return nil, err
		}
		return windowsFromHypr(clients, monitors)
	}
	out, err := exec.CommandContext(ctx, "swaymsg", "-t", "get_tree", "-r").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get sway tree: %w", err)
	}
	return windowsFromSway(out)
}

func sortMonitors(ms []Monitor) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].X != ms[j].X {
			return ms[i].X < ms[j].X
		}
		return ms[i].Y < ms[j].Y
	})
}

func monitorsFromHypr(data []byte) ([]Monitor, error) {
	hm, err := parseHyprMonitors(data)
	if err != nil {
		return nil, err
	}
	var ms []Monitor
	for _, m := range hm {
		ms = append(ms, Monitor{
			Name: m.Name, Description: m.Make + " " + m.Model,
			X: m.X, Y: m.Y, Width: m.Width, Height: m.Height, Focused: m.Focused,
		})
	}
	sortMonitors(ms)
	return ms, nil
}

func monitorsFromSway(data []byte) ([]Monitor, error) {
	var outputs []swayOutput
	if err := json.Unmarshal(data, &outputs); err != nil {
		return nil, fmt.Errorf("failed to parse sway outputs: %w", err)
	}
	var ms []Monitor
	for _, o := range outputs {
		if !o.Active {
			continue
		}
		ms = append(ms, Monitor{
			Name: o.Name, Description: o.Make + " " + o.Model,
			X: o.Rect.X, Y: o.Rect.Y, Width: o.Rect.Width, Height: o.Rect.Height, Focused: o.Focused,
		})
	}
	sortMonitors(ms)
	return ms, nil
}

func windowsFromHypr(clients, monitors []byte) ([]Window, error) {
	var cs []struct {
		Address   string `json:"address"`
		Mapped    bool   `json:"mapped"`
		Hidden    bool   `json:"hidden"`
		At        [2]int `json:"at"`
		Size      [2]int `json:"size"`
		Class     string `json:"class"`
		Title     string `json:"title"`
		Workspace struct {
			ID int `json:"id"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(clients, &cs); err != nil {
		return nil, fmt.Errorf("failed to parse hyprland clients: %w", err)
	}
	var ms []struct {
		Active struct {
			ID int `json:"id"`
		} `json:"activeWorkspace"`
		Special struct {
			ID int `json:"id"`
		} `json:"specialWorkspace"`
	}
	if err := json.Unmarshal(monitors, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse hyprland monitors: %w", err)
	}
	visible := map[int]bool{}
	for _, m := range ms {
		visible[m.Active.ID] = true
		if m.Special.ID != 0 {
			visible[m.Special.ID] = true
		}
	}

	var ws []Window
	for _, c := range cs {
		if !c.Mapped || c.Hidden || c.Size[0] <= 0 || c.Size[1] <= 0 || !visible[c.Workspace.ID] {
			continue
		}
		ws = append(ws, Window{ID: c.Address, Title: c.Title, Class: c.Class, X: c.At[0], Y: c.At[1], Width: c.Size[0], Height: c.Size[1]})
	}
	return ws, nil
}

type swayView struct {
	swayNode
	ID               int    `json:"id"`
	Name             string `json:"name"`
	AppID            string `json:"app_id"`
	Visible          bool   `json:"visible"`
	WindowProperties struct {
		Class string `json:"class"`
	} `json:"window_properties"`
	Nodes         []swayView `json:"nodes"`
	FloatingNodes []swayView `json:"floating_nodes"`
}

func windowsFromSway(data []byte) ([]Window, error) {
	var root swayView
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse sway tree: %w", err)
	}
	var ws []Window
	var walk func(n *swayView)
	walk = func(n *swayView) {
		if len(n.Nodes) == 0 && len(n.FloatingNodes) == 0 {
			if (n.Type == "con" || n.Type == "floating_con") && n.Visible && n.Rect.Width > 0 && n.Rect.Height > 0 {
				class := n.AppID
				if class == "" {
					class = n.WindowProperties.Class
				}
				ws = append(ws, Window{ID: strconv.Itoa(n.ID), Title: n.Name, Class: class, X: n.Rect.X, Y: n.Rect.Y, Width: n.Rect.Width, Height: n.Rect.Height})
			}
			return
		}
		for i := range n.Nodes {
			walk(&n.Nodes[i])
		}
		for i := range n.FloatingNodes {
			walk(&n.FloatingNodes[i])
		}
	}
	walk(&root)
	return ws, nil
}
