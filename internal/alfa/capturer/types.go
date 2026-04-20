package capturer

// Schema is the value required in a BatchInput / BatchOutput
// `schema` field per RFC 0001 § Capturer Protocol.
const Schema = "web-capture-archive/v1"

// WriterCmd is the argv the capturer forks per artifact to store
// bytes in a content-addressed blob store.
type WriterCmd struct {
	Cmd []string `json:"cmd"`
}

// Defaults are applied to each CaptureRequest that does not override.
type Defaults struct {
	Browser   string `json:"browser,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	Split     *bool  `json:"split,omitempty"`
}

// CaptureRequest is one entry in BatchInput.Captures.
type CaptureRequest struct {
	Name       string         `json:"name"`
	Format     string         `json:"format"`
	Browser    string         `json:"browser,omitempty"`
	Options    map[string]any `json:"options,omitempty"`
	Split      *bool          `json:"split,omitempty"`
	Isolation  string         `json:"isolation,omitempty"`
	Extensions []ExtensionRef `json:"extensions,omitempty"`
	Flags      []string       `json:"flags,omitempty"`
}

// ExtensionRef identifies a browser extension by id + version.
type ExtensionRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// BatchInput is the JSON document piped to chrest capture-batch on stdin.
type BatchInput struct {
	Schema   string           `json:"schema"`
	Writer   WriterCmd        `json:"writer"`
	URL      string           `json:"url"`
	Defaults Defaults         `json:"defaults,omitempty"`
	Captures []CaptureRequest `json:"captures"`
}

// ArtifactRef points at one content-addressed blob in the writer's
// store. Shape mirrors RFC 0001 § Capturer Protocol.
type ArtifactRef struct {
	ID         string `json:"id"`
	Size       int64  `json:"size"`
	MediaType  string `json:"media_type"`
	Normalized *bool  `json:"normalized,omitempty"`
}

// CaptureResult is one entry in BatchOutput.Captures. Success produces
// spec + payload (+ envelope when split=true); failure produces error
// without artifact refs.
type CaptureResult struct {
	Name     string       `json:"name"`
	Spec     *ArtifactRef `json:"spec,omitempty"`
	Payload  *ArtifactRef `json:"payload,omitempty"`
	Envelope *ArtifactRef `json:"envelope,omitempty"`
	Error    *ErrorEntry  `json:"error,omitempty"`
}

// ErrorEntry matches RFC 0001's per-capture and batch-level error
// shape: machine-readable kind + human-readable message.
type ErrorEntry struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// CapturerInfo identifies the capturer that produced the batch.
type CapturerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// BatchOutput is the JSON document read from chrest capture-batch's stdout.
type BatchOutput struct {
	Schema   string          `json:"schema"`
	Capturer CapturerInfo    `json:"capturer"`
	Errors   []ErrorEntry    `json:"errors"`
	Captures []CaptureResult `json:"captures"`
}
