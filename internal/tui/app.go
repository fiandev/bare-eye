// Package tui implements the Bare Eye terminal dashboard.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/widgets"

	"github.com/fiandev/bare-eye/internal/agent"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/keys"
	"github.com/fiandev/bare-eye/internal/scheduler"
	"github.com/fiandev/bare-eye/internal/sshx"
	"github.com/fiandev/bare-eye/internal/state"
)

type mode int

// DefaultRenderTick is how often the dashboard repaints by default. It is
// independent of the SSH collection interval, which decides how often new
// samples (chart points) arrive. It can be overridden with --tick.
const DefaultRenderTick = 100 * time.Millisecond

const (
	modeOverview mode = iota
	modeDetail
	modeForm
	modeHelp
	modeConfirm
	modeFilter
	modeKeyInfo
	modePassword
	modeAgentInput
	modeAgentLog
)

type asyncResult struct {
	kind     string
	id       string
	err      error
	srv      config.ServerConfig
	password string
	fromForm bool
}

type confirmState struct {
	title   string
	message string
	onYes   func()
	onNo    func()
}

type passwordState struct {
	hostID string
	srv    config.ServerConfig
	value  string
	after  func(password string)
}

type agentApproval struct {
	req agent.Approval
	res chan bool
}

// App is the dashboard state machine.
type App struct {
	cfg     *config.Config
	secrets *config.Secrets
	store   *state.Store
	events  *state.EventLog
	sched   *scheduler.Scheduler
	version string

	mode     mode
	previous mode
	selected int
	filter   string
	visible  []state.HostState

	form         *formState
	confirm      *confirmState
	password     *passwordState
	overlay      string
	overlayTitle string

	agentRunner     *agent.Runner
	agentBusy       bool
	agentCancel     context.CancelFunc
	agentReturn     mode
	agentEvents     chan agent.Event
	agentApproval   chan agentApproval
	pendingApproval *agentApproval
	transcript      []string
	agentScroll     int
	lastReply       string

	status   string
	statusAt time.Time

	width, height int
	quit          bool
	dirtyClear    bool

	header    *widgets.Paragraph
	table     *widgets.Table
	cpu       *widgets.Gauge
	mem       *widgets.Gauge
	swap      *widgets.Gauge
	spark     *axisChart
	detail    *widgets.Paragraph
	disk      *widgets.List
	net       *widgets.Paragraph
	netPlot   *axisChart
	eventLog  *widgets.List
	agentView *widgets.Paragraph
	prompt    *widgets.Input
	modal     *widgets.Paragraph

	async chan asyncResult
}

// Run starts the dashboard and blocks until the user quits. tick controls how
// often the screen repaints; zero uses DefaultRenderTick.
func Run(cfg *config.Config, secrets *config.Secrets, store *state.Store, events *state.EventLog, sched *scheduler.Scheduler, version string, tick time.Duration) error {
	if tick <= 0 {
		tick = DefaultRenderTick
	}
	if err := ui.Init(); err != nil {
		return fmt.Errorf("initialize terminal UI: %w", err)
	}
	defer ui.Close()

	a := newApp(cfg, secrets, store, events, sched, version)
	a.layout()
	a.dirtyClear = true
	a.refresh()
	a.render()

	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	uiEvents := ui.PollEvents()

	for !a.quit {
		select {
		case e := <-uiEvents:
			a.handleEvent(e)
		case <-ticker.C:
			a.refresh()
		case r := <-a.async:
			a.handleAsync(r)
		case ev := <-a.agentEvents:
			a.handleAgentEvent(ev)
		case req := <-a.agentApproval:
			if a.agentBusy {
				a.showAgentApproval(req)
			} else {
				req.res <- false
			}
		}
		if a.quit {
			break
		}
		a.render()
	}
	return nil
}

