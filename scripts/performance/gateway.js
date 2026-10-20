import http from 'k6/http';
import {check,sleep} from 'k6';
import {Counter,Rate,Trend} from 'k6/metrics';
import {sha256} from 'k6/crypto';

const base=(__ENV.GATEWAY_PERF_URL||'http://127.0.0.1:8080').replace(/\/$/,'');
const key=__ENV.GATEWAY_PERF_KEY;
const model=__ENV.GATEWAY_PERF_MODEL||'mock/mock-video-v1';
const runID=__ENV.GATEWAY_PERF_RUN_ID||String(Date.now());
if(!/^https?:\/\/(127\.0\.0\.1|localhost|\[::1\])(?::[0-9]{1,5})?$/.test(base))throw new Error('This load script accepts loopback origins only; use a separate controlled staging runner.');
if(!key)throw new Error('GATEWAY_PERF_KEY is required');
if(!model.startsWith('mock/'))throw new Error('An explicit mock provider model is required');
const accepted=new Counter('generations_accepted'),completed=new Counter('generations_completed');
const quota=new Counter('quota_denials'),errors=new Rate('generation_errors');
const admission=new Trend('admission_ms',true),lifecycle=new Trend('lifecycle_ms',true);
export const options={
 scenarios:{generation:{executor:'ramping-arrival-rate',startRate:1,timeUnit:'1s',preAllocatedVUs:100,maxVUs:256,stages:[{target:1,duration:'30s'},{target:10,duration:'1m'},{target:50,duration:'1m'},{target:0,duration:'15s'}],gracefulStop:'2m'}},
 thresholds:{generation_errors:['rate==0'],dropped_iterations:['count==0']},
};
export default function(){
 const headers={Authorization:`Bearer ${key}`,'Content-Type':'application/json','User-Agent':'Sociaro-Gateway-Isolated-Performance/1.0','Idempotency-Key':`perf-${runID}-${__VU}-${__ITER}`};
 const at=Date.now();const response=http.post(base+'/v1/generations',JSON.stringify({model,prompt:'Isolated free performance fixture'}),{headers,tags:{operation:'admission'}});
 admission.add(response.timings.duration);
 if(response.status===429){quota.add(1);errors.add(__ENV.GATEWAY_PERF_EXPECT_QUOTA!=='1');return;}
 if(!check(response,{'generation accepted':r=>r.status===202})){errors.add(true);return;}
 accepted.add(1);const id=response.json('id');const timeout=Number(__ENV.GATEWAY_PERF_TASK_TIMEOUT||90)*1000;
 while(Date.now()-at<timeout){
  const status=http.get(base+'/v1/generations/'+id,{headers,tags:{operation:'status'}});
  if(status.status!==200){errors.add(true);return;}
  const task=status.json();if(task.status==='failed'){errors.add(true);return;}
  if(task.status==='completed'){
   const download=http.get(base+'/v1/generations/'+id+'/content',{headers,responseType:'binary',tags:{operation:'download'}});
   const valid=check(download,{'result downloadable':r=>r.status===200&&r.body.byteLength>0,'result hash matches':r=>!task.result_sha256||sha256(r.body,'hex')===task.result_sha256});
   errors.add(!valid);if(valid){completed.add(1);lifecycle.add(Date.now()-at);}return;
  }
  sleep(1);
 }
 errors.add(true);
}
