package tui

import (
	"fmt"
	"image"
	"math"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/drawille"
)

// chartSeries is one line plotted by axisChart.
type chartSeries struct {
	Color ui.Color
	Data  []float64
}

// axisChart draws one or more braille lines inside a bordered block, with a
// labelled Y axis so the scale and unit of the data are readable.
//
// It is a small custom widget rather than widgets.Plot because Plot's braille
// renderer assumes an origin of (0,0) and draws outside its block anywhere else.
type axisChart struct {
	ui.Block

	Series   []chartSeries
	MaxVal   float64
	YFormat  func(float64) string
	EmptyMsg string
}

func newAxisChart() *axisChart {
	return &axisChart{
		Block: *ui.NewBlock(),
	}
}

// percentAxisLabel formats a Y axis tick as a percentage.
func percentAxisLabel(v float64) string {
	return fmt.Sprintf("%.0f%%", v)
}

func (c *axisChart) Draw(buf *ui.Buffer) {
	c.Block.Draw(buf)
	inner := c.Inner
	if inner.Dx() < 4 || inner.Dy() < 2 {
		return
	}

	var peak float64
	lines := 0
	for _, s := range c.Series {
		if len(s.Data) < 2 {
			continue
		}
		lines++
		if p := seriesPeak(s.Data); p > peak {
			peak = p
		}
	}
	if lines == 0 {
		if c.EmptyMsg != "" {
			buf.SetString(ui.TrimString(c.EmptyMsg, inner.Dx()), ui.NewStyle(ui.ColorGrey), inner.Min)
		}
		return
	}

	max := c.MaxVal
	if max <= 0 {
		max = peak
	}
	if max <= 0 {
		max = 1
	}
	max = niceCeil(max)

	labels := c.axisLabels(max)
	gutter := 0
	for _, l := range labels {
		if w := len([]rune(l)); w > gutter {
			gutter = w
		}
	}
	gutter++ // keep one space between the labels and the plot
	plotW := inner.Dx() - gutter
	if plotW < 4 {
		gutter, plotW = 0, inner.Dx()
	}
	if plotW < 2 {
		return
	}
	plot := image.Rect(inner.Min.X+gutter, inner.Min.Y, inner.Max.X, inner.Max.Y)

	rows := inner.Dy() - 1
	// Braille cells are 2x4 dots: one column per sample, four rows per text row.
	row := func(v float64) int {
		r := int(v / max * float64(rows))
		if r < 0 {
			return 0
		}
		if r > rows {
			return rows
		}
		return r
	}

	if gutter > 0 {
		style := ui.NewStyle(ui.ColorGrey)
		ys := []int{inner.Min.Y, inner.Min.Y + rows/2, inner.Min.Y + rows}
		prevY := -1
		for i, l := range labels {
			if i >= len(ys) || ys[i] == prevY {
				continue
			}
			buf.SetString(ui.TrimString(l, gutter-1), style, image.Pt(inner.Min.X, ys[i]))
			prevY = ys[i]
		}
	}

	canvas := drawille.NewCanvas()
	maxLen := 0
	for _, s := range c.Series {
		if len(s.Data) > maxLen {
			maxLen = len(s.Data)
		}
	}
	for _, s := range c.Series {
		if len(s.Data) < 2 {
			continue
		}
		dots := float64(plotW*2 - 1)
		// Right-align shorter series so all lines share the same time axis.
		offset := maxLen - len(s.Data)
		prevX, prevY := -1, 0
		for i, v := range s.Data {
			x := int(float64(offset+i) / float64(maxLen-1) * dots)
			y := (rows - row(v)) * 4
			if prevX >= 0 {
				canvas.SetLine(
					image.Pt(prevX, prevY),
					image.Pt(x, y),
					drawille.Color(s.Color),
				)
			}
			prevX, prevY = x, y
		}
	}
	for p, cell := range canvas.GetCells() {
		dest := p.Add(plot.Min)
		if dest.In(plot) {
			buf.SetCell(ui.NewCell(cell.Rune, ui.NewStyle(ui.Color(cell.Color))), dest)
		}
	}
}

// axisLabels returns the top, middle and bottom tick labels.
func (c *axisChart) axisLabels(max float64) []string {
	format := c.YFormat
	if format == nil {
		format = func(v float64) string { return fmt.Sprintf("%.0f", v) }
	}
	return []string{format(max), format(max / 2), format(0)}
}

// niceCeil rounds v up to a readable 1/2/5 x 10^n value so axis ticks are
// round numbers instead of arbitrary peaks.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	pow := math.Pow(10, math.Floor(math.Log10(v)))
	switch n := v / pow; {
	case n <= 1:
		return pow
	case n <= 2:
		return 2 * pow
	case n <= 5:
		return 5 * pow
	default:
		return 10 * pow
	}
}
