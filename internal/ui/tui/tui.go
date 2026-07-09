// Package tui is friday's interactive control room: the full-screen bubbletea
// Program that bare `friday` launches on a real terminal. It is a frontend over
// the existing engine and verbs, never a new one — it drives engine.Push/Pull/
// Import directly and renders their []engine.Change, so the plain-text CLI path
// stays byte-identical and untouched.
//
// The control room owns all of its selection UI with bubbles components; it
// never calls the huh-backed prompts in internal/ui (each runs its own
// tea.Program and would deadlock nested inside this one).
package tui

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/presets"
	"github.com/zhivko-kocev/friday/internal/setupcmd"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

// screen is the control room's current view. Navigation is a flat state
// machine — the surface is shallow and every screen returns to home via esc.
type screen int

const (
	screenHome screen = iota
	screenColdStart
	screenSyncPick
	screenSetupAgent
	screenSetupItems
	screenDiscoverPick
	screenShareInput
	screenShareConfirm
	screenRunning
	screenConflict
	screenChanges
	screenError
	screenHelp
)

// MenuEntry is one command shown on the home screen. The caller (cli) builds
// these from its command table so the names and summaries stay the single
// source of truth; the control room maps a known name to a native action.
type MenuEntry struct {
	Name    string
	Summary string
}

