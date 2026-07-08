package ui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/zhivko-kocev/friday/internal/output"
)

// WithSpinner runs fn while showing an animated spinner titled msg. On a
// non-interactive terminal it prints one info line and runs fn directly — no
// motion, nothing that could corrupt piped output. Reserved for genuinely slow
// synchronous work (network git operations), never decoration.
//
// It is built on bubbles/spinner (a stable, tagged dependency friday already
// carries) rather than huh's spinner subpackage, which is only published as an
// untagged pseudo-version.
func WithSpinner(msg string, fn func() error) error {
	if !Interactive() {
		output.Info("%s", msg)
		return fn()
	}

	// fn runs in its own goroutine; closing done both quits the Bubble Tea
	// program (via waitForClose) and lets the caller read fn's error after the
	// UI tears down. A buffered result keeps the goroutine from blocking.
	var ferr error
	done := make(chan struct{})
	go func() {
		ferr = fn()
		close(done)
	}()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	_, runErr := tea.NewProgram(spinnerModel{sp: sp, title: " " + msg, done: done}).Run()
	<-done // fn finished (close broadcasts to every receiver) → ferr is set
	if runErr != nil {
		return runErr
	}
	return ferr
}

type spinnerModel struct {
	sp    spinner.Model
	title string
	done  <-chan struct{}
}

type spinnerDoneMsg struct{}

func (m spinnerModel) Init() tea.Cmd {
	return tea.Batch(m.sp.Tick, waitForClose(m.done))
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(spinnerDoneMsg); ok {
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.sp, cmd = m.sp.Update(msg)
	return m, cmd
}

func (m spinnerModel) View() string { return "  " + m.sp.View() + m.title + "\n" }

// waitForClose turns fn's completion (done closed) into a Bubble Tea message.
func waitForClose(done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-done
		return spinnerDoneMsg{}
	}
}
