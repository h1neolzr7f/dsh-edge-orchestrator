import https from 'node:https';
import {createHash} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
const terminal = new Set(['succeeded','failed','cancelled']);
const id = encodeURIComponent;
export class FleetError extends Error {
  constructor(code,message,status){super(`${code}: ${message}`);this.code=code;this.status=status;}
}
/** Native HTTPS transport. No redirect following or disabled certificate checks. */
export class FleetClient {
  constructor({url,token,ca,timeoutMs=30000}) {
    const u=new URL(url);
    if(u.protocol!=='https:'||u.username||u.password||u.search||u.hash)throw Error('HTTPS Coordinator URL required');
    this.url=u.toString().replace(/\/$/,'');this.token=token;this.ca=ca;this.timeoutMs=timeoutMs;
  }
  async request(method,route,body,{signal,headers={},raw=false,timeoutMs=this.timeoutMs}={}) {
    signal?.throwIfAborted();
    const bytes=body==null?null:Buffer.isBuffer(body)?body:Buffer.from(JSON.stringify(body));
    return new Promise((resolve,reject)=>{
      const req=https.request(this.url+route,{method,ca:this.ca,signal,headers:{Authorization:`Bearer ${this.token??''}`,'Content-Type':Buffer.isBuffer(body)?'application/octet-stream':'application/json',...(bytes?{'Content-Length':bytes.length}:{}),...headers}},res=>{
        const chunks=[];let length=0;
        res.on('data',b=>{length+=b.length;if(length>64*1024*1024)res.destroy(Error('response exceeds 64 MiB'));else chunks.push(b);});
        res.on('error',reject);
        res.on('end',()=>{const b=Buffer.concat(chunks);try{
          if(res.statusCode>=300){let e;try{e=JSON.parse(b.toString());}catch{}throw new FleetError(e?.code??'HTTP_ERROR',e?.message??`HTTP ${res.statusCode}`,res.statusCode);}
          resolve(raw?b:JSON.parse(b.toString()));
        }catch(e){reject(e);}});
      });
      const timer=setTimeout(()=>req.destroy(new FleetError('TIMEOUT','request deadline exceeded')),timeoutMs);
      req.once('close',()=>clearTimeout(timer));req.on('error',reject);req.end(bytes);
    });
  }
  devices(o){return this.request('GET','/v1/devices',null,o);}
  snapshot(o){return this.request('GET','/v1/snapshot',null,o);}
  plan(planId,o){return this.request('GET',`/v1/plans/${id(planId)}`,null,o);}
  submit(plan,o){return this.request('POST','/v1/plans',plan,o);}
  events(after=0,through=0,limit=500,o){return this.request('GET',`/v1/events?after_coord_seq=${after}&high_watermark=${through}&limit=${limit}`,null,o);}
  control(planId,action,o){if(!['pause','resume','cancel'].includes(action))throw Error('invalid action');return this.request('POST',`/v1/plans/${id(planId)}/${action}`,null,o);}
  approvals(o){return this.request('GET','/v1/approvals',null,o);}
  resolveApproval(approvalId,decision,reason='',o){return this.request('POST',`/v1/approvals/${id(approvalId)}/resolve`,{decision,reason},o);}
  createPairing(payload={},o){return this.request('POST','/v1/pairings',payload,o);}
  async waitPlan(planId,{signal,timeoutMs=120000,pollMs=200}={}) {
    const deadline=Date.now()+timeoutMs;
    for(;;){signal?.throwIfAborted();const left=deadline-Date.now();if(left<=0)throw new FleetError('TIMEOUT','plan remains active');
      const p=await this.plan(planId,{signal,timeoutMs:Math.min(left,this.timeoutMs)});if(terminal.has(p.status))return p;
      await delay(Math.min(pollMs,Math.max(1,deadline-Date.now())),undefined,{signal});
    }
  }
  async uploadArtifact(metadata,bytes,o) {
    bytes=Buffer.from(bytes);const sha256=createHash('sha256').update(bytes).digest('hex');
    let u=await this.request('POST','/v1/artifacts/uploads',{...metadata,size_bytes:bytes.length,sha256},o);
    const chunkSize=u.chunk_size;
    while(u.confirmed_offset<bytes.length){const offset=u.confirmed_offset;u=await this.request('PATCH',`/v1/artifacts/uploads/${id(u.upload_id)}?offset=${offset}`,bytes.subarray(offset,offset+chunkSize),o);if(u.confirmed_offset<=offset)throw Error('upload made no progress');}
    return this.request('POST',`/v1/artifacts/uploads/${id(u.upload_id)}/complete`,null,o);
  }
  async downloadArtifact(ref,o={}) {
    const data=await this.request('GET',`/v1/artifacts/${id(ref.artifact_id)}`,null,{...o,raw:true});
    if(data.length!==ref.size_bytes||createHash('sha256').update(data).digest('hex')!==ref.sha256)throw Error('artifact integrity mismatch');
    return data;
  }
}
