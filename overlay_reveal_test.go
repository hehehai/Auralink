package main

import (
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/overlay/layout"
	"github.com/hehehai/Auralink/internal/session"
)

// TestOverlayRevealOnce: a showing turns its window opaque once. When its
// view builds, the fallback timer (ovRevealMax) does nothing; when the view
// never builds, the timer reveals it, once; a window hidden before its view
// builds is never revealed by its timer.
func TestOverlayRevealOnce(t *testing.T) {
	a, _, _ := newTestApp(t)
	ft := new(fakeTimers).install(a)
	now := time.Now()
	// A: shown, and the view builds: revealed by the build, not again.
	snap(a, session.Snapshot{ID: "A", State: session.Recording, StartedAt: now})
	if !a.ov.reveal || a.ov.reveals != 0 {
		t.Fatalf("A shows: reveal %v, reveals %d", a.ov.reveal, a.ov.reveals)
	}
	tt := hudTester(a.overlayView, a.ov.w+2*layout.Pad, a.ov.h+2*layout.Pad)
	tt.Frame()
	if a.ov.reveal || a.ov.reveals != 1 {
		t.Fatalf("A built: reveal %v, reveals %d", a.ov.reveal, a.ov.reveals)
	}
	ft.advance(time.Minute)
	if a.ov.reveals != 1 {
		t.Errorf("A: the fallback revealed again (%d reveals)", a.ov.reveals)
	}
	// A ends and is gone before B shows.
	snap(a, session.Snapshot{ID: "A", State: session.Cancelled})
	ft.advance(ovGone)
	if a.ov.visible {
		t.Fatal("A stays on screen")
	}
	// B: hidden before its view builds; its timer must not show it.
	snap(a, session.Snapshot{ID: "B", State: session.Recording, StartedAt: now})
	snap(a, session.Snapshot{ID: "B", State: session.Cancelled})
	ft.advance(time.Minute)
	if a.ov.reveals != 1 || a.ov.visible {
		t.Errorf("B hidden before its build: reveals %d, visible %v", a.ov.reveals, a.ov.visible)
	}
	// C: no view builds; the timer is what reveals the window, once.
	snap(a, session.Snapshot{ID: "C", State: session.Recording, StartedAt: now})
	ft.advance(time.Minute)
	if a.ov.reveals != 2 || a.ov.reveal {
		t.Errorf("C never built: reveals %d (want 2), reveal %v", a.ov.reveals, a.ov.reveal)
	}
}
