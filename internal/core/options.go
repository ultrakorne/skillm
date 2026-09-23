// Package core holds skillm's presentation-free operations. Every entry point
// takes its inputs as parameters — an Options value, a context and a Reporter —
// and returns typed results and errors. core never prompts, never prints,
// never reads the working directory, the process's standard streams or the
// cmd flag globals: the terminal front-end in cmd and the JSON protocol are
// both thin layers over it. arch_test.go enforces the import side of this.
package core

import "context"

// Options carries what every operation needs from its caller: the resolved
// Home, the directory the operation treats as current (for Local scope), and
// the caller's answers to the safety questions cmd would otherwise prompt for.
type Options struct {
	// Home is the resolved Home directory (see store.Home).
	Home string
	// Cwd is the directory treated as the current project. Operations that
	// do not look at the current directory ignore it, so it may be empty.
	Cwd string
	// Force permits overwriting entries skillm did not create.
	Force bool
	// Yes answers "yes" to every confirmation the operation would need.
	Yes bool
	// Lock takes Home's cross-process lock and returns its release. The
	// operations that fetch before they write (Update, Import and
	// AutoImportTrackedRoots) call it around their write phases only, so a
	// slow network fetch never holds the lock; core never locks Home any
	// other way. Nil means the caller already holds the lock for the whole
	// call. The other operations never call it: their caller holds the lock.
	Lock func(ctx context.Context) (unlock func(), err error)
}

// lock takes Home's lock through o.Lock, or does nothing when the caller
// holds it already (o.Lock is nil).
func (o Options) lock(ctx context.Context) (unlock func(), err error) {
	if o.Lock == nil {
		return func() {}, nil
	}
	return o.Lock(ctx)
}
