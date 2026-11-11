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
	Replay        bool   `json:"replay,omitempty"`
	ReplayOutput  string `json:"replay_output,omitempty"`
	ReplaySeconds int    `json:"replay_seconds,omitempty"`
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

// PreferencesStatus is the preferences-status reply, carried as JSON in
// Response.Message. The *Env fields report environment overrides seen by the daemon.
type PreferencesStatus struct {
	SaveDir            string `json:"save_dir"`
	LegacySaveDir      string `json:"legacy_save_dir"`
	SaveDirEnv         bool   `json:"save_dir_env"`
	AIModelEnv         bool   `json:"ai_model_env"`
	ModnetModelEnv     bool   `json:"modnet_model_env"`
	ModnetModel        string `json:"modnet_model"`
	ModnetModelDefault string `json:"modnet_model_default"`
	OnnxRuntimeLibEnv  bool   `json:"onnxruntime_lib_env"`
	CleanupActive      bool   `json:"cleanup_active"`
}
