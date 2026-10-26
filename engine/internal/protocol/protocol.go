// Package protocol defines the JSON contract between the bedrock-advisor
// plugin and advisor-engine. The plugin writes a Request on stdin and reads a
// Response from stdout. Bump Version on any breaking change.
package protocol

// Version is the protocol version this engine speaks.
const Version = 1

// Modes for Request.Mode.
const (
	// ModeAsk builds the advisor prompt and calls Bedrock.
	ModeAsk = "ask"
	// ModePrompt builds the advisor prompt and returns it without calling a
	// model, so the plugin can send it through Claude Code's own credentials.
	ModePrompt = "prompt"
)

// Error kinds for ErrorInfo.Kind.
const (
	ErrInput        = "input"
	ErrTranscript   = "transcript"
	ErrCredentials  = "credentials"
	ErrAccessDenied = "access_denied"
	ErrThrottled    = "throttled"
	ErrTimeout      = "timeout"
	ErrAPI          = "api"
)

// Request is what the plugin sends on stdin.
type Request struct {
	Protocol       int    `json:"protocol"`
	Mode           string `json:"mode"`
	TranscriptPath string `json:"transcriptPath"`
	ProjectsRoot   string `json:"projectsRoot"`
	Question       string `json:"question,omitempty"`
	Trigger        string `json:"trigger,omitempty"`
	// Detail is untrusted text the trigger is about, such as a tool error.
	// It is quoted as data, apart from the question.
	Detail          string `json:"detail,omitempty"`
	Model           string `json:"model,omitempty"`
	Region          string `json:"region,omitempty"`
	MaxTokens       int    `json:"maxTokens,omitempty"`
	MaxContextChars int    `json:"maxContextChars,omitempty"`
	TimeoutMs       int    `json:"timeoutMs,omitempty"`
}

// Usage reports advisor token counts.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// Stats describes how the transcript was reduced for the advisor.
type Stats struct {
	Records  int `json:"records"`
	Kept     int `json:"kept"`
	Omitted  int `json:"omitted"`
	Chars    int `json:"chars"`
	RawBytes int `json:"rawBytes"`
}

// ErrorInfo describes a failure the plugin can act on.
type ErrorInfo struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Response is what the engine writes on stdout.
type Response struct {
	Protocol int        `json:"protocol"`
	OK       bool       `json:"ok"`
	Text     string     `json:"text,omitempty"`
	Model    string     `json:"model,omitempty"`
	System   string     `json:"system,omitempty"`
	Prompt   string     `json:"prompt,omitempty"`
	Usage    *Usage     `json:"usage,omitempty"`
	Stats    *Stats     `json:"stats,omitempty"`
	Error    *ErrorInfo `json:"error,omitempty"`
}
