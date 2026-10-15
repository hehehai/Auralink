package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hehehai/Auralink/internal/core"
	"github.com/hehehai/Auralink/internal/helper/proto"
)

// LogSource is the source of the helper's lines in the logs.
const LogSource = "helper"

// New returns a Supervisor for cfg. Nothing runs until Start.
func New(cfg Config) Supervisor {
	if cfg.HelloTimeout == 0 {
		cfg.HelloTimeout = 10 * time.Second
	}
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = 3
	}
	if cfg.RestartWindow == 0 {
		cfg.RestartWindow = 10 * time.Minute
	}
	if cfg.CancelGrace == 0 {
		cfg.CancelGrace = 3 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	return &supervisor{cfg: cfg, status: Status{State: Stopped, Since: time.Now()}}
}

type supervisor struct {
	cfg    Config
	ctrlID atomic.Int64

	mu       sync.Mutex // guards the fields below
	status   Status
	proc     *proc         // the running process; nil when none
	ready    chan struct{} // closed when the current start succeeded or failed
	restarts []time.Time   // automatic restarts within the window
	timer    *time.Timer   // the pending automatic restart
	stopped  bool
}

// proc is one helper process.
type proc struct {
	s      *supervisor
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	tail   *lineTail
	exited chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]*call
	closed  bool

	helloDone   atomic.Bool
	intentional atomic.Bool // stopped on purpose: no failure, no restart
	// Set before the process is killed for a reason of the supervisor.
	failReason FailReason
	failErr    string
	noRestart  bool
}

type call struct {
	method string
	notify func(proto.Message)
	done   chan struct{}
	resp   proto.Message
	err    error
}

func (c *call) result() (json.RawMessage, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.resp.Error != nil {
		return nil, c.resp.Error
	}
	return c.resp.Result, nil
}

func (s *supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = false
	idle := s.proc == nil && s.timer == nil
	s.mu.Unlock()
	if idle {
		s.spawn(false)
	}
	return nil
}

func (s *supervisor) publish(st Status) { s.cfg.Bus.Publish(StatusChanged{Status: st}) }

// statusLocked is a copy of the status to publish. Caller holds s.mu.
func (s *supervisor) statusLocked() Status {
	st := s.status
	if st.Hello != nil {
		h := *st.Hello
		h.Methods = slices.Clone(h.Methods)
		st.Hello = &h
	}
	if s.proc != nil {
		st.Running = s.proc.running()
	}
	return st
}

func (s *supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

// spawn starts a process. auto marks an automatic restart.
func (s *supervisor) spawn(auto bool) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	s.status.RestartAt = time.Time{}
	if auto {
		s.status.Restarts++
	}
	if s.ready == nil || isClosed(s.ready) {
		s.ready = make(chan struct{})
	}
	if s.cfg.Bin == "" {
		s.failLocked(FailMissing, BinName+" was not found: build it (helper/README.md), or set AURALINK_HELPER", "")
		st := s.statusLocked()
		s.mu.Unlock()
		s.publish(st)
		return
	}
	s.mu.Unlock()

	p, err := s.startProcess()
	s.mu.Lock()
	if err != nil {
		s.failLocked(FailStart, err.Error(), "")
		st := s.statusLocked()
		s.mu.Unlock()
		s.publish(st)
		return
	}
	if s.stopped {
		s.mu.Unlock()
		p.intentional.Store(true)
		p.kill()
		return
	}
	s.proc = p
	s.status.State, s.status.PID, s.status.Since = Starting, p.cmd.Process.Pid, time.Now()
	s.status.Err, s.status.Reason, s.status.StderrTail, s.status.Hello = "", "", "", nil
	st := s.statusLocked()
	s.mu.Unlock()
	s.publish(st)
	go s.handshake(p)
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// failLocked marks the helper failed and wakes callers waiting for it.
// Caller holds s.mu.
func (s *supervisor) failLocked(reason FailReason, msg, tail string) {
	s.status.State, s.status.Reason, s.status.Err, s.status.StderrTail = Failed, reason, msg, tail
	s.status.PID, s.status.Hello, s.status.Since = 0, nil, time.Now()
	if s.ready != nil && !isClosed(s.ready) {
		close(s.ready)
	}
}

func (s *supervisor) startProcess() (*proc, error) {
	cmd := exec.Command(s.cfg.Bin)
	cmd.Dir = filepath.Dir(s.cfg.Bin)
	cmd.Env = append(os.Environ(), s.cfg.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", s.cfg.Bin, err)
	}
	p := &proc{s: s, cmd: cmd, stdin: stdin, tail: newLineTail(40), exited: make(chan struct{}), pending: map[string]*call{}}
	var logFile *os.File
	if s.cfg.LogDir != "" {
		logFile = openLog(filepath.Join(s.cfg.LogDir, "helper.log"))
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			p.tail.add(line)
			if logFile != nil {
				fmt.Fprintln(logFile, line)
			}
			if strings.TrimSpace(line) != "" {
				s.emit(core.LogEntry{Time: time.Now(), Level: stderrLevel(line), Source: LogSource, Message: line})
			}
		}
	}()
	go func() {
		p.read(stdout)
		<-stderrDone
		err := cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		s.exited(p, err)
	}()
	return p, nil
}

