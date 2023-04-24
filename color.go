package emcee

import "github.com/fatih/color"

var (
	colorWheel = []*color.Color{
		color.New(color.FgRed),
		color.New(color.FgGreen),
		color.New(color.FgYellow),
		color.New(color.FgBlue),
		color.New(color.FgMagenta),
		color.New(color.FgCyan),

		color.New(color.FgRed).Add(color.Bold),
		color.New(color.FgGreen).Add(color.Bold),
		color.New(color.FgYellow).Add(color.Bold),
		color.New(color.FgBlue).Add(color.Bold),
		color.New(color.FgMagenta).Add(color.Bold),
		color.New(color.FgCyan).Add(color.Bold),

		color.New(color.FgRed).Add(color.Faint),
		color.New(color.FgGreen).Add(color.Faint),
		color.New(color.FgYellow).Add(color.Faint),
		color.New(color.FgBlue).Add(color.Faint),
		color.New(color.FgMagenta).Add(color.Faint),
		color.New(color.FgCyan).Add(color.Faint),
	}

	colorCh chan *color.Color

	colorIndex int
)

func init() {
	colorCh = make(chan *color.Color)
	go func() {
		for {
			if colorIndex == len(colorWheel)-1 {
				colorIndex = 0
			}
			colorCh <- colorWheel[colorIndex]
			colorIndex += 1
		}
	}()
}

func ColorPrintfFunc() func(format string, a ...interface{}) {
	select {
	case col := <-colorCh:
		return func(format string, a ...interface{}) {
			defer color.Unset()
			col.PrintfFunc()(format, a...)
		}
	}
}
