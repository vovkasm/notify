package test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/rjeczalik/notify"
)

// FuncType represents enums for notify.Watcher interface.
type FuncType string

const (
	Watch            = FuncType("Watch")
	Unwatch          = FuncType("Unwatch")
	Dispatch         = FuncType("Dispatch")
	Rewatch          = FuncType("Rewatch")
	RecursiveWatch   = FuncType("RecursiveWatch")
	RecursiveUnwatch = FuncType("RecursiveUnwatch")
	RecursiveRewatch = FuncType("RecursiveRewatch")
	Stop             = FuncType("Stop")
)

// RuntimeType TODO
type RuntimeType uint8

const (
	Watcher   RuntimeType = 1 << iota // implements Watcher only
	Rewatcher                         // implements Watcher + Rewatcher
	Recursive                         // implements Watcher + Rewatcher + Recursive
)

// All TODO
//
// NOTE(rjeczalik): For use only as a key of Record.
const All RuntimeType = Watcher | Rewatcher | Recursive

// Strings implements fmt.Stringer interface.
func (typ RuntimeType) String() string {
	switch typ {
	case Watcher:
		return "RuntimeWatcher"
	case Rewatcher:
		return "RuntimeRewatcher"
	case Recursive:
		return "RuntimeRecursive"
	}
	return "<unknown runtime type>"
}

// Chans is a utility function which gives slice of opened channels for
// notify.EventInfo type.
//
// Chans TODO
func Chans(v ...interface{}) []chan notify.EventInfo {
	switch n := len(v); n {
	case 0:
		panic("notify: called test.Chans with no arguments")
	case 1:
		switch v := v[0].(type) {
		case int:
			ch := make([]chan notify.EventInfo, v)
			for i := range ch {
				ch[i] = make(chan notify.EventInfo, 16)
			}
			return ch
		case chan notify.EventInfo:
			return []chan notify.EventInfo{v}
		default:
			panic("notify: called test.Chans with unsupported argument type")
		}
	default:
		ch := make([]chan notify.EventInfo, n)
		for i := range ch {
			c, ok := v[i].(chan notify.EventInfo)
			if !ok {
				panic("notify: called test.Chans with unsupported argument type")
			}
			ch[i] = c
		}
		return ch
	}
}

// Call represents single call to notify.Watcher issued by the notify.Runtime
// and recorded by a spy notify.Watcher mock.
//
// TODO(rjeczalik): Merge/embed notify.EventInfo here?
type Call struct {
	F  FuncType
	C  chan notify.EventInfo
	P  string       // regular Path argument and old path from RecursiveRewatch call
	NP string       // new Path argument from RecursiveRewatch call
	E  notify.Event // regular Event argument and old Event from a Rewatch call
	NE notify.Event // new Event argument from Rewatch call
}

// Event TODO
type Event struct {
	P string
	E notify.Event
}

// Event implements notify.EventInfo interface.
func (e Event) Event() notify.Event { return e.E }
func (e Event) Path() string        { return e.P }
func (e Event) IsDir() bool         { return isDir(e.P) }
func (e Event) String() string      { return e.E.String() + " - " + e.P }
func (e Event) Sys() interface{}    { return nil }

// Record TODO
type Record map[RuntimeType][]Call

// CallCase describes a single test case. Call describes either Watch or Stop
// invocation made on a Runtime, while Record describes all internal calls
// to the Watcher implementation made by Runtime during that call.
type CallCase struct {
	Call   Call   // call invoked on Runtime
	Record Record // intermediate calls recorded during above call
}

// EventCase TODO
type EventCase struct {
	Event    Event
	Receiver []chan notify.EventInfo // receiver only
}

type runtime struct {
	Spy
	runtime *notify.Runtime
	n       int
	ch      chan<- notify.EventInfo
}

func (rt *runtime) Dispatch(ch chan<- notify.EventInfo, _ <-chan struct{}) {
	rt.ch = ch
}

func (rt *runtime) invoke(call Call) error {
	switch call.F {
	case Watch:
		return rt.runtime.Watch(call.P, call.C, call.E)
	case Stop:
		rt.runtime.Stop(call.C)
		return nil
	}
	panic("test.(*runtime).invoke: invalid Runtime call: " + call.F)
}

// R represents a fixture for notify.Runtime testing.
type r struct {
	t *testing.T
	r map[RuntimeType]*runtime
}

// R gives new fixture for notify.Runtime testing. It initializes a new Runtime
// with a spy Watcher mock, which is used for retrospecting calls to it the Runtime
// makes.
func R(t *testing.T, types ...RuntimeType) *r {
	if len(types) == 0 {
		types = []RuntimeType{Watcher, Rewatcher, Recursive}
	}
	r := &r{
		t: t,
		r: make(map[RuntimeType]*runtime, len(types)),
	}
	for _, typ := range types {
		// TODO(rjeczalik): Copy FS to allow for modying tree via Create and
		// Delete events.
		rt := &runtime{}
		r.r[typ] = rt
		rt.runtime = notify.NewRuntimeWatcher(SpyWatcher(typ, rt), FS)
	}
	return r
}