// Run starts the control room and blocks until the user quits. It is only
// reached from bare `friday` on a real terminal (cli gates on ui.Interactive);
// piped/CI/--no-interactive never get here and keep the plain path. It returns
// the process exit code: 0 on a clean quit, 1 if the Program itself errored.
func Run(version string, menu []MenuEntry, cfg *config.Config, installed []string, loadErr error,
	coldStart bool, reload func() (*config.Config, []string, error)) int {
	m := newModel(version, menu, cfg, installed, loadErr)
	m.reload = reload
	// A missing/empty ~/.friday opens the control room on the cold-start input
	// instead of the error screen (loadErr is ignored in that case).
	if coldStart {
		m.screen = screenColdStart
		m.input.Focus()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.send.set(p.Send) // wire the conflict bridge before the loop starts
	final, err := p.Run()
	if err != nil {
		return 1
	}
	if fm, ok := final.(model); ok {
		return fm.exit
	}
	return 0
}

type model struct {
	screen screen
	width  int
	height int
	styles styles

	cfg       *config.Config
	loadErr   error
	installed []string // adapter names whose target dir exists (from cli)
	cwd       string   // project dir the control room was launched in (for setup)
	send      *sender  // event-loop Send handle for the conflict bridge
	// reload re-runs the store load after cold-start clones/scaffolds ~/.friday
	// (loadUserOrDefault lives in cli, which the TUI can't import). Set by Run.
	reload func() (*config.Config, []string, error)
	input  textinput.Model // cold-start store-URL entry

	menu        list.Model
	agents      list.Model // agent picker for setup
	pick        checklist
	catalog     []setupcmd.Item // the store catalog behind the current setup checklist
	discovered  []engine.Change // importable target-only files behind the discover checklist
	setupAgent  string          // the agent chosen on screenSetupAgent
	shareMsg    string          // the commit message entered for share, carried to confirm
	shareOrigin string          // the store's origin URL, captured once for the confirm screen
	vp          viewport.Model
	sp          spinner.Model

	// pending, when non-nil, means screenChanges is showing a dry-run PREVIEW:
	// enter re-runs the same operation with apply=true. One closure captures
	// whatever the op needs (push adapters, setup agent + items), so the
	// preview → confirm → apply machinery is shared across every write flow.
	pending applyFunc

	// Conflict-resolution state for an in-flight apply. activeBridge is non-nil
	// only between apply-start and engineDoneMsg; conflict is set only while the
	// modal is up; aborting guards a single close of the bridge's abort channel.
	activeBridge *bridge
	conflict     *conflictState
	aborting     bool

	result     []engine.Change
	opErr      error    // a genuine failure from the last op (rendered as an error)
	notice     string   // an informational message (rendered dim)
	warnings   []string // non-fatal advisories from an apply (drift-save, snapshot)
	applied    bool     // the last result came from a real apply (show confirmation)
	showDiff   bool     // changes screen: `d` toggles per-file diffs
	helpReturn screen   // the screen to return to when the help overlay closes
	// diffCache memoizes the changes-screen body per showDiff for the current
	// result, so toggling `d` doesn't recompute every file's line diff. It's a
	// reference type shared across the model's value copies; toChangesScreen
	// resets it when a new result lands.
	diffCache map[bool]string
	exit      int
}

// engineDoneMsg carries the result of an engine call back onto the Update
// goroutine. Long/engine work runs inside a tea.Cmd (off Update), mirroring the
// goroutine→msg bridge in internal/ui/spinner.go.
type engineDoneMsg struct {
	changes  []engine.Change
	err      error
	applied  bool     // false = a dry-run preview; true = a real apply
	warnings []string // advisories collected in the Cmd goroutine (never model fields)
}

// applyFunc produces the engine command for a previewed operation. apply=false
// is the dry-run preview (br is nil); apply=true commits, with br carrying the
// conflict-resolution bridge (nil br on apply means report-only). The closure
// captures everything else the op needs, so the flow is op-agnostic.
type applyFunc func(apply bool, br *bridge) tea.Cmd

func newModel(version string, menu []MenuEntry, cfg *config.Config, installed []string, loadErr error) model {
	items := make([]list.Item, len(menu))
	for i, e := range menu {
		items[i] = commandItem{name: e.Name, summary: e.Summary}
	}

	st := newStyles()
	const w, h = 80, 24 // sane defaults until the first WindowSizeMsg arrives

	l := newList("friday "+version, items, w, h, st)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	in := textinput.New()
	in.Placeholder = "git@github.com:you/ai-config.git"
	in.Prompt = "> "

	start, exit := screenHome, 0
	if loadErr != nil {
		// A genuine load failure (store present but broken) mirrors the plain
		// path's exit 1. An absent/empty store instead opens cold-start (Run),
		// where loadErr is nil, so exit stays 0.
		start, exit = screenError, 1
	}

	cwd, _ := os.Getwd() // "" is guarded at the setup call site (see selectCommand)

	return model{
		screen:    start,
		exit:      exit,
		width:     w,
		height:    h,
		styles:    st,
		cfg:       cfg,
		loadErr:   loadErr,
		installed: installed,
		cwd:       cwd,
		send:      &sender{},
		input:     in,
		menu:      l,
		agents:    newList("", nil, w, h, st), // valid empty list; populated when setup opens
		vp:        viewport.New(w, h-verticalChrome),
		sp:        sp,
	}
}

// verticalChrome is the number of rows the title and footer reserve around a
// scrolling body (viewport / list).
const verticalChrome = 4

// pickVerb labels the confirm action on each checklist screen (package-level so
// View doesn't rebuild the map every frame).
var pickVerb = map[screen]string{
	screenSyncPick:     "sync",
	screenSetupItems:   "apply",
	screenDiscoverPick: "import",
}

// listHeight is how many checklist rows fit, reserving space for the title,
// scroll indicators, and footer.
func (m model) listHeight() int {
	if h := m.height - 7; h > 3 {
		return h
	}
	return 3
}

// newList builds a bubbles list styled like the rest of the control room: a
// themed title and none of list's built-in help/status/filter chrome (the
// control room owns navigation and the footer).
func newList(title string, items []list.Item, w, h int, st styles) list.Model {
	l := list.New(items, list.NewDefaultDelegate(), w, h-verticalChrome)
	l.Title = title
	l.Styles.Title = st.title
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	return l
}

func (m model) Init() tea.Cmd {
	if m.screen == screenColdStart {
		return textinput.Blink // start the cursor blinking on the cold-start input
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.menu.SetSize(msg.Width, msg.Height-verticalChrome)
		m.agents.SetSize(msg.Width, msg.Height-verticalChrome)
		m.vp.Width = msg.Width
		m.vp.Height = msg.Height - verticalChrome
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case needConflictMsg:
		// If the user is already cancelling (or the apply has finished), don't
		// pop the modal for a conflict the resolver has fast-skipped — just make
		// sure the resolver isn't left waiting.
		if m.aborting || m.activeBridge == nil {
			select {
			case msg.reply <- engine.Resolution{Choice: engine.ConflictSkip}:
			default:
			}
			return m, nil
		}
		// Render the modal once now (info.BaseContent is already resolved by the
		// engine via BaseLookup); View returns this cached string so the line diff
		// isn't recomputed on every keystroke while the modal is up.
		cs := &conflictState{info: msg.info, reply: msg.reply}
		cs.body = renderConflict(cs.info, cs.info.BaseContent, m.styles)
		m.conflict = cs
		m.screen = screenConflict
		return m, nil

	case engineDoneMsg:
		m.result, m.opErr, m.notice = msg.changes, msg.err, ""
		m.warnings = msg.warnings
		// A cancelled apply is not a success: don't show the green "✓ applied"
		// confirmation, and note what happened.
		m.applied = msg.applied && msg.err == nil && !m.aborting
		if m.aborting && msg.err == nil {
			m.notice = "cancelled — some changes were skipped"
			m.result = nil
		}
		m.aborting = false
		m.activeBridge = nil // the apply (if any) has finished; ctrl+c quits again
		m.conflict = nil
		// A dry-run preview keeps pending set (enter will apply it); a completed
		// apply (or any status/errored run) clears it, making the changes screen
		// terminal.
		if msg.applied || msg.err != nil {
			m.pending = nil
		}
		return m.toChangesScreen()

	case catalogReadyMsg:
		switch {
		case msg.err != nil:
			m.opErr = msg.err
			return m.toChangesScreen()
		case msg.empty:
			m.notice = "store has nothing to apply — add core.md, rules/, skills/, …"
			return m.toChangesScreen()
		}
		m.setupAgent = msg.agent
		m.catalog = msg.items
		m.pick = newChecklist("setup — apply what to this project? (agent: "+msg.agent+")", msg.choices)
		m.screen = screenSetupItems
		return m, nil

	case discoverReadyMsg:
		switch {
		case msg.err != nil:
			m.opErr = msg.err
			return m.toChangesScreen()
		case msg.empty:
			m.notice = "nothing new to import — every agent file is already in your store"
			return m.toChangesScreen()
		}
		m.discovered = msg.changes
		m.pick = newChecklist("discover — import which files into your store?", msg.choices)
		m.screen = screenDiscoverPick
		return m, nil

	case coldStartDoneMsg:
		if msg.err != nil {
			// Stay on the input so a mistyped URL can be fixed in place.
			m.opErr = msg.err
			m.screen = screenColdStart
			m.input.Focus()
			return m, textinput.Blink
		}
		m.cfg, m.installed, m.loadErr, m.opErr = msg.cfg, msg.installed, nil, nil
		m.notice = msg.advisory // e.g. "scaffolded without git" — shown on home
		m.screen = screenHome
		return m, nil

	case shareDoneMsg:
		m.result, m.applied, m.warnings, m.opErr = nil, false, nil, msg.err
		if msg.err == nil {
			m.notice = msg.notice // MR/PR link + "local store untouched until the MR merges"
		}
		return m.toChangesScreen()

	case spinner.TickMsg:
		if m.screen == screenRunning {
			var cmd tea.Cmd
			m.sp, cmd = m.sp.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// Forward cursor-blink and other non-key messages to the active text input.
	if m.screen == screenColdStart || m.screen == screenShareInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	// Forward everything else to the active scrolling component.
	var cmd tea.Cmd
	switch m.screen {
	case screenHome:
		m.menu, cmd = m.menu.Update(msg)
	case screenChanges:
		m.vp, cmd = m.vp.Update(msg)
	}
	return m, cmd
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		// While an apply is in flight, ctrl+c cancels the operation (so the
		// engine unwinds to store.Save() and drift stays consistent) rather than
		// killing the app mid-write. A second ctrl+c on the results screen quits.
		if m.activeBridge != nil {
			return m.abortApply()
		}
		return m, tea.Quit
	}

	// Help overlay: any key closes it (back to where it opened); `?` opens it
	// from any screen except the text inputs (where `?` is a literal character)
	// and the in-flight/modal screens — during an apply or a conflict prompt only
	// ctrl+c acts, and opening help there would strand the running spinner's tick.
	if m.screen == screenHelp {
		m.screen = m.helpReturn
		return m, nil
	}
	if msg.String() == "?" && m.screen != screenColdStart && m.screen != screenShareInput &&
		m.screen != screenRunning && m.screen != screenConflict {
		m.helpReturn = m.screen
		m.screen = screenHelp
		return m, nil
	}

	switch m.screen {
	case screenColdStart:
		switch msg.Type {
		case tea.KeyEsc:
			return m, tea.Quit // no store yet — nothing to fall back to
		case tea.KeyEnter:
			url := strings.TrimSpace(m.input.Value())
			m.opErr = nil
			m.screen = screenRunning
			return m, tea.Batch(m.sp.Tick, coldStartCmd(url, m.reload))
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case screenShareInput:
		switch msg.Type {
		case tea.KeyEsc:
			m.screen = screenHome
			return m, nil
		case tea.KeyEnter:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil // a commit message is required
			}
			m.shareMsg = text
			m.screen = screenShareConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case screenShareConfirm:
		switch msg.Type {
		case tea.KeyEsc:
			m.screen = screenShareInput // back to edit the message
			m.input.Focus()
			return m, textinput.Blink
		case tea.KeyEnter:
			// Confirmed: publish (pushes a proposal branch + opens an MR).
			m.screen = screenRunning
			return m, tea.Batch(m.sp.Tick, shareCmd(m.cfg.StoreDir, m.shareMsg))
		}
		return m, nil

	case screenHome:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "enter":
			return m.selectCommand()
		}
		var cmd tea.Cmd
		m.menu, cmd = m.menu.Update(msg)
		return m, cmd

	case screenSyncPick, screenSetupItems, screenDiscoverPick:
		switch msg.String() {
		case "esc", "b":
			m.screen = screenHome
			return m, nil
		case "enter":
			return m.confirmPick()
		}
		m.pick = m.pick.update(msg)
		return m, nil

	case screenSetupAgent:
		switch msg.String() {
		case "esc", "b":
			m.screen = screenHome
			return m, nil
		case "enter":
			return m.chooseSetupAgent()
		}
		var cmd tea.Cmd
		m.agents, cmd = m.agents.Update(msg)
		return m, cmd

	case screenConflict:
		if m.conflict == nil {
			return m, nil
		}
		switch msg.String() {
		case "k":
			return m.resolve(engine.ConflictKeepCanonical)
		case "t":
			return m.resolve(engine.ConflictTakeTarget)
		case "s", "esc":
			return m.resolve(engine.ConflictSkip)
		}
		return m, nil

	case screenChanges:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "esc", "b":
			m.pending = nil
			m.showDiff = false
			m.screen = screenHome
			return m, nil
		case "d":
			// Toggle per-file diffs and re-render the viewport in place.
			m.showDiff = !m.showDiff
			m.vp.SetContent(m.body())
			return m, nil
		case "enter":
			// In preview mode, enter commits the previewed operation.
			if m.pending != nil {
				return m.startApply(m.pending)
			}
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case screenError:
		return m, tea.Quit

	case screenRunning:
		// ctrl+c (handled above) is the only key that does anything while an op
		// is in flight; ignore the rest until the result arrives.
		return m, nil
	}
	return m, nil
}

// HandlesCommand reports whether selectCommand has a real branch for a menu
// entry of this name. Keep the cases in sync with selectCommand's switch below;
// launchTUI's guard test cross-checks this against porcelainMenu() so a new
// porcelain command can't ship as a tile that does nothing when selected.
func HandlesCommand(name string) bool {
	switch name {
	case "status", "sync", "setup", "share", "discover":
		return true
	}
	return false
}

// selectCommand fires the native action for the highlighted home entry. Every
// menu tile — sync, setup, status, share, discover — is wired end-to-end here.
func (m model) selectCommand() (tea.Model, tea.Cmd) {
	it, ok := m.menu.SelectedItem().(commandItem)
	if !ok {
		return m, nil
	}
	// selectCommand runs only from home, where the store is loaded (a load error
	// routes to screenError, cold-start to screenColdStart) — so cfg is non-nil.
	// The guard is a single defensive backstop rather than one per branch.
	if m.cfg == nil {
		return m, nil
	}
	// Every selection starts from a clean result state so a prior op's result,
	// error, confirmation, or advisories can't bleed onto the next screen.
	// Engine paths repopulate these via engineDoneMsg.
	m.result, m.opErr, m.notice, m.warnings, m.applied = nil, nil, "", nil, false
	switch it.name {
	case "status":
		m.screen = screenRunning
		return m, tea.Batch(m.sp.Tick, statusCmd(m.cfg))
	case "sync":
		if len(m.installed) == 0 {
			m.notice = "no installed agents detected — nothing to push"
			return m.toChangesScreen()
		}
		items := make([]checklistItem, len(m.installed))
		for i, name := range m.installed {
			items[i] = checklistItem{label: name, value: name, checked: true}
		}
		m.pick = newChecklist("sync — capture edits & fan out to which agents?", items)
		m.screen = screenSyncPick
		return m, nil
	case "setup":
		if m.cwd == "" {
			m.notice = "couldn't determine the current directory — setup needs a project dir"
			return m.toChangesScreen()
		}
		agentItems := make([]list.Item, len(presets.Names()))
		for i, name := range presets.Names() {
			agentItems[i] = commandItem{name: name}
		}
		m.agents = newList("setup — which agent will this project use?", agentItems, m.width, m.height, m.styles)
		m.screen = screenSetupAgent
		return m, nil
	case "share":
		store := m.cfg.StoreDir
		origin := ""
		if git.Available() && git.IsRepo(store) {
			origin = git.OriginURL(store)
		}
		if origin == "" {
			m.notice = "not a git-backed store — run `friday init` with a remote, or `friday remote init <url>`"
			return m.toChangesScreen()
		}
		m.shareOrigin = origin
		m.input.SetValue("")
		m.input.Placeholder = "what changed? (commit message)"
		m.input.Focus()
		m.screen = screenShareInput
		return m, textinput.Blink
	case "discover":
		if len(m.installed) == 0 {
			m.notice = "no installed agents detected — nothing to discover"
			return m.toChangesScreen()
		}
		m.screen = screenRunning
		return m, tea.Batch(m.sp.Tick, discoverCmd(m.cfg, m.installed))
	default:
		// Every menu tile is wired; an unknown name shouldn't reach here. Stay
		// put defensively rather than showing a misleading message.
		return m, nil
	}
}

// toChangesScreen renders the current result/notice/error into the viewport and
// switches to it — the shared tail of every path that lands on screenChanges
// without going through the engine.
func (m model) toChangesScreen() (tea.Model, tea.Cmd) {
	m.diffCache = map[bool]string{} // fresh result → drop the previous body cache
	m.vp.SetContent(m.body())
	m.vp.GotoTop()
	m.screen = screenChanges
	return m, nil
}

// startPreview arms a previewable operation: store the apply closure, run the
// dry-run (nil bridge — no resolution in a preview), and show the plan; enter on
// the changes screen then commits it.
func (m model) startPreview(f applyFunc) (tea.Model, tea.Cmd) {
	m.pending = f
	m.screen = screenRunning
	return m, tea.Batch(m.sp.Tick, f(false, nil))
}

// startApply commits an operation for real: arm a fresh conflict bridge (so
// drifted files prompt instead of skip and Ctrl-C can cancel), then run
// f(true, br) on the running screen. The bridge/screen bookkeeping lives here so
// every apply-launch site sets the same invariants.
func (m model) startApply(f applyFunc) (tea.Model, tea.Cmd) {
	br := newBridge(m.send)
	m.activeBridge = br
	m.aborting = false
	m.screen = screenRunning
	return m, tea.Batch(m.sp.Tick, f(true, br))
}

// confirmPick fires the enter action for whichever checklist screen is active —
// the one part that genuinely differs between the sync/setup/discover pickers,
// which otherwise share their esc-back and item-toggle handling.
func (m model) confirmPick() (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenSyncPick:
		checked := m.pick.checked()
		if len(checked) == 0 {
			// Empty selection must never fall through to Push: an empty Adapters
			// means "all adapters", the opposite of intent.
			return m, nil
		}
		cfg := m.cfg
		return m.startPreview(func(apply bool, br *bridge) tea.Cmd { return syncCmd(cfg, checked, apply, br) })
	case screenSetupItems:
		selected := m.selectedCatalogItems()
		if len(selected) == 0 {
			return m, nil // nothing selected → no-op, same as the push picker
		}
		agent, store, cwd := m.setupAgent, m.cfg.StoreDir, m.cwd
		return m.startPreview(func(apply bool, br *bridge) tea.Cmd {
			return setupCmd(agent, selected, store, cwd, apply, br)
		})
	case screenDiscoverPick:
		sel := m.selectedImports()
		if len(sel) == 0 {
			return m, nil
		}
		// The scan + checklist is the confirmation; import writes into the
		// git-tracked store, so apply directly (a bridge covers the rare case the
		// store already holds a divergent copy).
		cfg := m.cfg
		return m.startApply(func(apply bool, br *bridge) tea.Cmd { return importCmd(cfg, sel, apply, br) })
	}
	return m, nil
}

// resolve answers the current conflict and returns to the running screen while
// the engine continues (it may raise the next conflict, or finish). The reply
// channel is buffered, so the send never blocks Update.
func (m model) resolve(choice engine.ConflictChoice) (tea.Model, tea.Cmd) {
	m.conflict.reply <- engine.Resolution{Choice: choice}
	m.conflict = nil
	m.screen = screenRunning
	return m, m.sp.Tick
}

// abortApply cancels an in-flight apply: closing the bridge's abort channel
// unblocks a waiting resolver and makes every later conflict fast-skip, so the
// engine still unwinds to store.Save() and the drift store stays consistent.
// engineDoneMsg then lands on the results screen. Idempotent (guards a second
// close), and it leaves screenConflict so a stray key can't act on a stale
// conflict.
func (m model) abortApply() (tea.Model, tea.Cmd) {
	if m.activeBridge != nil && !m.aborting {
		m.aborting = true
		close(m.activeBridge.abort)
	}
	m.conflict = nil
	m.screen = screenRunning
	return m, m.sp.Tick
}

// chooseSetupAgent launches the catalog read for the highlighted agent. The
// store walk + dry-run happen off the Update goroutine (catalogCmd) with the
// spinner, so a large knowledge repo never freezes the event loop.
func (m model) chooseSetupAgent() (tea.Model, tea.Cmd) {
	it, ok := m.agents.SelectedItem().(commandItem)
	if !ok {
		return m, nil
	}
	m.screen = screenRunning
	return m, tea.Batch(m.sp.Tick, catalogCmd(it.name, m.cfg.StoreDir, m.cwd))
}

// catalogReadyMsg carries the built setup checklist back onto the Update
// goroutine after catalogCmd reads the store.
type catalogReadyMsg struct {
	agent   string
	items   []setupcmd.Item
	choices []checklistItem
	empty   bool
	err     error
}

// catalogCmd reads the store catalog for agent and builds the checklist rows
// with the shared applied/differs labels and pre-check baseline
// (setupcmd.Suggestions), off the Update goroutine.
func catalogCmd(agent, storeDir, cwd string) tea.Cmd {
	return func() tea.Msg {
		items, err := setupcmd.Catalog(storeDir)
		if err != nil {
			return catalogReadyMsg{agent: agent, err: err}
		}
		if len(items) == 0 {
			return catalogReadyMsg{agent: agent, empty: true}
		}
		preset, ok := presets.Get(agent)
		if !ok {
			return catalogReadyMsg{agent: agent, err: fmt.Errorf("unknown agent %q", agent)}
		}
		states := setupcmd.ItemStates(preset, agent, items, storeDir, cwd)
		sugg := setupcmd.Suggestions(items, states)
		choices := make([]checklistItem, len(items))
		for i := range items {
			choices[i] = checklistItem{label: sugg[i].Label, value: strconv.Itoa(i), checked: sugg[i].Checked}
		}
		return catalogReadyMsg{agent: agent, items: items, choices: choices}
	}
}

// selectedCatalogItems maps the checked rows back to catalog items. Values are
// catalog indices and checked() returns them in display (catalog) order, which
// matches the CLI's sorted selection — concatenate rules build output in that
// order.
func (m model) selectedCatalogItems() []setupcmd.Item {
	var out []setupcmd.Item
	for _, v := range m.pick.checked() {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(m.catalog) {
			out = append(out, m.catalog[i])
		}
	}
	return out
}

// coldStartDoneMsg carries the outcome of a cold-start clone/scaffold + reload.
type coldStartDoneMsg struct {
	cfg       *config.Config
	installed []string
	advisory  string // non-fatal note (e.g. scaffolded without git)
	err       error
}

// coldStartCmd bootstraps ~/.friday from the cold-start input — clone a store
// repo (non-blank url) or scaffold a fresh one (blank) — then reloads the store
// so the control room can open on home. Runs off the Update goroutine with the
// spinner; the pure initcmd seams do no printing of their own.
//
// On a failed clone or scaffold it removes the store dir before returning:
// cold-start only runs when ~/.friday was absent or empty, so a partial init
// (a half clone leaving .git/, or a scaffold that wrote files then failed) is
// safe to delete, and doing so lets a retry — clone or blank-input scaffold —
// start clean instead of hitting "destination already exists".
func coldStartCmd(url string, reload func() (*config.Config, []string, error)) tea.Cmd {
	return func() tea.Msg {
		storeDir, err := config.UserStoreDir()
		if err != nil {
			return coldStartDoneMsg{err: err}
		}
		var advisory string
		if url == "" {
			advisory, err = initcmd.Scaffold(storeDir)
		} else {
			advisory, err = initcmd.Clone(storeDir, url)
		}
		if err != nil {
			_ = os.RemoveAll(storeDir) // reset a partial init so a retry starts clean
			return coldStartDoneMsg{err: err}
		}
		if reload == nil {
			return coldStartDoneMsg{err: fmt.Errorf("internal: no store loader wired")}
		}
		cfg, installed, err := reload()
		return coldStartDoneMsg{cfg: cfg, installed: installed, advisory: advisory, err: err}
	}
}

// shareDoneMsg carries the outcome of a share/propose.
type shareDoneMsg struct {
	notice string
	err    error
}

// shareCmd publishes the store as a proposal: it pushes an ephemeral branch and
// opens an MR (git.Propose), mirroring `remote propose`'s defaults. Runs off the
// Update goroutine with the spinner. Only reached after the confirm screen —
// this is the one outward-facing, hard-to-reverse action in the control room.
func shareCmd(storeDir, msg string) tea.Cmd {
	return func() tea.Msg {
		branch := git.DefaultProposeBranch()
		target := git.DefaultBranch(storeDir)
		out, err := git.Propose(storeDir, branch, target, msg)
		switch {
		case errors.Is(err, git.ErrNothingToCommit):
			return shareDoneMsg{notice: "nothing to propose — the store already matches the remote"}
		case err != nil:
			return shareDoneMsg{err: err}
		}
		note := fmt.Sprintf("proposed %s (targeting %s) — local store untouched until the MR merges", branch, target)
		if out = strings.TrimSpace(out); out != "" {
			note = out + "\n" + note // forges print the MR/PR link here
		}
		return shareDoneMsg{notice: note}
	}
}

// statusCmd computes the push-direction plan (no writes) and hands it back as a
// message. This is the pending-render axis of `friday status`; the CLI's status
// additionally reads the drift store for a local-edit column and prints
// install-state — the control room surfaces a hand-edit as a conflict row here
// instead, and shows install-state on home.
func statusCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		ch, err := engine.Push(cfg, engine.Options{DryRun: true})
		return engineDoneMsg{changes: ch, err: err}
	}
}

