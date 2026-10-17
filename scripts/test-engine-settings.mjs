import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
const html=readFileSync('internal/gateway/index.html','utf8');
function fixture(){
 const elements=new Map(),calls=[];
 const $=id=>{if(!elements.has(id)){const classes=new Set(['hide']);elements.set(id,{value:'',checked:false,disabled:false,textContent:'',classList:{add:x=>classes.add(x),remove:x=>classes.delete(x),contains:x=>classes.has(x),toggle:(x,on)=>on?classes.add(x):classes.delete(x)}})}return elements.get(id)};
 const api=(path,method='GET',body)=>new Promise((resolve,reject)=>calls.push({path,method,body,resolve,reject}));
 for(const prefix of ["$('#engine-source').onchange=","$('#load-engine-settings').onclick=","$('#engine-settings-form').onsubmit="]){
  const line=html.split('\n').find(x=>x.startsWith(prefix));assert.ok(line);runInNewContext(line,{$,api});
 }
 return {$,calls,load:()=>$('#load-engine-settings').onclick(),save:()=>$('#engine-settings-form').onsubmit({preventDefault(){}})};
}
let f=fixture();f.$('#engine-source').value='aps';let p=f.load();
assert(f.$('#engine-source').disabled);assert(f.$('#load-engine-settings').disabled);
f.$('#engine-source').value='vertex';f.$('#engine-source').onchange();
f.calls[0].resolve({max_retries:9,request_timeout_seconds:500,aggregate_stream:true});await p;
assert.equal(f.$('#engine-retries').value,'');assert(f.$('#engine-settings-form').classList.contains('hide'));assert(!f.$('#engine-source').disabled);
p=f.load();f.calls[1].resolve({max_retries:2,request_timeout_seconds:300,aggregate_stream:false});await p;
assert.equal(f.calls[1].path,'/api/engine-settings/vertex');assert.equal(f.$('#engine-retries').value,2);
assert(f.$('#engine-aggregate-label').classList.contains('hide'));
p=f.load();f.calls[2].reject(Error('fixture failure'));await p;assert(!f.$('#engine-source').disabled);
f=fixture();f.$('#engine-source').value='aps';f.$('#engine-retries').value='4';f.$('#engine-timeout').value='120';f.$('#engine-aggregate').checked=true;
p=f.save();f.$('#engine-source').value='vertex';f.$('#engine-retries').value='2';
assert.equal(f.calls[0].path,'/api/engine-settings/aps');assert.equal(JSON.stringify(f.calls[0].body),JSON.stringify({max_retries:4,request_timeout_seconds:120,aggregate_stream:true}));
f.calls[0].resolve({saved:true});await p;
p=f.save();assert.equal(f.calls[1].path,'/api/engine-settings/vertex');assert(!f.calls[1].body.aggregate_stream);f.calls[1].resolve({saved:true});await p;
console.log('Engine settings retain their source during delayed reads and saves.');
