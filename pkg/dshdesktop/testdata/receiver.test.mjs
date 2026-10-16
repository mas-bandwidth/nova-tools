// Executing native-loop contract regression. No network, Redis, model or app.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { applyReceiver } from '../receiver.mjs';
function fixture(t, status='running',longPath=false) {
 const base=fs.mkdtempSync(path.join(os.tmpdir(),'dsh-receiver-'));
 let directory=base;
 if(longPath)while(directory.length+121<1024){directory=path.join(directory,'x'.repeat(120));fs.mkdirSync(directory,{mode:0o700})}
 fs.chmodSync(directory,0o700);
 const callbacks=new Map(),steps=new Map(),messages=[],warnings=[];
 let live=true,sequence=0;
 const agent={id:'session-1',status,ctx:{on(event,fn){steps.set(event,fn);return()=>steps.delete(event)}},
  steer(message){messages.push(message);agent.status='running'},
  followup(message){messages.push(message);agent.status='running'}};
 const ctx={agents:{get(id){return live&&id===agent.id?agent:undefined}},logger:{warn(message){warnings.push(message)}},on(event,fn){callbacks.set(event,fn);return()=>callbacks.delete(event)}};
 const receiver=applyReceiver(ctx,{eventsDir:directory,sessionId:agent.id});
 t.after(()=>{receiver.dispose();fs.rmSync(base,{recursive:true,force:true})});
 function publish(id,challenge=false,nonce=''){
  fs.writeFileSync(path.join(directory,`${id}.json`),JSON.stringify({event_id:id,external_message:{subject:`HOOK CHALLENGE nonce=${nonce}`},local_receiver:{proof:'nova-hook prove --nonce <nonce>'}}),{mode:0o600});
  // Match Go atomic publication, never mutate or reuse a previously read file.
  const temp=path.join(directory,`.${id}.tmp`);
  fs.writeFileSync(temp,JSON.stringify({version:1,eventID:id,challenge,queuedAt:String(++sequence).padStart(20,"0")}),{mode:0o600});
  fs.renameSync(temp,path.join(directory,".notices",`notice-${id}.json`));
 }
 return {directory,agent,receiver,messages,warnings,publish,callbacks,steps,setLive(value){live=value}};
}
test('busy pending ordinary does not hide fresh current challenge; notice alone names nonce-bearing receiver',t=>{
 const f=fixture(t);
 f.publish('ordinary-1');f.receiver.drain();
 assert.equal(f.messages.length,1);
 for(let i=2;i<52;i++)f.publish(`ordinary-${i}`);
 f.receiver.drain();assert.equal(f.messages.length,1,'ordinary burst remains coalesced behind pending native input');
 f.publish('challenge-current',true,'newest-nonce');f.receiver.drain();
 assert.equal(f.messages.length,2,'fresh native input reaches the active loop despite pending ordinary input');
 const message=f.messages[1];
 assert.deepEqual(message.source,{kind:'plugin:nova-hook'},'DSH v4 refuses kind plugin; the producer owns plugin:<name>');
 assert.notEqual(message.source.kind,'plugin');
 assert.equal(message.role,'user');
 assert.match(message.id,/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
 assert.ok(Object.isFrozen(message)&&Object.isFrozen(message.content[0])&&Object.isFrozen(message.source));
 assert.doesNotMatch(message.content[0].text,/newest-nonce/,'external payload never becomes native text');
 const file=message.content[0].text.split('Saved external event JSON: ')[1];
 assert.equal(JSON.parse(fs.readFileSync(file,'utf8')).external_message.subject,'HOOK CHALLENGE nonce=newest-nonce');
 f.receiver.drain();assert.equal(f.messages.length,2,'consumed challenge is silent');
 assert.ok(fs.existsSync(path.join(f.directory,'.notices','notice-ordinary-2.json')),'priority admission retains folded ordinary work');
});
test('an already idle live agent is woken without tool/Stop boundary; consumed queue is silent',t=>{
 const f=fixture(t,'idle');let wakes=0;
 f.agent.followup=message=>{wakes++;f.messages.push(message);f.agent.status='running'};
 f.publish('idle-challenge',true,'idle-nonce');f.receiver.drain();
 assert.equal(wakes,1);assert.equal(f.agent.status,'running');
 f.receiver.drain();assert.equal(wakes,1);
 assert.equal(fs.readdirSync(path.join(f.directory,".notices")).filter(name=>name.startsWith('notice-')).length,0);
});
test('one-shot rejected/filtered claim releases ordinary inbox hold without replay or acknowledgment',async t=>{
 for(const result of [{kind:'reject'},{kind:'enter',messages:[]}]){
  const f=fixture(t);f.publish('first');f.receiver.drain();
  for(let i=0;i<50;i++)f.publish(`folded-${i}`);
  const step=f.steps.get('agent/pre-step');
  // Match the loop: the original input has already left the inbox; it is never
  // offered again, even when middleware rejects or replaces the whole batch.
  await step({messages:[f.messages[0]]},async()=>result);
  f.receiver.drain();assert.equal(f.messages.length,2);
  assert.match(f.messages[1].content[0].text,/50 ordinary messages/);
  f.receiver.drain();assert.equal(f.messages.length,2);
 }
});
test('publication during consumption cannot resurrect or lose an earlier summary',t=>{
 const f=fixture(t);f.publish('old');
 f.agent.steer=message=>{f.messages.push(message);f.publish('new-challenge',true,'new-nonce')};
 f.receiver.drain();
 f.agent.steer=message=>f.messages.push(message);
 f.receiver.drain();f.receiver.drain();
 assert.equal(f.messages.length,2);
 assert.match(f.messages[1].content[0].text,/new-challenge.json/);
 assert.ok(!fs.existsSync(path.join(f.directory,'.notices','notice-old.json')));
});
test('cold/disposed target retains queue; native uncertainty is claimed and never replayed',t=>{
 const f=fixture(t);f.setLive(false);f.publish('cold',true);f.receiver.drain();
 assert.equal(f.messages.length,0);assert.ok(fs.existsSync(path.join(f.directory,'.notices','notice-cold.json')));
 f.setLive(true);f.agent.steer=message=>{f.messages.push(message);throw Error('exception after admission')};
 assert.throws(()=>f.receiver.drain(),/exception after admission/);
 assert.ok(fs.existsSync(path.join(f.directory,'.notices','claimed-cold.json')));
 f.receiver.drain();assert.equal(f.messages.length,1,'uncertain native outcome never retries');
});
test('filesystem publication itself triggers idle wake, no model or bus polling',{timeout:2000},async t=>{
 const f=fixture(t,'idle');
 const observed=new Promise(resolve=>{f.agent.followup=message=>{f.messages.push(message);f.agent.status='running';resolve()}});
 f.publish('watched',true,'watch-nonce');
 await observed;
 assert.equal(f.messages.length,1);
});

test('only newest queued challenge is surfaced after a cold interval',t=>{
 const f=fixture(t);f.publish('z-old',true,'old-nonce');f.publish('a-new',true,'current-nonce');f.receiver.drain();
 assert.equal(f.messages.length,1);assert.match(f.messages[0].content[0].text,/a-new.json/);
 f.receiver.drain();assert.equal(f.messages.length,1);
});
test('large burst and maximum path grammar keep native text bounded to one receiver path plus count',t=>{
 const f=fixture(t,'running',true);
 f.publish('x'.repeat(128));
 for(let i=0;i<1000;i++)f.publish(`event-${String(i).padStart(5,'0')}`);
 f.receiver.drain();
 assert.equal(f.messages.length,1);
 const text=f.messages[0].content[0].text;
 assert.match(text,/1001 ordinary messages/);
 assert.ok(f.directory.length>900);
 assert.equal((text.match(/\.json/g)||[]).length,1);
 assert.ok(text.length<1600);
});

test('mounted entry returns an idempotent Cordis disposer that releases normal lifecycle resources',async t=>{
 const base=fs.mkdtempSync(path.join(os.tmpdir(),'dsh-native-entry-'));
 fs.chmodSync(base,0o700);t.after(()=>fs.rmSync(base,{recursive:true,force:true}));
 fs.copyFileSync(new URL('../plugin.mjs',import.meta.url),path.join(base,'plugin.mjs'));
 fs.copyFileSync(new URL('../receiver.mjs',import.meta.url),path.join(base,'nova-hook-dsh-receiver.mjs'));
 const {apply}=await import(pathToFileURL(path.join(base,'plugin.mjs')).href);
 let listeners=0;
 const ctx={agents:{get(){return undefined}},logger:{warn(){}},on(){listeners++;return()=>listeners--}};
 const dispose=apply(ctx,{eventsDir:base,sessionId:'session-1'});
 assert.equal(typeof dispose,'function');
 assert.ok(fs.existsSync(path.join(base,'.native-receiver')));
 assert.equal(listeners,2);
 dispose();dispose();
 assert.equal(listeners,0);
 assert.ok(!fs.existsSync(path.join(base,'.native-receiver')));
});
test('session.json target follows a new session without reload; the old hold is not inherited',t=>{
 const base=fs.mkdtempSync(path.join(os.tmpdir(),'dsh-receiver-'));
 fs.chmodSync(base,0o700);
 const mk=id=>{const a={id,status:'idle',messages:[],ctx:{on(){return()=>{}}},steer(m){a.messages.push(m)},followup(m){a.messages.push(m);a.status='running'}};return a};
 const agents=new Map([['session-old',mk('session-old')],['session-new',mk('session-new')]]);
 const ctx={agents:{get(id){return agents.get(id)}},logger:{warn(){}},on(){return()=>{}}};
 const target=id=>fs.writeFileSync(path.join(base,'session.json'),JSON.stringify({version:1,sessionId:id}),{mode:0o600});
 target('session-old');
 const receiver=applyReceiver(ctx,{eventsDir:base});
 t.after(()=>{receiver.dispose();fs.rmSync(base,{recursive:true,force:true})});
 let seq=0;
 const publish=id=>{fs.writeFileSync(path.join(base,`${id}.json`),'{}',{mode:0o600});fs.writeFileSync(path.join(base,'.notices',`notice-${id}.json`),JSON.stringify({version:1,eventID:id,challenge:false,queuedAt:String(++seq).padStart(20,'0')}),{mode:0o600})};
 publish('one');receiver.drain();
 assert.equal(agents.get('session-old').messages.length,1);
 target('session-new');publish('two');receiver.drain();
 assert.equal(agents.get('session-new').messages.length,1,'the new session is woken; the old ordinary hold does not carry over');
 assert.equal(agents.get('session-old').messages.length,1);
});
test('resolved notice-*.json files in eventsDir are pruned to maxResolvedNotices while the active notice stands',t=>{
 const f=fixture(t);
 for(let i=1;i<=40;i++){
  f.publish(`notice-${String(i).padStart(4,'0')}`,true,`nonce-${i}`);
  f.receiver.drain();
 }
 const notices=fs.readdirSync(f.directory).filter(name=>/^notice-.*\.json$/.test(name)).sort();
 assert.equal(notices.length,17,'16 resolved notices plus 1 active notice');
 assert.ok(notices.includes('notice-0040.json'),'active newest notice stands');
 assert.ok(notices.includes('notice-0024.json'),'oldest retained resolved notice stands');
 assert.ok(!notices.includes('notice-0001.json'),'older resolved notices are pruned');
});
