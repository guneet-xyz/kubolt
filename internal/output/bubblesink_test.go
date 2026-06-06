package output

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

func fakeKeyPress(s string) tea.KeyPressMsg {
	switch s {
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "q":
		return tea.KeyPressMsg{Code: 'q', Text: "q"}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

func updateModel(t *testing.T, m bubbleModel, msg tea.Msg) (bubbleModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	bm, ok := next.(bubbleModel)
	if !ok {
		t.Fatalf("Update returned non-bubbleModel: %T", next)
	}
	return bm, cmd
}

func TestBubbleSink_TreeRenderOrder_Diamond(t *testing.T) {
	m := newBubbleModel()
	events := []Event{
		{Kind: NodeStart, App: "a"},
		{Kind: NodeStart, App: "b", Parents: []string{"a"}},
		{Kind: NodeStart, App: "c", Parents: []string{"a"}},
		{Kind: NodeStart, App: "d", Parents: []string{"b", "c"}},
	}
	for _, e := range events {
		m, _ = updateModel(t, m, sinkEventMsg{event: e})
	}

	if len(m.roots) != 1 || m.roots[0] != "a" {
		t.Errorf("expected roots [a], got %v", m.roots)
	}
	if got := m.children["a"]; len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("expected children[a]=[b,c], got %v", got)
	}
	if got := m.children["b"]; len(got) != 1 || got[0] != "d" {
		t.Errorf("expected children[b]=[d], got %v", got)
	}
	if _, ok := m.byName["d"]; !ok {
		t.Fatal("d not registered in byName")
	}
	if got := m.byName["d"].parents; len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("expected d.parents=[b,c], got %v", got)
	}
	if got := m.children["c"]; len(got) != 0 {
		t.Errorf("expected c to have no direct children (d under b), got %v", got)
	}
}

func TestBubbleSink_KeyCtrlC_Quits(t *testing.T) {
	m := newBubbleModel()
	m, cmd := updateModel(t, m, fakeKeyPress("ctrl+c"))

	if !m.quitting {
		t.Error("expected quitting=true after ctrl+c")
	}
	if cmd == nil {
		t.Fatal("expected non-nil quit command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("quit command returned nil msg")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestBubbleSink_KeyQ_Quits(t *testing.T) {
	m := newBubbleModel()
	m, cmd := updateModel(t, m, fakeKeyPress("q"))

	if !m.quitting {
		t.Error("expected quitting=true after q")
	}
	if cmd == nil {
		t.Fatal("expected non-nil quit command")
	}
}

func TestBubbleSink_MultiParentAnnotation(t *testing.T) {
	m := newBubbleModel()
	for _, e := range []Event{
		{Kind: NodeStart, App: "b"},
		{Kind: NodeStart, App: "c"},
		{Kind: NodeStart, App: "d", Parents: []string{"b", "c"}},
	} {
		m, _ = updateModel(t, m, sinkEventMsg{event: e})
	}

	ts, ok := m.byName["d"]
	if !ok {
		t.Fatal("d not in byName")
	}
	if len(ts.parents) != 2 {
		t.Fatalf("expected 2 parents, got %d (%v)", len(ts.parents), ts.parents)
	}
	if ts.parents[0] != "b" || ts.parents[1] != "c" {
		t.Errorf("expected parents=[b,c], got %v", ts.parents)
	}

	out := m.render()
	if !strings.Contains(out, "(also: c)") {
		t.Errorf("expected render to contain '(also: c)', got %q", out)
	}
}

func TestBubbleSink_EmitNonBlocking(t *testing.T) {
	s := NewBubbleTeaSink(&bytes.Buffer{})

	for i := range bubbleEventBuffer * 2 {
		s.Emit(Event{Kind: NodeStart, App: fmt.Sprintf("x%d", i)})
	}

	if got := s.DropCount(); got == 0 {
		t.Errorf("expected drops>0 when buffer full, got %d", got)
	}
}

func TestBubbleSink_EmitAfterCloseIsNoOp(t *testing.T) {
	s := NewBubbleTeaSink(&bytes.Buffer{})
	close(s.done)
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	s.Emit(Event{Kind: NodeStart, App: "x"})

	if got := s.DropCount(); got != 0 {
		t.Errorf("expected no drops on closed sink (events ignored), got %d", got)
	}
}

func TestBubbleSink_ConcurrentEmit(t *testing.T) {
	s := NewBubbleTeaSink(&bytes.Buffer{})

	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			app := fmt.Sprintf("app-%d", worker)
			s.Emit(Event{Kind: NodeStart, App: app})
			for i := range 50 {
				s.Emit(Event{Kind: NodeLine, App: app, Stream: "stdout", Text: fmt.Sprintf("line-%d", i)})
			}
			s.Emit(Event{Kind: NodeDone, App: app})
		}(worker)
	}
	wg.Wait()
}

