package tracedoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// LedgerSuffix is the tail of every ledger filename.
	LedgerSuffix = "tracedoc.md"
	// VolumeThresholdBytes is the size at which a new session starts a new
	// volume instead of appending to the current one.
	VolumeThresholdBytes int64 = 200 * 1024

	StateFilename  = ".tracedoc-state.json"
	LockFilename   = ".tracedoc.lock"
	MarkerFilename = ".tracedoc-on"
)

// LedgerError reports a ledger that cannot be located safely, such as two
// files claiming the same volume number. The adapter logs it and gives up
// rather than guessing which volume a session belongs to.
type LedgerError struct{ Msg string }

func (e *LedgerError) Error() string { return e.Msg }

// State is the per-project bookkeeping file.
type State struct {
	Sessions      map[string]*SessionState `json:"sessions"`
	CurrentVolume string                   `json:"current_volume"`
}

// SessionState records where a session was last written, so a later hook
// run can append only what is new.
type SessionState struct {
	Offset int    `json:"offset"`
	Volume string `json:"volume"`
}

// VolumeNumber maps a ledger filename to its volume number; a name with no
// -NN suffix is volume 1.
//
// This is the original's /-(\d+)\.md$/ spelled out rather than compiled: the
// regexp package costs 440-480 KB -- about a sixth of this binary, measured
// on 2026-10-03 at linux/amd64, darwin/amd64 and windows/amd64 -- and this one
// pattern is the only thing in the plugin that would have needed it.
func VolumeNumber(filename string) int {
	base := strings.TrimSuffix(filename, ".md")
	dash := strings.LastIndex(base, "-")
	if dash < 0 {
		return 1
	}
	digits := base[dash+1:]
	if digits == "" {
		return 1
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 1
		}
	}
	number, err := strconv.Atoi(digits)
	if err != nil {
		return 1
	}
	return number
}

// VolumeFilename maps a volume number to a filename. Volume 1 carries no
// number.
func VolumeFilename(project, today string, number int) string {
	stem := strings.TrimSuffix(LedgerSuffix, ".md")
	if number == 1 {
		return fmt.Sprintf("%s-%s-%s", today, project, LedgerSuffix)
	}
	return fmt.Sprintf("%s-%s-%s-%02d.md", today, project, stem, number)
}

// LedgerPattern is the shell-style glob matching every volume of a project.
func LedgerPattern(project string) string {
	return fmt.Sprintf("*-%s-tracedoc*.md", project)
}

// globLedgers lists the ledger volumes in cwd.
//
// This replaces glob.glob, which normalises case on Windows: there the match
// is folded, so a ledger written for "MyProj" is still found under "myproj".
// filepath.Match is case-sensitive on every platform, so the folding has to
// be explicit.
func globLedgers(cwd, project string) ([]string, error) {
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return nil, err
	}
	fold := runtime.GOOS == "windows"
	pattern := LedgerPattern(project)
	if fold {
		pattern = strings.ToLower(pattern)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// A leading "*" does not match a leading dot in Python's glob, so
		// dotfiles are excluded here too.
		if strings.HasPrefix(name, ".") {
			continue
		}
		candidate := name
		if fold {
			candidate = strings.ToLower(candidate)
		}
		if ok, _ := filepath.Match(pattern, candidate); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// LocateLatestVolume finds the volume a new session should append to. It
// prefers the state file's pointer, falling back to the highest-numbered
// ledger on disk, and reports a LedgerError when several files share that
// number.
func LocateLatestVolume(cwd, project string, st *State) (string, error) {
	if st != nil && st.CurrentVolume != "" {
		path := filepath.Join(cwd, st.CurrentVolume)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return st.CurrentVolume, nil
		}
	}

	names, err := globLedgers(cwd, project)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", nil
	}

	sort.SliceStable(names, func(i, j int) bool {
		return VolumeNumber(names[i]) < VolumeNumber(names[j])
	})
	best := names[len(names)-1]
	highest := VolumeNumber(best)
	claimants := 0
	for _, name := range names {
		if VolumeNumber(name) == highest {
			claimants++
		}
	}
	if claimants > 1 {
		sorted := append([]string(nil), names...)
		sort.Strings(sorted)
		return "", &LedgerError{
			Msg: fmt.Sprintf("ambiguous ledger volumes: %s", strings.Join(sorted, ", ")),
		}
	}
	return best, nil
}

