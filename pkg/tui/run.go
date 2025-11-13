package tui

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/sirupsen/logrus"

	"github.com/ourorg/goui/pkg/domain"
	"github.com/ourorg/goui/pkg/engine"
	"github.com/ourorg/goui/pkg/spec"
	"github.com/ourorg/goui/pkg/service"
)

type Options struct {
	Logo                string
	DebugUI             bool
	DefaultInfoDuration time.Duration
}

const (
	normalModeText    = "Normal Mode"
	normalModeHotKeys = "Hotkeys:\n[green]/ search\n[green]: command"
	baseHotkeys      = "Hotkeys:\n[green]/ search\n[green]: command"
)

type App struct {
	eng       *engine.Engine
	reg       *service.RegistryFacade
	opts      Options

	// UI components matching original layout exactly
	tapp           *tview.Application
	prioPane       *tview.TextView
	modeStatePane1 *tview.TextView
	modeStatePane2 *tview.TextView
	logoPane       *tview.TextView
	command        *tview.InputField
	statePane      *tview.TextView
	mainGrid       *tview.Grid
	info           *tview.TextView

	table       *tview.Table
	text        *tview.TextView
	list        *tview.List

	// Mode management
	currentMode int
	helpActive  bool

	// Autocompletion state
	sugg    []string
	suggIdx int

	// Selection state now by IDs for TODO19
	selectedIDs map[string]bool

	// Row and list index to entry ID mapping for current render
	rowID       map[int]string
	listIndexID map[int]string

	// Built-in command functions
	quitFunc        func()
	showHelpFunc    func()
	showAliasesFunc func()
}

func Run(e *engine.Engine, reg *service.RegistryFacade, opts Options) error {
	a := New(e, reg, opts)
	return a.Run()
}

// New creates a new TUI application instance
func New(e *engine.Engine, reg *service.RegistryFacade, opts Options) *App {
	return &App{
		eng:         e,
		reg:         reg,
		opts:        opts,
		selectedIDs: map[string]bool{},
		rowID:       map[int]string{},
		listIndexID: map[int]string{},
	}
}

// SetBuiltinCallbacks sets the callback functions for built-in commands
func (a *App) SetBuiltinCallbacks(quit func(), showHelp func(), showAliases func()) {
	a.quitFunc = quit
	a.showHelpFunc = showHelp
	a.showAliasesFunc = showAliases
}

// Run starts the TUI application
func (a *App) Run() error {
	a.tapp = tview.NewApplication()

	// Initialize all UI components exactly like original
	a.modeStatePane1 = tview.NewTextView().
		SetDynamicColors(true).
		SetText(normalModeText)

	a.modeStatePane2 = tview.NewTextView().
		SetDynamicColors(true).
		SetText(normalModeHotKeys)

	a.prioPane = tview.NewTextView().
		SetText("Mode: " + a.eng.ExecMode().String())

	a.logoPane = tview.NewTextView().
		SetDynamicColors(true).
		SetText(a.opts.Logo)

	if a.logoPane.GetText(false) == "" {
		// Default framework logo
		a.logoPane.SetText(`[blue]GoTUI Framework[white]
Terminal User Interface`)
	}

	a.command = tview.NewInputField()
	a.mainGrid = tview.NewGrid()

	a.statePane = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)

	a.info = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	// Configure UI based on debug settings exactly like original
	if a.opts.DebugUI {
		a.modeStatePane1.SetBorder(true).SetTitle("MSP1")
		a.modeStatePane2.SetBorder(true).SetTitle("MSP2")
		a.prioPane.SetBorder(true).SetTitle("P")
		a.logoPane.SetBorder(true).SetTitle("L")
		a.command.SetBorder(true).SetTitle("C")
		a.mainGrid.SetBorder(true).SetTitle("M")
		a.info.SetBorder(true).SetTitle("I")
		a.statePane.SetBorder(true).SetTitle("S")
	} else {
		a.command.SetFieldBackgroundColor(tcell.ColorBlack).SetBorder(true)
		a.mainGrid.SetBorder(true).SetTitle(" Main Pane ").SetBorderAttributes(tcell.AttrNone)
		a.command.SetFocusFunc(func() {
			a.command.SetBorderColor(tcell.ColorGreen)
		})
		a.command.SetBlurFunc(func() {
			a.command.SetBorderColor(tcell.ColorWhite)
		})
		a.mainGrid.SetFocusFunc(func() {
			a.mainGrid.SetBorderColor(tcell.ColorGreen)
		})
		a.mainGrid.SetBlurFunc(func() {
			a.mainGrid.SetBorderColor(tcell.ColorWhite)
		})
	}

	a.text = tview.NewTextView()
	a.table = tview.NewTable()
	a.list = tview.NewList()
	a.currentMode = domain.ModeNormal

	// Setup input handling
	a.setupInputHandling()

	// Set initial state
	a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())

	// Use exact original layout
	a.tapp.SetRoot(a.setupUI(), true)

	// initial render
	a.render(a.eng.BuildSpec())

	// initialize hotkeys
	a.updateHotkeys()

	return a.tapp.Run()
}