func TestBubbleSink_NodeLifecycleStates(t *testing.T) {
	m := newBubbleModel()

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: TreeStart, Count: 4}})
	if m.total != 4 {
		t.Errorf("expected total=4 from TreeStart.Count, got %d", m.total)
	}

	m, startCmd := updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "alpha"}})
	if ts := m.byName["alpha"]; ts == nil || ts.status != bubbleStatusRunning {
		t.Fatalf("expected alpha running, got %+v", ts)
	}
	if startCmd == nil {
		t.Error("expected NodeStart to return spinner.Tick command")
	}

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{
		Kind: NodeLine, App: "alpha", Text: "deploying chart\n", Stage: "copying",
	}})
	ts := m.byName["alpha"]
	if ts.lastLine != "deploying chart" {
		t.Errorf("expected lastLine='deploying chart', got %q", ts.lastLine)
	}
	if ts.stage != "copying" {
		t.Errorf("expected stage='copying', got %q", ts.stage)
	}

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "alpha"}})
	if got := m.byName["alpha"].status; got != bubbleStatusDone {
		t.Errorf("expected status=done, got %q", got)
	}
	if m.done != 1 {
		t.Errorf("expected done counter=1, got %d", m.done)
	}

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "beta"}})
	wantErr := errors.New("helm boom")
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "beta", Err: wantErr}})
	if got := m.byName["beta"]; got.status != bubbleStatusFailed || got.err != wantErr {
		t.Errorf("expected beta failed with err=%v, got %+v", wantErr, got)
	}

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{
		Kind: NodeSkip, App: "gamma", Reason: "dep failed",
	}})
	if got := m.byName["gamma"]; got == nil {
		t.Fatal("gamma not registered on NodeSkip")
	}
}

func TestBubbleSink_TreeDoneSetsQuitting(t *testing.T) {
	m := newBubbleModel()
	m, cmd := updateModel(t, m, sinkEventMsg{event: Event{Kind: TreeDone}})
	if !m.quitting {
		t.Error("expected quitting=true after TreeDone")
	}
	if cmd == nil {
		t.Fatal("expected non-nil quit command from TreeDone")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected TreeDone to emit tea.QuitMsg")
	}
}

func TestBubbleSink_WindowSizeUpdatesDimensions(t *testing.T) {
	m := newBubbleModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.windowW != 120 || m.windowH != 40 {
		t.Errorf("expected window 120x40, got %dx%d", m.windowW, m.windowH)
	}
}

func TestBubbleSink_SpinnerTickAdvancesRunningOnly(t *testing.T) {
	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "running-app"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "done-app"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "done-app"}})

	tick := spinner.TickMsg{Time: time.Now(), ID: m.byName["running-app"].spinner.ID()}
	m, _ = updateModel(t, m, tick)

	if got := m.byName["done-app"].status; got != bubbleStatusDone {
		t.Errorf("expected done-app to remain done, got %q", got)
	}
	if got := m.byName["running-app"].status; got != bubbleStatusRunning {
		t.Errorf("expected running-app to remain running, got %q", got)
	}
}

func TestBubbleSink_RunNonTTY(t *testing.T) {
	var buf bytes.Buffer
	s := NewBubbleTeaSink(&buf)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- s.Run(ctx)
	}()

	s.Emit(Event{Kind: TreeStart, Count: 2})
	s.Emit(Event{Kind: NodeStart, App: "a"})
	s.Emit(Event{Kind: NodeDone, App: "a"})
	s.Emit(Event{Kind: TreeDone})

	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Run did not exit before context deadline")
	}

	s.Close()
}

func TestBubbleSink_DiamondRenderOrderUnderFirstParent(t *testing.T) {
	m := newBubbleModel()
	for _, e := range []Event{
		{Kind: NodeStart, App: "root"},
		{Kind: NodeStart, App: "left", Parents: []string{"root"}},
		{Kind: NodeStart, App: "right", Parents: []string{"root"}},
		{Kind: NodeStart, App: "merge", Parents: []string{"left", "right"}},
	} {
		m, _ = updateModel(t, m, sinkEventMsg{event: e})
	}

	for _, child := range m.children["right"] {
		if child == "merge" {
			t.Error("merge should be under left (first parent), not right")
		}
	}
	mergeUnderLeft := false
	for _, child := range m.children["left"] {
		if child == "merge" {
			mergeUnderLeft = true
		}
	}
	if !mergeUnderLeft {
		t.Errorf("expected merge under left, children[left]=%v", m.children["left"])
	}
}

func TestBubbleSink_NodeLine_AppendsToBufferAndKeepsLastLine(t *testing.T) {
	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "alpha"}})

	if ts := m.byName["alpha"]; ts.buffer != nil {
		t.Errorf("expected buffer nil before any NodeLine, got %v", ts.buffer)
	}

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "alpha", Text: "first line"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "alpha", Text: "second line"}})

	ts := m.byName["alpha"]
	if ts.buffer == nil {
		t.Fatal("expected buffer to be lazily allocated on first NodeLine")
	}
	if ts.lastLine != "second line" {
		t.Errorf("expected lastLine='second line', got %q", ts.lastLine)
	}
	contents := string(ts.buffer.Bytes())
	if !strings.Contains(contents, "first line") || !strings.Contains(contents, "second line") {
		t.Errorf("expected buffer to contain both lines, got %q", contents)
	}
}

