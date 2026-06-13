package supervisor

// Wire protocol between the CLI and a supervisor: one newline-delimited JSON
// Request, one Response, per connection.

const (
	OpExec       = "exec"
	OpPing       = "ping"
	OpStop       = "stop"
	OpInfo       = "info"
	OpScreenshot = "screenshot"
	OpSnapshot   = "snapshot"
)

type Request struct {
	Op       string `json:"op"`
	Cmd      string `json:"cmd,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

type Response struct {
	OK        bool   `json:"ok"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Output    string `json:"output,omitempty"`
	PNGBase64 string `json:"png_base64,omitempty"`
	Error     string `json:"error,omitempty"`
	State     *State `json:"state,omitempty"`
}