func (a *App) showInfo(msg string) {
	if msg == "" { return }
	a.info.SetText(msg)
	go func() {
		time.Sleep(a.opts.DefaultInfoDuration)
		a.tapp.QueueUpdateDraw(func() { a.info.SetText("") })
	}()
}

// writeSelectionToState commits current selection to the state using TODO19 StateWriter
func (a *App) writeSelectionToState() {
	var ids []string
	for id, ok := range a.selectedIDs {
		if ok {
			ids = append(ids, id)
		}
	}
	// Use StateWriter from TODO19 architecture
	_ = a.eng.StateCtrl().SetNextState(a.eng.CurrentState().ID, func(m map[string]interface{}) {
		m["selection"] = ids
	})
}

// selectedArgFromCurrentView grabs argument from current selection using state args like arg_col
func (a *App) selectedArgFromCurrentView(st *domain.State) string {
	if st == nil {
		return ""
	}
	switch st.LayoutKind {
	case domain.DisplayTable:
		// compute first data row offset
		start := 1
		if _, ok := st.Args["headers"].([]string); ok {
			start = 2
		}
		r, _ := a.table.GetSelection()
		if r < start {
			return ""
		}
		col := 0
		if v, ok := st.Args["arg_col"].(int); ok {
			col = v
		}
		cell := a.table.GetCell(r, col)
		if cell == nil {
			return ""
		}
		return strings.TrimPrefix(cell.Text, "[x] ")
	case domain.DisplayList:
		idx := a.list.GetCurrentItem()
		main, _ := a.list.GetItemText(idx)
		return strings.TrimPrefix(main, "[x] ")
	default:
		return ""
	}
}

func (a *App) onEnterSelection() {
	st := a.eng.CurrentState()
	if st == nil {
		return
	}
	alias, _ := st.Args["on_enter_alias"].(string)
	if alias == "" {
		return
	}
	arg := a.selectedArgFromCurrentView(st)
	if arg == "" {
		return
	}
	msg, sp, _ := a.eng.Execute(alias, []string{arg})
	a.showInfo(msg)
	a.render(sp)
	a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
	a.updateHotkeys()
}

func (a *App) onDeleteSelection() {
	st := a.eng.CurrentState()
	if st == nil {
		return
	}
	alias, _ := st.Args["on_delete_alias"].(string)
	if alias == "" {
		return
	}
	arg := a.selectedArgFromCurrentView(st)
	if arg == "" {
		return
	}
	msg, sp, _ := a.eng.Execute(alias, []string{arg})
	a.showInfo(msg)
	a.render(sp)
	a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
	a.updateHotkeys()
}

func (a *App) updateHotkeys() {
	text := baseHotkeys
	if a.currentMode == domain.ModeNormal {
		st := a.eng.CurrentState()
		if st != nil {
			if s, ok := st.Args["enter_label"].(string); ok && s != "" {
				text += "\n[green]" + s
			}
			if s, ok := st.Args["delete_label"].(string); ok && s != "" {
				text += "\n[green]" + s
			}
			if labels, ok := st.Args["bulk_labels"].([]string); ok && len(labels) > 0 {
				for _, l := range labels {
					text += "\n[green]" + l
				}
			}
		}
	}
	a.modeStatePane2.SetText(text)
}

