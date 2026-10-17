package actnow

import (
	"runtime"

	"github.com/hehehai/actnowkit/registry"
)

// System is discovery's view of macOS (registry.DefaultSystem), each call
// on one OS thread. actnowkit v0.2.0 asks AppKit inside an autorelease
// pool without locking the thread, and a goroutine may move to another
// thread between the pool's push and pop, which crashes (Auralink's own
// macx.Frontmost did, from a goroutine). Auralink discovers on a
// goroutine, so it locks the thread around each call.
var System registry.System = threadSystem{registry.DefaultSystem}

type threadSystem struct{ sys registry.System }

func (s threadSystem) AppPaths(bundleID string) []string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return s.sys.AppPaths(bundleID)
}

func (s threadSystem) Running(bundleID string) []registry.Process {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return s.sys.Running(bundleID)
}

func (s threadSystem) BundleID(appPath string) (string, error) { return s.sys.BundleID(appPath) }

// teamID is registry.TeamID on one OS thread (see System).
func teamID() string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return registry.TeamID()
}
