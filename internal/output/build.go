package output

import (
	"fmt"
	"sync"
	"time"
)

// Step is one build step as the Build view shows it.
type Step struct {
	Key     string // "installing"
	Label   string // "Installing"
	State   string // pending, running, done, failed, skipped
	Seconds *int
}

// Build shows a build as it happens: its log lines, if wanted, and under
// them the step it is on, with a spinner and the time so far:
//
//	  added 112 packages in 3s
//	✓ Installing                    4s
//	⠼ Building                      0:12
//
// Everything goes to stderr, so stdout keeps only the result. Piped or in
// CI, lines are printed as they come and each step once when it ends.
// Nothing is shown with --json; with --quiet, only the steps' ends.
type Build struct {
	p     *Printer
	lines bool

	mu      sync.Mutex
	printed map[string]string // step key -> the state last printed
	label   string
	started time.Time
	stop    chan struct{}
	stopped chan struct{}
}

// Build starts a build view. lines says whether log lines are shown.
func (p *Printer) Build(lines bool) *Build {
	return &Build{p: p, lines: lines && !p.Quiet, printed: map[string]string{}}
}

func (b *Build) silent() bool { return b.p.JSON }

// Line prints one log line. tone is "good", "bad" or "plain".
func (b *Build) Line(text, tone string) {
	if b.silent() || !b.lines {
		return
	}
	text = Clean(text)
	switch tone {
	case "bad":
		text = paint(b.p.errColor, red, text)
	case "good":
		text = paint(b.p.errColor, green, text)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stop != nil {
		fmt.Fprint(b.p.Err, "\r\x1b[K")
	}
	fmt.Fprintf(b.p.Err, "    %s\n", text)
	b.drawLocked()
}

// Steps records the steps' states: a step that ended is printed once, with
// ✓ (or ✗), and the running one spins.
func (b *Build) Steps(steps []Step) {
	if b.silent() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	running := ""
	for _, s := range steps {
		prev := b.printed[s.Key]
		switch s.State {
		case "done", "failed":
			if prev == s.State {
				continue
			}
			b.printed[s.Key] = s.State
			glyph, style := "✓", green
			if s.State == "failed" {
				glyph, style = "✗", red
			}
			detail := ""
			if s.Seconds != nil {
				detail = Elapsed(time.Duration(*s.Seconds) * time.Second)
			} else if b.label == s.Label && !b.started.IsZero() {
				detail = Elapsed(time.Since(b.started))
			}
			if b.stop != nil {
				fmt.Fprint(b.p.Err, "\r\x1b[K")
			}
			fmt.Fprintf(b.p.Err, "  %s %s%s\n", paint(b.p.errColor, style, glyph), pad(Clean(s.Label)), paint(b.p.errColor, dim, detail))
			if b.label == s.Label {
				b.label = ""
			}
		case "running":
			running = s.Label
			if prev != "running" {
				b.printed[s.Key] = "running"
				if !b.p.live && !b.p.Quiet {
					fmt.Fprintf(b.p.Err, "  … %s\n", Clean(s.Label))
				}
			}
		}
	}
	if running != b.label {
		b.label, b.started = running, time.Now()
	}
	b.spinLocked()
}

// spinLocked starts or stops the spinner to match b.label.
func (b *Build) spinLocked() {
	if !b.p.live {
		return
	}
	switch {
	case b.label != "" && b.stop == nil:
		b.stop, b.stopped = make(chan struct{}), make(chan struct{})
		go b.spin(b.stop, b.stopped)
	case b.label == "" && b.stop != nil:
		b.haltLocked()
	}
	b.drawLocked()
}

func (b *Build) drawLocked() {
	if b.stop == nil || b.label == "" {
		return
	}
	i := int(time.Since(b.started)/(100*time.Millisecond)) % len(frames)
	fmt.Fprintf(b.p.Err, "\r\x1b[K  %s %s%s", paint(b.p.errColor, yellow, frames[i]), pad(Clean(b.label)), paint(b.p.errColor, dim, Elapsed(time.Since(b.started))))
}

func (b *Build) spin(stop, stopped chan struct{}) {
	defer close(stopped)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			b.mu.Lock()
			b.drawLocked()
			b.mu.Unlock()
		}
	}
}

// haltLocked stops the spinner and clears its line. The spinner goroutine
// takes b.mu to draw, so the lock is let go while it finishes.
func (b *Build) haltLocked() {
	if b.stop == nil {
		return
	}
	stop, stopped := b.stop, b.stopped
	b.stop, b.stopped = nil, nil
	close(stop)
	b.mu.Unlock()
	<-stopped
	b.mu.Lock()
	fmt.Fprint(b.p.Err, "\r\x1b[K")
}

// Close stops the spinner. Call it when the build ends, before printing more.
func (b *Build) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.label = ""
	b.haltLocked()
}