// warnInto returns a Warnf sink that appends advisories to dst (a goroutine-
// local slice returned via the message) instead of printing to stdout.
func warnInto(dst *[]string) func(string, ...any) {
	return func(format string, args ...any) {
		*dst = append(*dst, fmt.Sprintf(format, args...))
	}
}

// applyOpts fills the write-time engine options shared by every apply: the Warnf
// sink and, when a bridge is present, the interactive conflict resolver + base
// lookup. A nil bridge leaves OnConflict unset (report-only, today's behavior).
func applyOpts(opts engine.Options, br *bridge, warnings *[]string) engine.Options {
	opts.Warnf = warnInto(warnings)
	if br != nil {
		opts.OnConflict = br.resolver()
		opts.BaseLookup = br.base
		opts.Abort = br.abort // Ctrl-C halts the apply loop, not just conflicts
	}
	return opts
}

// syncCmd is the true sync: capture local edits (pull), then fan the store out
// (push), over the same adapters — the composition cmdSync performs. Both phases
// run through the bridge; a shared PulledStorePaths keeps multi-adapter pull
// provenance straight. If the user aborts during pull, the push phase is
// skipped so cancel actually cancels.
func syncCmd(cfg *config.Config, adapters []string, apply bool, br *bridge) tea.Cmd {
	return func() tea.Msg {
		var warnings []string
		captured := map[string]bool{}

		pullOpts := engine.Options{Adapters: adapters, DryRun: !apply, PulledStorePaths: captured}
		if apply {
			pullOpts = applyOpts(pullOpts, br, &warnings)
		}
		pullCh, err := engine.Pull(cfg, pullOpts)
		if err != nil {
			return engineDoneMsg{err: err, applied: apply, warnings: warnings}
		}

		// Cancelled during the pull phase → don't fan anything out.
		if br != nil {
			select {
			case <-br.abort:
				if apply {
					warnings = append(warnings, recordSnapshot(pullCh)...)
				}
				return engineDoneMsg{changes: pullCh, applied: apply, warnings: warnings}
			default:
			}
		}

		pushOpts := engine.Options{Adapters: adapters, DryRun: !apply}
		if apply {
			pushOpts = applyOpts(pushOpts, br, &warnings)
		}
		pushCh, err := engine.Push(cfg, pushOpts)
		if err != nil {
			return engineDoneMsg{err: err, applied: apply, warnings: warnings}
		}

		all := append(pullCh, pushCh...)
		if apply {
			warnings = append(warnings, recordSnapshot(all)...)
		}
		return engineDoneMsg{changes: all, err: err, applied: apply, warnings: warnings}
	}
}

