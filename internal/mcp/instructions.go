package mcp

// instructions is sent to a client when it connects, which is the only moment
// an agent reliably reads anything about a server.
//
// Tool descriptions explain one call at a time and nothing explains the shape,
// so an agent given these tools uses them singly and never finds the loop:
// most obviously, it starts work and then polls for the result because it does
// not know waiting is possible. What follows is the loop, in the order it is
// meant to happen, written for the agent that will run it rather than for a
// person reading documentation.
const instructions = `Orkestar runs coding agents as durable, tracked work. Tasks outlive the
sessions that do them, and every agent session belongs to a workspace.

Delegating a piece of work:

1. workspace_create with the directory the work happens in. Reuses an existing
   workspace for that directory, so it is safe to call every time.
2. task_create with a title and, where it helps, a description. Say what done
   looks like: the description is what the agent doing the work is told, and it
   is what a reviewer judges against. Use depends_on when one task cannot start
   until another finishes.
3. task_create_worktree if the work changes files. The agent then works on the
   task's own git branch instead of the workspace checkout, which is what makes
   task_diff show its changes and lets several tasks run at once without
   colliding.
4. task_start to launch an agent on it. This assigns the task, starts the
   session in the worktree, and sends it the task as its opening prompt; pass
   your own prompt to say something more specific. Call agent_list first if you
   do not know which adapters are available.
5. task_wait until "done", or "finished" if a cancellation is an acceptable
   outcome. Do not call task_list in a loop: waiting is what these tools are
   for, and polling costs you a turn every time you ask.

While work is running:

- agent_wait until "blocked" tells you when an agent genuinely cannot continue
  without a person. That is the moment worth surfacing to the user, and it is
  usually the only one.
- task_wait until "startable" is how to hold a task that depends on others:
  it returns when nothing blocks it any more.
- task_diff shows what a task changed, and any reviewer verdict recorded
  against it.
- agent_prompt sends a follow-up to a session that is already running.

Finishing:

- task_set_status to "done". A task created with auto_review runs a reviewer
  agent over its diff first and refuses to complete if the reviewer asks for
  changes; the refusal carries the reason.
- artifact_create records something durable a task produced — a test result, a
  log, a build — so it survives the session that made it.
- task_update corrects a title, a description or dependencies afterwards.
  Dependencies discovered mid-flight belong here rather than in a new task.

Work that is done the same way repeatedly:

- template_list shows what the workspace has declared in .orkestar/templates.
  A template is a pipeline someone committed beside the code: its tasks, what
  depends on what, and which agent does each.
- template_apply creates all of it at once. With start, it launches the agents
  for the tasks nothing is blocking, and returns the rest in "waiting"; take
  those with task_wait until startable, then task_start.

Two things worth knowing:

- There is no way to stop another agent from here. That is deliberate: ending a
  session destroys work in progress, so it stays a decision a person makes.
- resource_acquire takes a lease on something that cannot be shared, such as a
  device or a single-instance editor. Take one before using such a thing when
  several agents are running, and release it when done.`