// ExpectCalls translates values specified by the cases' keys into Watch calls
// executed on the fixture's Runtime. A spy Watcher mock records calls to it
// and compares with the expected ones for each key in the list.
func (r *r) ExpectCalls(cases []CallCase) {
	SlashCases(cases)
	var record []Call
	for i, cas := range cases {
		for typ, rt := range r.r {
			// Invoke call and record underlying calls.
			if err := rt.invoke(cas.Call); err != nil {
				r.t.Fatalf("want Runtime.%s(...)=nil; got %v (i=%d, typ=%v)",
					cas.Call.F, err, i, typ)
			}
			// Skip if expected and got records were empty.
			got := rt.Spy[rt.n:]
			if len(cas.Record) == 0 && len(got) == 0 {
				continue
			}
			// Find expected records for typ Runtime.
			if rec, ok := cas.Record[typ]; ok {
				record = rec
			} else {
				for rectyp, rec := range cas.Record {
					if rectyp&typ != 0 {
						record = rec
						break
					}
				}
			}
			if !reflect.DeepEqual(got, Spy(record)) {
				r.t.Errorf("want recorded=%+v; got %+v (i=%d, typ=%v)",
					record, got, i, typ)
			}
			rt.n = len(rt.Spy)
		}
	}
}

var timeout = 50 * time.Millisecond

func str(ei notify.EventInfo) string {
	if ei == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{Name()=%q, Event()=%v, IsDir()=%v}", ei.Path(), ei.Event(),
		ei.IsDir())
}

func equal(want, got notify.EventInfo) error {
	if (want == nil && got != nil) || (want != nil && got == nil) {
		return fmt.Errorf("want EventInfo=%s; got %s", str(want), str(got))
	}
	if want.Path() != got.Path() || want.Event() != got.Event() || want.IsDir() != got.IsDir() {
		return fmt.Errorf("want EventInfo=%s; got %s", str(want), str(got))
	}
	return nil
}

// ExpectEvents TODO
func (r *r) ExpectEvents(cases []EventCase) {
	SlashCases(cases)
	for i, cas := range cases {
		for typ, rt := range r.r {
			n := len(cas.Receiver)
			done := make(chan struct{})
			ev := make(map[<-chan notify.EventInfo]notify.EventInfo)
			go func() {
				cases := make([]reflect.SelectCase, n)
				for i := range cases {
					cases[i].Chan = reflect.ValueOf(cas.Receiver[i])
					cases[i].Dir = reflect.SelectRecv
				}
				for n := len(cases); n > 0; n = len(cases) {
					j, v, ok := reflect.Select(cases)
					if !ok {
						r.t.Fatalf("notify/test: unexpected chan close (i=%d, "+
							"typ=%v, j=%d", i, typ, j)
					}
					ch := cases[j].Chan.Interface().(chan notify.EventInfo)
					ev[ch] = v.Interface().(notify.EventInfo)
					cases[j], cases = cases[n-1], cases[:n-1]
				}
				close(done)
			}()
			rt.ch <- cas.Event
			select {
			case <-done:
			case <-time.After(timeout):
				r.t.Fatalf("ExpectEvents has timed out after %v (i=%d, typ=%v)",
					timeout, i, typ)
			}
			for _, got := range ev {
				if err := equal(cas.Event, got); err != nil {
					r.t.Errorf("want err=nil; got %v (i=%d, typ=%v)", err, i, typ)
				}
			}
		}
	}
}

// Spy is a mock for notify.Watcher interface, which records every call.
type Spy []Call

// SpyWatcher TODO
func SpyWatcher(typ RuntimeType, rt *runtime) notify.Watcher {
	switch typ {
	case Watcher:
		return struct {
			notify.Watcher
		}{rt}
	case Rewatcher:
		return struct {
			notify.Watcher
			notify.Rewatcher
		}{rt, rt}
	case Recursive:
		return struct {
			notify.Watcher
			notify.Rewatcher
			notify.RecursiveWatcher
			notify.RecursiveRewatcher
		}{rt, rt, rt, rt}
	}
	panic(fmt.Sprintf("notify/test: unsupported runtime type: %d (%s)", typ, typ.String()))
}

// Watch implements notify.Watcher interface.
func (s *Spy) Watch(p string, e notify.Event) (err error) {
	*s = append(*s, Call{F: Watch, P: p, E: e})
	return
}

// Unwatch implements notify.Watcher interface.
func (s *Spy) Unwatch(p string) (err error) {
	*s = append(*s, Call{F: Unwatch, P: p})
	return
}

// Rewatch implements notify.Rewatcher interface.
func (s *Spy) Rewatch(p string, olde, newe notify.Event) (err error) {
	*s = append(*s, Call{F: Rewatch, P: p, E: olde, NE: newe})
	return
}

// RecursiveWatch implements notify.RecursiveWatcher interface.
func (s *Spy) RecursiveWatch(p string, e notify.Event) (err error) {
	*s = append(*s, Call{F: RecursiveWatch, P: p, E: e})
	return
}

// RecursiveUnwatch implements notify.RecursiveWatcher interface.
func (s *Spy) RecursiveUnwatch(p string) (err error) {
	*s = append(*s, Call{F: RecursiveUnwatch, P: p})
	return
}

// RecursiveRewatch implements notify.RecursiveRewatcher interface.
func (s *Spy) RecursiveRewatch(oldp, newp string, olde, newe notify.Event) (err error) {
	*s = append(*s, Call{F: RecursiveRewatch, P: oldp, NP: newp, E: olde, NE: newe})
	return
}