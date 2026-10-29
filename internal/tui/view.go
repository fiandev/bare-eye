package tui

import (
	"fmt"
	"strings"

	ui "github.com/metaspartan/gotui/v5"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/state"
)

func (a *App) layout() {
	w, h := ui.TerminalDimensions()
	if w <= 0 || h <= 0 {
		return
	}
	a.width, a.height = w, h

	headerH := 3
	if h < 10 {
		headerH = 1
	}
	a.header.SetRect(0, 0, w, headerH)

	eventsH := 6
	if h < 30 {
		eventsH = 4
	}
	if h < 20 {
		eventsH = 0
	}
	// The agent input and response panels only exist when the agent is enabled.
	inputH, respH := 0, 0
	if a.aiEnabled() {
		inputH = 3
		if h < 14 {
			inputH = 0
		}
		respH = 6
		if h < 34 {
			respH = 5
		}
		if h < 26 {
			respH = 4
		}
		if h < 22 {
			respH = 0
		}
	}
	bodyTop := headerH
	avail := h - inputH - respH - eventsH
	if avail < bodyTop+6 {
		eventsH = 0
		avail = h - inputH - respH
	}
	if avail < bodyTop+6 {
		respH = 0
		avail = h - inputH
	}
	if avail < bodyTop+6 {
		inputH = 0
		avail = h
	}
	bottom := h
	if inputH > 0 {
		a.prompt.SetRect(0, h-inputH, w, h)
		bottom = h - inputH
	} else {
		a.prompt.SetRect(0, 0, 0, 0)
	}
	if respH > 0 {
		a.agentView.SetRect(0, bottom-respH, w, bottom)
		bottom -= respH
	} else {
		a.agentView.SetRect(0, 0, 0, 0)
	}
	if eventsH > 0 {
		a.eventLog.SetRect(0, bottom-eventsH, w, bottom)
	} else {
		a.eventLog.SetRect(0, 0, 0, 0)
	}
	bodyBottom := avail

	if w >= 110 {
		leftW := int(float64(w) * 0.56)
		if leftW < 60 {
			leftW = 60
		}
		if leftW > w-38 {
			leftW = w - 38
		}
		a.table.SetRect(0, bodyTop, leftW, bodyBottom)
		rx := leftW
		rightH := bodyBottom - bodyTop
		gaugeH := 3
		if rightH < 14 {
			gaugeH = 2
		}
		y := bodyTop
		a.cpu.SetRect(rx, y, w, y+gaugeH)
		y += gaugeH
		a.mem.SetRect(rx, y, w, y+gaugeH)
		y += gaugeH
		a.swap.SetRect(rx, y, w, y+gaugeH)
		y += gaugeH
		if bodyBottom-y >= 3 {
			a.spark.SetRect(rx, y, w, bodyBottom)
		} else {
			a.spark.SetRect(rx, y, w, y)
		}
	} else {
		half := bodyTop + (bodyBottom-bodyTop)/2
		if half < bodyTop+4 {
			half = bodyTop + 4
		}
		a.table.SetRect(0, bodyTop, w, half)
		y := half
		gaugeH := 3
		if bodyBottom-y < 9 {
			gaugeH = 2
		}
		a.cpu.SetRect(0, y, w, y+gaugeH)
		y += gaugeH
		if y+gaugeH <= bodyBottom {
			a.mem.SetRect(0, y, w, y+gaugeH)
			y += gaugeH
		} else {
			a.mem.SetRect(0, y, w, y)
		}
		if y+gaugeH <= bodyBottom {
			a.swap.SetRect(0, y, w, y+gaugeH)
			y += gaugeH
		} else {
			a.swap.SetRect(0, y, w, y)
		}
		a.spark.SetRect(0, y, w, y)
	}

	// Detail layout.
	leftW := int(float64(w) * 0.52)
	if leftW < 40 {
		leftW = 40
	}
	if leftW > w-30 {
		leftW = w - 30
	}
	half := bodyTop + (bodyBottom-bodyTop)/2
	a.detail.SetRect(0, bodyTop, leftW, half)
	a.disk.SetRect(0, half, leftW, bodyBottom)

	// Right column: network counters on top, rate chart underneath.
	rightH := bodyBottom - bodyTop
	netH := rightH
	switch {
	case rightH >= 18:
		netH = 9
	case rightH >= 12:
		netH = 7
	}
	netEnd := bodyTop + netH
	a.net.SetRect(leftW, bodyTop, w, netEnd)
	if bodyBottom-netEnd >= 5 {
		a.netPlot.SetRect(leftW, netEnd, w, bodyBottom)
	} else {
		a.netPlot.SetRect(leftW, netEnd, w, netEnd)
	}
}

