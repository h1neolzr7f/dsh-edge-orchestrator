import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,readFile,writeFile,mkdir,rm,symlink} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {spawn,execFileSync} from 'node:child_process';
import {once} from 'node:events';
import net from 'node:net';
import {fileURLToPath} from 'node:url';
import {FleetClient} from '../src/client.mjs';
import {apply,createTools,validatePlan} from '../src/index.mjs';
const repo=fileURLToPath(new URL('../../../',import.meta.url));
const run=(exe,args,opts={})=>execFileSync(exe,args,{cwd:repo,encoding:'utf8',...opts});
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function poll(fn){let last;for(let i=0;i<200;i++){try{const v=await fn();if(v)return v;}catch(e){last=e;}await sleep(50);}throw last??Error('poll timeout');}
async function port(){const s=net.createServer();s.listen(0,'127.0.0.1');await once(s,'listening');const n=s.address().port;await new Promise(r=>s.close(r));return n;}

test('native Cordis contract, all eight tools, TLS, paired processes and artifact roundtrip', {timeout:120000}, async t=>{
 const root=await mkdtemp(path.join(tmpdir(),'fleet-node-'));const children=[];let logs='';
 t.after(async()=>{for(const p of children)p.kill();await Promise.all(children.map(p=>p.exitCode!==null?null:Promise.race([once(p,'exit'),sleep(3000)])));await rm(root,{recursive:true,force:true});});
 const bin=name=>path.join(root,name+(process.platform==='win32'?'.exe':''));
 for(const name of ['coordinator','edge-agent']) run('go',['build','-buildvcs=false','-o',bin(name),`./apps/${name}/cmd/${name}`]);
 run('go',['run','./tests/fixtures/tls-cert',root]);const ca=await readFile(path.join(root,'cert.pem'),'utf8');
 const endpoint=`https://127.0.0.1:${await port()}`;
 const launch=(exe,args,env={})=>{const p=spawn(exe,args,{env:{...process.env,DSH_WORKER_TOKENS:'',DSH_WORKER_TOKEN:'',...env},stdio:['ignore','pipe','pipe']});children.push(p);p.stdout.on('data',b=>logs+=b);p.stderr.on('data',b=>logs+=b);return p;};
 launch(bin('coordinator'),['--listen',new URL(endpoint).host,'--db',path.join(root,'c.db'),'--tls-cert',path.join(root,'cert.pem'),'--tls-key',path.join(root,'key.pem')],{DSH_CONTROLLER_TOKEN:'controller'});
 const client=new FleetClient({url:endpoint,token:'controller',ca,timeoutMs:10000});
 await poll(()=>client.devices());
 const workspace=path.join(root,'repo');await mkdir(workspace);run('git',['init','-q',workspace]);run('git',['-C',workspace,'config','user.email','fixture@example.invalid']);run('git',['-C',workspace,'config','user.name','Fixture']);await writeFile(path.join(workspace,'tracked.txt'),'baseline\n');run('git',['-C',workspace,'add','.']);run('git',['-C',workspace,'commit','-m','fixture']);
 for(const id of ['node-a','node-b']){const pair=await client.createPairing({device_hint:id,coordinator_url:endpoint});launch(bin('edge-agent'),['--id',id,'--pair',pair.pairing_code,'--coordinator',endpoint,'--db',path.join(root,id+'.db'),'--ca',path.join(root,'cert.pem'),'--workspace',`workspace/project=${workspace}`,'--allow-programs',JSON.stringify([process.execPath])]);}
 await poll(async()=>{const d=await client.devices();return d.length===2&&d.every(x=>x.online);});
 const tools=createTools({client,workspaceRoot:path.join(root,'downloads'),pollMs:25,waitMs:15000});
 const invoke=(name,args={})=>tools.find(x=>x.name===name).execute(args,{});
 const registered=[];apply({tools:{register(d){assert.equal(typeof d.output.render,'function');assert.equal(d.parameters.type,'object');registered.push(d);return()=>{};}}},{client});assert.equal(registered.length,8);
 assert.equal((await invoke('fleet.list_devices')).length,2);assert.ok((await invoke('fleet.list_capabilities')).some(x=>x.capability_id==='process.exec'));
 const manifest=await invoke('fleet.inspect_device',{device_id:'node-a'});assert.equal(manifest.device_id,'node-a');
 const fresh=await invoke('fleet.inspect_device',{device_id:'node-a',fresh:true});assert.equal(fresh.status,'succeeded');
 const task=(task_id,capability,input,device='node-a')=>({task_id,target_device_id:device,capability:{id:capability,version:'^1.0.0'},input,depends_on:[]});
 const producer=task('test','process.exec',{program:process.execPath,args:['-e',"require('fs').writeFileSync('junit.xml','<testsuite tests=\"1\" failures=\"0\"/>');console.log('tests passed')"],cwd:'workspace/project',timeout_ms:5000});producer.requires_approval=true;producer.approval_reason='Run local tests';producer.output_artifacts=[{name:'junit',path:'junit.xml',media_type:'application/xml'}];
 const inspect=task('git','git.inspect',{workspace:'workspace/project',mode:'diff'},'node-b');
 const accepted=await invoke('fleet.plan_tasks',{plan_id:'node_plan',idempotency_key:'node_key',goal:'tests and git',tasks:[producer,inspect]});assert.equal(accepted.plan_id,'node_plan');
 let pending=await poll(async()=>{const a=await client.approvals();return a.find(x=>x.plan_id==='node_plan'&&x.status==='pending');});
 await client.resolveApproval(pending.id,'approved','fixture approval');
 const done=await client.waitPlan('node_plan',{timeoutMs:15000,pollMs:25});assert.equal(done.status,'succeeded',JSON.stringify(done)+logs);
 const result=await invoke('fleet.get_task',{plan_id:'node_plan',task_id:'test'});assert.equal(result.status,'succeeded');assert.match(result.log_tail,/tests passed/);assert.equal(result.artifacts.length,1);
 const artifacts=await invoke('fleet.get_artifacts',{plan_id:'node_plan',task_id:'test',text:true,save_dir:'run'});assert.match(artifacts[0].text,/<testsuite/);assert.equal(await readFile(artifacts[0].saved_path,'utf8'),artifacts[0].text);
 await assert.rejects(invoke('fleet.get_artifacts',{plan_id:'node_plan',task_id:'test',save_dir:'../escape'}),/path|workspace/i);
 if(process.platform!=='win32'){await mkdir(path.join(root,'downloads','safe'));await symlink(root,path.join(root,'downloads','safe','escape'));await assert.rejects(invoke('fleet.get_artifacts',{plan_id:'node_plan',task_id:'test',save_dir:'safe/escape'}),/symlink|path|workspace/i);}
 const single=task('single','device.inspect',{});assert.equal((await invoke('fleet.dispatch_task',{task:single,wait:true})).status,'succeeded');
 const slow=task('slow','process.exec',{program:process.execPath,args:['-e','setTimeout(()=>{},30000)'],cwd:'workspace/project',timeout_ms:35000});
 const active=await invoke('fleet.dispatch_task',{task:slow});
 await assert.rejects(client.waitPlan(active.plan_id,{timeoutMs:20,pollMs:5}),/TIMEOUT/);
 const waitingAbort=new AbortController();const abortTimer=setTimeout(()=>waitingAbort.abort(),25);await assert.rejects(client.waitPlan(active.plan_id,{signal:waitingAbort.signal,pollMs:10}),/abort/i);clearTimeout(abortTimer);
 await invoke('fleet.cancel_task',{plan_id:active.plan_id});await invoke('fleet.cancel_task',{plan_id:active.plan_id});assert.equal((await client.waitPlan(active.plan_id,{timeoutMs:5000,pollMs:25})).status,'cancelled');
 const staged=Buffer.alloc(4*1024*1024+123,0x4a);const ref=await client.uploadArtifact({plan_id:'future',task_id:'future_task',name:'patch',media_type:'text/plain'},staged);assert.deepEqual(await client.downloadArtifact(ref),staged);assert.equal((await client.uploadArtifact({plan_id:'future',task_id:'future_task',name:'patch',media_type:'text/plain'},staged)).artifact_id,ref.artifact_id);
 const controller=new AbortController();controller.abort();await assert.rejects(client.devices({signal:controller.signal}),/abort/i);
 await assert.rejects(new FleetClient({url:endpoint,token:'controller'}).devices(),/certificate|self.signed/i);
 assert.throws(()=>new FleetClient({url:'http://localhost',token:'x'}),/HTTPS/);
 assert.throws(()=>validatePlan({plan_id:'cycle',idempotency_key:'key',goal:'bad',tasks:[{...single,depends_on:['single']}]}),/cycle/);
});