// discoverReadyMsg carries the importable (target-only) files the scan found,
// as a checklist, back onto the Update goroutine.
type discoverReadyMsg struct {
	changes []engine.Change
	choices []checklistItem
	empty   bool
	err     error
}

// discoverCmd scans the installed agents for files not yet in the store
// (engine.Import, dry-run — the `pull --discover` walk) and builds a checklist
// of what could be captured. Runs off the Update goroutine.
func discoverCmd(cfg *config.Config, adapters []string) tea.Cmd {
	return func() tea.Msg {
		found, err := engine.Import(cfg, engine.Options{Adapters: adapters, DryRun: true})
		if err != nil {
			return discoverReadyMsg{err: err}
		}
		var changes []engine.Change
		var choices []checklistItem
		for _, ch := range found {
			if ch.Action != engine.ActionCreate && ch.Action != engine.ActionUpdate {
				continue // only files that would actually be captured
			}
			choices = append(choices, checklistItem{
				label:   ch.Adapter + "  " + ch.DestRel,
				value:   strconv.Itoa(len(changes)),
				checked: true,
			})
			// selectedImports reads only Adapter and Sources; drop the file-content
			// byte slices so we don't pin every discovered file's bytes in the model.
			ch.SrcContent, ch.OldContent, ch.NewContent = nil, nil, nil
			changes = append(changes, ch)
		}
		if len(changes) == 0 {
			return discoverReadyMsg{empty: true}
		}
		return discoverReadyMsg{changes: changes, choices: choices}
	}
}

