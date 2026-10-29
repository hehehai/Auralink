package logs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/Auralink/internal/core"
)

func TestRing(t *testing.T) {
	bus := core.NewBus()
	n := 0
	core.On(bus, func(core.LogAppended) { n++ })
	r := New(Options{Size: 4, Bus: bus})
	defer r.Close()
	for i := range 6 {
		src := "app"
		if i%2 == 1 {
			src = "helper:capture"
		}
		r.Append(core.LogEntry{Source: src, Message: fmt.Sprint(i)})
	}
	if n != 6 {
		t.Errorf("published %d", n)
	}
	msgs := func(es []core.LogEntry) string {
		var s []string
		for _, e := range es {
			s = append(s, e.Message)
		}
		return strings.Join(s, ",")
	}
	if got := msgs(r.Tail(10, "")); got != "2,3,4,5" {
		t.Errorf("all %s", got)
	}
	if got := msgs(r.Tail(1, "")); got != "5" {
		t.Errorf("newest %s", got)
	}
	if got := msgs(r.Tail(10, "helper:")); got != "3,5" {
		t.Errorf("helper %s", got)
	}
	if got := msgs(r.Tail(10, "app")); got != "2,4" {
		t.Errorf("app %s", got)
	}
	if e := r.Tail(1, "")[0]; e.Level != core.SeverityInfo || e.Time.IsZero() {
		t.Errorf("defaults %+v", e)
	}
	// Seq numbers the lines from 1, whatever the ring dropped.
	var seqs []uint64
	for _, e := range r.Tail(10, "") {
		seqs = append(seqs, e.Seq)
	}
	if fmt.Sprint(seqs) != "[3 4 5 6]" {
		t.Errorf("seqs %v", seqs)
	}
}

func TestFilesRotate(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Dir: dir, MaxFileBytes: 300, Keep: 2})
	l := log.New(r.Writer("app"), "", 0)
	for i := range 20 {
		l.Printf("line %02d with some padding to fill the file", i)
	}
	l.Print("an error happened")
	r.Close()
	if r.Tail(1, "")[0].Level != core.SeverityError {
		t.Error("error level not detected")
	}
	cur, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil || !strings.Contains(string(cur), "an error happened") {
		t.Fatalf("current %q %v", cur, err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".2")); err != nil {
		t.Error("no second rotation:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".3")); err == nil {
		t.Error("kept more than 2 rotated files")
	}
	for _, name := range []string{FileName, FileName + ".1"} {
		st, _ := os.Stat(filepath.Join(dir, name))
		if st.Size() > 300 {
			t.Errorf("%s is %d bytes", name, st.Size())
		}
	}
	r.Close() // twice is fine
}

// The log file's lines read back (ReadTail) and put before a run's lines
// (Preload): what the logs page shows after a restart.
func TestReadTailAndPreload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.Local)
	var b strings.Builder
	for i := range 3000 {
		src := "app"
		if i%3 == 1 {
			src = "helper:capture"
		}
		b.WriteString(Format(core.LogEntry{Time: base.Add(time.Duration(i) * time.Second), Level: core.SeverityInfo, Source: src, Message: fmt.Sprintf("line %04d", i)}) + "\n")
	}
	b.WriteString(Format(core.LogEntry{Time: base.Add(3000 * time.Second), Level: core.SeverityWarning, Source: "app", Message: "a warning"}) + "\n")
	b.WriteString("a second line of a message, not written by Format\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadTail(path, 1<<20, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2000 {
		t.Fatalf("%d lines, want the newest 2000", len(got))
	}
	if last := got[len(got)-1]; last.Message != "a second line of a message, not written by Format" || last.Source != "app" {
		t.Errorf("last line %+v", last)
	}
	if prev := got[len(got)-2]; prev.Message != "a warning" || prev.Level != core.SeverityWarning || !prev.Time.Equal(base.Add(3000*time.Second)) {
		t.Errorf("warning line %+v", prev)
	}
	if first := got[0]; first.Message != "line 1002" || !first.Time.Equal(base.Add(1002*time.Second)) {
		t.Errorf("first line %+v", first)
	}
	if e := got[1]; e.Message != "line 1003" || e.Source != "helper:capture" {
		t.Errorf("helper line %+v", e)
	}

	// The byte limit counts first: the line it cuts is dropped, not kept half.
	small, err := ReadTail(path, 300, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(small) == 0 || len(small) > 6 || small[0].Message[:4] != "line" {
		t.Errorf("300 bytes: %d lines, first %+v", len(small), small[0])
	}
	if _, err := ReadTail(filepath.Join(dir, "none.log"), 1<<20, 10); !os.IsNotExist(err) {
		t.Errorf("a missing file: %v", err)
	}

	bus := core.NewBus()
	published := 0
	core.On(bus, func(core.LogAppended) { published++ })
	r := New(Options{Size: 10, Bus: bus}) // memory only: nothing is written
	r.Preload(got[len(got)-3:])
	r.Append(core.LogEntry{Source: "app", Message: "this run"})
	if published != 1 {
		t.Errorf("published %d, want only this run's line", published)
	}
	lines := r.Tail(10, "")
	if len(lines) != 4 || lines[0].Message != "line 2999" || lines[3].Message != "this run" || lines[3].Seq != 4 {
		t.Errorf("ring %+v", lines)
	}
}

func TestParseRejects(t *testing.T) {
	for _, l := range []string{"", "not a log line", "2026-10-29 09:14:02.118 BOGUS   app  x", "2026-10-29 09:14:02.118"} {
		if _, ok := Parse(l); ok {
			t.Errorf("parsed %q", l)
		}
	}
}