// openLog opens the stderr log for appending, keeping one previous file
// once it passes 10 MB.
func openLog(path string) *os.File {
	if st, err := os.Stat(path); err == nil && st.Size() > 10<<20 {
		os.Rename(path, path+".1")
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return f
}

func (s *supervisor) handshake(p *proc) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.HelloTimeout)
	defer cancel()
	raw, err := p.call(ctx, "", proto.MethodHello, proto.HelloParams{Protocol: proto.Version, App: s.cfg.AppVersion, LogLevel: "info"}, nil)
	var hello proto.HelloResult
	if err == nil {
		err = json.Unmarshal(raw, &hello)
	}
	var perr *proto.Error
	switch {
	case p.isClosed():
		return // exited: exited reports it
	case errors.Is(err, context.DeadlineExceeded):
		p.killFor(FailHandshake, fmt.Sprintf("the helper did not answer helper.hello within %s", s.cfg.HelloTimeout), false)
		return
	case errors.As(err, &perr) && perr.Code == proto.CodeProtocolMismatch:
		p.killFor(FailProtocol, "helper version mismatch: rebuild "+BinName+" ("+perr.Message+")", true)
		return
	case err != nil:
		p.killFor(FailHandshake, "helper.hello failed: "+err.Error(), false)
		return
	case hello.Protocol != proto.Version:
		p.killFor(FailProtocol, fmt.Sprintf("helper version mismatch: rebuild %s (it speaks protocol %d; want %d)", BinName, hello.Protocol, proto.Version), true)
		return
	}
	p.helloDone.Store(true)
	s.mu.Lock()
	if s.proc != p {
		s.mu.Unlock()
		return
	}
	s.status.State, s.status.Since, s.status.Hello = Ready, time.Now(), &hello
	if hello.PID != 0 {
		s.status.PID = hello.PID
	}
	if !isClosed(s.ready) {
		close(s.ready)
	}
	st := s.statusLocked()
	s.mu.Unlock()
	s.publish(st)
}

// read routes the helper's stdout: responses to their calls, a job's
// notifications to its call's notify, others to OnNotify, log lines to
// the log.
func (p *proc) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			p.handleLine(line)
		}
		if err != nil {
			if err != io.EOF {
				p.tail.add("stdout: " + err.Error())
			}
			io.Copy(io.Discard, r) // drain, so the process is not blocked writing
			return
		}
	}
}

// readLine reads one line of at most proto.MaxLine bytes; a longer one is
// skipped.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(buf)+len(chunk) <= proto.MaxLine {
			buf = append(buf, chunk...)
		} else {
			buf = buf[:0:0]
		}
		if err != nil || !isPrefix {
			return buf, err
		}
	}
}