func newApp(cfg *config.Config, secrets *config.Secrets, store *state.Store, events *state.EventLog, sched *scheduler.Scheduler, version string) *App {
	a := &App{
		cfg:     cfg,
		secrets: secrets,
		store:   store,
		events:  events,
		sched:   sched,
		version: version,
		async:   make(chan asyncResult, 8),
	}
	a.agentEvents = make(chan agent.Event, 64)
	a.agentApproval = make(chan agentApproval, 1)
	a.agentRunner = agent.New(cfg, secrets, store)
	a.header = widgets.NewParagraph()
	a.header.Title = " Bare Eye "
	a.header.TitleStyle = ui.NewStyle(ui.ColorCyan, ui.ColorClear, ui.ModifierBold)
	a.header.BorderStyle = ui.NewStyle(ui.ColorCyan)

	a.table = widgets.NewTable()
	a.table.Title = " Servers "
	a.table.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
	a.table.TextStyle = ui.NewStyle(ui.ColorWhite)
	a.table.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, ui.ColorCyan, ui.ModifierBold)
	a.table.RowSeparator = false
	a.table.FillRow = true

	a.cpu = widgets.NewGauge()
	a.cpu.Title = " CPU "
	a.cpu.BarColor = ui.ColorGreen
	a.cpu.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.mem = widgets.NewGauge()
	a.mem.Title = " Memory "
	a.mem.BarColor = ui.ColorCyan
	a.mem.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.swap = widgets.NewGauge()
	a.swap.Title = " Swap "
	a.swap.BarColor = ui.ColorYellow
	a.swap.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.spark = newAxisChart()
	a.spark.Title = " Trends · CPU % (green) / MEM % (cyan) "
	a.spark.TitleBottom = " Y axis: percent (0–100%) "
	a.spark.YFormat = percentAxisLabel
	a.spark.MaxVal = 100
	a.spark.EmptyMsg = " waiting for samples…"
	a.spark.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.detail = widgets.NewParagraph()
	a.detail.Title = " Host Detail "
	a.detail.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
	a.detail.WrapText = false

	a.disk = widgets.NewList()
	a.disk.Title = " Filesystems "
	a.disk.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.net = widgets.NewParagraph()
	a.net.Title = " Network & System "
	a.net.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.netPlot = newAxisChart()
	a.netPlot.Title = " Network rate "
	a.netPlot.TitleBottom = " RX green · TX yellow · Y axis: bytes per second "
	a.netPlot.YFormat = humanRate
	a.netPlot.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
	a.netPlot.EmptyMsg = " waiting for the next sample…"

	a.eventLog = widgets.NewList()
	a.eventLog.Title = " Recent Events (· info  ! warn  × error) "
	a.eventLog.BorderStyle = ui.NewStyle(ui.ColorLightGrey)

	a.agentView = widgets.NewParagraph()
	a.agentView.Title = " Agent response · Ctrl+A transcript "
	a.agentView.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
	a.agentView.WrapText = true

	a.prompt = widgets.NewInput()
	a.prompt.Title = " Agent "
	a.prompt.TextStyle = ui.NewStyle(ui.ColorWhite)
	a.prompt.CursorStyle = ui.NewStyle(ui.ColorBlack, ui.ColorCyan)
	a.prompt.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
	a.prompt.Placeholder = "Describe a VPS setup task…   ( : to focus · Ctrl+A transcript )"

	a.modal = widgets.NewParagraph()
	a.modal.BorderStyle = ui.NewStyle(ui.ColorCyan)
	a.modal.TitleStyle = ui.NewStyle(ui.ColorCyan, ui.ColorClear, ui.ModifierBold)

	a.selected = 0
	return a
}

func (a *App) quitApp() {
	if a.agentCancel != nil {
		a.agentCancel()
		a.agentCancel = nil
	}
	a.quit = true
}

func (a *App) cancelAgent() {
	if a.agentCancel == nil {
		return
	}
	a.agentCancel()
	a.agentCancel = nil
	a.agentBusy = false
	if a.pendingApproval != nil {
		select {
		case a.pendingApproval.res <- false:
		default:
		}
		a.pendingApproval = nil
	}
	if a.mode == modeConfirm {
		a.confirm = nil
		a.setMode(modeOverview)
	}
	a.agentLine("× cancelled by operator")
	a.setStatus("agent cancelled")
}