// importCmd captures the selected target-only files into the store. It imports
// per adapter, each scoped to only that adapter's chosen sources — importing all
// adapters with a single Only set would capture another agent's file that shares
// a target-relative path (e.g. commands/foo.md under both .claude and .cursor).
func importCmd(cfg *config.Config, sel map[string][]string, apply bool, br *bridge) tea.Cmd {
	return func() tea.Msg {
		var warnings []string
		var all []engine.Change
		var err error
		// Import in a stable adapter order so the change rows render the same way
		// each run (ranging the map directly is nondeterministic).
		adapters := make([]string, 0, len(sel))
		for adapter := range sel {
			adapters = append(adapters, adapter)
		}
		slices.Sort(adapters)
	imports:
		for _, adapter := range adapters {
			// Ctrl-C between adapters stops launching further imports (each engine
			// call also honors Abort per-change); what already landed is journaled below.
			if apply && br != nil {
				select {
				case <-br.abort:
					break imports
				default:
				}
			}
			opts := engine.Options{Adapters: []string{adapter}, Only: sel[adapter], DryRun: !apply}
			if apply {
				opts = applyOpts(opts, br, &warnings)
			}
			var ch []engine.Change
			if ch, err = engine.Import(cfg, opts); err != nil {
				break imports
			}
			all = append(all, ch...)
		}
		// Journal whatever actually landed, even if a later adapter errored or the
		// user cancelled: the earlier adapters' writes are already on disk, so
		// rollback must be able to reverse them — gating the snapshot would strand them.
		if apply && len(all) > 0 {
			warnings = append(warnings, recordSnapshot(all)...)
		}
		return engineDoneMsg{changes: all, err: err, applied: apply, warnings: warnings}
	}
}

