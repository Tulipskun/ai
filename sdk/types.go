package sdk

import "context"

type Role string

const (
	RoleUser       Role = "user"
	RoleModel      Role = "model"
	RoleToolCall   Role = "tool_call"
	RoleToolResult Role = "tool_result"
)

type ContentType string

const ContentText ContentType = "text"

type ContentPart struct {
	Type ContentType `json:"type"`
	Text string      `json:"text,omitempty"`
}

type Message struct {
	Role    Role          `json:"role"`
	Content []ContentPart `json:"content,omitempty"`
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolResult struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

type Turn struct {
	Role       Role          `json:"role"`
	Content    []ContentPart `json:"content,omitempty"`
	ToolCall   *ToolCall     `json:"tool_call,omitempty"`
	ToolResult *ToolResult   `json:"tool_result,omitempty"`
}

type ThinkingLevel string

const (
	ThinkingNone   ThinkingLevel = "none"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
)

// ProviderID identifies the logical service/account used for a request.
type ProviderID string

// AdapterID identifies the wire/API protocol adapter used to execute a request.
type AdapterID string

const (
	ProviderOpenRouter ProviderID = "openrouter"
	ProviderOpenCode   ProviderID = "opencode"

	AdapterOpenAI    AdapterID = "openai"
	AdapterAnthropic AdapterID = "anthropic"
	AdapterGemini    AdapterID = "gemini"
)

// ModelRoute binds a logical provider/model pair to an underlying adapter.
type ModelRoute struct {
	Provider ProviderID `json:"provider"`
	Model    string     `json:"model"`
	Adapter  AdapterID  `json:"adapter"`
}

// SessionConfig defines immutable routing and credential affinity for a session.
type SessionConfig struct {
	ID       string     `json:"id"`
	Provider ProviderID `json:"provider"`
	Model    string     `json:"model"`
	KeyIndex int        `json:"key_index"`
}

type Request struct {
	Provider        ProviderID    `json:"provider,omitempty"`
	SystemPrompt    string        `json:"system_prompt,omitempty"`
	Messages        []Turn        `json:"messages,omitempty"`
	Tools           []Tool        `json:"tools,omitempty"`
	Model           string        `json:"model"`
	Temperature     *float64      `json:"temperature,omitempty"`
	ThinkingLevel   ThinkingLevel `json:"thinking_level,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
}

func (r Request) RequestProvider() ProviderID { return r.Provider }

type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type CacheInfo struct {
	Hit   bool   `json:"hit"`
	Layer string `json:"layer,omitempty"`
}

type Response struct {
	Provider     string        `json:"provider"`
	Model        string        `json:"model"`
	Content      []ContentPart `json:"content,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	FinishReason string        `json:"finish_reason,omitempty"`
	Usage        Usage         `json:"usage"`
	Cache        CacheInfo     `json:"cache"`
}

type EventType string

const (
	EventText     EventType = "text"
	EventToolCall EventType = "tool_call"
	EventDone     EventType = "done"
	EventError    EventType = "error"
)

type Event struct {
	Type     EventType `json:"type"`
	Text     string    `json:"text,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Response *Response `json:"response,omitempty"`
	Err      error     `json:"-"`
}

type Provider interface {
	Name() string
	Generate(context.Context, Request) (Response, error)
	Stream(context.Context, Request) (<-chan Event, error)
}