func (a *App) render() {
	if a.dirtyClear {
		ui.Clear()
		a.dirtyClear = false
	}
	if a.width < 50 || a.height < 10 {
		a.modal.Title = " Bare Eye "
		a.modal.Text = fmt.Sprintf("\n  Terminal too small: %dx%d\n\n  Bare Eye needs at least 50x10.\n  Resize the terminal to continue.", a.width, a.height)
		a.modal.SetRect(0, 0, a.width, a.height)
		ui.Render(a.modal)
		return
	}
	switch a.mode {
	case modeForm:
		if a.form != nil {
			title := " Add server "
			if !a.form.isNew {
				title = " Edit " + a.form.originalID + " "
			}
			a.renderModal(title, a.form.render())
		}
	case modeHelp:
		title := a.overlayTitle
		if title == "" {
			title = " Keyboard help "
		}
		a.renderModal(title, a.overlay)
	case modeKeyInfo:
		a.renderModal(" Bare Eye key / security notice ", a.overlay)
	case modeConfirm:
		if a.confirm != nil {
			a.renderModal(a.confirm.title, "\n  "+strings.ReplaceAll(a.confirm.message, "\n", "\n  ")+"\n\n  [y] yes     [n] no")
		}
	case modePassword:
		if a.password != nil {
			text := fmt.Sprintf("\n  Password for %s@%s\n\n  [%s]\n\n  Enter to save · Esc to cancel", a.password.srv.User, a.password.srv.Addr(), strings.Repeat("•", len([]rune(a.password.value))))
			a.renderModal(" SSH password required ", text)
		}
	case modeAgentLog:
		a.renderAgentLog()
	case modeDetail:
		a.renderDetail()
	default:
		a.renderOverview()
	}
}

func (a *App) renderOverview() {
	hosts := a.visible
	online, issues := 0, 0
	for _, h := range hosts {
		switch h.Status {
		case state.StatusOnline:
			online++
		case state.StatusDisabled:
		default:
			issues++
		}
	}
	header := fmt.Sprintf(" Bare Eye %s   hosts %d · online %d · issues %d   interval %s ",
		a.version, len(hosts), online, issues, a.cfg.Monitoring.Interval.Duration())
	if a.filter != "" {
		header += fmt.Sprintf("  filter: %q", a.filter)
	}
	a.header.Text = truncate(header, a.width-2)
	hint := "? help · a add · e edit · t test · r reconnect · d toggle · x remove · / filter"
	if a.aiEnabled() {
		hint += " · : agent"
	}
	hint += " · q quit"
	if a.agentBusy {
		hint = "agent: working…   |   " + hint
	}
	if a.status != "" {
		hint = a.status + "   |   " + hint
	}
	a.header.TitleBottom = " " + truncate(hint, a.width-2)

	a.buildTable()
	a.buildDetailPanels()
	a.buildEvents()
	a.updatePrompt()
	a.updateAgentView()
	items := []ui.Drawable{a.header}
	for _, w := range []ui.Drawable{a.table, a.cpu, a.mem, a.swap} {
		if w.GetRect().Dy() > 0 && w.GetRect().Dx() > 0 {
			items = append(items, w)
		}
	}
	if a.spark.GetRect().Dy() > 0 {
		items = append(items, a.spark)
	}
	if a.eventLog.GetRect().Dy() > 0 {
		items = append(items, a.eventLog)
	}
	if a.agentView.GetRect().Dy() > 0 {
		items = append(items, a.agentView)
	}
	if a.prompt.GetRect().Dy() > 0 {
		items = append(items, a.prompt)
	}
	ui.Render(items...)
}