// selectedImports maps the checked discover rows back to their source paths,
// grouped by adapter, so each adapter's import is scoped to just its own picks.
func (m model) selectedImports() map[string][]string {
	out := map[string][]string{}
	for _, v := range m.pick.checked() {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(m.discovered) {
			ch := m.discovered[i]
			out[ch.Adapter] = append(out[ch.Adapter], ch.Sources...)
		}
	}
	return out
}

// setupCmd applies the selected store items into the project at cwd for the
// chosen agent (apply=false previews). Mirrors pushCmd: advisories — items with
// no project mapping, plus drift-save/snapshot problems — are collected here and
// returned via the message, never written to stdout under the alt-screen.
func setupCmd(agent string, items []setupcmd.Item, storeDir, cwd string, apply bool, br *bridge) tea.Cmd {
	return func() tea.Msg {
		var warnings []string
		cfg, only, skipped, err := setupcmd.Resolve(agent, items, storeDir, cwd)
		for _, it := range skipped {
			warnings = append(warnings, fmt.Sprintf("%s/%s — %s has no project mapping", it.Category, it.Name, agent))
		}
		if err != nil {
			return engineDoneMsg{err: err, applied: apply, warnings: warnings}
		}
		opts := engine.Options{Only: only, DryRun: !apply}
		if apply {
			opts = applyOpts(opts, br, &warnings)
		}
		ch, err := engine.Push(cfg, opts)
		if err == nil && apply {
			warnings = append(warnings, recordSnapshot(ch)...)
		}
		return engineDoneMsg{changes: ch, err: err, applied: apply, warnings: warnings}
	}
}