// setupUI creates the main UI layout exactly like original
func (a *App) setupUI() tview.Primitive {
	headerGrid := tview.NewGrid().
		SetRows(8). // Assuming the header should span 8 rows
		SetColumns(-1, -1, -1, 35). // Distribute columns equally
		AddItem(a.prioPane, 0, 0, 8, 1, 0, 0, false).
		AddItem(a.modeStatePane1, 0, 1, 8, 1, 0, 0, false).
		AddItem(a.modeStatePane2, 0, 2, 8, 1, 0, 0, false).
		AddItem(a.logoPane, 0, 3, 8, 1, 0, 0, false)

	// SubGrid that holds StatePane and mainPane views
	subGrid := tview.NewGrid().
		SetRows(3, -1). // One line for StatePane and the rest for mainPane
		AddItem(a.statePane, 0, 0, 1, 1, 0, 0, false).
		AddItem(a.mainGrid, 1, 0, 1, 1, 0, 0, true) // MainPane gets the focus by default in this subgrid

	return tview.NewGrid().
		SetRows(8, 3, -1, 3). // 8 lines for header, 3 lines for commandPane, rest for subGrid, and 3 for infoPane
		AddItem(headerGrid, 0, 0, 1, 1, 0, 0, false).
		AddItem(a.command, 1, 0, 1, 1, 0, 0, false).
		AddItem(subGrid, 2, 0, 1, 1, 0, 0, true). // subGrid gets the default focus
		AddItem(a.info, 3, 0, 1, 1, 0, 0, false) // Place infoPane at the bottom
}