func (a *App) setStatus(format string, args ...any) {
	a.status = fmt.Sprintf(format, args...)
	a.statusAt = time.Now()
}

func (a *App) setMode(m mode) {
	if a.mode != m {
		a.previous = a.mode
		a.mode = m
		a.dirtyClear = true
	}
}

func (a *App) handleEvent(e ui.Event) {
	if e.ID == "<Resize>" {
		a.layout()
		a.dirtyClear = true
		return
	}
	if e.ID == "<C-c>" {
		a.quitApp()
		return
	}
	if e.ID == "<C-x>" && a.agentBusy {
		a.cancelAgent()
		return
	}
	switch a.mode {
	case modeForm:
		a.handleFormKey(e.ID)
	case modeConfirm:
		a.handleConfirmKey(e.ID)
	case modeFilter:
		a.handleFilterKey(e.ID)
	case modeHelp, modeKeyInfo:
		a.setMode(modeOverview)
	case modePassword:
		a.handlePasswordKey(e.ID)
	case modeAgentInput:
		a.handleAgentInputKey(e.ID)
	case modeAgentLog:
		a.handleAgentLogKey(e.ID)
	case modeDetail:
		a.handleDetailKey(e.ID)
	default:
		a.handleOverviewKey(e.ID)
	}
}

func (a *App) handleOverviewKey(id string) {
	switch id {
	case "q":
		a.quitApp()
	case "?":
		a.showHelp()
	case "<Up>", "k":
		a.moveSelection(-1)
	case "<Down>", "j":
		a.moveSelection(1)
	case "<Home>", "g":
		a.selected = 0
	case "<End>", "G":
		a.selected = len(a.visible) - 1
	case "<Enter>":
		if a.currentHost() != nil {
			a.setMode(modeDetail)
		}
	case "a":
		a.form = newForm(a.cfg, a.secrets, nil)
		a.setMode(modeForm)
	case "e":
		if srv := a.currentServer(); srv != nil {
			a.form = newForm(a.cfg, a.secrets, srv)
			a.setMode(modeForm)
		}
	case "t":
		if srv := a.currentServer(); srv != nil {
			a.testServer(*srv, "", false)
		}
	case "r":
		if srv := a.currentServer(); srv != nil {
			a.sched.Reconnect(srv.ID)
			a.store.SetPending(srv.ID)
			a.setStatus("reconnecting %s…", srv.DisplayName())
		}
	case "d":
		a.toggleEnabled()
	case "x":
		a.confirmRemove()
	case "/":
		a.mode = modeFilter
		a.dirtyClear = true
	case ":":
		a.focusAgent()
	case "<C-a>":
		a.openAgentLog()
	case "K":
		a.showKeyInfo()
	}
}

func (a *App) handleDetailKey(id string) {
	switch id {
	case "q":
		a.quitApp()
	case "<Escape>", "<Enter>", "h":
		a.setMode(modeOverview)
	case "?":
		a.showHelp()
	case "e":
		if srv := a.currentServer(); srv != nil {
			a.form = newForm(a.cfg, a.secrets, srv)
			a.setMode(modeForm)
		}
	case "t":
		if srv := a.currentServer(); srv != nil {
			a.testServer(*srv, "", false)
		}
	case "r":
		if srv := a.currentServer(); srv != nil {
			a.sched.Reconnect(srv.ID)
			a.setStatus("reconnecting %s…", srv.DisplayName())
		}
	case ":":
		a.focusAgent()
	case "<C-a>":
		a.openAgentLog()
	}
}

func (a *App) handleFilterKey(id string) {
	switch id {
	case "<Escape>":
		a.filter = ""
		a.setMode(modeOverview)
	case "<Enter>":
		a.setMode(modeOverview)
	case "<Backspace>":
		if r := []rune(a.filter); len(r) > 0 {
			a.filter = string(r[:len(r)-1])
		}
	case "<C-u>":
		a.filter = ""
	default:
		if len([]rune(id)) == 1 {
			a.filter += id
		}
	}
}

// aiEnabled reports whether the agent UI (input and response panels) is on.
func (a *App) aiEnabled() bool {
	return a.cfg != nil && a.cfg.AI.Enabled
}

