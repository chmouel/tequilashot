package protocol

// Request represents a command request to the daemon
type Request struct {
	Command string                 `json:"command"`
	Action  string                 `json:"action"`
	Options map[string]interface{} `json:"options,omitempty"`
}

// Response represents a response from the daemon
type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	File    string `json:"file,omitempty"`
	State   *State `json:"state,omitempty"`
}

// State represents the current daemon state
type State struct {
	Recording     bool   `json:"recording"`
	Paused        bool   `json:"paused"`
	RecordingFile string `json:"recording_file,omitempty"`
	VirtualCamera bool   `json:"virtual_camera,omitempty"`
}

// WaybarStatus represents the status for waybar integration
type WaybarStatus struct {
	Text    string `json:"text"`
	Tooltip string `json:"tooltip"`
	Class   string `json:"class"`
	Alt     string `json:"alt"`
}
