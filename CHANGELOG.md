# Changelog

## 0.2.0

Terminal correctness and throughput: a leaked window title no longer lands in an
agent's prompt, sustained output costs a fraction of what it did, and a terminal
pane's text can be selected and copied.

- Stop a UTF-8 character inside an OSC, DCS, SOS, PM or APC sequence from
  ending it early. Claude Code sets the window title to "✳ <conversation>"
  when a turn finishes, and the 9C byte of U+2733 was read as a String
  Terminator, printing the rest of the title into the agent pane's input box.
- Select text in a terminal pane by dragging, and copy it to the system
  clipboard on release. Hold Shift to select past a program that has taken the
  mouse over.
- Render a screen when a client is ready for it rather than for every chunk of
  PTY output, and hold client repaints to one per 16ms. Sustained output cost
  the daemon 136us per chunk and now costs 18us.

## 0.1.0

Initial versioned release: daemon-owned agent and shell sessions, reconnectable
terminal panes, workspaces and tasks, code review and editing, and SQLite metadata.

- Preserve Shift+Enter as a modified Enter key in embedded terminals.
- Make confirmed reset stop the daemon and clear state in one command, including
  upgrades from daemons without the reset method.
- Add version reporting, macOS/Linux archives, a Homebrew formula, and Windows
  ConPTY/named-pipe support with PowerShell build and installation scripts.