func (a *App) focusAgent() {
	if !a.aiEnabled() {
		a.showAgentDisabled()
		return
	}
	if a.mode != modeAgentInput {
		a.agentReturn = a.mode
	}
	a.setMode(modeAgentInput)
}

// showAgentDisabled explains how to turn the agent back on when ':' is pressed.
func (a *App) showAgentDisabled() {
	a.overlayTitle = " AI agent is off "
	a.overlay = "\n  The AI agent is disabled in config.yaml.\n\n  Enable it with:\n    bare-eye config ai on\n\n  then restart bare-eye. The agent input and response panels will\n  appear once it is enabled.\n"
	a.setMode(modeHelp)
}

func (a *App) handleAgentInputKey(id string) {
	switch id {
	case "<Escape>":
		a.setMode(a.agentReturn)
	case "<Enter>":
		text := a.prompt.Text
		if strings.TrimSpace(text) == "" {
			return
		}
		if a.agentBusy {
			a.setStatus("agent is already running; wait for it to finish")
			return
		}
		a.prompt.Text = ""
		a.prompt.Cursor = 0
		a.setMode(a.agentReturn)
		a.submitAgent(text)
	case "<Backspace>":
		a.prompt.Backspace()
	case "<Left>":
		a.prompt.MoveCursorLeft()
	case "<Right>":
		a.prompt.MoveCursorRight()
	case "<Home>":
		a.prompt.Cursor = 0
	case "<End>":
		a.prompt.Cursor = len([]rune(a.prompt.Text))
	case "<C-u>":
		a.prompt.Text = ""
		a.prompt.Cursor = 0
	case "<C-a>":
		a.openAgentLog()
	default:
		if r := []rune(id); len(r) == 1 {
			a.prompt.InsertRune(r[0])
		}
	}
}

func (a *App) openAgentLog() {
	if !a.aiEnabled() {
		a.showAgentDisabled()
		return
	}
	if a.mode != modeAgentLog {
		a.agentReturn = a.mode
	}
	a.agentScroll = 0
	a.setMode(modeAgentLog)
}

func (a *App) handleAgentLogKey(id string) {
	switch id {
	case "<Escape>", "q":
		a.setMode(a.agentReturn)
	case "<Up>", "k":
		a.agentScroll++
	case "<Down>", "j":
		a.agentScroll--
	case "<PgUp>":
		a.agentScroll += 10
	case "<PgDown>":
		a.agentScroll -= 10
	case "g":
		a.agentScroll = len(a.transcript)
	case "G":
		a.agentScroll = 0
	}
	if a.agentScroll < 0 {
		a.agentScroll = 0
	}
	if a.agentScroll > len(a.transcript) {
		a.agentScroll = len(a.transcript)
	}
}

func (a *App) submitAgent(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if a.agentBusy {
		a.setStatus("agent is already running; wait for it to finish")
		return
	}
	if err := a.agentRunner.Ready(); err != nil {
		a.overlay = agent.SetupHint(a.cfg.AI.BaseURL, a.cfg.AI.APIKeyEnv, a.cfg.AI.Model)
		a.overlayTitle = " Agent setup "
		a.setMode(modeHelp)
		return
	}
	hint := ""
	if srv := a.currentServer(); srv != nil {
		hint = fmt.Sprintf("selected host = %s (%s)", srv.ID, srv.Addr())
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.agentBusy = true
	a.agentCancel = cancel
	a.agentScroll = 0
	a.lastReply = ""
	a.agentLine("you: %s", text)
	a.events.Add(state.LevelInfo, "agent", "task: "+truncate(text, 80))
	a.setStatus("agent: thinking…")
	runner := a.snapshotRunner()
	go func() {
		defer cancel()
		err := runner.Run(ctx, text, hint, agent.Callbacks{
			Emit: func(ev agent.Event) {
				select {
				case a.agentEvents <- ev:
				case <-ctx.Done():
				}
			},
			Approve: func(ctx context.Context, req agent.Approval) bool {
				res := make(chan bool, 1)
				select {
				case a.agentApproval <- agentApproval{req: req, res: res}:
				case <-ctx.Done():
					return false
				}
				select {
				case ok := <-res:
					return ok
				case <-ctx.Done():
					return false
				}
			},
		})
		ev := agent.Event{Kind: agent.EventDone}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				ev.Kind = agent.EventInfo
				ev.Text = "task cancelled"
			} else {
				ev.Kind = agent.EventError
				ev.Text = err.Error()
			}
		}
		select {
		case a.agentEvents <- ev:
		case <-ctx.Done():
		}
	}()
}