func (a *App) renderDetail() {
	a.header.Text = truncate(a.detailHeader(), a.width-2)
	hint := "Esc back · e edit · t test · r reconnect"
	if a.aiEnabled() {
		hint += " · : agent"
	}
	hint += " · q quit"
	if a.agentBusy {
		hint = "agent: working…   |   " + hint
	}
	a.header.TitleBottom = " " + truncate(hint, a.width-2)
	a.buildDetailPanels()
	a.buildEvents()
	a.updatePrompt()
	a.updateAgentView()
	items := []ui.Drawable{a.header}
	for _, w := range []ui.Drawable{a.detail, a.disk, a.net, a.netPlot} {
		if w.GetRect().Dy() > 0 && w.GetRect().Dx() > 0 {
			items = append(items, w)
		}
	}
	if a.eventLog.GetRect().Dy() > 0 {
		items = append(items, a.eventLog)
	}
	if a.agentView.GetRect().Dy() > 0 {
		items = append(items, a.agentView)
	}
	if a.prompt.GetRect().Dy() > 0 {
		items = append(items, a.prompt)
	}
	ui.Render(items...)
}

func (a *App) detailHeader() string {
	srv := a.currentServer()
	if srv == nil {
		return " Bare Eye "
	}
	return fmt.Sprintf(" Bare Eye %s   %s   %s ", a.version, srv.DisplayName(), srv.Addr())
}

func (a *App) buildTable() {
	inner := a.table.Inner.Dx()
	if inner <= 0 {
		inner = a.width - 2
	}
	statusW, cpuW, memW, diskW, loadW, updW := 9, 5, 5, 5, 6, 6
	fixed := statusW + cpuW + memW + diskW + loadW + updW + 7
	rest := inner - fixed
	if rest < 20 {
		rest = 20
	}
	nameW := rest * 2 / 5
	addrW := rest - nameW
	a.table.ColumnWidths = []int{nameW, addrW, statusW, cpuW, memW, diskW, loadW, updW}
	a.table.Rows = [][]string{{"NAME", "ADDRESS", "STATUS", "CPU", "MEM", "DISK", "LOAD", "UPD"}}
	styles := map[int]ui.Style{
		0: ui.NewStyle(ui.ColorSilver, ui.ColorClear, ui.ModifierBold),
	}
	for i, h := range a.visible {
		srv, err := a.cfg.FindServer(h.ID)
		name, addr := h.ID, ""
		if err == nil {
			name = srv.DisplayName()
			addr = srv.Addr()
		}
		cpu, mem, disk, load := "—", "—", "—", "—"
		if h.HasMetrics {
			if h.Metrics.CPUValid {
				cpu = pct(h.Metrics.CPUPercent)
			}
			mem = pct(h.Metrics.MemPercent())
			if maxFS, ok := maxDisk(h.Metrics.Filesystems); ok {
				disk = pct(maxFS)
			}
			if h.Metrics.LoadValid {
				load = fmt.Sprintf("%.2f", h.Metrics.Load1)
			}
		}
		row := []string{
			truncate(name, nameW-1),
			truncate(addr, addrW-1),
			statusLabel(h.Status),
			cpu, mem, disk, load,
			ago(h.LastSuccess),
		}
		a.table.Rows = append(a.table.Rows, row)
		styles[i+1] = ui.NewStyle(statusColor(h.Status))
	}
	a.table.RowStyles = styles
	a.table.SelectedRow = a.selected + 1
	if len(a.visible) == 0 {
		a.table.SelectedRow = 0
	}
}

