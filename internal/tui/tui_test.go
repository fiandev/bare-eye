package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/fiandev/bare-eye/internal/agent"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/state"
)

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0:                      "0 B",
		512:                    "512 B",
		1024:                   "1.0 KiB",
		1536:                   "1.5 KiB",
		1024 * 1024:            "1.0 MiB",
		3 * 1024 * 1024 * 1024: "3.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanUptime(t *testing.T) {
	cases := map[time.Duration]string{
		0:                            "—",
		30 * time.Minute:             "30m",
		3*time.Hour + 20*time.Minute: "3h 20m",
		50 * time.Hour:               "2d 2h 0m",
	}
	for in, want := range cases {
		if got := humanUptime(in); got != want {
			t.Errorf("humanUptime(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := truncate("hello world", 6); got != "hello…" {
		t.Errorf("got %q", got)
	}
	if got := truncate("hello", 1); got != "h" {
		t.Errorf("got %q", got)
	}
	if got := truncate("hello", 0); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestStatusLabelsAndColors(t *testing.T) {
	if statusLabel(state.StatusOnline) != "online" {
		t.Error("online label")
	}
	if statusLabel(state.StatusStale) != "STALE" {
		t.Error("stale label")
	}
	if statusColor(state.StatusOnline) == statusColor(state.StatusOffline) {
		t.Error("online and offline should differ in color")
	}
	if thresholdColor(90, 0, 1) != 1 {
		t.Error("zero threshold must be disabled")
	}
	if thresholdColor(90, 80, 1) == 1 {
		t.Error("threshold should change the color")
	}
}

func TestFormBuild(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	f := newForm(cfg, nil, nil)
	f.field("name").value = "My Server"
	f.field("host").value = "192.0.2.5"
	f.field("user").value = "monitor"
	f.field("tags").value = "prod, web"
	srv, err := f.build(cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if srv.ID != "my-server" {
		t.Errorf("id = %q", srv.ID)
	}
	if srv.Port != 22 || srv.Auth.Method != config.AuthKey || !srv.Enabled {
		t.Errorf("srv = %+v", srv)
	}
	if len(srv.Tags) != 2 {
		t.Errorf("tags = %v", srv.Tags)
	}
	if srv.Auth.KeyPath != cfg.SSH.DefaultKey {
		t.Errorf("key path = %q", srv.Auth.KeyPath)
	}

	f.field("port").value = "notaport"
	if _, err := f.build(cfg); err == nil {
		t.Error("invalid port should fail")
	}
}

func TestFormPasswordEditing(t *testing.T) {
	cfg, _ := config.Default()
	f := newForm(cfg, nil, nil)
	for i := range f.fields {
		if f.fields[i].key == "password" {
			f.focus = i
		}
	}
	f.insert("s")
	f.insert("e")
	f.insert("c")
	f.backspace()
	if f.password != "se" {
		t.Errorf("password = %q", f.password)
	}
	if f.fields[f.focus].value != "" {
		t.Error("password must not be stored in the visible field value")
	}
}

func TestFormAuthCycle(t *testing.T) {
	cfg, _ := config.Default()
	f := newForm(cfg, nil, nil)
	for i := range f.fields {
		if f.fields[i].key == "auth" {
			f.focus = i
		}
	}
	f.adjust(1)
	if got := f.field("auth").value; got != config.AuthAgent {
		t.Errorf("auth = %q, want agent", got)
	}
	f.adjust(1)
	if got := f.field("auth").value; got != config.AuthPassword {
		t.Errorf("auth = %q, want password", got)
	}
}

func TestNetSeriesAndPeak(t *testing.T) {
	hist := []state.Sample{
		{NetRX: 100, NetTX: 10},
		{NetRX: 400, NetTX: 50},
	}
	rx, tx := netSeries(hist)
	if len(rx) != 2 || rx[1] != 400 || tx[1] != 50 {
		t.Errorf("series = %v / %v", rx, tx)
	}
	if peak := seriesPeak(rx, tx); peak != 400 {
		t.Errorf("peak = %v, want 400", peak)
	}
}

func TestHistorySeriesSkipsInvalidCPU(t *testing.T) {
	hist := []state.Sample{
		{CPUValid: false, Mem: 10},
		{CPUValid: true, CPU: 20, Mem: 30},
	}
	cpu, mem := historySeries(hist)
	if len(cpu) != 1 || cpu[0] != 20 {
		t.Errorf("cpu = %v", cpu)
	}
	if len(mem) != 2 || mem[1] != 30 {
		t.Errorf("mem = %v", mem)
	}
	if !strings.Contains(statusLabel(state.StatusHostKey), "host") {
		t.Error("host-key label")
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	return newApp(cfg, nil, state.New(10*time.Second), state.NewEventLog(20), nil, "test")
}

func TestAgentInputEditing(t *testing.T) {
	a := newTestApp(t)
	a.cfg.AI.Enabled = true
	a.focusAgent()
	if a.mode != modeAgentInput {
		t.Fatalf("mode = %v", a.mode)
	}
	for _, r := range []string{"s", "e", "t", "u", "p"} {
		a.handleAgentInputKey(r)
	}
	a.handleAgentInputKey("<Backspace>")
	if a.prompt.Text != "setu" {
		t.Fatalf("text = %q", a.prompt.Text)
	}
	a.handleAgentInputKey("<Left>")
	a.handleAgentInputKey("X")
	if a.prompt.Text != "setXu" || a.prompt.Cursor != 4 {
		t.Fatalf("text = %q cursor = %d", a.prompt.Text, a.prompt.Cursor)
	}
	a.handleAgentInputKey("<C-u>")
	if a.prompt.Text != "" || a.prompt.Cursor != 0 {
		t.Fatalf("text = %q cursor = %d", a.prompt.Text, a.prompt.Cursor)
	}
	a.handleAgentInputKey("<Escape>")
	if a.mode != modeOverview {
		t.Fatalf("mode = %v", a.mode)
	}
}

func TestAgentSubmitShowsSetupWhenUnconfigured(t *testing.T) {
	a := newTestApp(t)
	a.cfg.AI.Enabled = true
	a.focusAgent()
	a.handleAgentInputKey("x")
	a.handleAgentInputKey("<Enter>")
	if a.mode != modeHelp {
		t.Fatalf("mode = %v, want help", a.mode)
	}
	if !strings.Contains(a.overlay, "ai:") || !strings.Contains(a.overlay, "BARE_EYE_API_KEY") {
		t.Fatalf("overlay = %q", a.overlay)
	}
	if a.agentBusy {
		t.Fatal("agent must not run without configuration")
	}
}

func TestAgentDisabledHidesInput(t *testing.T) {
	a := newTestApp(t)
	a.cfg.AI.Enabled = false
	a.focusAgent()
	if a.mode != modeHelp {
		t.Fatalf("mode = %v, want help", a.mode)
	}
	if !strings.Contains(a.overlay, "config ai on") {
		t.Fatalf("overlay = %q", a.overlay)
	}
	a.setMode(modeOverview)
	a.showHelp()
	if strings.Contains(a.overlay, "agent transcript") {
		t.Fatalf("help should not list agent keys when disabled:\n%s", a.overlay)
	}
	a.cfg.AI.Enabled = true
	a.showHelp()
	if !strings.Contains(a.overlay, "agent transcript") {
		t.Fatalf("help should list agent keys when enabled:\n%s", a.overlay)
	}
}

func TestAgentApprovalModalAnswers(t *testing.T) {
	a := newTestApp(t)
	res := make(chan bool, 1)
	a.showAgentApproval(agentApproval{
		req: agent.Approval{Host: "web-01", Command: "apt-get install -y nginx", Purpose: "install nginx"},
		res: res,
	})
	if a.mode != modeConfirm || a.confirm == nil {
		t.Fatalf("mode = %v", a.mode)
	}
	a.handleConfirmKey("y")
	select {
	case ok := <-res:
		if !ok {
			t.Fatal("approval = false")
		}
	default:
		t.Fatal("approval was not delivered")
	}
}

func TestAgentTranscriptScroll(t *testing.T) {
	a := newTestApp(t)
	a.transcript = []string{"one", "two", "three"}
	a.handleAgentLogKey("<Up>")
	if a.agentScroll != 1 {
		t.Fatalf("scroll = %d", a.agentScroll)
	}
	a.handleAgentLogKey("<Down>")
	a.handleAgentLogKey("<Down>")
	if a.agentScroll != 0 {
		t.Fatalf("scroll = %d", a.agentScroll)
	}
	a.handleAgentLogKey("g")
	if a.agentScroll != 3 {
		t.Fatalf("scroll = %d", a.agentScroll)
	}
}

func TestAgentResponsePanelShowsLatestReply(t *testing.T) {
	a := newTestApp(t)
	a.handleAgentEvent(agent.Event{Kind: agent.EventAssistant, Text: "Saya akan memasang nginx."})
	if a.lastReply != "Saya akan memasang nginx." {
		t.Fatalf("lastReply = %q", a.lastReply)
	}
	a.updateAgentView()
	if !strings.Contains(a.agentView.Text, "nginx") {
		t.Fatalf("panel text = %q", a.agentView.Text)
	}
	a.handleAgentEvent(agent.Event{Kind: agent.EventAssistant, Text: "Selesai. Nginx aktif."})
	a.updateAgentView()
	if !strings.Contains(a.agentView.Text, "Selesai") {
		t.Fatalf("panel text = %q", a.agentView.Text)
	}
}

func TestAgentApprovalIncludesLastReply(t *testing.T) {
	a := newTestApp(t)
	a.lastReply = "Saya akan memasang nginx."
	res := make(chan bool, 1)
	a.showAgentApproval(agentApproval{
		req: agent.Approval{Host: "web-01", Command: "apt-get install -y nginx", Purpose: "install nginx"},
		res: res,
	})
	if a.confirm == nil || !strings.Contains(a.confirm.message, "Saya akan memasang nginx") {
		t.Fatalf("message = %q", a.confirm.message)
	}
}