// recordSnapshot journals the created/updated files so `friday rollback` can
// undo a TUI-applied push, mirroring the CLI's snapshot safety. It returns any
// non-fatal problems as advisory strings rather than printing (the alt-screen
// owns stdout) — a failed snapshot never fails the writes that already landed.
func recordSnapshot(changes []engine.Change) []string {
	writes := engine.SnapshotWrites(changes)
	if len(writes) == 0 {
		return nil
	}
	dir, err := snapshot.Dir()
	if err != nil {
		return []string{"snapshot skipped: " + err.Error()}
	}
	if _, err := snapshot.Record(dir, writes); err != nil {
		return []string{"snapshot failed: " + err.Error()}
	}
	return nil
}

func (m model) View() string {
	switch m.screen {
	case screenError:
		msg := "no user store — run `friday init` first"
		if m.loadErr != nil {
			msg = m.loadErr.Error()
		}
		return "\n" + m.styles.title.Render("friday") + "\n\n  " +
			m.styles.errText.Render(msg) + "\n\n" + m.footer("q/esc quit")

	case screenColdStart:
		var b strings.Builder
		b.WriteString("\n" + m.styles.title.Render("friday — first run") + "\n\n")
		b.WriteString("  Paste a store repo to clone, or leave blank to scaffold a fresh one:\n\n  ")
		b.WriteString(m.input.View() + "\n")
		if m.opErr != nil {
			b.WriteString("\n  " + m.styles.errText.Render(m.opErr.Error()) + "\n")
		}
		return b.String() + "\n" + m.footer("enter confirm · esc quit")

	case screenRunning:
		label, hint := "working…", "ctrl+c quit"
		if m.aborting {
			label = "cancelling…"
		}
		if m.activeBridge != nil {
			hint = "ctrl+c cancel"
		}
		return "\n" + m.styles.title.Render("friday") + "\n\n  " +
			m.sp.View() + label + "\n\n" + m.footer(hint)

	case screenConflict:
		if m.conflict == nil {
			return ""
		}
		// Body is rendered once when the conflict arrives (see needConflictMsg).
		return "\n" + m.conflict.body + "\n" +
			m.footer("k keep · t take · s skip · ctrl+c cancel")

	case screenHelp:
		return "\n" + helpView(m.styles) + "\n" + m.footer("any key to close")

	case screenShareInput:
		return "\n" + m.styles.title.Render("share — propose store changes for review") + "\n\n  " +
			m.input.View() + "\n\n" + m.footer("enter continue · esc cancel")

	case screenShareConfirm:
		return "\n" + m.styles.title.Render("share — confirm") + "\n\n" +
			"  push a proposal branch to:\n    " + m.styles.changeHd.Render(m.shareOrigin) + "\n\n" +
			"  message: " + m.shareMsg + "\n\n" +
			m.footer("enter push · esc back")

	case screenSetupAgent:
		return m.agents.View() + "\n" + m.footer("↑/↓ move · enter select · esc back")

	case screenSyncPick, screenSetupItems, screenDiscoverPick:
		verb := pickVerb[m.screen]
		hint := "space toggle · a all · enter " + verb + " · esc back"
		if !m.pick.anyChecked() {
			hint = "space toggle · a all · select at least one · esc back"
		}
		return "\n" + m.pick.view(m.styles, m.listHeight(), m.width) + "\n" + m.footer(hint)

	case screenChanges:
		hint := "↑/↓ scroll · d diff · esc back · q quit · ? help"
		if m.pending != nil {
			hint = "enter apply · d diff · esc back · ? help"
		}
		return m.vp.View() + "\n" + m.footer(hint)

	default: // screenHome
		view := m.menu.View()
		// A cold-start advisory (e.g. scaffolded without git) rides in on the
		// first home render; it clears on the next command selection.
		if m.notice != "" {
			view += "\n  " + m.styles.warn.Render(m.notice)
		}
		return view + "\n" + m.footer("↑/↓ move · enter run · q quit · ? help")
	}
}