// setupInputHandling configures keyboard shortcuts and command input exactly like original
func (a *App) setupInputHandling() {
	// Set up autocompletion for the command pane - disable popup, precompute for Tab handler
	a.command.SetAutocompleteFunc(func(current string) []string {
		// no popup; we only precompute list for our Tab handler
		if a.currentMode != domain.ModeCommand {
			return nil
		}
		if len(current) == 0 {
			a.sugg = nil
			a.suggIdx = 0
			return nil
		}
		a.sugg = a.eng.Suggestions(current)
		a.suggIdx = 0
		return nil // returning nil disables tview's popup
	})

	a.tapp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Handle Tab-based autocompletion in command mode only
		if a.currentMode == domain.ModeCommand {
			switch event.Key() {
			case tcell.KeyTAB:
				cur := a.command.GetText()
				if len(a.sugg) == 0 {
					if len(cur) == 0 {
						return nil // do nothing on empty text
					}
					a.sugg = a.eng.Suggestions(cur) // compute once
					a.suggIdx = 0
				}
				if len(a.sugg) > 0 {
					a.command.SetText(a.sugg[a.suggIdx])         // accept current
					// Support upon moving to v0.42.0 https://github.com/rivo/tview/issues/892
					//a.command.SetCursorPosition(len(a.sugg[a.suggIdx])) // put caret at end
					a.suggIdx = (a.suggIdx + 1) % len(a.sugg)   // advance for next Tab
				}
				return nil
			case tcell.KeyBacktab:
				if len(a.sugg) > 0 {
					a.suggIdx = (a.suggIdx - 1 + len(a.sugg)) % len(a.sugg)
					a.command.SetText(a.sugg[a.suggIdx])
					// Support upon moving to v0.42.0 https://github.com/rivo/tview/issues/892
					//a.command.SetCursorPosition(len(a.sugg[a.suggIdx]))
				}
				return nil
			}
		}

		switch a.currentMode {
		case domain.ModeNormal:
			// only intercept the keys we need; otherwise pass through
			switch event.Key() {
			case tcell.KeyEnter:
				a.onEnterSelection()
				return nil
			case tcell.KeyCtrlK:
				a.onDeleteSelection()
				return nil
			case tcell.KeyCtrlR:
				// Redo shortcut (TODO19 vim-like)
				a.onRedo()
				return nil
			case tcell.KeyRune:
				// bulk bindings from state args
				st := a.eng.CurrentState()
				if st != nil {
					if m, ok := st.Args["bulk_keys"].(map[string]string); ok && m != nil {
						r := string(event.Rune())
						if alias, ok2 := m[r]; ok2 && alias != "" {
							a.bulkRun(alias)
							return nil
						}
					}
				}
				switch event.Rune() {
				case '/':
					logrus.Debug("Switching to search mode")
					a.currentMode = domain.ModeSearch
					a.tapp.SetFocus(a.command)
					a.modeStatePane1.SetText("Search Mode Active")
					a.modeStatePane2.SetText("")
					return nil
				case ':':
					logrus.Debug("Switching to command mode")
					a.currentMode = domain.ModeCommand
					a.tapp.SetFocus(a.command)
					a.modeStatePane1.SetText("Command Entry Mode")
					a.modeStatePane2.SetText("")
					return nil
				case '?':
					a.ShowHelp()
					return nil
				case 'u':
					// Undo shortcut (TODO19 vim-like)
					a.onUndo()
					return nil
				}
			}
			return event // let list/table handle arrows
		case domain.ModeSearch, domain.ModeCommand:
			// In these modes, just let the commandPane handle the input.
		}

		// Esc closes help if open
		if event.Key() == tcell.KeyEscape && a.helpActive {
			a.HideHelp()
			return nil
		}

		return event
	})

	// Set up live search and command suggestion updates
	a.command.SetChangedFunc(func(text string) {
		if a.currentMode == domain.ModeCommand {
			if len(text) == 0 {
				a.sugg = nil
				a.suggIdx = 0
				return
			}
			a.sugg = a.eng.Suggestions(text)
			a.suggIdx = 0
		}
		if a.currentMode == domain.ModeSearch {
			// Use StateWriter from TODO19 architecture
			_ = a.eng.StateCtrl().SetNextState(a.eng.CurrentState().ID, func(m map[string]interface{}) {
				m["searchTerm"] = text
			})
			a.render(a.eng.BuildSpec())
		}
	})

	a.command.SetDoneFunc(func(key tcell.Key) {
		switch a.currentMode {
		case domain.ModeSearch:
			// Use StateWriter from TODO19 architecture
			_ = a.eng.StateCtrl().SetNextState(a.eng.CurrentState().ID, func(m map[string]interface{}) {
				delete(m, "searchTerm")
			})
			a.command.SetText("")
			a.render(a.eng.BuildSpec())
		case domain.ModeCommand:
			commandInput := strings.TrimSpace(a.command.GetText())
			// allow users to type a leading ':' inside the input field (vim-style)
			if strings.HasPrefix(commandInput, ":") {
				commandInput = strings.TrimSpace(commandInput[1:])
			}
			// nothing to do on empty input
			if commandInput == "" {
				break
			}
			alias, args := domain.ParseInput(commandInput)
			logrus.Debugf("Executing command: %s with args: %v", alias, args)

			// Execute through engine
			msg, sp, _ := a.eng.Execute(alias, args)
			a.showInfo(msg)
			a.render(sp)
			a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
			a.command.SetText("")

			logrus.Debugf("Cleaned up after: %s with args: %v", alias, args)
		}
		a.currentMode = domain.ModeNormal
		a.modeStatePane1.SetText(normalModeText)
		a.tapp.SetFocus(a.mainGrid)
		a.updateHotkeys()
	})
}