func (a *App) buildDetailPanels() {
	h := a.currentHost()
	srv := a.currentServer()
	if h == nil || srv == nil {
		a.cpu.Percent, a.cpu.Label = 0, "no host selected"
		a.mem.Percent, a.mem.Label = 0, "no host selected"
		a.swap.Percent, a.swap.Label = 0, "no host selected"
		a.detail.Text = "\n  No servers configured.\n\n  Press 'a' to add one, or run:\n    bare-eye hosts add"
		a.disk.Rows = nil
		a.net.Text = ""
		a.netPlot.Series = nil
		a.netPlot.MaxVal = 1
		a.netPlot.Title = " Network rate "
		a.spark.Series = nil
		return
	}
	m := h.Metrics
	th := a.cfg.Monitoring.Thresholds
	stale := h.HasMetrics && h.Status != state.StatusOnline && h.Status != state.StatusStale
	suffix := ""
	if stale {
		suffix = "  · last sample"
	}

	cpuLabel := "CPU  —"
	if m.CPUValid {
		cpuLabel = fmt.Sprintf("CPU  %.1f%%%s", m.CPUPercent, suffix)
	}
	a.cpu.Percent = clampPct(m.CPUPercent)
	a.cpu.Label = cpuLabel
	a.cpu.BarColor = thresholdColor(m.CPUPercent, th.CPU, ui.ColorGreen)
	if stale {
		a.cpu.BarColor = ui.ColorGrey
	}

	memPct := m.MemPercent()
	a.mem.Percent = clampPct(memPct)
	a.mem.Label = fmt.Sprintf("RAM  %s / %s  (%.0f%%)%s", humanBytes(m.MemUsed), humanBytes(m.MemTotal), memPct, suffix)
	a.mem.BarColor = thresholdColor(memPct, th.Mem, ui.ColorCyan)
	if stale {
		a.mem.BarColor = ui.ColorGrey
	}

	swapPct := m.SwapPercent()
	a.swap.Percent = clampPct(swapPct)
	if m.SwapTotal == 0 {
		a.swap.Label = "Swap  none configured"
	} else {
		a.swap.Label = fmt.Sprintf("Swap  %s / %s  (%.0f%%)%s", humanBytes(m.SwapUsed), humanBytes(m.SwapTotal), swapPct, suffix)
	}
	a.swap.BarColor = thresholdColor(swapPct, th.Swap, ui.ColorYellow)
	if stale {
		a.swap.BarColor = ui.ColorGrey
	}

	cpuData, memData := historySeries(h.History)
	a.spark.Series = []chartSeries{
		{Color: ui.ColorGreen, Data: cpuData},
		{Color: ui.ColorCyan, Data: memData},
	}

	a.detail.Title = " " + srv.DisplayName() + " "
	var b strings.Builder
	fmt.Fprintf(&b, "\n  Address   %s@%s\n", srv.User, srv.Addr())
	fmt.Fprintf(&b, "  Status    %s · last update %s\n", statusLabel(h.Status), ago(h.LastSuccess))
	if h.Error != "" {
		fmt.Fprintf(&b, "  Error     %s\n", truncate(h.Error, 70))
	}
	fmt.Fprintf(&b, "  OS        %s\n", dash(m.Kernel))
	fmt.Fprintf(&b, "  Hostname  %s\n", dash(m.Hostname))
	fmt.Fprintf(&b, "  Cores     %s\n", cores(m.CPUCores))
	fmt.Fprintf(&b, "  Uptime    %s\n", humanUptime(m.Uptime))
	if m.LoadValid {
		fmt.Fprintf(&b, "  Load      %.2f / %.2f / %.2f\n", m.Load1, m.Load5, m.Load15)
	} else {
		b.WriteString("  Load      —\n")
	}
	if m.CPUValid {
		fmt.Fprintf(&b, "  CPU       %.1f%%\n", m.CPUPercent)
	} else {
		b.WriteString("  CPU       — (first sample)\n")
	}
	fmt.Fprintf(&b, "  Memory    %s / %s (%.0f%%)\n", humanBytes(m.MemUsed), humanBytes(m.MemTotal), memPct)
	fmt.Fprintf(&b, "  Swap      %s / %s\n", humanBytes(m.SwapUsed), humanBytes(m.SwapTotal))
	a.detail.Text = b.String()

	a.disk.Title = fmt.Sprintf(" Filesystems (%d) ", len(m.Filesystems))
	a.disk.Rows = nil
	for _, fs := range m.Filesystems {
		line := fmt.Sprintf("%-16s %s / %s  %.0f%%", truncate(fs.Mount, 16), humanBytes(fs.Used), humanBytes(fs.Total), fs.Percent)
		a.disk.Rows = append(a.disk.Rows, line)
	}
	if len(m.Filesystems) == 0 {
		a.disk.Rows = []string{"  no filesystem data"}
	}

	var n strings.Builder
	fmt.Fprintf(&n, "\n  RX rate   %s\n", humanRate(m.NetRXRate))
	fmt.Fprintf(&n, "  TX rate   %s\n", humanRate(m.NetTXRate))
	fmt.Fprintf(&n, "  RX total  %s\n", humanBytes(m.NetRXBytes))
	fmt.Fprintf(&n, "  TX total  %s\n", humanBytes(m.NetTXBytes))
	if len(m.Warnings) > 0 {
		fmt.Fprintf(&n, "\n  Unavailable: %s\n", strings.Join(m.Warnings, ", "))
	}
	if !h.HasMetrics {
		fmt.Fprintf(&n, "\n  Waiting for the first sample…\n")
	}
	a.net.Text = n.String()

	a.buildNetChart(h)
}

