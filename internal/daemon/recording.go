package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// maxRecordingBytes caps one recording. Terminal output is unbounded, so a
// recording that is not capped is a disk-filling feature; reaching the cap
// closes the file and leaves what was captured.
const maxRecordingBytes = 8 << 20

// recordingDirectory is where recordings live, beside the socket and the
// database, so a session's files are cleaned up with its runtime directory.
const recordingDirectory = "recordings"

// terminalRecorder writes an asciicast v2 stream: one JSON header line, then
// one [seconds, "o", data] line per output chunk. The format is what makes a
// recording useful outside Orkestar.
type terminalRecorder struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	taskID string
	// started is the zero point every event time is relative to.
	started   time.Time
	bytes     int64
	truncated bool
}

func newTerminalRecorder(path, taskID string, columns, rows int, started time.Time) (*terminalRecorder, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create recording: %w", err)
	}
	header, err := json.Marshal(map[string]any{
		"version": 2, "width": columns, "height": rows, "timestamp": started.Unix(),
	})
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("encode recording header: %w", err)
	}
	if _, err := file.Write(append(header, '\n')); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write recording header: %w", err)
	}
	return &terminalRecorder{
		file: file, path: path, taskID: taskID, started: started,
		bytes: int64(len(header)) + 1,
	}, nil
}

// write appends one output chunk, stopping at the cap. A closed file makes
// this a no-op, which is what the output pump sees once a recording ended.
func (r *terminalRecorder) write(at time.Time, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return
	}
	if r.bytes+int64(len(data)) > maxRecordingBytes {
		r.truncated = true
		_ = r.file.Close()
		r.file = nil
		return
	}
	event, err := json.Marshal([]any{at.Sub(r.started).Seconds(), "o", string(data)})
	if err != nil {
		return
	}
	n, err := r.file.Write(append(event, '\n'))
	if err != nil {
		_ = r.file.Close()
		r.file = nil
		return
	}
	r.bytes += int64(n)
}

func (r *terminalRecorder) close() (int64, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return r.bytes, r.truncated, nil
	}
	err := r.file.Close()
	r.file = nil
	return r.bytes, r.truncated, err
}

// RecordingStatus is what terminal.record reports: where the file is, how
// much was captured, and the artifact that points at it.
type RecordingStatus struct {
	TerminalID string             `json:"terminal_id"`
	Recording  bool               `json:"recording"`
	Path       string             `json:"path,omitempty"`
	Bytes      int64              `json:"bytes,omitempty"`
	Truncated  bool               `json:"truncated,omitempty"`
	TaskID     string             `json:"task_id,omitempty"`
	Artifact   *workflow.Artifact `json:"artifact,omitempty"`
}

// startRecording begins capturing a terminal's output. A restored terminal has
// no process to record, and one recorder per terminal is enough.
func (s *terminalSession) startRecording(taskID, runtimeDirectory string) (RecordingStatus, error) {
	s.mu.Lock()
	metadata := s.metadata
	s.mu.Unlock()
	if s.process == nil {
		return RecordingStatus{}, errors.New("terminal is no longer running")
	}
	directory := filepath.Join(runtimeDirectory, recordingDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return RecordingStatus{}, fmt.Errorf("create recordings directory: %w", err)
	}
	started := time.Now().UTC()
	path := filepath.Join(directory, fmt.Sprintf("%s-%d.cast", metadata.ID, started.Unix()))
	recorder, err := newTerminalRecorder(path, taskID, metadata.Columns, metadata.Rows, started)
	if err != nil {
		return RecordingStatus{}, err
	}
	if !s.recorder.CompareAndSwap(nil, recorder) {
		_, _, _ = recorder.close()
		return RecordingStatus{}, errors.New("terminal is already recording")
	}
	s.mu.Lock()
	s.metadata.Recording = true
	s.metadata.RecordingPath = path
	s.mu.Unlock()
	return RecordingStatus{TerminalID: metadata.ID, Recording: true, Path: path}, nil
}

// stopRecording closes the capture. The file stays where it was written; the
// caller turns it into an artifact when the recording belongs to a task.
func (s *terminalSession) stopRecording() (RecordingStatus, error) {
	recorder := s.recorder.Swap(nil)
	if recorder == nil {
		return RecordingStatus{}, errors.New("terminal is not recording")
	}
	bytes, truncated, _ := recorder.close()
	s.mu.Lock()
	metadata := s.metadata
	s.metadata.Recording = false
	s.mu.Unlock()
	return RecordingStatus{
		TerminalID: metadata.ID, Path: recorder.path, TaskID: recorder.taskID,
		Bytes: bytes, Truncated: truncated,
	}, nil
}

// terminalRecord handles terminal.record. The task is carried through start
// and stop so a recording can be attached to the work it shows.
func (s *Server) terminalRecord(rawParams json.RawMessage) (RecordingStatus, error) {
	var params struct {
		TerminalID string `json:"terminal_id"`
		Action     string `json:"action"`
		TaskID     string `json:"task_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return RecordingStatus{}, fmt.Errorf("decode terminal record params: %w", err)
	}
	session, ok := s.findTerminal(params.TerminalID)
	if !ok {
		return RecordingStatus{}, fmt.Errorf("terminal %q does not exist", params.TerminalID)
	}

	switch params.Action {
	case "start":
		if params.TaskID != "" {
			if _, err := s.tasks.Get(params.TaskID); err != nil {
				return RecordingStatus{}, err
			}
		}
		return session.startRecording(params.TaskID, filepath.Dir(s.socketPath))
	case "stop":
		status, err := session.stopRecording()
		if err != nil {
			return RecordingStatus{}, err
		}
		// The task may have been given at start or at stop; the artifact is
		// written once, at the end, when the file is complete.
		taskID := params.TaskID
		if taskID == "" {
			taskID = status.TaskID
		}
		if taskID != "" {
			artifact, err := s.artifacts.Add(taskID, workflow.ArtifactRecording, "terminal recording", status.Path, "")
			if err != nil {
				return RecordingStatus{}, err
			}
			status.Artifact = &artifact
		}
		return status, nil
	default:
		return RecordingStatus{}, fmt.Errorf("action must be start or stop")
	}
}