func (a *App) render(s spec.Spec) {
	a.mainGrid.Clear()

	// Sync our local selection with incoming spec selection (TODO19)
	if len(s.Selection) > 0 {
		if a.selectedIDs == nil {
			a.selectedIDs = map[string]bool{}
		} else {
			// clear previous marks not present anymore
			for k := range a.selectedIDs {
				delete(a.selectedIDs, k)
			}
		}
		for _, id := range s.Selection {
			a.selectedIDs[id] = true
		}
	}

	switch s.Kind {
	case spec.KindText:
		a.text.SetText(s.Text.Body)
		a.mainGrid.AddItem(a.text, 0, 0, 1, 1, 0, 0, true)
	case spec.KindTable:
		a.table.Clear()
		a.table.SetSelectable(true, false) // Enable row selection
		a.rowID = map[int]string{} // Reset row mapping

		// title
		a.table.SetCell(0, 0, tview.NewTableCell(s.Table.Title).SetAlign(tview.AlignCenter).SetSelectable(false))

		// headers
		start := 1
		if len(s.Table.Headers) > 0 {
			for j, h := range s.Table.Headers {
				a.table.SetCell(1, j, tview.NewTableCell(h).SetSelectable(false))
			}
			start = 2
		}

		// Prefer Entries (TODO19), fallback to Rows for compatibility
		entries := s.Table.Entries
		if len(entries) == 0 && len(s.Table.Rows) > 0 {
			// Convert Rows to Entries for consistent handling
			for _, r := range s.Table.Rows {
				entries = append(entries, spec.Entry{ID: "", Values: r})
			}
		}

		// Build rows and mark selection by ID
		for i, e := range entries {
			rowIdx := start + i
			a.rowID[rowIdx] = e.ID
			for j, c := range e.Values {
				cellText := c
				if e.ID != "" && a.selectedIDs[e.ID] && j == 0 {
					cellText = "[x] " + c
				}
				cell := tview.NewTableCell(cellText)
				if e.ID != "" && a.selectedIDs[e.ID] {
					cell.SetAttributes(tcell.AttrBold)
				}
				a.table.SetCell(rowIdx, j, cell)
			}
		}

		// Ensure visible selection and focus
		firstRow := start
		if a.table.GetRowCount() > firstRow {
			a.table.Select(firstRow, 0)
		}
		// Keep headers visually fixed
		a.table.SetFixed(start, 0)

		// Set up Space key for selection toggle by ID
		a.table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
			if ev.Key() == tcell.KeyRune && ev.Rune() == ' ' {
				r, _ := a.table.GetSelection()
				if r < start { // Skip title and header rows
					return nil
				}
				a.toggleTableRowByID(r)
				return nil
			}
			return ev
		})

		a.mainGrid.AddItem(a.table, 0, 0, 1, 1, 0, 0, true)
		a.tapp.SetFocus(a.table) // make the cursor visible in the table
	case spec.KindList:
		for a.list.GetItemCount() > 0 { a.list.RemoveItem(0) }
		a.list.ShowSecondaryText(true)
		a.list.SetHighlightFullLine(true) // make the cursor obvious
		a.listIndexID = map[int]string{} // Reset list mapping

		for i, it := range s.List.Items {
			id := it.Main // default ID for list (TODO19)
			a.listIndexID[i] = id
			main := it.Main
			if a.selectedIDs[id] {
				main = "[x] " + it.Main
			}
			a.list.AddItem(main, it.Secondary, it.Shortcut, nil)
		}
		if a.list.GetItemCount() > 0 {
			a.list.SetCurrentItem(0)
		}

		// Set up Space key for selection toggle by ID
		a.list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
			if ev.Key() == tcell.KeyRune && ev.Rune() == ' ' {
				idx := a.list.GetCurrentItem()
				a.toggleListItemByID(idx)
				return nil
			}
			return ev
		})

		a.mainGrid.AddItem(a.list, 0, 0, 1, 1, 0, 0, true)
		a.tapp.SetFocus(a.list) // ensure the highlight is visible
	default:
		logrus.Warn("unknown spec kind")
	}
	a.updateHotkeys()
}

// Stop stops the TUI application
func (a *App) Stop() {
	a.tapp.Stop()
}

// ShowHelp displays help text in the main pane exactly like original
func (a *App) ShowHelp() {
	a.helpActive = true
	helpText := a.helpText()

	helpView := tview.NewTextView().SetDynamicColors(true).SetText(helpText)
	a.mainGrid.Clear()
	a.mainGrid.AddItem(helpView, 0, 0, 1, 1, 0, 0, true)
	a.statePane.SetText("[red:black:b]Help")
}

// helpText returns help text, checking state args first like original
func (a *App) helpText() string {
	state := a.eng.CurrentState()
	if state != nil && state.Args != nil {
		if s, ok := state.Args["help_text"].(string); ok && s != "" {
			return s
		}
	}
	return `Help
- / enter search
- : enter command
- Enter on row to open details
- Ctrl+K to delete
- u to undo, Ctrl+R to redo
- :q to quit, :a to list aliases, :h for help, Esc to close help`
}

// ShowAliases displays the aliases table
func (a *App) ShowAliases() {
	headers, rows := service.BuildAliasesTableModelWithShortcuts(
		a.reg.CommandRegistry().GetCommands(),
		a.reg.StateRegistry().GetStates(),
	)

	aliasesSpec := spec.Spec{
		Kind: spec.KindTable,
		Table: &spec.Table{
			Title:   "Aliases",
			Headers: headers,
			Rows:    rows,
		},
	}

	a.render(aliasesSpec)
	a.statePane.SetText("[red:black:b]Aliases")
}