// CreateVolume writes a ledger volume and its header, returning the filename.
func CreateVolume(cwd, project, today, prevFn string, number int) (string, error) {
	name := VolumeFilename(project, today, number)
	var header string
	if number == 1 {
		header = fmt.Sprintf("# %s · TraceDoc\n\n", project)
		header += "This file is auto-generated by the ai-tracedoc plugin " +
			"from Claude Code session transcripts. " +
			"It has not been manually curated.\n\n"
	} else {
		header = fmt.Sprintf("# %s · TraceDoc (Volume %d)\n\n", project, number)
		header += fmt.Sprintf("Continued from: [%s](./%s)\n\n", prevFn, prevFn)
	}
	if err := os.WriteFile(filepath.Join(cwd, name), []byte(header), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// FormatSessionHeader renders the heading a session's entries hang from.
func FormatSessionHeader(dateStr, title, sessionID string) string {
	return fmt.Sprintf("\n## %s · %s\n\n<!-- session: %s -->\n\n", dateStr, title, sessionID)
}

// FormatEntries renders Q&A entries as Markdown. A non-empty T prefixes the
// question with its local time; an empty A leaves the question unanswered.
//
// The parts-then-join shape is load-bearing, not stylistic. Joining inserts
// a separator after the empty terminator part as well, so each entry ends
// with the blank line the format calls for. Building the string by appending
// as you go produces one newline fewer at every entry boundary -- which is
// exactly the kind of drift that silently forks the ledger.
func FormatEntries(entries []Entry) string {
	var parts []string
	for _, entry := range entries {
		if entry.T != "" {
			parts = append(parts, fmt.Sprintf("**Question:** %s · %s\n", entry.T, entry.Q))
		} else {
			parts = append(parts, fmt.Sprintf("**Question:** %s\n", entry.Q))
		}
		if entry.A != "" {
			parts = append(parts, fmt.Sprintf("\n**Answer:** %s\n", entry.A))
		}
		parts = append(parts, "")
	}
	return strings.Join(parts, "\n")
}

// LoadState reads the state file. A missing or corrupt file yields an empty
// state rather than an error, so a half-written state never blocks a session.
func LoadState(cwd string) *State {
	empty := func() *State { return &State{Sessions: map[string]*SessionState{}} }
	raw, err := os.ReadFile(filepath.Join(cwd, StateFilename))
	if err != nil {
		return empty()
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return empty()
	}
	if state.Sessions == nil {
		state.Sessions = map[string]*SessionState{}
	}
	return &state
}

// SaveState writes the state file atomically, via a temporary file and a
// replace, so a reader never sees a partial write.
func SaveState(cwd string, st *State) error {
	raw, err := marshalState(st)
	if err != nil {
		return err
	}
	path := filepath.Join(cwd, StateFilename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}

// marshalState matches Python's json.dump(..., ensure_ascii=False, indent=2):
// non-ASCII is written raw and < > & are not escaped.
func marshalState(st *State) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(st); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ProjectLock runs fn while holding the project's exclusive lock, which spans
// the whole critical section: read state -> append ledger -> save state.
//
// The lock file is opened O_RDWR, never O_APPEND. On Windows Go turns
// O_APPEND into FILE_APPEND_DATA and drops GENERIC_WRITE, and LockFileEx
// rejects a handle without it with ERROR_ACCESS_DENIED -- a failure that
// surfaces as a bare "Access is denied." from a process that still exits 0.
func ProjectLock(cwd string, fn func() error) error {
	fh, err := os.OpenFile(filepath.Join(cwd, LockFilename),
		os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	if err := lockRange(fh); err != nil {
		return err
	}
	defer func() { _ = unlockRange(fh) }()
	return fn()
}

// appendText appends one write, always with LF endings.
//
// The Python original opened in text mode, which translated "\n" to
// os.linesep: the same content became CRLF on Windows and LF elsewhere. No
// Windows user ever ran that code successfully -- the fcntl import fails
// outright -- so no CRLF ledger exists to stay compatible with, and one line
// ending on every platform is what keeps a ledger committed from a mixed-OS
// team from churning.
func appendText(path, text string) error {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	_, err = fh.WriteString(text)
	return err
}

// AppendFooterLink points an old volume at the one that continues it.
func AppendFooterLink(cwd, prevFn, nextFn string) error {
	return appendText(filepath.Join(cwd, prevFn),
		fmt.Sprintf("\n\n---\n\nContinued in: [%s](./%s)\n", nextFn, nextFn))
}

// ShouldStartNewVolume reports whether a volume that has reached size should
// be closed off and a new one started.
//
// The other half of the rule -- that only a session arriving at a volume may
// split it, and one already placed in a volume never does -- is not a
// parameter here because it is not a caller's choice: AppendSession returns
// early for a session it has already placed, so a continuation cannot reach
// this call at all. That is what keeps a session's entries contiguous.
func ShouldStartNewVolume(size, threshold int64) bool {
	return size > threshold
}

// AppendSession appends one session's Q&A to the ledger, creating or rolling
// over a volume as needed. The caller must hold ProjectLock. State is updated
// in place, and callers should not pass a session with no entries.
func AppendSession(cwd, project string, session *Session, state *State,
	today string, threshold int64) error {
	if today == "" {
		today = time.Now().Format("20060102")
	}
	if state.Sessions == nil {
		state.Sessions = map[string]*SessionState{}
	}

	// A session already placed in a volume keeps appending there for its
	// whole life, so its entries stay contiguous.
	if known, ok := state.Sessions[session.SessionID]; ok && known.Volume != "" {
		return appendText(filepath.Join(cwd, known.Volume),
			FormatEntries(session.Entries))
	}

	current, err := LocateLatestVolume(cwd, project, state)
	if err != nil {
		return err
	}
	if current != "" {
		if info, statErr := os.Stat(filepath.Join(cwd, current)); statErr == nil {
			if ShouldStartNewVolume(info.Size(), threshold) {
				name, createErr := CreateVolume(cwd, project, today, current,
					VolumeNumber(current)+1)
				if createErr != nil {
					return createErr
				}
				if err := AppendFooterLink(cwd, current, name); err != nil {
					return err
				}
				current = name
			}
		}
	} else {
		name, createErr := CreateVolume(cwd, project, today, "", 1)
		if createErr != nil {
			return createErr
		}
		current = name
	}

	text := FormatSessionHeader(session.Date, session.Title, session.SessionID)
	text += FormatEntries(session.Entries)
	if err := appendText(filepath.Join(cwd, current), text); err != nil {
		return err
	}

	state.Sessions[session.SessionID] = &SessionState{Offset: 0, Volume: current}
	state.CurrentVolume = current
	return nil
}
