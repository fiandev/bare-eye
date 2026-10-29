// Package agent implements the optional LLM automation loop: an
// OpenAI-compatible chat client with SSH tools for guided VPS setup.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/sshx"
	"github.com/fiandev/bare-eye/internal/state"
)

// EventKind classifies agent progress for the dashboard.
type EventKind string

const (
	EventInfo      EventKind = "info"
	EventAssistant EventKind = "assistant"
	EventTool      EventKind = "tool"
	EventResult    EventKind = "result"
	EventError     EventKind = "error"
	EventDone      EventKind = "done"
)

// Event is one progress message emitted by a run.
type Event struct {
	Kind    EventKind
	Text    string
	Host    string
	Command string
}

// Approval is a mutating command awaiting operator consent.
type Approval struct {
	Host    string
	Command string
	Purpose string
}

// Callbacks connects the agent to the dashboard. Emit must not block for long;
// Approve blocks until the operator answers or the context is cancelled.
type Callbacks struct {
	Emit    func(Event)
	Approve func(context.Context, Approval) bool
}

// Errors returned before a run starts.
var (
	ErrDisabled = errors.New("AI agent is disabled; set ai.enabled: true")
	ErrNoAPIKey = errors.New("no AI API key found; set BARE_EYE_API_KEY (or ai.api_key_env)")
)

// Runner executes one agent task at a time.
type Runner struct {
	cfg     *config.Config
	secrets *config.Secrets
	store   *state.Store
	client  *Client
}

// New builds a runner. The API client is only created when the agent is enabled.
func New(cfg *config.Config, secrets *config.Secrets, store *state.Store) *Runner {
	r := &Runner{cfg: cfg, secrets: secrets, store: store}
	if cfg != nil && cfg.AI.Enabled {
		r.client = NewClient(cfg.AI.BaseURL, cfg.AI.APIKey(secrets), cfg.AI.Model, cfg.AI.Timeout.Duration())
	}
	return r
}

// Ready reports whether the agent can run now.
func (r *Runner) Ready() error {
	if r == nil || r.cfg == nil {
		return ErrDisabled
	}
	if !r.cfg.AI.Enabled {
		return ErrDisabled
	}
	if r.client == nil || strings.TrimSpace(r.client.APIKey) == "" {
		return ErrNoAPIKey
	}
	return nil
}

// Run drives the model/tool loop until it produces a final answer or hits the
// configured step limit.
func (r *Runner) Run(ctx context.Context, task, hint string, cb Callbacks) error {
	if err := r.Ready(); err != nil {
		return err
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return errors.New("empty agent task")
	}
	emit := func(kind EventKind, text string) {
		if cb.Emit != nil {
			cb.Emit(Event{Kind: kind, Text: text})
		}
	}
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt(task, hint)},
	}
	tools := toolDefinitions()
	maxSteps := r.cfg.AI.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 12
	}
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := r.client.Chat(ctx, messages, tools)
		if err != nil {
			return err
		}
		for i := range msg.ToolCalls {
			if msg.ToolCalls[i].ID == "" {
				msg.ToolCalls[i].ID = fmt.Sprintf("call_%d_%d", step, i)
			}
			if msg.ToolCalls[i].Type == "" {
				msg.ToolCalls[i].Type = "function"
			}
		}
		messages = append(messages, msg)
		if text := strings.TrimSpace(msg.Content); text != "" {
			emit(EventAssistant, text)
		}
		if len(msg.ToolCalls) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				emit(EventInfo, "agent finished without a message")
			}
			return nil
		}
		for _, call := range msg.ToolCalls {
			result := r.executeTool(ctx, call, cb, emit)
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}
	return fmt.Errorf("agent stopped after %d steps without a final answer", maxSteps)
}

func (r *Runner) executeTool(ctx context.Context, call ToolCall, cb Callbacks, emit func(EventKind, string)) string {
	switch call.Function.Name {
	case "list_hosts":
		return r.listHosts()
	case "run_command":
		return r.runCommand(ctx, call.Function.Arguments, cb, emit)
	default:
		return toolError("unknown tool %q", call.Function.Name)
	}
}

type runCommandArgs struct {
	Host    string `json:"host"`
	Command string `json:"command"`
	Purpose string `json:"purpose"`
}