func (a *App) snapshotRunner() *agent.Runner {
	cfg := *a.cfg
	cfg.Servers = append([]config.ServerConfig(nil), a.cfg.Servers...)
	var secrets *config.Secrets
	if a.secrets != nil {
		secrets = &config.Secrets{Passwords: make(map[string]string, len(a.secrets.Passwords))}
		for k, v := range a.secrets.Passwords {
			secrets.Passwords[k] = v
		}
	}
	return agent.New(&cfg, secrets, a.store)
}

func (a *App) showAgentApproval(req agentApproval) {
	a.pendingApproval = &req
	a.setStatus("agent: waiting for approval on %s", req.req.Host)
	plan := strings.TrimSpace(a.lastReply)
	prefix := ""
	if plan != "" {
		prefix = "Agent: " + truncate(strings.ReplaceAll(plan, "\n", " "), 300) + "\n\n"
	}
	a.confirm = &confirmState{
		title: " Agent · approve command ",
		message: prefix + fmt.Sprintf("Host: %s\n\nPurpose: %s\n\nCommand:\n  %s\n\nRun this command on the server?",
			req.req.Host, dash(req.req.Purpose), req.req.Command),
		onYes: func() { req.res <- true },
		onNo:  func() { req.res <- false },
	}
	a.setMode(modeConfirm)
}

func (a *App) handleAgentEvent(ev agent.Event) {
	switch ev.Kind {
	case agent.EventAssistant:
		a.lastReply = ev.Text
		a.agentBlock("assistant:", ev.Text)
		a.events.Add(state.LevelInfo, "agent", truncate(firstLine(ev.Text), 90))
		a.setStatus("agent: %s", truncate(firstLine(ev.Text), 70))
	case agent.EventTool:
		a.agentLine("→ %s  $ %s", ev.Host, ev.Command)
		if strings.TrimSpace(ev.Text) != "" {
			a.agentLine("   %s", ev.Text)
		}
		a.events.Add(state.LevelInfo, "agent", truncate(ev.Host+" $ "+ev.Command, 90))
		a.setStatus("agent: running %s…", ev.Host)
	case agent.EventResult:
		a.agentBlock("   ←", ev.Text)
		a.events.Add(state.LevelInfo, "agent", truncate(ev.Host+" result: "+firstLine(ev.Text), 90))
	case agent.EventInfo:
		a.agentLine("· %s", ev.Text)
	case agent.EventError:
		if strings.TrimSpace(a.lastReply) == "" {
			a.lastReply = "Error: " + ev.Text
		}
		a.agentLine("× %s", ev.Text)
		a.events.Add(state.LevelError, "agent", truncate(ev.Text, 90))
		a.setStatus("agent error: %s", truncate(ev.Text, 80))
		a.agentBusy = false
		a.agentCancel = nil
		a.pendingApproval = nil
	case agent.EventDone:
		a.agentBusy = false
		a.agentCancel = nil
		a.pendingApproval = nil
		a.setStatus("agent: ready")
	}
}

func (a *App) agentLine(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	a.transcript = append(a.transcript, line)
	if len(a.transcript) > 800 {
		a.transcript = a.transcript[len(a.transcript)-800:]
	}
}

