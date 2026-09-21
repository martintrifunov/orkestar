package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

// replayPane plays an asciicast recording inside a pane. It is a client-side
// pane like the editor and the review: the daemon owns the file, the client
// owns the screen that shows it.
type replayPane struct {
	mu       sync.Mutex
	path     string
	title    string
	width    int
	height   int
	events   []replayEvent
	screen   *terminal.Screen
	next     int
	position float64
	playing  bool
	err      error
}

// replayEvent is one output chunk at a time offset in seconds.
type replayEvent struct {
	at   float64
	data string
}

// replayTickMsg advances a playing recording. The pane is carried rather than
// looked up so a closed pane simply stops asking.
type replayTickMsg struct {
	pane *embeddedTerminal
}

func replayTick(pane *embeddedTerminal) tea.Cmd {
	return tea.Tick(replayStep, func(time.Time) tea.Msg { return replayTickMsg{pane: pane} })
}

// replayStep is one animation frame. Terminal playback is not video; ten
// frames a second is smooth enough for output that arrives in bursts.
const replayStep = 100 * time.Millisecond

func newReplayPane(path string, width, height int) *replayPane {
	// One row is the status line, so the emulator gets the rest.
	screenHeight := max(1, height-1)
	pane := &replayPane{
		path:   path,
		title:  "Replay",
		width:  max(1, width),
		height: screenHeight,
		screen: terminal.NewScreen(max(1, width), screenHeight),
	}
	if err := pane.load(); err != nil {
		pane.err = err
	}
	return pane
}

// load reads the asciicast: a header line, then [seconds, "o", data] events.
// A line that does not parse is skipped rather than failing the whole replay,
// since a recording cut off mid-write ends in a partial line.
func (r *replayPane) load() error {
	file, err := os.Open(r.path)
	if err != nil {
		return fmt.Errorf("open recording: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if first {
			first = false
			var header struct {
				Version int `json:"version"`
				Width   int `json:"width"`
				Height  int `json:"height"`
			}
			if err := json.Unmarshal(line, &header); err != nil {
				return fmt.Errorf("read recording header: %w", err)
			}
			if header.Version != 2 {
				return fmt.Errorf("unsupported recording version %d", header.Version)
			}
			if header.Width > 0 && header.Height > 0 {
				r.width, r.height = header.Width, header.Height
				r.screen.Resize(r.width, r.height)
			}
			continue
		}
		var event []json.RawMessage
		if err := json.Unmarshal(line, &event); err != nil || len(event) < 3 {
			continue
		}
		var at float64
		var kind, data string
		if json.Unmarshal(event[0], &at) != nil || json.Unmarshal(event[1], &kind) != nil || json.Unmarshal(event[2], &data) != nil {
			continue
		}
		if kind != "o" {
			continue
		}
		r.events = append(r.events, replayEvent{at: at, data: data})
	}
	return scanner.Err()
}

// advance feeds every event up to the current position into the screen. It is
// called under the lock by Render, which is where playback actually happens.
func (r *replayPane) advance() {
	for r.next < len(r.events) && r.events[r.next].at <= r.position {
		_, _ = r.screen.Write([]byte(r.events[r.next].data))
		r.next++
	}
}

func (r *replayPane) duration() float64 {
	if len(r.events) == 0 {
		return 0
	}
	return r.events[len(r.events)-1].at
}

// restart clears the screen and starts from the beginning.
func (r *replayPane) restart() {
	_ = r.screen.Close()
	r.screen = terminal.NewScreen(r.width, r.height)
	r.next = 0
	r.position = 0
}

// toggle starts or pauses playback. Pressing play at the end replays.
func (r *replayPane) toggle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.playing && r.position >= r.duration() {
		r.restart()
	}
	r.playing = !r.playing
}

// step moves playback forward by one frame, stopping at the end.
func (r *replayPane) step(delta time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.playing {
		return
	}
	r.position += delta.Seconds()
	if r.position >= r.duration() {
		r.position = r.duration()
		r.playing = false
	}
}

// seek moves the position and replays the screen up to it.
func (r *replayPane) seek(seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	position := r.position + seconds
	if position < 0 {
		position = 0
	}
	if position > r.duration() {
		position = r.duration()
	}
	r.restart()
	r.position = position
}

func (r *replayPane) Render() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "Recording could not be replayed\n\n" + r.err.Error()
	}
	r.advance()
	state := "paused"
	if r.playing {
		state = "playing"
	}
	status := fmt.Sprintf("%s · %s / %s · %s · space play/pause · ←/→ seek · r restart",
		r.title, replayClock(r.position), replayClock(r.duration()), state)
	return status + "\n" + r.screen.Frame().Content
}

// updateReplayKey drives playback from the keyboard. Playback is a client
// concern, so the keys are local to the pane rather than daemon actions.
func (m Model) updateReplayKey(pane *embeddedTerminal, k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case " ", "space":
		pane.replay.toggle()
		if pane.replay.playing {
			return m, replayTick(pane)
		}
	case "left", "h":
		pane.replay.seek(-5)
	case "right", "l":
		pane.replay.seek(5)
	case "r":
		pane.replay.restart()
	}
	return m, nil
}

func (r *replayPane) Cursor() (int, int, bool) { return 0, 0, false }

func (r *replayPane) Resize(columns, rows int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.width = max(1, columns)
	r.height = max(1, rows-1)
	r.screen.Resize(r.width, r.height)
}

func (r *replayPane) Input([]byte)         {}
func (r *replayPane) Paste(string)         {}
func (r *replayPane) Navigation(rune, int) {}
func (r *replayPane) Close() error         { return r.screen.Close() }

// replayClock renders seconds as m:ss for the status line.
func replayClock(seconds float64) string {
	total := int(seconds)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
