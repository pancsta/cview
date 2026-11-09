package tcelllivepage

// EnvLivepageChild marks the child process of the livepage bridge, which runs
// the regular TTY command on a PTY.
const EnvLivepageChild = "AM_LIVEPAGE_CHILD"

// CmdOpts configures [RunCmd].
type CmdOpts struct {
	// Title is the title of the default HTML page. Optional.
	Title string
	// Addr is the HTTP listen address.
	Addr string
	// Printf logs the livepage URL and server errors. Optional.
	Printf func(msg string, args ...any)
}