func (a *App) agentBlock(prefix, text string) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	for i, line := range strings.Split(text, "\n") {
		if i == 0 {
			a.agentLine("%s %s", prefix, line)
		} else {
			a.agentLine("    %s", line)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (a *App) handleConfirmKey(id string) {
	if a.confirm == nil {
		a.setMode(modeOverview)
		return
	}
	a.pendingApproval = nil
	switch strings.ToLower(id) {
	case "y":
		fn := a.confirm.onYes
		a.confirm = nil
		a.setMode(modeOverview)
		if fn != nil {
			fn()
		}
	case "n", "<Escape>", "<Enter>":
		fn := a.confirm.onNo
		a.confirm = nil
		a.setMode(modeOverview)
		if fn != nil {
			fn()
		}
	}
}

func (a *App) handlePasswordKey(id string) {
	if a.password == nil {
		a.setMode(modeOverview)
		return
	}
	switch id {
	case "<Escape>":
		a.password = nil
		a.setMode(modeOverview)
	case "<Enter>":
		p := a.password
		a.password = nil
		a.setMode(modeOverview)
		if p.after != nil {
			p.after(p.value)
		}
	case "<Backspace>":
		if r := []rune(a.password.value); len(r) > 0 {
			a.password.value = string(r[:len(r)-1])
		}
	default:
		if len([]rune(id)) == 1 {
			a.password.value += id
		}
	}
}

func (a *App) moveSelection(delta int) {
	if len(a.visible) == 0 {
		return
	}
	a.selected += delta
	if a.selected < 0 {
		a.selected = 0
	}
	if a.selected >= len(a.visible) {
		a.selected = len(a.visible) - 1
	}
}

func (a *App) currentHost() *state.HostState {
	if a.selected < 0 || a.selected >= len(a.visible) {
		return nil
	}
	h := a.visible[a.selected]
	return &h
}

func (a *App) currentServer() *config.ServerConfig {
	h := a.currentHost()
	if h == nil {
		return nil
	}
	srv, err := a.cfg.FindServer(h.ID)
	if err != nil {
		return nil
	}
	return srv
}

func (a *App) toggleEnabled() {
	srv := a.currentServer()
	if srv == nil {
		return
	}
	srv.Enabled = !srv.Enabled
	if err := a.cfg.Save(); err != nil {
		a.setStatus("save failed: %v", err)
		return
	}
	a.sched.Restart(a.cfg)
	if srv.Enabled {
		a.setStatus("%s enabled", srv.DisplayName())
	} else {
		a.setStatus("%s disabled", srv.DisplayName())
	}
}

func (a *App) confirmRemove() {
	srv := a.currentServer()
	if srv == nil {
		return
	}
	a.confirm = &confirmState{
		title:   " Remove server ",
		message: fmt.Sprintf("Remove %s (%s) from Bare Eye?\n\nThis only deletes the local entry; it does not change the server.\nStored password (if any) will also be deleted.", srv.DisplayName(), srv.Addr()),
		onYes: func() {
			a.cfg.RemoveServer(srv.ID)
			if a.secrets != nil {
				a.secrets.Delete(srv.ID)
				_ = a.secrets.Save()
			}
			if err := a.cfg.Save(); err != nil {
				a.setStatus("save failed: %v", err)
				return
			}
			a.sched.Restart(a.cfg)
			a.setStatus("removed %s", srv.DisplayName())
		},
	}
	a.setMode(modeConfirm)
}

func (a *App) showHelp() {
	a.overlayTitle = " Keyboard help "
	lines := []string{
		"",
		"  Navigation",
		"    ↑/↓ or j/k      select server",
		"    Enter           host detail",
		"    Esc             back / cancel",
		"    /               filter inventory",
	}
	if a.aiEnabled() {
		lines = append(lines,
			"    :               agent: describe a VPS task",
			"    Ctrl+A          agent transcript (↑/↓ scroll)",
			"    Ctrl+X          cancel a running agent task",
		)
	}
	lines = append(lines,
		"    ?               this help",
		"    q, Ctrl+C       quit",
		"",
		"  Host actions",
		"    a               add server",
		"    e               edit selected server",
		"    t               test SSH connection",
		"    r               reconnect now",
		"    d               enable / disable monitoring",
		"    x               remove server (local only)",
		"    K               show Bare Eye public key",
		"",
		"  Legend",
		"    green = online   yellow = pending / STALE / unsupported",
		"    red = offline / auth / host-key   grey = disabled",
		"    Values shown while a host is down are labelled \"last sample\".",
		"",
		"  Data is collected over SSH with read-only commands. Stale data is never",
		"  shown as live; a changed host key blocks the connection until reviewed.",
	)
	a.overlay = strings.Join(lines, "\n")
	a.setMode(modeHelp)
}

func (a *App) showKeyInfo() {
	keyPath := a.cfg.SSH.DefaultKey
	pub, err := keys.PublicKey(keyPath)
	if err != nil {
		a.overlay = fmt.Sprintf("\n  No Bare Eye public key available.\n\n  Path: %s\n  Error: %v\n\n  Run: bare-eye keygen", keyPath, err)
	} else {
		fp, _ := keys.Fingerprint(keyPath)
		a.overlay = fmt.Sprintf("\n  Public key: %s\n\n  Fingerprint: %s\n\n  Add it to the server with:\n    bare-eye key install <host-id>\n\n  or append it manually to ~/.ssh/authorized_keys on the target.", pub, fp)
	}
	a.setMode(modeKeyInfo)
}

func (a *App) testServer(srv config.ServerConfig, formPassword string, fromForm bool) {
	name := srv.DisplayName()
	a.setStatus("testing %s…", name)
	if fromForm && a.form != nil {
		a.form.status = "testing…"
	}
	cfg := a.cfg
	secrets := a.secrets
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := scheduler.TestConnection(ctx, srv, cfg, secrets, formPassword, "")
		a.async <- asyncResult{kind: "test", id: srv.ID, err: err, srv: srv, password: formPassword, fromForm: fromForm}
	}()
}

func (a *App) handleAsync(r asyncResult) {
	if r.kind != "test" {
		return
	}
	srv := r.srv
	setFormStatus := func(msg string) {
		if r.fromForm && a.form != nil {
			a.form.status = msg
		}
	}
	if r.err == nil {
		a.setStatus("connection OK: %s", srv.DisplayName())
		setFormStatus("connection OK")
		return
	}
	var unknown *sshx.UnknownHostKeyError
	if errors.As(r.err, &unknown) {
		host := unknown.Host
		fp := unknown.Fingerprint
		a.confirm = &confirmState{
			title:   " Unknown host key ",
			message: fmt.Sprintf("The host %s is not in Bare Eye's known_hosts.\n\nFingerprint (SHA256): %s\n\nTrust this key and save it?", host, fp),
			onYes: func() {
				if err := sshx.AppendKnownHost(a.cfg.SSH.KnownHosts, host, unknown.Key); err != nil {
					a.setStatus("could not save host key: %v", err)
					return
				}
				if r.fromForm && a.form != nil {
					a.setMode(modeForm)
				}
				a.testServer(r.srv, r.password, r.fromForm)
			},
		}
		a.setMode(modeConfirm)
		return
	}
	var changed *sshx.ChangedHostKeyError
	if errors.As(r.err, &changed) {
		a.overlay = fmt.Sprintf("\n  ⚠ HOST KEY CHANGED for %s\n\n  Presented: %s\n  Previously stored: %s\n\n  Bare Eye will NOT connect or replace the stored key automatically.\n  Investigate the server (possible MITM or rebuild), then remove the old\n  entry from:\n    %s\n  and re-verify the new fingerprint.", changed.Host, changed.Fingerprint, strings.Join(changed.Previous, ", "), a.cfg.SSH.KnownHosts)
		a.setMode(modeKeyInfo)
		return
	}
	var pass *sshx.PassphraseRequiredError
	if errors.As(r.err, &pass) {
		a.setStatus("key is passphrase-protected; run `bare-eye hosts test %s` in a shell", r.id)
		setFormStatus("key is passphrase-protected; save and test from the CLI")
		return
	}
	if errors.Is(r.err, sshx.ErrPasswordRequired) {
		a.promptPassword(r.srv, func(pw string) {
			if r.fromForm && a.form != nil {
				a.form.password = pw
				a.setMode(modeForm)
				a.testServer(r.srv, pw, true)
				return
			}
			if a.secrets != nil {
				a.secrets.Set(r.srv.ID, pw)
				if err := a.secrets.Save(); err != nil {
					a.setStatus("could not save password: %v", err)
					return
				}
			}
			a.testServer(r.srv, pw, false)
		})
		return
	}
	a.setStatus("test failed: %v", r.err)
	setFormStatus("test failed: " + r.err.Error())
}

func (a *App) promptPassword(srv config.ServerConfig, after func(string)) {
	a.password = &passwordState{hostID: srv.ID, srv: srv, after: after}
	a.setMode(modePassword)
}

// --- form handling ---

func (a *App) handleFormKey(id string) {
	if a.form == nil {
		a.setMode(modeOverview)
		return
	}
	switch id {
	case "<Escape>":
		a.form = nil
		a.setMode(modeOverview)
	case "<Tab>", "<Down>":
		a.form.next(1)
	case "<Backtab>", "<Up>":
		a.form.next(-1)
	case "<Left>":
		a.form.adjust(-1)
	case "<Right>":
		a.form.adjust(1)
	case "<Backspace>":
		a.form.backspace()
	case "<Enter>":
		a.saveForm()
	case "<C-t>":
		a.testForm()
	default:
		if len([]rune(id)) == 1 {
			if id == " " {
				fl := &a.form.fields[a.form.focus]
				if fl.kind == fieldAuth || fl.kind == fieldBool {
					a.form.adjust(1)
					return
				}
			}
			a.form.insert(id)
		}
	}
}

func (a *App) testForm() {
	if a.form == nil {
		return
	}
	srv, err := a.form.build(a.cfg)
	if err != nil {
		a.form.status = "invalid: " + err.Error()
		return
	}
	a.form.status = "testing…"
	pw := a.form.password
	a.testServer(srv, pw, true)
}

func (a *App) saveForm() {
	if a.form == nil {
		return
	}
	srv, err := a.form.build(a.cfg)
	if err != nil {
		a.form.status = "invalid: " + err.Error()
		return
	}
	// Assign a unique ID for new servers.
	if a.form.isNew {
		base := srv.ID
		for i := 2; ; i++ {
			if _, err := a.cfg.FindServer(srv.ID); err != nil {
				break
			}
			srv.ID = fmt.Sprintf("%s-%d", base, i)
		}
	}
	if a.secrets != nil {
		if srv.Auth.Method == config.AuthPassword && a.form.password != "" {
			a.secrets.Set(srv.ID, a.form.password)
			if err := a.secrets.Save(); err != nil {
				a.form.status = "could not save password: " + err.Error()
				return
			}
		} else if srv.Auth.Method != config.AuthPassword {
			a.secrets.Delete(srv.ID)
			_ = a.secrets.Save()
		}
	}
	a.cfg.UpsertServer(srv)
	if err := a.cfg.Save(); err != nil {
		a.form.status = "save failed: " + err.Error()
		return
	}
	a.sched.Restart(a.cfg)
	a.form = nil
	a.setMode(modeOverview)
	a.setStatus("saved %s", srv.DisplayName())
}

func (a *App) refresh() {
	snap := a.store.Snapshot()
	filter := strings.ToLower(strings.TrimSpace(a.filter))
	a.visible = a.visible[:0]
	for _, h := range snap {
		if filter == "" || strings.Contains(strings.ToLower(h.ID), filter) {
			a.visible = append(a.visible, h)
			continue
		}
		srv, err := a.cfg.FindServer(h.ID)
		if err != nil {
			continue
		}
		match := strings.Contains(strings.ToLower(srv.DisplayName()), filter) ||
			strings.Contains(strings.ToLower(srv.Host), filter)
		for _, tag := range srv.Tags {
			if strings.Contains(strings.ToLower(tag), filter) {
				match = true
				break
			}
		}
		if match {
			a.visible = append(a.visible, h)
		}
	}
	if a.selected >= len(a.visible) {
		a.selected = len(a.visible) - 1
	}
	if a.selected < 0 {
		a.selected = 0
	}
	if !a.statusAt.IsZero() && time.Since(a.statusAt) > 10*time.Second {
		a.status = ""
	}
}
