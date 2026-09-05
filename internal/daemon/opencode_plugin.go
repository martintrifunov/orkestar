package daemon

// The plugin forwards only lifecycle identifiers, never message/prompt bodies.
// Permission requests remain in OpenCode's own UI if the bridge is unavailable.
const openCodePlugin = `import { spawn } from "node:child_process";
function send(input) {
  return new Promise(resolve => {
    const child = spawn(process.env.ORKESTAR_EXECUTABLE, ["hook"], {stdio: ["pipe", "pipe", "ignore"]});
    let data = "";
    const timer = setTimeout(() => { child.kill(); resolve({}); }, 540000);
    child.stdout.on("data", b => { if (data.length < 65536) data += b; });
    child.on("error", () => { clearTimeout(timer); resolve({}); });
    child.on("close", () => {
      clearTimeout(timer);
      try { resolve(JSON.parse(data || "{}")); } catch { resolve({}); }
    });
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(input));
  });
}
export const OrkestarPlugin = async ({ client }) => {
  const children = new Set();
  const known = new Set();
  const pending = new Set();
  let lifecycle = Promise.resolve();
  return {
    event: async ({ event }) => {
      const p = event.properties || {};
      const info = p.info || {};
      const session_id = p.sessionID || info.id;
      if (!session_id) return;
      if (info.parentID) children.add(session_id);
      if (children.has(session_id)) return;
      let hook_event_name;
      if ((event.type === "session.created" || event.type === "session.updated") && !known.has(session_id)) {
        known.add(session_id);
        hook_event_name = "SessionStart";
      }
      if (event.type === "session.status") hook_event_name = p.status?.type === "idle" ? "Stop" : "UserPromptSubmit";
      if (event.type === "session.idle") hook_event_name = "Stop";
      if (event.type === "session.deleted") hook_event_name = "SessionEnd";
      if (event.type === "permission.replied") {
        pending.delete(p.requestID || p.permissionID || p.id);
        hook_event_name = "PermissionResolved";
      }
      if (event.type === "permission.asked") hook_event_name = "PermissionRequest";
      if (!hook_event_name) return;
      const input = {hook_event_name, session_id, tool_name: p.permission || p.type || "tool",
        permission_id: p.requestID || p.permissionID || p.id};
      if (event.type !== "permission.asked") {
        // Preserve lifecycle order without blocking OpenCode's event bus.
        lifecycle = lifecycle.then(() => send(input));
        return;
      }
      pending.add(p.id);
      void lifecycle.then(() => send(input)).then(async result => {
        const behavior = result.hookSpecificOutput?.decision?.behavior;
        if (!pending.delete(p.id) || !behavior) return;
        try {
          const reply = behavior === "allow" ? "once" : "reject";
          if (client.permission?.reply) await client.permission.reply({requestID: p.id, reply});
          else await client.postSessionIdPermissionsPermissionId({path: {id: session_id, permissionID: p.id}, body: {response: reply}});
        } catch { /* Keep the native prompt available when delivery fails. */ }
      });
    }
  };
};
`
