// Package core holds skillm's presentation-free operations. Every entry point
// takes its inputs as parameters — an Options value, a context and a Reporter —
// and returns typed results and errors. core never prompts, never prints,
// never reads the working directory, the process's standard streams or the
// cmd flag globals: the terminal front-end in cmd and the JSON protocol are
// both thin layers over it. arch_test.go enforces the import side of this.
package core

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
}