// HideHelp returns to the normal display
func (a *App) HideHelp() {
	a.helpActive = false
	a.render(a.eng.BuildSpec())
	a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
}

// Legacy methods kept for fallback only (TODO19 uses ID-based selection)
func (a *App) toggleTableRow(r int) {
	// Fallback visual-only selection for entries without IDs
	cell := a.table.GetCell(r, 0)
	currentText := cell.Text

	if strings.HasPrefix(currentText, "[x] ") {
		cell.SetText(strings.TrimPrefix(currentText, "[x] "))
		cell.SetAttributes(tcell.AttrNone)
	} else {
		cell.SetText("[x] " + currentText)
		cell.SetAttributes(tcell.AttrBold)
	}
	a.table.SetCell(r, 0, cell)
}

func (a *App) toggleListItem(i int) {
	// Fallback visual-only selection for entries without IDs
	main, sec := a.list.GetItemText(i)

	if strings.HasPrefix(main, "[x] ") {
		main = strings.TrimPrefix(main, "[x] ")
	} else {
		main = "[x] " + main
	}
	a.list.SetItemText(i, main, sec)
}

// toggleTableRowByID toggles selection state by stable ID (TODO19)
func (a *App) toggleTableRowByID(r int) {
	id := a.rowID[r]
	if id == "" {
		// fallback to visual toggle of first column if no ID
		a.toggleTableRow(r)
		return
	}
	if a.selectedIDs == nil {
		a.selectedIDs = map[string]bool{}
	}
	a.selectedIDs[id] = !a.selectedIDs[id]

	// Update cell decoration
	cell := a.table.GetCell(r, 0)
	text := cell.Text
	if a.selectedIDs[id] {
		if !strings.HasPrefix(text, "[x] ") {
			cell.SetText("[x] " + text)
		}
		cell.SetAttributes(tcell.AttrBold)
	} else {
		cell.SetText(strings.TrimPrefix(text, "[x] "))
		cell.SetAttributes(tcell.AttrNone)
	}
	a.table.SetCell(r, 0, cell)

	// Persist selection to state args for engine and CLI
	a.writeSelectionToState()
}

// toggleListItemByID toggles selection state by stable ID (TODO19)
func (a *App) toggleListItemByID(i int) {
	id := a.listIndexID[i]
	if id == "" {
		// fallback to original method if no ID
		a.toggleListItem(i)
		return
	}
	if a.selectedIDs == nil {
		a.selectedIDs = map[string]bool{}
	}
	a.selectedIDs[id] = !a.selectedIDs[id]

	// Update item display
	main, sec := a.list.GetItemText(i)
	if a.selectedIDs[id] {
		if !strings.HasPrefix(main, "[x] ") {
			main = "[x] " + main
		}
	} else {
		main = strings.TrimPrefix(main, "[x] ")
	}
	a.list.SetItemText(i, main, sec)

	// Persist selection to state args for engine and CLI
	a.writeSelectionToState()
}

// onUndo performs undo operation using TODO19 state management
func (a *App) onUndo() {
	if a.eng.Undo() {
		// Re-render after successful undo
		a.render(a.eng.BuildSpec())
		a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
		a.updateHotkeys()
		a.showInfo("Undo")
	}
}

// onRedo performs redo operation using TODO19 state management
func (a *App) onRedo() {
	if a.eng.Redo() {
		// Re-render after successful redo
		a.render(a.eng.BuildSpec())
		a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
		a.updateHotkeys()
		a.showInfo("Redo")
	}
}

func (a *App) currentSelectedIDs() []string {
	ids := make([]string, 0, len(a.selectedIDs))
	for id, on := range a.selectedIDs {
		if on && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (a *App) bulkRun(alias string) {
	ids := a.currentSelectedIDs()
	if len(ids) == 0 {
		a.showInfo("No rows selected")
		return
	}
	msg, sp, _ := a.eng.Execute(alias, ids)
	a.showInfo(msg)
	a.render(sp)
	a.statePane.SetText("[red:black:b]" + a.eng.CurrentState().ShortName())
	a.updateHotkeys()
}