func (p *proc) handleLine(line []byte) {
	m, err := proto.Decode(line)
	if err != nil {
		p.tail.add("stdout: " + string(line))
		return
	}
	switch m.Kind() {
	case proto.KindResponse:
		p.mu.Lock()
		c := p.pending[m.ID]
		delete(p.pending, m.ID)
		p.mu.Unlock()
		if c != nil {
			c.resp = m
			close(c.done)
		}
	case proto.KindNotification:
		var ref struct {
			Job string `json:"job"`
		}
		m.DecodeParams(&ref)
		if m.Method == proto.MethodLog {
			p.log(m)
		}
		var c *call
		if ref.Job != "" {
			p.mu.Lock()
			c = p.pending[ref.Job]
			p.mu.Unlock()
		}
		switch {
		case c != nil:
			if c.notify != nil {
				c.notify(m)
			}
		case ref.Job != "":
			// A job that answered already (or was never ours): dropped.
		case m.Method != proto.MethodLog && p.s.cfg.OnNotify != nil:
			p.s.cfg.OnNotify(m)
		}
	}
}

func (p *proc) log(m proto.Message) {
	var lp proto.LogParams
	if m.DecodeParams(&lp) != nil {
		return
	}
	level := core.Severity(lp.Level)
	switch level {
	case core.SeverityDebug, core.SeverityInfo, core.SeverityWarning, core.SeverityError:
	default:
		level = core.SeverityInfo
	}
	p.s.emit(core.LogEntry{Time: time.Now(), Level: level, Source: LogSource, Message: lp.Message})
}

// emit hands a log line to Config.Log, or the bus.
func (s *supervisor) emit(e core.LogEntry) {
	if s.cfg.Log != nil {
		s.cfg.Log(e)
		return
	}
	s.cfg.Bus.Publish(core.LogAppended{Entry: e})
}

// stderrLevel guesses the level of a free-form stderr line.
func stderrLevel(line string) core.Severity {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "fatal") || strings.Contains(l, "error"):
		return core.SeverityError
	case strings.Contains(l, "warning"):
		return core.SeverityWarning
	}
	return core.SeverityInfo
}

func (p *proc) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *proc) running() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []string
	for id, c := range p.pending {
		if proto.IsJob(c.method) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// killFor kills the process for a reason of the supervisor's; exited
// reports it (and restarts it unless noRestart).
func (p *proc) killFor(reason FailReason, msg string, noRestart bool) {
	p.mu.Lock()
	if p.failReason == "" {
		p.failReason, p.failErr, p.noRestart = reason, msg, noRestart
	}
	p.mu.Unlock()
	p.kill()
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
}

func (p *proc) write(m proto.Message) error {
	line, err := proto.Encode(m)
	if err != nil {
		return err
	}
	if len(line) > proto.MaxLine {
		return fmt.Errorf("request of %d bytes is over the protocol's limit", len(line))
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdin.Write(line); err != nil {
		return fmt.Errorf("writing to the helper: %v: %w", err, ErrExited)
	}
	return nil
}

// call sends a request and waits for its response (see Supervisor.Call).
func (p *proc) call(ctx context.Context, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	if id == "" {
		id = fmt.Sprintf("c%d", p.s.ctrlID.Add(1))
	}
	req, err := proto.NewRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("%s params: %w", method, err)
	}
	c := &call{method: method, notify: notify, done: make(chan struct{})}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrExited
	}
	if _, dup := p.pending[id]; dup {
		p.mu.Unlock()
		return nil, fmt.Errorf("request id %s is already in flight: %w", id, core.ErrInvalid)
	}
	p.pending[id] = c
	p.mu.Unlock()
	if err := p.write(req); err != nil {
		p.drop(id)
		return nil, err
	}

	select {
	case <-c.done:
		return c.result()
	case <-ctx.Done():
	}
	if !proto.IsJob(method) {
		p.drop(id)
		return nil, ctx.Err()
	}
	// Ask the helper to stop the job; it answers the job with "cancelled".
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), p.s.cfg.CancelGrace)
		defer cancel()
		p.call(cctx, "", proto.MethodJobCancel, proto.CancelParams{Job: id}, nil)
	}()
	grace := time.NewTimer(p.s.cfg.CancelGrace)
	defer grace.Stop()
	select {
	case <-c.done:
	case <-grace.C:
		p.s.cfg.Logf("helper: job %s did not stop within %s of being cancelled: restarting the helper", id, p.s.cfg.CancelGrace)
		p.killFor(FailStuck, fmt.Sprintf("job %s did not stop within %s of being cancelled", id, p.s.cfg.CancelGrace), false)
		<-c.done
	}
	return c.result()
}

