import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';

// Exercise the actual worker against asynchronous browser APIs. No account,
// browser profile, prompt, or provider request is used by this fixture.
const manifest=JSON.parse(readFileSync('internal/googleweb/extension/manifest.json'));
assert.deepEqual(manifest.permissions,['scripting']);
const sockets=[],navigated=[],removed=[],executed=[];
let receiver,tabURL='http://127.0.0.1:12345/mixed-browser/connect#'+'a'.repeat(64),blockedGet;
class Socket{
  static OPEN=1;
  readyState=1;sent=[];
  constructor(url){this.url=url;sockets.push(this)}
  send(raw){this.sent.push(JSON.parse(raw))}
  close(){if(this.readyState!==1)return;this.readyState=3;this.onclose?.()}
}
const chrome={runtime:{onMessage:{addListener:f=>receiver=f}},tabs:{
  create:async()=>{throw Error('must reuse the launched tab')},
  get:async()=>{if(blockedGet)await blockedGet;return{url:tabURL,status:'complete',windowId:7}},
  remove:async id=>{removed.push(id)},update:async(id,options)=>{if(options.url){navigated.push({id,...options});tabURL=options.url}return{id}},onRemoved:{addListener:()=>{}}
},windows:{update:async()=>{}},scripting:{executeScript:async options=>{
  assert.equal(options.target.tabId,1);assert.equal(options.world,'MAIN');
  if(options.files)return[{}];
  const [method]=options.args;executed.push(method);
  return[{result:method==='state'?{status:'ready'}:true}];
}}};
runInNewContext(readFileSync('internal/googleweb/extension/worker.js','utf8'),{chrome,WebSocket:Socket,URL,Promise,Error,Number,Set,Map,setTimeout,clearTimeout,setInterval,clearInterval});
assert.equal(receiver({kind:'connect'},{tab:{},url:'https://attacker.example/'},()=>{}),false);
let connected;
const ready=new Promise(resolve=>connected=resolve);
assert.equal(receiver({kind:'connect'},{tab:{id:1},url:'http://127.0.0.1:12345/mixed-browser/connect#'+'a'.repeat(64)},connected),true);
const socket=sockets[0];
await socket.onmessage({data:JSON.stringify({kind:'hello',source:'aistudio'})});
assert.equal((await ready).ok,true);
assert.equal(navigated[0].id,1);
assert.equal(navigated[0].url,'https://aistudio.google.com/prompts/new_chat#mixed-api-session');
const command=(id,method,args=[])=>socket.onmessage({data:JSON.stringify({id,method,args})});
const settle=()=>new Promise(resolve=>setTimeout(resolve,10));
await command(1,'state');await settle();assert.equal(socket.sent.find(m=>m.id===1).result.status,'ready');
await command(2,'eval',['document.cookie']);await settle();assert.equal(socket.sent.some(m=>m.id===2),false);
// A slow start must not be overtaken by its cancellation. Otherwise a late
// request could run after Go already released the source lease.
let unblock;blockedGet=new Promise(resolve=>unblock=resolve);
await command(3,'start',['b'.repeat(48),{kind:'build'}]);await command(4,'cancel',['b'.repeat(48)]);await settle();
assert.equal(socket.sent.some(m=>m.id===4),false);
unblock();blockedGet=null;await settle();
assert.deepEqual(executed.slice(-2),['start','cancel']);
assert.deepEqual(socket.sent.filter(m=>[3,4].includes(m.id)).map(m=>m.id),[3,4]);
let unblockInit;blockedGet=new Promise(resolve=>unblockInit=resolve);
await command(5,'initialize');await command(6,'cancelInitialize');await settle();assert.equal(socket.sent.some(m=>m.id===6),false);
unblockInit();blockedGet=null;await settle();assert.deepEqual(executed.slice(-2),['initialize','cancelInitialize']);
tabURL='https://accounts.google.com/';const count=executed.length;
await command(7,'state');await settle();assert.equal(socket.sent.find(m=>m.id===7).result.status,'waiting');assert.equal(executed.length,count);
socket.close();await settle();assert.deepEqual(removed,[1]);
console.log('Browser extension pairing, official-origin restriction, command ordering and owned-tab cleanup passed.');
