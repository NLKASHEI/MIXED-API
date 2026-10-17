import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {webcrypto} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';

const script=readFileSync('internal/googleweb/runtime.js','utf8');
const rpc='https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/';
const valid='[[[[[[[[null,"hello"]],"model"],1]],null,[2,1,3]]]]';
const sent=[];let responseStatus=200,responseText=valid;
class FakeXHR{open(){}setRequestHeader(){}send(){}addEventListener(){}}
class Textarea{set value(v){this.text=v}getClientRects(){return [1]}dispatchEvent(){}}
const textarea=new Textarea();
const window={default_MakerSuite:{async native(service,hash){/* yield .snapshot({ content */return 'proof-'+hash}},fetch:async(url,options)=>{sent.push({url,options});return new Response(responseText,{status:responseStatus})}};
const globals={window,location:{origin:'https://aistudio.google.com',href:'https://aistudio.google.com/prompts/new_chat'},document:{cookie:'',querySelectorAll:()=>[textarea]},XMLHttpRequest:FakeXHR,HTMLTextAreaElement:Textarea,InputEvent:class{},Event:class{},Headers,Request,URL,TextEncoder,TextDecoder,crypto:webcrypto,structuredClone,AbortController,Uint8Array,btoa,atob,setTimeout,setInterval:()=>1,clearInterval(){},addEventListener(){}};
runInNewContext(script,globals);
const session=window.__mixedGoogleWeb;
assert.equal(session.state().status,'waiting');
await window.default_MakerSuite.native({},'manual');
responseStatus=403;await window.fetch(rpc+'GenerateContent',{headers:{'x-goog-api-key':'fixture-page-key'},body:'[]'});await delay(20);
assert.equal(session.state().status,'waiting','rejected request accepted as authorization');
responseStatus=200;responseText='[[],[7,"denied"]]';await window.fetch(rpc+'GenerateContent',{});await delay(20);
assert.equal(session.state().status,'waiting','RPC error accepted as authorization');
responseText=valid;await window.fetch(rpc+'GenerateContent',{});await delay(20);
assert.equal(session.state().status,'ready','successful manual generation did not prepare session');
const id='a'.repeat(48);
responseText='["{\"candidates\":[]}"]';
assert.equal(session.start(id,{kind:'build',model:'gemini-test',unary:true,body:{contents:[{parts:[{text:'hello'}]}]},url:'https://attacker.invalid/'}),true);
assert.equal(session.start('b'.repeat(48),{kind:'models'}),false,'overlapping proof accepted');
let result;for(let i=0;i<100;i++){result=session.read(id);if(result.done)break;await delay(10)}
assert.equal(result.error,'');assert.equal(result.status,200);
const request=sent.at(-1);assert.equal(request.url,rpc+'ProxyUnaryCall');
const body=JSON.parse(request.options.body);assert.equal(body[0],'/v1beta/models/gemini-test:generateContent');assert.equal(body[3],'POST');assert(body[2].startsWith('proof-'));assert.equal(textarea.text,'hello');
assert(!JSON.stringify(request).includes('attacker.invalid'));
session.cancel(id);assert.equal(session.start(id,{kind:'build',model:'../../invalid',body:{}}),false);
assert.equal(session.start(id,{kind:'build_models',page_token:'page-2'}),true);await delay(20);
assert.equal(sent.at(-1).url,rpc+'ProxyUnaryCall');assert.equal(JSON.parse(sent.at(-1).options.body)[3],'GET');session.cancel(id);
console.log('Google official-session bootstrap, rejection, unary RPC, catalog, origin and serialization checks passed.');
