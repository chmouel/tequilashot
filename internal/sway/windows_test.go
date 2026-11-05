package sway

import "testing"

func TestWindowsFromHypr(t *testing.T) {
	clients := []byte(`[
{"address":"0xabc","mapped":true,"hidden":false,"at":[10,20],"size":[800,600],"class":"kitty","title":"zsh","workspace":{"id":1}},
{"mapped":true,"hidden":false,"at":[0,0],"size":[100,100],"class":"other","title":"x","workspace":{"id":2}},
{"mapped":false,"hidden":false,"at":[0,0],"size":[100,100],"class":"ghost","title":"g","workspace":{"id":1}},
{"mapped":true,"hidden":true,"at":[0,0],"size":[100,100],"class":"hid","title":"h","workspace":{"id":1}}]`)
	monitors := []byte(`[{"activeWorkspace":{"id":1},"specialWorkspace":{"id":0}}]`)
	ws, err := windowsFromHypr(clients, monitors)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Class != "kitty" || ws[0].ID != "0xabc" || ws[0].Geometry() != "10,20 800x600" {
		t.Fatalf("unexpected windows: %+v", ws)
	}
}

func TestWindowsFromSway(t *testing.T) {
	tree := []byte(`{"type":"root","nodes":[{"type":"output","nodes":[{"type":"workspace","nodes":[
{"id":7,"type":"con","visible":true,"app_id":"foot","name":"term","rect":{"x":0,"y":20,"width":500,"height":400},"nodes":[],"floating_nodes":[]},
{"type":"con","visible":false,"app_id":"bg","name":"bg","rect":{"x":0,"y":0,"width":500,"height":400},"nodes":[],"floating_nodes":[]}],
"floating_nodes":[{"id":9,"type":"floating_con","visible":true,"name":"x","window_properties":{"class":"Xapp"},"rect":{"x":5,"y":6,"width":70,"height":80},"nodes":[],"floating_nodes":[]}]}]}]}`)
	ws, err := windowsFromSway(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].Class != "foot" || ws[0].ID != "7" || ws[1].ID != "9" || ws[1].Class != "Xapp" || ws[1].Geometry() != "5,6 70x80" {
		t.Fatalf("unexpected windows: %+v", ws)
	}
}

func TestWindowsFromHyprTabbedGroup(t *testing.T) {
	clients := []byte(`[
{"address":"0x1","mapped":true,"hidden":false,"visible":false,"at":[7,72],"size":[3826,2081],"class":"kitty","workspace":{"id":1},"grouped":["0x1","0x2"]},
{"address":"0x2","mapped":true,"hidden":false,"visible":true,"at":[7,72],"size":[3826,2081],"class":"pavucontrol","workspace":{"id":1},"grouped":["0x1","0x2"]}
]
`)
	monitors := []byte(`[{"activeWorkspace":{"id":1}}]`)
	wins, err := windowsFromHypr(clients, monitors)
	if err != nil {
		t.Fatal(err)
	}
	if len(wins) != 1 || wins[0].ID != "0x2" {
		t.Fatalf("inactive group members must not share the active tab's preview: %+v", wins)
	}
}

func TestMonitorsOrdered(t *testing.T) {
	ms, err := monitorsFromHypr([]byte(`[{"name":"B","x":0,"y":2160,"width":1920,"height":1080},{"name":"A","x":0,"y":0,"width":3840,"height":2160}]`))
	if err != nil || len(ms) != 2 || ms[0].Name != "A" {
		t.Fatalf("unexpected: %+v %v", ms, err)
	}
	ms, err = monitorsFromSway([]byte(`[{"name":"X","active":false},{"name":"Y","active":true,"rect":{"x":0,"y":0,"width":10,"height":20}}]`))
	if err != nil || len(ms) != 1 || ms[0].Name != "Y" {
		t.Fatalf("unexpected: %+v %v", ms, err)
	}
}
