package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCodePluginLifecycleAndPermissionAPIs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed to execute the OpenCode plugin fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.mjs"), []byte(openCodePlugin), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "hook")
	if err := os.WriteFile(helper, []byte(`#!/bin/sh
read payload
printf '%s\n' "$payload" >> "$FIXTURE_HOOK_LOG"
printf '{"hookSpecificOutput":{"decision":{"behavior":"allow"}}}'
`), 0755); err != nil {
		t.Fatal(err)
	}
	script := `import assert from 'node:assert/strict';
import fs from 'node:fs';
import { OrkestarPlugin } from './plugin.mjs';
const log = process.env.FIXTURE_HOOK_LOG;
const read = () => {try {return fs.readFileSync(log,'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)}catch{return []}};
const wait = async predicate => {const end=Date.now()+15000;while(!predicate()){if(Date.now()>end)throw Error('plugin timed out; events='+JSON.stringify(read().map(e=>e.hook_event_name)));await new Promise(r=>setTimeout(r,10));}};
for (const modern of [true,false]) {
 fs.writeFileSync(log,'');
 const replies=[];
 const client=modern ? {permission:{reply:async args=>replies.push(args)}} : {postSessionIdPermissionsPermissionId:async args=>replies.push(args)};
 const plugin=await OrkestarPlugin({client});
 const emit=(type,properties)=>plugin.event({event:{type,properties}});
 await emit('session.created',{info:{id:'root'}});
 await emit('session.status',{sessionID:'root',status:{type:'busy'}});
 await emit('session.updated',{info:{id:'root'}});
 await emit('session.created',{info:{id:'child',parentID:'root'}});
 await emit('session.status',{sessionID:'child',status:{type:'busy'}});
 await emit('permission.asked',{id:'request',sessionID:'root',permission:'bash',patterns:['secret-command']});
 await wait(()=>replies.length===1);
 assert.deepEqual(replies[0],modern ? {requestID:'request',reply:'once'} : {path:{id:'root',permissionID:'request'},body:{response:'once'}});
 await emit('permission.replied',{sessionID:'root',requestID:'request'});
 await emit('session.idle',{sessionID:'root'});
 await wait(()=>read().some(e=>e.hook_event_name==='Stop'));
 assert.deepEqual(read().map(e=>e.hook_event_name),['SessionStart','UserPromptSubmit','PermissionRequest','PermissionResolved','Stop']);
 assert(!fs.readFileSync(log,'utf8').includes('secret-command'));
}
`
	path := filepath.Join(dir, "test.mjs")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, path)
	cmd.Env = append(os.Environ(), "ORKESTAR_EXECUTABLE="+helper, "FIXTURE_HOOK_LOG="+filepath.Join(dir, "events"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin fixture: %v\n%s", err, b)
	}
}