type execResult struct {
	Host    string `json:"host"`
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (r *Runner) runCommand(ctx context.Context, raw string, cb Callbacks, emit func(EventKind, string)) string {
	var args runCommandArgs
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return toolError("invalid arguments: %v", err)
		}
	}
	args.Host = strings.TrimSpace(args.Host)
	args.Command = strings.TrimSpace(args.Command)
	args.Purpose = strings.TrimSpace(args.Purpose)
	if args.Host == "" || args.Command == "" {
		return toolError("host and command are required")
	}
	if len([]rune(args.Command)) > 4096 {
		return toolError("command is too long (max 4096 characters)")
	}
	srv, err := r.findServer(args.Host)
	if err != nil {
		return toolError("%v", err)
	}
	host := srv.DisplayName()
	readOnly := ReadOnly(args.Command)
	if !readOnly {
		purpose := args.Purpose
		if purpose == "" {
			purpose = "command changes the server"
		}
		emit(EventInfo, fmt.Sprintf("waiting for approval on %s: %s", host, args.Command))
		approve := cb.Approve
		if approve == nil {
			approve = func(context.Context, Approval) bool { return false }
		}
		if !approve(ctx, Approval{Host: host, Command: args.Command, Purpose: purpose}) {
			emit(EventInfo, fmt.Sprintf("operator declined command on %s", host))
			return mustJSON(map[string]any{
				"host":     host,
				"command":  args.Command,
				"declined": true,
				"message":  "The operator declined this command. Do not retry it. Explain what you wanted to do and propose an alternative.",
			})
		}
	}
	if cb.Emit != nil {
		cb.Emit(Event{Kind: EventTool, Text: args.Purpose, Host: host, Command: args.Command})
	}
	out, runErr := r.exec(ctx, srv, args.Command)
	res := execResult{Host: host, Command: args.Command, OK: runErr == nil, Output: clip(string(out), 16000)}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	preview := strings.TrimSpace(res.Output)
	if preview == "" && res.Error != "" {
		preview = res.Error
	}
	emit(EventResult, clip(preview, 4000))
	return mustJSON(res)
}

func (r *Runner) exec(ctx context.Context, srv config.ServerConfig, command string) ([]byte, error) {
	timeout := r.cfg.AI.CommandTimeout.Duration()
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	opts := sshx.Options{
		KnownHosts:     r.cfg.SSH.KnownHosts,
		DefaultKey:     r.cfg.SSH.DefaultKey,
		ConnectTimeout: srv.ConnectTimeoutOrDefault(r.cfg),
	}
	if r.secrets != nil {
		opts.Password, _ = r.secrets.Get(srv.ID)
	}
	client, err := sshx.Dial(ctx, srv, opts)
	if err != nil {
		return nil, describeDialError(err, srv)
	}
	defer client.Close()
	return client.Run(ctx, command, timeout)
}

func describeDialError(err error, srv config.ServerConfig) error {
	var unknown *sshx.UnknownHostKeyError
	if errors.As(err, &unknown) {
		return fmt.Errorf("unknown host key for %s; run `bare-eye hosts test %s` to review and trust it", srv.DisplayName(), srv.ID)
	}
	var changed *sshx.ChangedHostKeyError
	if errors.As(err, &changed) {
		return fmt.Errorf("host key changed for %s; investigate before reconnecting", srv.DisplayName())
	}
	if errors.Is(err, sshx.ErrPasswordRequired) {
		return fmt.Errorf("no stored password for %s; run `bare-eye config set password %s`", srv.DisplayName(), srv.ID)
	}
	return err
}

func (r *Runner) findServer(ref string) (config.ServerConfig, error) {
	if r.cfg == nil {
		return config.ServerConfig{}, errors.New("no configuration loaded")
	}
	if srv, err := r.cfg.FindServer(ref); err == nil {
		return *srv, nil
	}
	for _, srv := range r.cfg.Servers {
		if strings.EqualFold(srv.DisplayName(), ref) || strings.EqualFold(srv.Host, ref) {
			return srv, nil
		}
	}
	return config.ServerConfig{}, fmt.Errorf("host %q is not configured", ref)
}

func (r *Runner) listHosts() string {
	type hostInfo struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Host    string `json:"host"`
		User    string `json:"user"`
		Enabled bool   `json:"enabled"`
		Status  string `json:"status,omitempty"`
	}
	statuses := map[string]string{}
	if r.store != nil {
		for _, h := range r.store.Snapshot() {
			statuses[h.ID] = string(h.Status)
		}
	}
	out := make([]hostInfo, 0, len(r.cfg.Servers))
	for _, srv := range r.cfg.Servers {
		out = append(out, hostInfo{
			ID:      srv.ID,
			Name:    srv.DisplayName(),
			Host:    srv.Host,
			User:    srv.User,
			Enabled: srv.Enabled,
			Status:  statuses[srv.ID],
		})
	}
	return mustJSON(out)
}

func toolError(format string, args ...any) string {
	return mustJSON(map[string]string{"error": fmt.Sprintf(format, args...)})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"failed to encode tool result"}`
	}
	return string(b)
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 128 {
		return s[:max]
	}
	head := max * 3 / 4
	tail := max - head - 40
	if tail < 0 {
		tail = 0
	}
	return s[:head] + "\n… output truncated …\n" + s[len(s)-tail:]
}