// buildNetChart feeds the RX/TX rate history into the network chart.
func (a *App) buildNetChart(h *state.HostState) {
	a.netPlot.Series = nil
	a.netPlot.MaxVal = 1
	a.netPlot.Title = " Network rate "
	if a.netPlot.GetRect().Dy() <= 0 {
		return
	}
	rx, tx := netSeries(h.History)
	// One chart column per sample; keep only what fits.
	if inner := a.netPlot.Inner.Dx(); inner > 1 && len(rx) > inner {
		rx = rx[len(rx)-inner:]
		tx = tx[len(tx)-inner:]
	}
	if len(rx) < 2 {
		return
	}
	peak := seriesPeak(rx, tx)
	if peak <= 0 {
		peak = 1
	}
	a.netPlot.Series = []chartSeries{
		{Color: ui.ColorGreen, Data: rx},
		{Color: ui.ColorYellow, Data: tx},
	}
	a.netPlot.MaxVal = peak
	a.netPlot.Title = fmt.Sprintf(" Network rate · peak %s ", humanRate(peak))
}

func (a *App) buildEvents() {
	events := a.events.Recent(40)
	a.eventLog.Rows = nil
	for _, e := range events {
		symbol := "·"
		switch e.Level {
		case state.LevelError:
			symbol = "×"
		case state.LevelWarn:
			symbol = "!"
		}
		line := fmt.Sprintf("%s %s %-14s %s", e.At.Format("15:04:05"), symbol, truncate(e.Host, 14), truncate(e.Message, 72))
		a.eventLog.Rows = append(a.eventLog.Rows, line)
	}
	if len(events) == 0 {
		a.eventLog.Rows = []string{"  no events yet"}
	}
	a.eventLog.SelectedRow = 0
}

func (a *App) renderModal(title, text string) {
	w, h := a.width, a.height
	mw := w - 4
	if mw > 104 {
		mw = 104
	}
	if mw < 30 {
		mw = w
	}
	mh := h - 2
	if mh > 26 {
		mh = 26
	}
	if mh < 6 {
		mh = h
	}
	x := (w - mw) / 2
	y := (h - mh) / 2
	a.modal.Title = title
	a.modal.Text = text
	a.modal.SetRect(x, y, x+mw, y+mh)
	ui.Render(a.modal)
}

func (a *App) updatePrompt() {
	if a.prompt == nil {
		return
	}
	switch {
	case a.mode == modeAgentInput:
		a.prompt.Title = " Agent · Enter send · Esc close "
		a.prompt.TitleStyle = ui.NewStyle(ui.ColorCyan, ui.ColorClear, ui.ModifierBold)
		a.prompt.BorderStyle = ui.NewStyle(ui.ColorCyan)
		a.prompt.Placeholder = "Describe a VPS setup task…"
		a.prompt.TitleBottom = " every mutating command needs approval "
	case a.agentBusy:
		a.prompt.Title = " Agent · running… "
		a.prompt.BorderStyle = ui.NewStyle(ui.ColorYellow)
		a.prompt.Placeholder = "Agent is working · Ctrl+A shows the transcript"
		a.prompt.TitleBottom = " "
	default:
		a.prompt.Title = " Agent "
		a.prompt.TitleStyle = ui.NewStyle(ui.ColorLightGrey, ui.ColorClear, ui.ModifierBold)
		a.prompt.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
		a.prompt.Placeholder = "Describe a VPS setup task…   ( : to focus · Ctrl+A transcript )"
		a.prompt.TitleBottom = " "
	}
}

