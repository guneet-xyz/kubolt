// Package output: BubbleTeaSink renders a tree-shaped Bubble Tea TUI driven by
// sink Events. Emit() is non-blocking and thread-safe; Run() owns the
// tea.Program lifecycle and bridges the event channel to the program.
package output

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

const (
	bubbleEventBuffer   = 256
	bubbleLastLineLimit = 60
	bubbleStoredLineMax = 80
	bubbleLastLineLabel = "running"
	bubbleStatusPending = "pending"
	bubbleStatusRunning = "running"
	bubbleStatusDone    = "done"
	bubbleStatusFailed  = "failed"
	bubbleStatusSkipped = "skipped"
)

// sinkEventMsg wraps a sink Event so it can flow through tea.Program as a Msg.
type sinkEventMsg struct {
	event Event
}

// taskState tracks the live UI state for one node in the tree.
type taskState struct {
	name     string
	parents  []string
	spinner  spinner.Model
	status   string
	stage    string
	lastLine string
	err      error
	start    time.Time
	end      time.Time
}

// newTaskState constructs a fresh taskState in the "pending" status.
func newTaskState(name string, parents []string) *taskState {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	parentsCopy := append([]string(nil), parents...)
	return &taskState{
		name:    name,
		parents: parentsCopy,
		spinner: sp,
		status:  bubbleStatusPending,
	}
}

// icon returns the leading status glyph for this task's current state.
func (ts *taskState) icon() string {
	switch ts.status {
	case bubbleStatusRunning:
		return ts.spinner.View()
	case bubbleStatusDone:
		return "✓"
	case bubbleStatusFailed:
		return "✗"
	case bubbleStatusSkipped:
		return "—"
	default:
		return "·"
	}
}

// elapsed returns a human-readable duration string for this task.
func (ts *taskState) elapsed() string {
	if ts.start.IsZero() {
		return "-"
	}
	end := ts.end
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(ts.start).Truncate(time.Millisecond).String()
}

// bubbleModel is the Bubble Tea model that owns the tree of taskStates.
//
// The model is a value type, but its internal maps and slices are reference
// types. Mutations to the maps survive value-receiver calls. Mutations to
// `tasks` and `roots` slices grow them locally and are returned via the new
// model value from Update.
type bubbleModel struct {
	tasks    []*taskState
	byName   map[string]*taskState
	roots    []string
	children map[string][]string
	windowW  int
	windowH  int
	quitting bool
	total    int
	done     int
}

// newBubbleModel returns a fresh, empty bubbleModel ready for Update calls.
func newBubbleModel() bubbleModel {
	return bubbleModel{
		byName:   make(map[string]*taskState),
		children: make(map[string][]string),
	}
}