// helpView is the static keybinding reference shown by the `?` overlay.
func helpView(st styles) string {
	rows := [][2]string{
		{"↑ / ↓ / j / k", "move / scroll"},
		{"enter", "select / confirm / apply"},
		{"space", "toggle a checklist item"},
		{"a", "toggle all checklist items"},
		{"d", "toggle diffs on the changes screen"},
		{"k / t / s", "conflict: keep canonical / take target / skip"},
		{"esc", "back (quit on the first-run screen)"},
		{"q", "quit (on home and result screens)"},
		{"ctrl+c", "cancel an apply, or quit"},
		{"?", "toggle this help"},
	}
	var b strings.Builder
	b.WriteString(st.title.Render("keys") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + st.changeHd.Render(fmt.Sprintf("%-16s", r[0])) + st.footer.Render(r[1]) + "\n")
	}
	return b.String()
}

// body renders the changes screen content (the last op's result or error).
func (m model) body() string {
	var base string
	switch {
	case m.opErr != nil:
		base = m.styles.changeHd.Render("changes:") + "\n  " + m.styles.errText.Render(m.opErr.Error())
	case m.notice != "":
		base = m.styles.footer.Render(m.notice)
	default:
		// Memoized per showDiff so re-toggling `d` reuses the render instead of
		// recomputing every file's line diff (the map is reset per result).
		if v, ok := m.diffCache[m.showDiff]; ok {
			base = v
		} else {
			base = renderChanges(m.result, m.styles, m.showDiff)
			if m.diffCache != nil {
				m.diffCache[m.showDiff] = base
			}
		}
	}
	// In preview mode nothing has been written yet — say so, loudly, above the
	// plan, so the user knows enter is what commits it. After an apply, confirm
	// it landed so applied-state is unmistakable from preview-state.
	switch {
	case m.pending != nil:
		banner := "preview — nothing written yet"
		// In a sync preview the push plan is computed before the pull applies
		// (same caveat the CLI prints), so it can shift once edits are captured.
		if hasDirection(m.result, engine.DirPull) && hasDirection(m.result, engine.DirPush) {
			banner += " · the push plan is computed before pull applies"
		}
		base = m.styles.footer.Render(banner) + "\n\n" + base
	case m.applied:
		n := writtenCount(m.result)
		base = m.styles.ok.Render(fmt.Sprintf("✓ applied %d file(s)", n)) + "\n\n" + base
	}
	for _, w := range m.warnings {
		base += "\n" + m.styles.warn.Render("! "+w)
	}
	return base
}

// writtenCount is how many changes actually wrote a file.
func writtenCount(changes []engine.Change) int {
	n := 0
	for _, ch := range changes {
		if ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate {
			n++
		}
	}
	return n
}

func (m model) footer(hint string) string { return m.styles.footer.Render(hint) }

// commandItem is a home-screen entry implementing bubbles/list's default item.
type commandItem struct {
	name    string
	summary string
}

func (c commandItem) Title() string       { return c.name }
func (c commandItem) Description() string { return c.summary }
func (c commandItem) FilterValue() string { return c.name }
