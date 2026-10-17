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
class Textarea{get value(){return this.text||''}set value(v){this.text=v}getClientRects(){return [1]}dispatchEvent(){}}
const textarea=new Textarea();
// Current upstream also recognizes Promise-based snapshot wrappers, without
// the old transpiler's literal `yield` spelling.
const window={default_MakerSuite:{async native(service,hash){/* e9b .snapshot) */return 'proof-'+hash}},fetch:async(url,options)=>{sent.push({url,options});return new Response(responseText,{status:responseStatus})}};
const globals={window,location:{origin:'https://aistudio.google.com',href:'https://aistudio.google.com/prompts/new_chat'},document:{cookie:'',querySelectorAll:selector=>selector==='ms-prompt-box textarea'?[textarea]:[]},XMLHttpRequest:FakeXHR,HTMLTextAreaElement:Textarea,InputEvent:class{},Event:class{},KeyboardEvent:class{constructor(type){this.type=type}},Headers,Request,URL,TextEncoder,TextDecoder,crypto:webcrypto,structuredClone,AbortController,Uint8Array,btoa,atob,setTimeout,clearTimeout,DOMException,setInterval:()=>1,clearInterval(){},addEventListener(){}};
runInNewContext(script,globals);
const session=window.__mixedGoogleWeb;
assert.equal(session.state().status,'waiting');
let clicks=0;
const runButton={disabled:false,closest:()=>null,getClientRects:()=>[1],click(){clicks++;window.default_MakerSuite.native(()=>{},'bootstrap').then(()=>window.fetch(rpc+'GenerateContent',{headers:{'x-goog-api-key':'fixture-page-key'},body:'[]'})).catch(()=>{});}};
globals.document.querySelectorAll=selector=>selector==='ms-run-button button'?[runButton]:selector==='ms-prompt-box textarea'?[textarea]:[];
assert.equal(session.state().can_initialize,true);
textarea.text='unsent user draft';
assert.equal(session.state().can_initialize,false,'automatic connection would overwrite a draft');
assert.equal(session.initialize().status,'waiting');assert.equal(clicks,0,'initialization overwrote a user draft');
textarea.text='';
assert.equal(session.state().can_initialize,true,'cleared draft did not resume automatic connection');
assert.equal(session.initialize().status,'initializing');
await delay(150);
assert.equal(clicks,1);
assert.equal(sent.length,0,'bootstrap was sent upstream instead of intercepted');
assert.equal(session.state().status,'ready','automatic official-page bootstrap did not capture a function service');
assert.equal(textarea.text,'','bootstrap prompt not restored');
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
const body=JSON.parse(request.options.body);assert.equal(body[0],'/v1beta/models/gemini-test:generateContent');assert.equal(body[3],'POST');assert(body[2].startsWith('proof-'));assert.equal(textarea.text,'','request left its prompt in the page and blocks reconnection');
assert(!JSON.stringify(request).includes('attacker.invalid'));
session.cancel(id);assert.equal(session.start(id,{kind:'build',model:'../../invalid',body:{}}),false);
assert.equal(session.start(id,{kind:'build_models',page_token:'page-2'}),true);await delay(20);
assert.equal(sent.at(-1).url,rpc+'ProxyUnaryCall');assert.equal(JSON.parse(sent.at(-1).options.body)[3],'GET');session.cancel(id);
const graph='https://cloudconsole-pa.clients6.google.com/v3/entityServices/AiplatformEntityService/schemas/AIPLATFORM_GRAPHQL:batchGraphql';
const complete={candidates:[{content:{parts:[{text:'{brace}'}]},finishReason:'STOP'}]};
let graphText=JSON.stringify({results:[{data:{ui:{streamGenerateContentAnonymous:[complete]}}}]}),proofCalls=0,graphRequest;
class GraphXHR{open(){}setRequestHeader(){}send(){}addEventListener(){}}
const graphWindow={grecaptcha:{enterprise:{execute:async()=>{proofCalls++;return 'fixture-proof'}}},fetch:async(url,options)=>{graphRequest={url,options};return new Response(graphText,{status:200})}};
runInNewContext(script,{...globals,window:graphWindow,XMLHttpRequest:GraphXHR,location:{origin:'https://console.cloud.google.com',href:'https://console.cloud.google.com/vertex-ai/studio/multimodal'}});
const graphSession=graphWindow.__mixedGoogleWeb;assert.equal(graphSession.state().status,'waiting');
await graphWindow.grecaptcha.enterprise.execute('official-page-site-key',{action:'official-action'});
const template={operationName:'StreamGenerateContentAnonymous',variables:{model:'manual'},requestContext:{origin:'CONSOLE'}};
await graphWindow.fetch(graph,{body:JSON.stringify(template)});await delay(20);
assert.equal(graphSession.state().status,'ready','object GraphQL response with array results was rejected');
graphText+=graphText;await graphWindow.fetch(graph,{body:JSON.stringify(template)});await delay(20);
assert.equal(graphSession.state().status,'ready','concatenated GraphQL results were rejected');
graphText+='garbage';await graphWindow.fetch(graph,{body:JSON.stringify(template)});await delay(20);
assert.equal(graphSession.state().status,'waiting','malformed GraphQL result accepted');
graphText=JSON.stringify([{results:[{data:{ui:{streamGenerateContentAnonymous:complete}}}]},{results:[{errors:[{message:'denied'}]}]}]);
await graphWindow.fetch(graph,{body:JSON.stringify(template)});await delay(20);
assert.equal(graphSession.state().status,'waiting','GraphQL trailing error accepted');
graphText=JSON.stringify([{results:[{data:{ui:{streamGenerateContentAnonymous:complete}}}]}]);
await graphWindow.fetch(graph,{body:JSON.stringify(template)});await delay(20);
assert.equal(graphSession.start(id,{kind:'aps',model:'gemini-test',body:{contents:[{parts:[{text:'hi'}]}]}}),true);await delay(20);
assert.equal(proofCalls,2);assert.equal(graphRequest.url,graph);assert.equal(JSON.parse(graphRequest.options.body).variables.recaptchaToken,'fixture-proof');
assert.equal(graphSession.cancel(id),true);assert.equal(graphRequest.options.signal.aborted,true);
// A fresh APS page initializes from its real contenteditable input without
// sending a generation or clicking a consent dialog.
let consent=true, graphUpstreamCalls=0;
const consentModal={getClientRects:()=>[1],getAttribute:name=>name==='aria-modal'?'true':null};
const input={isContentEditable:true,textContent:'',getClientRects:()=>[1],dispatchEvent(e){if(e.type==='keydown')graphWindow2.grecaptcha.enterprise.execute('official-key',{}).then(()=>graphWindow2.fetch(graph,{body:JSON.stringify(template)})).catch(()=>{});}};
const graphWindow2={grecaptcha:{enterprise:{execute:async()=> 'fixture-proof'}},fetch:async()=>{graphUpstreamCalls++;return new Response('[]')}};
runInNewContext(script,{...globals,window:graphWindow2,XMLHttpRequest:class extends FakeXHR {},location:{origin:'https://console.cloud.google.com',href:'https://console.cloud.google.com/agent-platform/studio/multimodal'},document:{cookie:'',querySelectorAll:s=>s.includes('aria-modal')?(consent?[consentModal]:[]):s==='[contenteditable="true"][role="textbox"]'?[input]:[]}});
assert.equal(graphWindow2.__mixedGoogleWeb.initialize().status,'waiting');
assert.equal(input.textContent,'','initializer touched input behind consent');
consent=false;
assert.equal(graphWindow2.__mixedGoogleWeb.state().can_initialize,true,'completed page dialog did not resume connection');
assert.equal(graphWindow2.__mixedGoogleWeb.initialize().status,'initializing');
await delay(150);
assert.equal(graphWindow2.__mixedGoogleWeb.state().status,'ready');
assert.equal(graphUpstreamCalls,0,'APS bootstrap escaped to upstream');
assert.equal(input.textContent,'');
// A signed-in Cloud Console need not emit the anonymous generation operation.
// Connecting the anonymous adapter must work without submitting a UI prompt.
let typed=0,anonSent;
const signedInput={...input,dispatchEvent(){typed++}};
const signedWindow={grecaptcha:{enterprise:{execute:async()=> 'official-session-proof'}},fetch:async(url,options)=>{anonSent={url,options};return new Response(graphText)}};
runInNewContext(script,{...globals,window:signedWindow,XMLHttpRequest:class extends FakeXHR{},location:{origin:'https://console.cloud.google.com',href:'https://console.cloud.google.com/agent-platform/studio/multimodal?project=example'},document:{cookie:'',querySelectorAll:s=>s==='[contenteditable="true"][role="textbox"]'?[signedInput]:[]}});
const signedSession=signedWindow.__mixedGoogleWeb;
signedSession.initialize();await delay(150);
assert.equal(signedSession.state().status,'ready','signed-in page incorrectly requires an anonymous UI request');
assert.equal(typed,0,'anonymous connection typed into the signed-in project');
assert.equal(signedSession.start(id,{kind:'aps',model:'gemini-test',body:{contents:[{parts:[{text:'hello'}]}]}}),true);await delay(30);
assert.equal(JSON.parse(anonSent.options.body).operationName,'StreamGenerateContentAnonymous');
assert.equal(JSON.parse(anonSent.options.body).querySignature,'2/l8eCsMMY49imcDQ/lwwXyL8cYtTjxZBF2dNqy69LodY=');
assert.equal(anonSent.options.credentials,'omit','anonymous generation inherited project login credentials');
signedSession.cancel(id);
// Cloud Console has visible nonmodal panels with role=dialog. Anonymous
// verification does not type in the UI and must not depend on a chat input.
let blocking=false,verificationRuns=0;
const nonModal={getClientRects:()=>[1],getAttribute:()=>null,closest:()=>null,contains:()=>false,tagName:'DIV',textContent:'Help panel'};
const actualModal={...nonModal,getAttribute:name=>name==='aria-modal'?'true':null};
const panelWindow={grecaptcha:{enterprise:{execute:async()=>{verificationRuns++;return 'proof'}}},fetch:async()=>{throw Error('unexpected generation')}};
runInNewContext(script,{...globals,window:panelWindow,XMLHttpRequest:class extends FakeXHR{},location:{origin:'https://console.cloud.google.com',href:'https://console.cloud.google.com/agent-platform/studio/multimodal'},document:{cookie:'',querySelectorAll:s=>s.includes('aria-modal')?[nonModal,...(blocking?[actualModal]:[])]:[]}});
const panelSession=panelWindow.__mixedGoogleWeb;
assert.equal(panelSession.state().can_initialize,true,'nonmodal panel or missing editor blocked anonymous verification');
blocking=true;assert.equal(panelSession.state().can_initialize,false,'real modal consent was ignored');
assert.equal(panelSession.initialize().status,'waiting');assert.equal(verificationRuns,0);
blocking=false;panelSession.initialize();await delay(30);
assert.equal(panelSession.state().status,'ready');assert.equal(verificationRuns,1);
let releaseProof,lateSent=0;
const proofWait=new Promise(resolve=>releaseProof=resolve);
const lateWindow={default_MakerSuite:{async native(service,hash){/* yield .snapshot({ content */await proofWait;return 'proof'}},fetch:async()=>{lateSent++;return new Response(valid)}};
const lateField=new Textarea();
const lateButton={disabled:false,closest:()=>null,getClientRects:()=>[1],click(){lateWindow.default_MakerSuite.native({},'bootstrap').then(()=>lateWindow.fetch(rpc+'GenerateContent',{headers:{'x-goog-api-key':'fixture'},body:'[]'})).catch(()=>{});}};
runInNewContext(script,{...globals,window:lateWindow,XMLHttpRequest:class extends FakeXHR{},document:{cookie:'',querySelectorAll:s=>s==='ms-prompt-box textarea'?[lateField]:s==='ms-run-button button'?[lateButton]:[]}});
assert.equal(lateWindow.__mixedGoogleWeb.initialize().status,'initializing');await delay(150);
lateWindow.__mixedGoogleWeb.cancelInitialize();releaseProof();await delay(20);
assert.equal(lateSent,0,'cancelled initialization leaked a delayed generation');
assert.equal(lateWindow.__mixedGoogleWeb.state().status,'waiting');
assert.equal(lateWindow.__mixedGoogleWeb.start(id,{kind:'models'}),false,'invalidated page allowed a new request');
// Cancellation while the official page is still preparing a request cannot
// acknowledge a released lease and permit another concurrent page operation.
let holdRequest=false,releaseRequest,preparingSent=0;
const requestWait=new Promise(resolve=>releaseRequest=resolve);
const preparingWindow={default_MakerSuite:{async native(service,hash){/* yield .snapshot({ content */if(holdRequest)await requestWait;return 'proof'}},fetch:async()=>{preparingSent++;return new Response(valid)}};
const preparingField=new Textarea();
runInNewContext(script,{...globals,window:preparingWindow,XMLHttpRequest:class extends FakeXHR{},document:{cookie:'',querySelectorAll:s=>s==='ms-prompt-box textarea'?[preparingField]:[]}});
const preparingSession=preparingWindow.__mixedGoogleWeb;
preparingSession.state();await preparingWindow.default_MakerSuite.native({},'fixture');
await preparingWindow.fetch(rpc+'GenerateContent',{headers:{'x-goog-api-key':'fixture'},body:'[]'});await delay(20);
holdRequest=true;
assert.equal(preparingSession.start(id,{kind:'playground',model:'gemini-test',body:[],binding:'hello',prompt:'hello'}),true);await delay(20);
assert.equal(preparingSession.cancel(id),false,'in-flight preparation reported safely cancelled');
assert.equal(preparingSession.start('c'.repeat(48),{kind:'models'}),false,'cancelled preparation overlaps the next request');
releaseRequest();await delay(20);
assert.equal(preparingSent,1,'cancelled generation was sent after preparation finished');
assert.equal(preparingField.text,'','cancelled request left page content behind');
console.log('Google official-session bootstrap, rejection, unary RPC, catalog, GraphQL framing, cancellation, origin and serialization checks passed.');