func (a *App) updateAgentView() {
	if a.agentView == nil {
		return
	}
	text := strings.TrimSpace(a.lastReply)
	switch {
	case text == "" && a.agentBusy:
		text = "Thinking…"
	case text == "":
		text = "No agent response yet.\n\nPress ':' and describe a VPS task. Ctrl+A opens the full transcript."
	case a.agentBusy:
		text += "\n\n(working…)"
	}
	a.agentView.Text = text
	switch {
	case a.agentBusy:
		a.agentView.Title = " Agent response · working… · Ctrl+A transcript "
		a.agentView.BorderStyle = ui.NewStyle(ui.ColorYellow)
	case a.mode == modeAgentInput:
		a.agentView.Title = " Agent response · Enter send · Ctrl+A transcript "
		a.agentView.BorderStyle = ui.NewStyle(ui.ColorCyan)
	default:
		a.agentView.Title = " Agent response · Ctrl+A transcript "
		a.agentView.BorderStyle = ui.NewStyle(ui.ColorLightGrey)
	}
}

func (a *App) renderAgentLog() {
	text := "\n  No agent transcript yet.\n\n  Press ':' , describe a VPS task, and press Enter.\n"
	if len(a.transcript) > 0 {
		maxLines := a.height - 8
		if maxLines > 24 {
			maxLines = 24
		}
		if maxLines < 5 {
			maxLines = 5
		}
		end := len(a.transcript) - a.agentScroll
		if end < maxLines {
			end = maxLines
		}
		if end > len(a.transcript) {
			end = len(a.transcript)
		}
		start := end - maxLines
		if start < 0 {
			start = 0
		}
		var b strings.Builder
		b.WriteString("\n")
		for _, line := range a.transcript[start:end] {
			b.WriteString("  " + truncate(line, a.width-8) + "\n")
		}
		if a.agentScroll > 0 {
			b.WriteString(fmt.Sprintf("\n  ↑ %d lines from the end\n", a.agentScroll))
		}
		text = b.String()
	}
	a.renderModal(" Agent transcript · ↑/↓ scroll · Esc close ", text)
}

func maxDisk(fs []collector.Filesystem) (float64, bool) {
	var max float64
	found := false
	for _, f := range fs {
		if f.Percent > max {
			max = f.Percent
			found = true
		}
	}
	return max, found
}

func historySeries(hist []state.Sample) (cpu, mem []float64) {
	start := 0
	if len(hist) > 120 {
		start = len(hist) - 120
	}
	for _, s := range hist[start:] {
		if s.CPUValid {
			cpu = append(cpu, s.CPU)
		}
		mem = append(mem, s.Mem)
	}
	return cpu, mem
}

func netSeries(hist []state.Sample) (rx, tx []float64) {
	start := 0
	if len(hist) > 120 {
		start = len(hist) - 120
	}
	for _, s := range hist[start:] {
		rx = append(rx, s.NetRX)
		tx = append(tx, s.NetTX)
	}
	return rx, tx
}

func seriesPeak(series ...[]float64) float64 {
	var peak float64
	for _, s := range series {
		for _, v := range s {
			if v > peak {
				peak = v
			}
		}
	}
	return peak
}

func statusColor(s state.Status) ui.Color {
	switch s {
	case state.StatusOnline:
		return ui.ColorGreen
	case state.StatusStale, state.StatusUnsupported, state.StatusPending:
		return ui.ColorYellow
	case state.StatusOffline, state.StatusAuthError, state.StatusHostKey:
		return ui.ColorRed
	case state.StatusDisabled:
		return ui.ColorGrey
	}
	return ui.ColorWhite
}

func thresholdColor(v, threshold float64, normal ui.Color) ui.Color {
	if threshold > 0 && v >= threshold {
		return ui.ColorRed
	}
	return normal
}

func clampPct(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(v + 0.5)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func cores(n int) string {
	if n <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d", n)
}
