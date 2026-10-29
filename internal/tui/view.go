package tui

import (
	"fmt"
	"strings"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/widgets"

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
	if h < 26 {
		eventsH = 4
	}
	if h < 16 {
		eventsH = 0
	}
	bodyTop := headerH
	bodyBottom := h - eventsH
	if bodyBottom < bodyTop+6 {
		bodyBottom = h
		eventsH = 0
	}
	if eventsH > 0 {
		a.eventLog.SetRect(0, h-eventsH, w, h)
	}

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
	a.net.SetRect(leftW, bodyTop, w, bodyBottom)
}

func (a *App) render() {
	if a.dirtyClear {
		ui.Clear()
		a.dirtyClear = false
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
		a.renderModal(" Keyboard help ", a.overlay)
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
	hint := "? help · a add · e edit · t test · r reconnect · d toggle · x remove · / filter · q quit"
	if a.status != "" {
		hint = a.status + "   |   " + hint
	}
	a.header.TitleBottom = " " + truncate(hint, a.width-2)

	a.buildTable()
	a.buildDetailPanels()
	a.buildEvents()
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
	ui.Render(items...)
}

func (a *App) renderDetail() {
	a.header.Text = truncate(a.detailHeader(), a.width-2)
	a.header.TitleBottom = " Esc back · e edit · t test · r reconnect · q quit"
	a.buildDetailPanels()
	a.buildEvents()
	items := []ui.Drawable{a.header}
	for _, w := range []ui.Drawable{a.detail, a.disk, a.net} {
		if w.GetRect().Dy() > 0 && w.GetRect().Dx() > 0 {
			items = append(items, w)
		}
	}
	if a.eventLog.GetRect().Dy() > 0 {
		items = append(items, a.eventLog)
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
		dummy := widgets.NewSparkline()
		dummy.Title = "CPU %"
		a.spark.Sparklines = []*widgets.Sparkline{dummy}
		a.detail.Text = "\n  No servers configured.\n\n  Press 'a' to add one, or run:\n    bare-eye hosts add"
		a.disk.Rows = nil
		a.net.Text = ""
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
	cpuSpark := widgets.NewSparkline()
	cpuSpark.Title = "CPU %"
	cpuSpark.Data = cpuData
	cpuSpark.LineColor = ui.ColorGreen
	cpuSpark.MaxVal = 100
	memSpark := widgets.NewSparkline()
	memSpark.Title = "MEM %"
	memSpark.Data = memData
	memSpark.LineColor = ui.ColorCyan
	memSpark.MaxVal = 100
	a.spark.Sparklines = []*widgets.Sparkline{cpuSpark, memSpark}

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