func TestBubbleSink_NodeDoneSuccess_ClearsBufferAndSkipsFailedDumps(t *testing.T) {
	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "alpha"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "alpha", Text: "hello"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "alpha"}})

	ts := m.byName["alpha"]
	if ts.status != bubbleStatusDone {
		t.Errorf("expected status=done, got %q", ts.status)
	}
	if ts.buffer != nil {
		t.Errorf("expected buffer cleared on success, got %v", ts.buffer)
	}
	if _, ok := m.failedDumps["alpha"]; ok {
		t.Errorf("success node must not appear in failedDumps")
	}
}

func TestBubbleSink_NodeDoneError_MovesBufferToFailedDumps(t *testing.T) {
	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "beta"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "beta", Text: "before failure"}})

	wantErr := errors.New("boom")
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "beta", Err: wantErr}})

	ts := m.byName["beta"]
	if ts.status != bubbleStatusFailed {
		t.Errorf("expected status=failed, got %q", ts.status)
	}
	if ts.buffer != nil {
		t.Errorf("expected ts.buffer nil after move to failedDumps, got %v", ts.buffer)
	}
	nb, ok := m.failedDumps["beta"]
	if !ok || nb == nil {
		t.Fatalf("expected failedDumps[beta] to be populated, got ok=%v nb=%v", ok, nb)
	}
	if !strings.Contains(string(nb.Bytes()), "before failure") {
		t.Errorf("expected captured content in moved buffer, got %q", string(nb.Bytes()))
	}
}

func TestBubbleSink_Close_DumpsFailedNodeBetweenMarkers(t *testing.T) {
	var w bytes.Buffer
	s := NewBubbleTeaSink(&w)

	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "boom"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "boom", Text: "captured-line"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "boom", Err: errors.New("nope")}})

	s.mu.Lock()
	s.model = m
	s.closed = true
	close(s.done)
	s.mu.Unlock()

	s.Close()

	out := w.String()
	startIdx := strings.Index(out, "--- output from boom ---\n")
	contentIdx := strings.Index(out, "captured-line")
	endIdx := strings.Index(out, "--- end output ---")
	if startIdx < 0 {
		t.Errorf("expected start marker, got %q", out)
	}
	if contentIdx < 0 {
		t.Errorf("expected captured line in dump, got %q", out)
	}
	if endIdx < 0 {
		t.Errorf("expected end marker, got %q", out)
	}
	if !(startIdx < contentIdx && contentIdx < endIdx) {
		t.Errorf("expected order start->content->end, got start=%d content=%d end=%d in %q",
			startIdx, contentIdx, endIdx, out)
	}
}

func TestBubbleSink_Close_NoFailures_NoMarkers(t *testing.T) {
	var w bytes.Buffer
	s := NewBubbleTeaSink(&w)

	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "ok"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "ok", Text: "happy-line"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "ok"}})

	s.mu.Lock()
	s.model = m
	s.closed = true
	close(s.done)
	s.mu.Unlock()

	s.Close()

	out := w.String()
	if strings.Contains(out, "--- output from") || strings.Contains(out, "--- end output ---") {
		t.Errorf("expected no failure markers, got %q", out)
	}
	if strings.Contains(out, "happy-line") {
		t.Errorf("successful node's buffer must not appear in writer, got %q", out)
	}
}

func TestBubbleSink_Close_TruncationMarker(t *testing.T) {
	var w bytes.Buffer
	s := NewBubbleTeaSink(&w)

	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "big"}})
	m.byName["big"].buffer = NewNodeBuffer(8)

	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "big", Text: "abcdef"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "big", Text: "ghijkl"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "big", Err: errors.New("oops")}})

	if nb := m.failedDumps["big"]; nb == nil || nb.TruncatedBytes() == 0 {
		t.Fatalf("test setup: expected truncation in moved buffer, got nb=%v", nb)
	}

	s.mu.Lock()
	s.model = m
	s.closed = true
	close(s.done)
	s.mu.Unlock()

	s.Close()

	out := w.String()
	if !strings.Contains(out, "bytes elided due to 1 MiB cap") {
		t.Errorf("expected truncation marker, got %q", out)
	}
	startIdx := strings.Index(out, "--- output from big ---\n")
	truncIdx := strings.Index(out, "[... ")
	if startIdx < 0 || truncIdx <= startIdx {
		t.Errorf("truncation marker must follow start marker, got %q", out)
	}
}

func TestBubbleSink_Close_Idempotent(t *testing.T) {
	var w bytes.Buffer
	s := NewBubbleTeaSink(&w)

	m := newBubbleModel()
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeStart, App: "boom"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeLine, App: "boom", Text: "line"}})
	m, _ = updateModel(t, m, sinkEventMsg{event: Event{Kind: NodeDone, App: "boom", Err: errors.New("nope")}})

	s.mu.Lock()
	s.model = m
	s.closed = true
	close(s.done)
	s.mu.Unlock()

	s.Close()
	first := w.String()
	s.Close()
	second := w.String()

	if first != second {
		t.Errorf("expected Close to be idempotent; first=%q second=%q", first, second)
	}
}
