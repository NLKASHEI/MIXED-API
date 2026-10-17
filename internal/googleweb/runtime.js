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
  // Public anonymous wire contract, cross-checked with APS2api payload.go and
  // the supplied vertex-singbox source. No account/project credentials here.
  const apsSiteKey='6LdCjtspAAAAAMcV4TGdWLJqRTEk1TfpdLqEnKdj';
  const apsPublicKey='AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g';
  const apsQuery='2/l8eCsMMY49imcDQ/lwwXyL8cYtTjxZBF2dNqy69LodY=';
  const originalFetch = window.fetch.bind(window);
  let headers = {}, template = null, graphURL = '', snapshot = null, service = null, captcha = null, observedSuccess = false;
  const requests = new Map();
  let bootstrap=null, initialized=false, invalidated=false, initializationMessage='';
  const promptValue=e=>e.isContentEditable?e.textContent:e.value;
  const visible=selector=>[...document.querySelectorAll(selector)].filter(e=>{
    if(!e.getClientRects().length||e.closest?.('[aria-hidden="true"],[hidden],[inert]'))return false;
    if(typeof getComputedStyle==='function'){const style=getComputedStyle(e);if(style.visibility==='hidden'||style.visibility==='collapse'||style.display==='none'||style.opacity==='0')return false}
    return true;
  });
  function blockingDialogs(){
    return visible('[role="dialog"],[aria-modal="true"],dialog[open]').filter(e=>{
      if(e.getAttribute?.('aria-modal')==='true'||e.tagName==='DIALOG'&&e.open)return true;
      if(e.closest?.('mat-dialog-container,.mat-mdc-dialog-container,[data-overlay-container]'))return true;
      if(document.activeElement&&e.contains?.(document.activeElement))return true;
      // Older consent widgets can omit aria-modal. Keep those user-owned,
      // while ordinary Cloud help/navigation panels must not block the source.
      return /登录|登入|条款|同意|验证|sign.?in|terms|consent|verif|captcha/i.test(e.getAttribute?.('aria-label')||e.textContent||'');
    });
  }
  const officialStudio=()=>!aps||/^\/(agent-platform|vertex-ai)\/studio\/multimodal(?:\/|$)/.test(new URL(location.href).pathname);
  function promptFields(){
    const primary=visible(ais?'ms-prompt-box textarea':'[contenteditable="true"][role="textbox"]');
    return primary.length?primary:visible('textarea');
  }
  function runButtons(){
    const primary=visible('ms-run-button button');if(primary.length)return primary;
    return visible('button').filter(b=>/^(Run|Build|Send|运行|发送|构建)(\s|$)/i.test((b.getAttribute('aria-label')||b.textContent||'').trim())||b.classList.contains('ctrl-enter-submits')||b.classList.contains('build-button'));
  }
  function unavailable(message){return {...state(),status:'waiting',message,can_initialize:false}}
  function setPrompt(textarea,value) {
    if(textarea.isContentEditable)textarea.textContent=value;
    else Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(textarea,value);
    textarea.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:value}));
    textarea.dispatchEvent(new Event('change',{bubbles:true}));
  }
  function cancelInitialize(completed=false) {
    const pending=bootstrap;if(!pending)return true;
    bootstrap=null;clearTimeout(pending.timer);
    if(pending.triggered&&!completed){invalidated=true;initialized=false;observedSuccess=false;initializationMessage='初始化已中止，请刷新官方页面后重新连接会话';}
    if(pending.textarea&&promptValue(pending.textarea)===pending.prompt)setPrompt(pending.textarea,'');
    return true;
  }
  function interceptBootstrap(kind,body) {
    if(invalidated)return Boolean(kind);
    if(!bootstrap)return false;
    if(ais && kind!=='GenerateContent')return false;
    if(aps){try{if(kind!=='aps'||JSON.parse(body).operationName!=='StreamGenerateContentAnonymous')return false}catch(_){return false}}
    initialized=ais?Boolean(snapshot && service && headers['x-goog-api-key']):Boolean(template && captcha);
    initializationMessage=initialized?'':'页面初始化未取得完整会话，请重新打开来源页面';
    cancelInitialize(true);return true;
  }
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
    initialized=false;
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
  window.fetch = async function(input, init) {
    const raw=input instanceof Request?input.url:input, kind=generationKind(raw);let body=init?.body;
    if(input instanceof Request) {
      body=body ?? await input.clone().text();
      capture(raw,init?.headers||input.headers,body);
    } else capture(raw,init?.headers,init?.body);
    if(interceptBootstrap(kind,body))throw new DOMException('Initialization complete','AbortError');
    const response=await originalFetch(input, init);observeFetch(kind,response).catch(()=>{});return response;
  };
  const open=XMLHttpRequest.prototype.open, setHeader=XMLHttpRequest.prototype.setRequestHeader, send=XMLHttpRequest.prototype.send;
  const xhrs=new WeakMap();
  XMLHttpRequest.prototype.open=function(method,url,...rest){xhrs.set(this,{url,headers:{}});return open.call(this,method,url,...rest)};
  XMLHttpRequest.prototype.setRequestHeader=function(k,v){const x=xhrs.get(this);if(x)x.headers[k]=v;return setHeader.call(this,k,v)};
  XMLHttpRequest.prototype.send=function(body){const x=xhrs.get(this);if(x){capture(x.url,x.headers,body);const kind=generationKind(x.url);if(interceptBootstrap(kind,body)){this.abort();queueMicrotask(()=>{this.dispatchEvent(new ProgressEvent('abort'));this.dispatchEvent(new ProgressEvent('loadend'))});return}if(kind)this.addEventListener('load',()=>{try{observe(kind,this.status,typeof this.responseText==='string'?this.responseText:'')}catch(_){}},{once:true})}return send.call(this,body)};
  function discover() {
    if (ais && !snapshot) {
      const maker=window.default_MakerSuite;
      if (maker) for (const key of Object.keys(maker)) {
        if (typeof maker[key] !== 'function') continue;
        const source=Function.prototype.toString.call(maker[key]);
        if (!/\.?snapshot\s*[()]/.test(source) || !/content|yield|Promise|e9b|Bcc/.test(source)) continue;
        snapshot=maker[key];
        maker[key]=function(...args){if(args[0] && ['object','function'].includes(typeof args[0]))service=args[0];return snapshot.apply(this,args)};
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
    if(invalidated)return {status:'waiting',message:initializationMessage};
    if(bootstrap)return {status:'initializing',message:'正在连接来源会话'};
    const ready=(observedSuccess||initialized) && (ais ? Boolean(snapshot && service && headers['x-goog-api-key']) : Boolean(template && captcha));
    let message=ready?'会话已连接，尚未验证实际推理':initializationMessage;
    const fields=promptFields(),dialogs=blockingDialogs();
    const empty=fields.length===1&&!(promptValue(fields[0])||'').trim();
    const canInitialize=!ready&&!initializationMessage&&!requests.size&&!dialogs.length&&(ais?empty&&!!snapshot&&runButtons().length===1:officialStudio()&&!!window.grecaptcha?.enterprise?.execute);
    if(!message)message=dialogs.length?'官方页面检测到 '+dialogs.length+' 个阻塞窗口，请本人处理该窗口中的登录、条款或验证提示':ais&&fields.length===1&&!empty?'请自行保存或清空官方页面的草稿，软件会继续连接':canInitialize?'正在自动连接会话':ais?'正在等待 AIS 聊天页面和验证函数加载':!officialStudio()?'无头V：请进入 Agent Studio 的多模态页面':!window.grecaptcha?.enterprise?.execute?'无头V：等待官方 reCAPTCHA 脚本加载，请检查浏览器代理':'无头V：正在检查官方页面状态';
    return {status:ready?'ready':'waiting',message,can_initialize:canInitialize,checks:ais?{snapshot:!!snapshot,service:!!service,headers:!!headers['x-goog-api-key'],input:fields.length===1,blocking_dialog:dialogs.length>0}:{official_page:officialStudio(),verification_script:!!window.grecaptcha?.enterprise?.execute,blocking_dialog:dialogs.length>0,template:!!template,verification:!!captcha},version:'0.4.4'};
  }
  function initializeAPS(){
    const enterprise=window.grecaptcha?.enterprise;
    if(!enterprise?.execute)return unavailable('无头V：官方验证脚本尚未加载，请检查浏览器代理能否访问 Google / reCAPTCHA，再刷新来源页面');
    // The signed-in UI sends project operations. It does not need to send an
    // anonymous generation just to construct the anonymous protocol envelope.
    const execute=enterprise.execute.bind(enterprise);
    const pending={timer:setTimeout(()=>{initializationMessage='无头V：官方验证接口超时，请检查来源页面及浏览器代理';cancelInitialize()},30000)};
    bootstrap=pending;
    Promise.resolve().then(()=>typeof enterprise.ready==='function'?new Promise(resolve=>enterprise.ready(resolve)):undefined).then(()=>{
      if(bootstrap!==pending)throw Error('cancelled');return execute(apsSiteKey,{});
    }).then(token=>{
      if(bootstrap!==pending)return;
      if(typeof token!=='string'||!token.length)throw Error('empty_verification');
      captcha={execute,key:apsSiteKey,options:{}};
      const entropy=crypto.getRandomValues(new Uint32Array(2));
      const pageViewId=1000000000000000+Number((BigInt(entropy[0])<<32n|BigInt(entropy[1]))%8000000000000000n);
      template={operationName:'StreamGenerateContentAnonymous',querySignature:apsQuery,requestContext:{clientVersion:'boq_cloud-boq-clientweb-vertexaistudio_20260630.00_p0',pagePath:'/agent-platform/studio/multimodal',pageViewId,trackingId:'d'+pageViewId,clientSessionId:crypto.randomUUID().toUpperCase(),backendOverrides:{},selectedPurview:{},jurisdiction:'global',localizationData:{locale:typeof navigator==='object'?navigator.language:'en',timezone:Intl.DateTimeFormat().resolvedOptions().timeZone}}};
      graphURL=graphHost+graphPath+'?key='+apsPublicKey+'&prettyPrint=false';
      initialized=true;initializationMessage='';cancelInitialize(true);
    }).catch(error=>{if(bootstrap!==pending)return;cancelInitialize(true);initializationMessage=/invalid site key|not loaded|invalid.*client/i.test(String(error?.message))?'无头V：官方页面未注册匿名验证 site key，请重新打开来源页面；此错误发生在验证初始化，尚未发送生成请求':'无头V：官方验证接口未返回有效结果，请查看来源页面验证提示或浏览器代理；尚未发送生成请求'});
    return state();
  }
  function initialize() {
    discover();
    if(invalidated || bootstrap || state().status==='ready')return state();
    initializationMessage='';
    if(requests.size)return unavailable('有请求正在执行，请完成后再连接会话');
    if(blockingDialogs().length)return unavailable('请本人处理官方页面的条款、登录或验证提示后再连接会话');
    const fields=promptFields();
    if(aps){if(!officialStudio())return unavailable('无头V：请进入 Agent Studio 的多模态页面');return initializeAPS()}
    const buttons=runButtons();
    if(!snapshot || buttons.length!==1 || fields.length!==1)return unavailable('AIS 初始化未完成：验证函数'+(snapshot?'已找到':'未找到')+'；输入框 '+fields.length+' 个；运行按钮 '+buttons.length+' 个。请进入 Playground 的新对话页，再连接会话');
    const textarea=fields[0],button=buttons[0];
    if((promptValue(textarea)||'').trim())return unavailable('官方页面有未发送的内容，请先自行保存或清空，再连接会话');
    const prompt='MIXED connection initialization';
    setPrompt(textarea,prompt);
    const pending={textarea,prompt,timer:setTimeout(()=>{initializationMessage='来源初始化超时，请检查官方页面的登录、验证或权限提示';cancelInitialize()},30000)};
    bootstrap=pending;
    (async()=>{
      // Angular/Quill updates its form asynchronously after input events.
      await new Promise(r=>setTimeout(r,100));
      while(bootstrap===pending && button && (button.disabled||button.closest('[aria-disabled="true"]')))await new Promise(r=>setTimeout(r,100));
      if(bootstrap!==pending)return;
      if(promptValue(textarea)!==pending.prompt){cancelInitialize(true);initializationMessage='输入内容已变化，已停止连接；请保存草稿后刷新来源页面';return}
      try {
        pending.triggered=true;
        if(ais)button.click();
        else textarea.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',code:'Enter',keyCode:13,which:13,bubbles:true,cancelable:true}));
      }catch(_){cancelInitialize();initializationMessage='来源初始化失败，请重新打开官方页面'}
    })();
    return state();
  }
  async function proof(binding,prompt) {
    if (!snapshot || !service) throw Error('authorization_required');
    const textarea=promptFields()[0];
    if (!textarea) throw Error('authorization_required');
    const previous=promptValue(textarea);
    setPrompt(textarea,prompt);
    try {
      const hash=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(binding));
      return await snapshot.call(window.default_MakerSuite,service,[...new Uint8Array(hash)].map(n=>n.toString(16).padStart(2,'0')).join(''));
    } finally {if(promptValue(textarea)===prompt)setPrompt(textarea,previous)}
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
    return {url:graphURL,headers:{'content-type':'application/json'},credentials:'omit',body:JSON.stringify(body)};
  }
  window.__mixedGoogleWeb={
    state, initialize, cancelInitialize,
    start(id,op){
      if (invalidated || bootstrap || requests.size || !/^[a-f0-9]{48}$/.test(id) || !op || typeof op.model!=='string' && !['models','build_models'].includes(op.kind)) return false;
      if(op.model && !/^[A-Za-z0-9._-]+$/.test(op.model)) return false;
      const s={controller:new AbortController(),preparing:true,status:0,headers:{},chunks:[],done:false,error:''};requests.set(id,s);
      (async()=>{try{
        const q=await request(op);if(s.controller.signal.aborted)throw Error('cancelled');s.preparing=false;
        const response=await originalFetch(q.url,{method:'POST',headers:q.headers,body:q.body,credentials:q.credentials||'include',redirect:'error',signal:s.controller.signal});
        s.status=response.status;if(s.status===401||s.status===403){observedSuccess=false;initialized=false;initializationMessage="来源拒绝请求，请检查本人授权与权限";}for(const k of ['content-type','retry-after']){const v=response.headers.get(k);if(v)s.headers[k]=v}
        if(!response.body)return;
        const reader=response.body.getReader();
        for(;;){const {value,done}=await reader.read();if(done)break;
          for(let offset=0;offset<value.length;offset+=32768){while(s.chunks.length>=8){if(s.controller.signal.aborted)throw Error('cancelled');await new Promise(r=>setTimeout(r,10))}
            s.chunks.push(btoa(String.fromCharCode(...value.subarray(offset,offset+32768))));
          }
        }
      }catch(error){s.error=s.controller.signal.aborted?'cancelled':error?.message==='authorization_required'?'authorization_required':s.preparing?'page_preparation':'browser_network'}finally{s.done=true}})();return true;
    },
    read(id){const s=requests.get(id);if(!s)return {error:'请求已结束',done:true};return {status:s.status,headers:s.headers,chunk:s.chunks.shift()||'',done:s.done&&s.chunks.length===0,error:s.error}},
    cancel(id){const s=requests.get(id);if(!s)return true;s.controller.abort();requests.delete(id);
      if(s.preparing&&!s.done){invalidated=true;initialized=false;observedSuccess=false;initializationMessage='请求准备已中止，请重新打开来源页面';return false}
      return true}
  };
})();