func (p *proc) drop(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// exited handles the end of a process: its calls fail, and an unexpected
// exit marks the helper failed and plans a restart.
func (s *supervisor) exited(p *proc, waitErr error) {
	p.mu.Lock()
	p.closed = true
	calls := p.pending
	p.pending = map[string]*call{}
	reason, msg, noRestart := p.failReason, p.failErr, p.noRestart
	p.mu.Unlock()
	close(p.exited)
	for _, c := range calls {
		c.err = ErrExited
		close(c.done)
	}

	s.mu.Lock()
	if s.proc != p {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	if p.intentional.Load() {
		s.mu.Unlock()
		return
	}
	if reason == "" {
		reason, msg = FailCrashed, "the helper exited"
		if waitErr != nil {
			msg += ": " + waitErr.Error()
		}
	}
	s.failLocked(reason, msg, p.tail.String())
	s.cfg.Logf("helper: %s", msg)
	if !noRestart && !s.stopped {
		now := time.Now()
		s.restarts = slices.DeleteFunc(s.restarts, func(t time.Time) bool { return now.Sub(t) > s.cfg.RestartWindow })
		if len(s.restarts) < s.cfg.MaxRestarts {
			delay := min(time.Second<<len(s.restarts), 30*time.Second)
			s.restarts = append(s.restarts, now)
			s.status.RestartAt = now.Add(delay)
			s.timer = time.AfterFunc(delay, func() { s.spawn(true) })
			s.ready = make(chan struct{}) // calls wait for the restart
		}
	}
	st := s.statusLocked()
	s.mu.Unlock()
	s.publish(st)
}

// readyProc waits for the helper to be ready and returns its process. A
// helper that is starting, or failed with a restart planned, is waited for
// (within ctx); a stopped one, or a failed one that will not restart, is an
// error at once.
func (s *supervisor) readyProc(ctx context.Context) (*proc, error) {
	for {
		s.mu.Lock()
		st, p, ready := s.status, s.proc, s.ready
		s.mu.Unlock()
		switch st.State {
		case Ready:
			if p != nil {
				return p, nil
			}
		case Starting, Failed:
			if st.State == Failed && st.RestartAt.IsZero() {
				break
			}
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		msg := string(st.State)
		if st.Err != "" {
			msg = st.Err
		}
		return nil, fmt.Errorf("the helper is not ready (%s): %w", msg, core.ErrHelper)
	}
}

func (s *supervisor) Call(ctx context.Context, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	p, err := s.readyProc(ctx)
	if err != nil {
		return nil, err
	}
	return p.call(ctx, id, method, params, notify)
}

func (s *supervisor) Restart() error {
	s.mu.Lock()
	s.stopped = false
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.restarts = nil
	s.status.Restarts = 0
	p := s.proc
	s.proc = nil
	// Callers wait for the new process instead of failing.
	s.status.State, s.status.Since = Starting, time.Now()
	if s.ready == nil || isClosed(s.ready) {
		s.ready = make(chan struct{})
	}
	s.mu.Unlock()
	if p != nil {
		p.intentional.Store(true)
		p.kill()
		<-p.exited
	}
	s.spawn(false)
	return nil
}

func (s *supervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	p := s.proc
	if p != nil {
		p.intentional.Store(true)
	}
	s.proc = nil
	s.status.State, s.status.PID, s.status.Hello = Stopped, 0, nil
	s.status.RestartAt, s.status.Since = time.Time{}, time.Now()
	if s.ready != nil && !isClosed(s.ready) {
		close(s.ready)
	}
	st := s.statusLocked()
	s.mu.Unlock()
	if p != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		p.call(ctx, "", proto.MethodShutdown, nil, nil)
		cancel()
		p.stdin.Close()
		select {
		case <-p.exited:
		case <-time.After(3 * time.Second):
			p.kill()
			<-p.exited
		}
	}
	s.publish(st)
}

// lineTail keeps the last lines written to it.
type lineTail struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func newLineTail(max int) *lineTail { return &lineTail{max: max} }

func (t *lineTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
