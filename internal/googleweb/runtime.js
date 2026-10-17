/* MIXED provider session. Runs only in the user's isolated official page.
 * WAA function discovery derives from Mag1cFall/AIStudio2API (MIT), pinned in
 * docs/third-party/google-web.md. No login, consent or challenge is clicked. */
(() => {
  'use strict';
  const ais = location.origin === 'https://aistudio.google.com';
  const aps = location.origin === 'https://console.cloud.google.com';
  if ((!ais && !aps) || window.__mixedGoogleWeb) return;
  const rpcHost = 'https://alkalimakersuite-pa.clients6.google.com';
  const rpcPath = '/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/';
  const graphHost = 'https://cloudconsole-pa.clients6.google.com';
  const graphPath = '/v3/entityServices/AiplatformEntityService/schemas/AIPLATFORM_GRAPHQL:batchGraphql';
  const originalFetch = window.fetch.bind(window);
  let headers = {}, template = null, graphURL = '', snapshot = null, service = null, captcha = null, observedSuccess = false;
  const requests = new Map();
  const allowedHeaders = new Set(['authorization','x-goog-api-key','x-goog-authuser','x-user-agent','x-aistudio-visit-id','x-goog-ext-519733851-bin','x-goog-ext-174067345-bin']);
  function parseSparse(text) {
    let result='', quoted=false, escaped=false, previous='';
    for(const c of text){
      if(quoted){result+=c;if(escaped)escaped=false;else if(c==='\\')escaped=true;else if(c==='"')quoted=false;continue}
      if((c===','&&(previous==='['||previous===','))||(c===']'&&previous===','))result+='null';
      result+=c;if(c==='"')quoted=true;if(!/\s/.test(c))previous=c;
    }
    return JSON.parse(result);
  }
  function generationKind(raw) {
    try {const u=new URL(raw,location.href);
      if(ais && u.origin===rpcHost && u.pathname.startsWith(rpcPath)) {const method=u.pathname.slice(rpcPath.length);if(['GenerateContent','ProxyStreamedCall','ProxyUnaryCall'].includes(method))return method}
      if(aps && u.origin===graphHost && u.pathname===graphPath)return 'aps';
    }catch(_){}return '';
  }
  function graphRows(text) {
    text=text.trim();if(text.startsWith('['))return JSON.parse(text);
    const rows=[];let start=-1,depth=0,quoted=false,escaped=false;
    for(let i=0;i<text.length;i++){
      const c=text[i];
      if(start<0){if(/\s/.test(c))continue;if(c!=='{')throw Error('invalid GraphQL root');start=i}
      if(quoted){if(escaped)escaped=false;else if(c==='\\')escaped=true;else if(c==='"')quoted=false;continue}
      if(c==='"')quoted=true;else if(c==='{'||c==='[')depth++;else if(c==='}'||c===']')depth--;
      if(depth===0){rows.push(JSON.parse(text.slice(start,i+1)));start=-1}
    }
    if(start>=0||!rows.length)throw Error('truncated GraphQL root');return rows;
  }
  function observe(kind,status,text) {
    if(!kind)return;
    if(status===401||status===403){observedSuccess=false;return}
    if(status!==200 || text.length>8*1024*1024)return;
    try {
      const root=kind==='aps'?graphRows(text):parseSparse(text), finished=g=>g&&!g.error&&g.candidates?.some(c=>c.finishReason&&c.finishReason!=='FINISH_REASON_UNSPECIFIED');
      if(kind==='aps') {
        const valid=Array.isArray(root)&&root.length&&root.every(row=>!row.errors?.length&&Array.isArray(row.results)&&row.results.every(r=>!r.errors?.length));
        observedSuccess=Boolean(valid&&root.some(row=>row.results.some(r=>{const data=r.data?.ui?.streamGenerateContentAnonymous;return (Array.isArray(data)?data:[data]).some(finished)})));
      }
      else if(kind==='GenerateContent') observedSuccess=Array.isArray(root?.[0])&&root[0].some(f=>f?.[0]?.some(c=>c?.[1]>0))&&root.slice(1).every(s=>!s?.[0]);
      else {const frames=kind==='ProxyUnaryCall'?[root]:root?.[0];observedSuccess=Array.isArray(frames)&&frames.some(f=>finished(JSON.parse(typeof f?.[0]==='string'?f[0]:atob(f?.[2]||''))))&&(kind==='ProxyUnaryCall'||root.slice(1).every(s=>!s?.[0]))}
    }catch(_){observedSuccess=false}
  }
  async function observeFetch(kind,response) {
    if(!kind)return;
    if(!response.ok){observe(kind,response.status,'');return}
    const reader=response.clone().body?.getReader();if(!reader)return;
    const decoder=new TextDecoder();let text='',size=0;
    try{for(;;){const {value,done}=await reader.read();if(done)break;size+=value.byteLength;if(size>8*1024*1024){reader.cancel().catch(()=>{});return}text+=decoder.decode(value,{stream:true})}text+=decoder.decode();observe(kind,response.status,text)}catch(_){}
  }
  function capture(url, supplied, body) {
    try {
      const u = new URL(url, location.href);
      if (ais && u.origin === rpcHost && u.pathname.startsWith(rpcPath)) {
        new Headers(supplied || {}).forEach((v,k)=>{if (allowedHeaders.has(k)) headers[k]=v});
      }
      if (aps && u.origin === graphHost && u.pathname === graphPath && typeof body === 'string') {
        const value=JSON.parse(body);
        if (value.operationName === 'StreamGenerateContentAnonymous' && value.variables && value.requestContext) {
          template=value; graphURL=u.href;
        }
      }
    } catch (_) {}
  }
  window.fetch = function(input, init) {
    if (input instanceof Request) input.clone().text().then(body=>capture(input.url, init?.headers || input.headers, init?.body || body)).catch(()=>{});
    else capture(input, init?.headers, init?.body);
    const kind=generationKind(input instanceof Request?input.url:input);
    const response=originalFetch(input, init);response.then(r=>observeFetch(kind,r)).catch(()=>{});return response;
  };
  const open=XMLHttpRequest.prototype.open, setHeader=XMLHttpRequest.prototype.setRequestHeader, send=XMLHttpRequest.prototype.send;
  const xhrs=new WeakMap();
  XMLHttpRequest.prototype.open=function(method,url,...rest){xhrs.set(this,{url,headers:{}});return open.call(this,method,url,...rest)};
  XMLHttpRequest.prototype.setRequestHeader=function(k,v){const x=xhrs.get(this);if(x)x.headers[k]=v;return setHeader.call(this,k,v)};
  XMLHttpRequest.prototype.send=function(body){const x=xhrs.get(this);if(x){capture(x.url,x.headers,body);const kind=generationKind(x.url);if(kind)this.addEventListener('load',()=>{try{observe(kind,this.status,typeof this.responseText==='string'?this.responseText:'')}catch(_){}},{once:true})}return send.call(this,body)};
  function discover() {
    if (ais && !snapshot) {
      const maker=window.default_MakerSuite;
      if (maker) for (const key of Object.keys(maker)) {
        if (typeof maker[key] !== 'function') continue;
        const source=Function.prototype.toString.call(maker[key]);
        if (!source.includes('.snapshot({') || !source.includes('content') || !source.includes('yield')) continue;
        snapshot=maker[key];
        maker[key]=function(...args){if(args[0] && typeof args[0]==='object')service=args[0];return snapshot.apply(this,args)};
        break;
      }
    }
    if (aps) {
      const enterprise=window.grecaptcha?.enterprise;
      if (enterprise?.execute && !enterprise.execute.__mixed) {
        const execute=enterprise.execute.bind(enterprise);
        const wrapped=function(key,options){captcha={execute,key,options:structuredClone(options)};return execute(key,options)};
        wrapped.__mixed=true;enterprise.execute=wrapped;
      }
    }
  }
  const timer=setInterval(discover,100);addEventListener('pagehide',()=>{clearInterval(timer);for(const s of requests.values())s.controller.abort()},{once:true});
  function state() {
    discover();
    const ready=observedSuccess && (ais ? Boolean(snapshot && service && headers['x-goog-api-key']) : Boolean(template && captcha));
    return {status:ready?'ready':'waiting', message:ready?'已连接':'请在此官方页面完成本人授权，并手动发送一条测试消息'};
  }
  async function proof(binding,prompt) {
    if (!snapshot || !service) throw Error('authorization_required');
    const textarea=[...document.querySelectorAll('ms-prompt-box textarea')].find(e=>e.getClientRects().length>0);
    if (!textarea) throw Error('authorization_required');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(textarea,prompt);
    textarea.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:prompt}));
    textarea.dispatchEvent(new Event('change',{bubbles:true}));
    const hash=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(binding));
    return await snapshot(service,[...new Uint8Array(hash)].map(n=>n.toString(16).padStart(2,'0')).join(''));
  }
  async function authHeaders() {
    const h={...headers,'content-type':'application/json+protobuf'};
    const cookies=Object.fromEntries(document.cookie.split(/;\s*/).map(v=>{const i=v.indexOf('=');return [v.slice(0,i),v.slice(i+1)]}));
    const sid=cookies.SAPISID || cookies['__Secure-3PAPISID'];
    if (sid) {const now=Math.floor(Date.now()/1000);const bytes=await crypto.subtle.digest('SHA-1',new TextEncoder().encode(now+' '+sid+' '+location.origin));h.authorization='SAPISIDHASH '+now+'_'+[...new Uint8Array(bytes)].map(n=>n.toString(16).padStart(2,'0')).join('')}
    return h;
  }
  async function request(op) {
    if (ais) {
      if (!['build','playground','models','build_models'].includes(op.kind)) throw Error('operation_invalid');
      let body,method;
      if(op.kind==='models'){body=[];method='ListModels'}
      else if(op.kind==='build_models'){body=['/v1beta/models',JSON.stringify({pageSize:'200',...(op.page_token?{pageToken:op.page_token}:{})}),null,'GET'];method='ProxyUnaryCall'}
      else if(op.kind==='build'){
        const path='/v1beta/models/'+op.model+(op.unary?':generateContent':':streamGenerateContent'), text=JSON.stringify(op.body);
        const prompt=op.body.contents.flatMap(c=>c.parts||[]).map(p=>p.text||'').join(' ');
        body=[path,text,await proof(path+' '+text,prompt)];if(op.unary)body.push('POST');method=op.unary?'ProxyUnaryCall':'ProxyStreamedCall';
      } else {
        body=op.body;
        body[4]=await proof(op.binding,op.prompt);method='GenerateContent';
      }
      return {url:rpcHost+rpcPath+method, headers:await authHeaders(), body:JSON.stringify(body)};
    }
    if(op.kind!=='aps' || !template || !captcha) throw Error('authorization_required');
    const body=structuredClone(template);
    body.variables={...op.body,model:op.model,region:'global',recaptchaToken:await captcha.execute(captcha.key,captcha.options)};
    return {url:graphURL,headers:{'content-type':'application/json'},body:JSON.stringify(body)};
  }
  window.__mixedGoogleWeb={
    state,
    start(id,op){
      if (requests.size || !/^[a-f0-9]{48}$/.test(id) || !op || typeof op.model!=='string' && !['models','build_models'].includes(op.kind)) return false;
      if(op.model && !/^[A-Za-z0-9._-]+$/.test(op.model)) return false;
      const s={controller:new AbortController(),status:0,headers:{},chunks:[],done:false,error:''};requests.set(id,s);
      (async()=>{try{
        const q=await request(op);if(s.controller.signal.aborted)throw Error('cancelled');
        const response=await originalFetch(q.url,{method:'POST',headers:q.headers,body:q.body,credentials:'include',redirect:'error',signal:s.controller.signal});
        s.status=response.status;if(s.status===401||s.status===403)observedSuccess=false;for(const k of ['content-type','retry-after']){const v=response.headers.get(k);if(v)s.headers[k]=v}
        if(!response.body)return;
        const reader=response.body.getReader();
        for(;;){const {value,done}=await reader.read();if(done)break;
          for(let offset=0;offset<value.length;offset+=32768){while(s.chunks.length>=8){if(s.controller.signal.aborted)throw Error('cancelled');await new Promise(r=>setTimeout(r,10))}
            s.chunks.push(btoa(String.fromCharCode(...value.subarray(offset,offset+32768))));
          }
        }
      }catch(_){s.error='会话请求失败，请检查官方页面的授权与验证状态'}finally{s.done=true}})();return true;
    },
    read(id){const s=requests.get(id);if(!s)return {error:'请求已结束',done:true};return {status:s.status,headers:s.headers,chunk:s.chunks.shift()||'',done:s.done&&s.chunks.length===0,error:s.error}},
    cancel(id){const s=requests.get(id);if(s)s.controller.abort();requests.delete(id);return true}
  };
})();