// Init starts spinner ticks for any tasks already in the model. Typically the
// model is empty when Init is called, so this returns nil.
func (m bubbleModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.tasks))
	for _, ts := range m.tasks {
		if ts.status == bubbleStatusRunning {
			cmds = append(cmds, ts.spinner.Tick)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m bubbleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sinkEventMsg:
		return m.handleEvent(msg.event)

	case spinner.TickMsg:
		var cmds []tea.Cmd
		for _, ts := range m.tasks {
			if ts.status != bubbleStatusRunning {
				continue
			}
			var cmd tea.Cmd
			ts.spinner, cmd = ts.spinner.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.windowW = msg.Width
		m.windowH = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// handleEvent updates the model state from a sink Event and returns the next
// command (e.g. starting a spinner Tick when a node begins running).
func (m bubbleModel) handleEvent(e Event) (tea.Model, tea.Cmd) {
	switch e.Kind {
	case TreeStart:
		m.total = e.Count
		return m, nil

	case NodeReady:
		m.ensureTask(e.App, e.Parents)
		return m, nil

	case NodeStart:
		ts := m.ensureTask(e.App, e.Parents)
		ts.status = bubbleStatusRunning
		ts.start = time.Now()
		ts.end = time.Time{}
		ts.err = nil
		return m, ts.spinner.Tick

	case NodeLine:
		if ts, ok := m.byName[e.App]; ok {
			line := strings.TrimRight(e.Text, "\r\n")
			ts.lastLine = truncate(line, bubbleStoredLineMax)
			if e.Stage != "" {
				ts.stage = e.Stage
			}
		}
		return m, nil

	case NodeDone:
		if ts, ok := m.byName[e.App]; ok {
			ts.end = time.Now()
			ts.err = e.Err
			if e.Err != nil {
				ts.status = bubbleStatusFailed
			} else {
				ts.status = bubbleStatusDone
			}
			m.done++
		}
		return m, nil

	case NodeSkip:
		ts := m.ensureTask(e.App, e.Parents)
		ts.status = bubbleStatusSkipped
		ts.end = time.Now()
		if ts.lastLine == "" && e.Reason != "" {
			ts.lastLine = e.Reason
		}
		m.done++
		return m, nil

	case TreeDone:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// ensureTask returns the task with the given name, creating it (and registering
// it in the tree) if it doesn't yet exist. Diamond-DAG rule: a node is rendered
// once under its first parent; remaining parents are surfaced via "(also: ...)"
// in the View renderer.
func (m *bubbleModel) ensureTask(name string, parents []string) *taskState {
	if ts, ok := m.byName[name]; ok {
		return ts
	}
	ts := newTaskState(name, parents)
	m.tasks = append(m.tasks, ts)
	m.byName[name] = ts
	if len(parents) == 0 {
		m.roots = append(m.roots, name)
	} else {
		first := parents[0]
		m.children[first] = append(m.children[first], name)
	}
	return ts
}

// View renders the tree-shaped UI as a Bubble Tea v2 View.
func (m bubbleModel) View() tea.View {
	return tea.NewView(m.render())
}

// render produces the tree as a single string. Exposed at package level so
// other code (and future helpers) can reuse the renderer without going through
// the tea.View wrapper.
func (m bubbleModel) render() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	for i, name := range m.roots {
		m.renderNode(&b, name, "", i == len(m.roots)-1)
	}
	return b.String()
}

// renderNode renders one node and recurses into its children. It uses
// box-drawing characters to draw the tree structure.
func (m bubbleModel) renderNode(b *strings.Builder, name, prefix string, isLast bool) {
	ts, ok := m.byName[name]
	if !ok {
		return
	}

	var glyph, childPrefix string
	if isLast {
		glyph = "└─ "
		childPrefix = prefix + "   "
	} else {
		glyph = "├─ "
		childPrefix = prefix + "│  "
	}

	line := prefix + glyph + ts.icon() + " " + name
	if ts.stage != "" {
		line += " [" + ts.stage + "]"
	}
	if ts.status == bubbleStatusRunning && ts.lastLine != "" {
		line += " " + truncate(ts.lastLine, bubbleLastLineLimit)
	}
	if ts.status == bubbleStatusDone {
		line += " ✓ " + ts.elapsed()
	}
	if ts.status == bubbleStatusFailed && ts.err != nil {
		line += " ✗ " + ts.err.Error()
	}
	if ts.status == bubbleStatusSkipped {
		line += " (skipped)"
	}
	if len(ts.parents) > 1 {
		line += " (also: " + strings.Join(ts.parents[1:], ", ") + ")"
	}
	fmt.Fprintln(b, line)

	for i, child := range m.children[name] {
		m.renderNode(b, child, childPrefix, i == len(m.children[name])-1)
	}
}

// BubbleTeaSink is a Sink that renders a Bubble Tea v2 dashboard. Emit is
// non-blocking and thread-safe; Run owns the tea.Program lifecycle and bridges
// queued events into it.
type BubbleTeaSink struct {
	mu        sync.Mutex
	closed    bool
	eventCh   chan Event
	dropCount int64
	done      chan struct{}
	w         io.Writer
	isTTY     bool
	program   *tea.Program // set while Run is active; nil otherwise
}

// NewBubbleTeaSink constructs a BubbleTeaSink that writes to w. If w is os.Stdout
// the sink renders an interactive TUI; otherwise it disables the renderer.
func NewBubbleTeaSink(w io.Writer) *BubbleTeaSink {
	return &BubbleTeaSink{
		eventCh: make(chan Event, bubbleEventBuffer),
		done:    make(chan struct{}),
		w:       w,
		isTTY:   w == os.Stdout,
	}
}

// Emit queues an event for the render loop. It never blocks: when the buffer is
// full, the event is dropped and dropCount is incremented atomically.
func (s *BubbleTeaSink) Emit(e Event) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	select {
	case s.eventCh <- e:
	default:
		atomic.AddInt64(&s.dropCount, 1)
	}
}

// DropCount returns the number of events dropped because the buffer was full.
// Useful for tests and observability.
func (s *BubbleTeaSink) DropCount() int64 {
	return atomic.LoadInt64(&s.dropCount)
}

// Run starts the underlying tea.Program and blocks until the program exits.
// The program is bound to ctx via tea.WithContext, so ctx cancellation cleanly
// stops the renderer. Run returns whatever error tea.Program.Run returns.
func (s *BubbleTeaSink) Run(ctx context.Context) error {
	defer close(s.done)

	m := newBubbleModel()

	opts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithOutput(s.w),
		// CLI cobra commands own SIGINT/SIGTERM and translate them to ctx
		// cancellation. Disable Bubble Tea's signal handler to avoid double
		// handling.
		tea.WithoutSignalHandler(),
	}
	if !s.isTTY {
		// Non-TTY (tests, redirected output): run without a renderer and
		// without a stdin reader so the program drives state cleanly.
		opts = append(opts, tea.WithoutRenderer(), tea.WithInput(nil))
	}

	p := tea.NewProgram(m, opts...)

	s.mu.Lock()
	s.program = p
	s.mu.Unlock()

	// Forwarder goroutine: pulls events off s.eventCh and pushes them into
	// the tea.Program. Exits when ctx is cancelled, eventCh is closed, or the
	// program has finished running.
	runDone := make(chan struct{})
	forwarderDone := make(chan struct{})
	go func() {
		defer close(forwarderDone)
		for {
			select {
			case e, ok := <-s.eventCh:
				if !ok {
					return
				}
				p.Send(sinkEventMsg{event: e})
			case <-ctx.Done():
				return
			case <-runDone:
				return
			}
		}
	}()

	_, err := p.Run()
	close(runDone)
	<-forwarderDone

	s.mu.Lock()
	s.program = nil
	s.closed = true
	s.mu.Unlock()

	return err
}

// Close stops accepting new events and waits for Run to finish. Close is
// idempotent. If Run is currently active, Close also asks the program to quit.
func (s *BubbleTeaSink) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// If Run was started, wait for it; otherwise s.done was never armed.
		select {
		case <-s.done:
		default:
		}
		return
	}
	s.closed = true
	p := s.program
	close(s.eventCh)
	s.mu.Unlock()

	if p != nil {
		p.Quit()
		<-s.done
	}
}

// truncate returns a string truncated to maxLen characters, with "…" appended
// if the original was longer. Handles multi-byte characters safely.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
