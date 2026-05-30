package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pterm/pterm"
)

const (
	tuiRefreshInterval = 200 * time.Millisecond
	tuiLastLineLimit   = 60
)

// appState tracks a single app's status.
type appState struct {
	name     string
	status   string
	start    time.Time
	end      time.Time
	lastLine string
	stderr   bytes.Buffer
	err      error
	reason   string
}

// TUISink renders a live pterm dashboard to a terminal.
// All rendering happens on a single internal goroutine.
// Emit is safe to call from multiple goroutines.
type TUISink struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool // protected by mu; set before eventCh is closed

	apps     map[string]*appState
	appOrder []string
	waves    int
	isTTY    bool

	eventCh chan Event
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewTUISink returns a Sink that renders a pterm live dashboard.
func NewTUISink(w io.Writer) *TUISink {
	if os.Getenv("NO_COLOR") != "" || w != os.Stdout {
		pterm.DisableColor()
	}

	t := &TUISink{
		w:       w,
		apps:    make(map[string]*appState),
		isTTY:   w == os.Stdout,
		eventCh: make(chan Event, 256),
		done:    make(chan struct{}),
	}
	t.wg.Add(1)
	go t.renderLoop()

	return t
}

// Emit sends an event to the render goroutine without blocking callers.
func (t *TUISink) Emit(e Event) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	select {
	case t.eventCh <- e:
	default:
	}
}

func (t *TUISink) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	close(t.done)
	close(t.eventCh)
	t.mu.Unlock()
	t.wg.Wait()
}

func (t *TUISink) renderLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(tuiRefreshInterval)
	defer ticker.Stop()

	var area *pterm.AreaPrinter
	if t.isTTY {
		areaPrinter := pterm.DefaultArea
		areaPrinter.SetWriter(t.w)
		startedArea, err := areaPrinter.Start()
		if err == nil {
			area = startedArea
		} else {
			t.isTTY = false
		}
	}

	for {
		select {
		case e, ok := <-t.eventCh:
			if !ok {
				t.finish(area)
				return
			}
			t.apply(e)
			if e.Kind == AllDone {
				t.finish(area)
				return
			}
			if !t.isTTY {
				continue
			}
			t.renderArea(area)

		case <-ticker.C:
			if t.isTTY {
				t.renderArea(area)
			}
		}
	}
}

func (t *TUISink) apply(e Event) {
	switch e.Kind {
	case WaveStart:
		t.waves++

	case AppStart:
		app := t.ensureApp(e.App)
		app.status = "running"
		app.start = time.Now()
		app.end = time.Time{}
		app.err = nil
		app.reason = ""

	case AppLine:
		app := t.ensureApp(e.App)
		line := strings.TrimRight(e.Text, "\r\n")
		app.lastLine = line
		if e.Stream == "stderr" {
			app.stderr.WriteString(line)
			app.stderr.WriteByte('\n')
		}

	case AppDone:
		app := t.ensureApp(e.App)
		app.end = time.Now()
		app.err = e.Err
		if e.Err != nil {
			app.status = "failed"
			return
		}
		app.status = "ok"

	case AppSkip:
		app := t.ensureApp(e.App)
		app.status = "skipped"
		app.reason = e.Reason
		app.end = time.Now()
		if app.lastLine == "" {
			app.lastLine = e.Reason
		}
	}
}

func (t *TUISink) finish(area *pterm.AreaPrinter) {
	if area != nil {
		t.renderArea(area)
		_ = area.Stop()
	}

	t.printSummary()
}

func (t *TUISink) ensureApp(name string) *appState {
	if name == "" {
		name = "(unknown)"
	}

	app, ok := t.apps[name]
	if ok {
		return app
	}

	app = &appState{name: name, status: "pending"}
	t.apps[name] = app
	t.appOrder = append(t.appOrder, name)

	return app
}

func (t *TUISink) renderArea(area *pterm.AreaPrinter) {
	if area == nil {
		return
	}

	area.Update(t.statusTable())
}

func (t *TUISink) statusTable() string {
	data := pterm.TableData{[]string{"", "app", "status", "elapsed", "last line"}}
	for _, name := range t.appOrder {
		app := t.apps[name]
		data = append(data, []string{
			statusIcon(app.status),
			app.name,
			app.status,
			formatElapsed(app),
			truncate(app.lastLine, tuiLastLineLimit),
		})
	}

	if len(data) == 1 {
		data = append(data, []string{"·", "waiting", "pending", "-", ""})
	}

	out, err := pterm.DefaultTable.WithHasHeader().WithData(data).Srender()
	if err != nil {
		return fallbackTable(t.appOrder, t.apps)
	}

	return out
}

func (t *TUISink) printSummary() {
	fmt.Fprintln(t.w, "kubolt app summary")
	fmt.Fprintln(t.w, "APP\tSTATUS\tELAPSED\tLAST LINE")
	for _, name := range t.appOrder {
		app := t.apps[name]
		fmt.Fprintf(t.w, "%s\t%s\t%s\t%s\n", app.name, app.status, formatElapsed(app), truncate(app.lastLine, tuiLastLineLimit))
	}

	for _, name := range t.appOrder {
		app := t.apps[name]
		if app.status != "failed" || app.stderr.Len() == 0 {
			continue
		}

		fmt.Fprintf(t.w, "\n[%s] stderr:\n%s", app.name, app.stderr.String())
	}
}

func fallbackTable(order []string, apps map[string]*appState) string {
	var b strings.Builder
	b.WriteString("kubolt app summary\n")
	for _, name := range order {
		app := apps[name]
		fmt.Fprintf(&b, "%s %s %s %s %s\n", statusIcon(app.status), app.name, app.status, formatElapsed(app), truncate(app.lastLine, tuiLastLineLimit))
	}
	return b.String()
}

func statusIcon(status string) string {
	switch status {
	case "running":
		return "⠋"
	case "ok":
		return "✓"
	case "failed":
		return "✗"
	case "skipped":
		return "○"
	default:
		return "·"
	}
}

func formatElapsed(app *appState) string {
	if app.start.IsZero() {
		return "-"
	}

	end := app.end
	if end.IsZero() {
		end = time.Now()
	}

	return end.Sub(app.start).Truncate(time.Millisecond).String()
}

func truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}

	runes := []rune(s)
	if max == 1 {
		return "…"
	}

	return string(runes[:max-1]) + "…"
}
