package tui

import (
	"image"
	"strings"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
)

func renderChart(t *testing.T, c *axisChart) string {
	t.Helper()
	var b strings.Builder
	buf := ui.NewBuffer(image.Rect(0, 0, c.GetRect().Dx(), c.GetRect().Dy()))
	c.Draw(buf)
	for y := 0; y < buf.Dy(); y++ {
		for x := 0; x < buf.Dx(); x++ {
			b.WriteRune(buf.GetCell(image.Pt(x, y)).Rune)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestAxisChartDrawsYLabels(t *testing.T) {
	c := newAxisChart()
	c.SetRect(0, 0, 40, 8)
	c.MaxVal = 100
	c.YFormat = percentAxisLabel
	c.Series = []chartSeries{{Color: ui.ColorGreen, Data: []float64{10, 50, 90}}}

	out := renderChart(t, c)
	t.Log("\n" + out)
	for _, want := range []string{"100%", "50%", "0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("chart output missing Y label %q:\n%s", want, out)
		}
	}
}

func TestAxisChartRateLabels(t *testing.T) {
	c := newAxisChart()
	c.SetRect(0, 0, 40, 8)
	c.YFormat = humanRate
	c.Series = []chartSeries{{Color: ui.ColorGreen, Data: []float64{0, 1024 * 1024 * 3}}}

	out := renderChart(t, c)
	t.Log("\n" + out)
	if !strings.Contains(out, "MiB/s") {
		t.Errorf("chart output missing rate unit:\n%s", out)
	}
}

func TestNiceCeil(t *testing.T) {
	cases := map[float64]float64{
		0:    1,
		1:    1,
		1.3:  2,
		3:    5,
		7:    10,
		100:  100,
		1234: 2000,
	}
	for in, want := range cases {
		if got := niceCeil(in); got != want {
			t.Errorf("niceCeil(%v) = %v, want %v", in, got, want)
		}
	}
}
