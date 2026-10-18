'use strict';
const sessions=new Map();
const origins={aistudio:'https://aistudio.google.com',aps:'https://console.cloud.google.com'};
const pages={aistudio:origins.aistudio+'/prompts/new_chat#mixed-api-session',aps:origins.aps+'/agent-platform/studio/multimodal#mixed-api-session'};
const methods=new Set(['state','initialize','cancelInitialize','start','read','cancel','show']);
chrome.runtime.onMessage.addListener((message,sender,respond)=>{
  if(message?.kind!=='connect'||!sender.tab)return false;
  let url;try{url=new URL(sender.url)}catch{return false}
  if(url.protocol!=='http:'||url.hostname!=='127.0.0.1'||!url.port||url.pathname!=='/mixed-browser/connect'||!/^#[a-f0-9]{64}$/.test(url.hash))return false;
  const endpoint='ws://'+url.host+'/mixed-browser/socket?token='+url.hash.slice(1);
  connect(endpoint,sender.tab.id,sender.url).then(()=>respond({ok:true}),()=>respond({ok:false}));return true;
});
function connect(endpoint,tabId,setupURL){
  const existing=sessions.get(endpoint);if(existing)return existing.ready;
  const session={tab:null,source:null,closed:false,requestActive:false,reconnects:0};sessions.set(endpoint,session);
  let resolveReady,rejectReady;
  session.ready=new Promise((resolve,reject)=>{resolveReady=resolve;rejectReady=reject});
  const finish=()=>{if(session.closed)return;session.closed=true;clearTimeout(session.timeout);clearTimeout(session.retry);clearInterval(session.heartbeat);sessions.delete(endpoint);rejectReady(Error('disconnected'))};
  session.finish=finish;
  session.timeout=setTimeout(()=>{finish();session.socket.close()},15000);
  function openSocket(){
    if(session.closed)return;
    const socket=new WebSocket(endpoint);session.socket=socket;
    socket.onmessage=async event=>{
      let message;try{message=JSON.parse(event.data)}catch{finish();socket.close();return}
      if(message.kind==='hello'){
        if(!pages[message.source]||(session.source&&session.source!==message.source)){finish();socket.close();return}
        try{
          if(!session.source){
            const tab=await chrome.tabs.get(tabId);
            if(session.closed||tab.url!==setupURL)throw Error('connection_tab_changed');
            session.source=message.source;session.tab=tabId;
            // Replace the localhost capability; never send it to Google.
            await chrome.tabs.update(tabId,{url:pages[session.source],active:true});
          }else await chrome.tabs.get(session.tab);
          if(session.closed||socket!==session.socket)return;
          socket.send(JSON.stringify({ready:true}));clearTimeout(session.timeout);session.reconnects=0;resolveReady();
        }catch{finish();socket.close()}
        return;
      }
      if(!Number.isSafeInteger(message.id)||!methods.has(message.method)||!session.tab)return;
      // Cancellation follows earlier commands and never falsely acknowledges
      // a delayed start. Old connections cannot reply on a new socket.
      session.commands=(session.commands||Promise.resolve()).then(async()=>{
        if(session.closed||session.socket!==socket||socket.readyState!==WebSocket.OPEN)return;
        try{const result=await execute(session,message.method,message.args||[]);if(socket.readyState===WebSocket.OPEN)socket.send(JSON.stringify({id:message.id,result}))}
        catch{if(socket.readyState===WebSocket.OPEN)socket.send(JSON.stringify({id:message.id,error:'page_unavailable'}))}
      });
    };
    socket.onclose=()=>{
      if(session.closed||session.socket!==socket)return;
      if(session.requestActive){
        // A generated request with no confirmed abort cannot be left running.
        // Login and verification alone never set this flag.
        finish();chrome.tabs.get(session.tab).then(tab=>{if(new URL(tab.url).origin===origins[session.source])return chrome.tabs.remove(session.tab)}).catch(()=>{});return;
      }
      // A disconnected bridge is not permission to delete the user's login.
      // Abort initialization if the official document is still reachable.
      session.commands=(session.commands||Promise.resolve()).then(()=>execute(session,'cancelInitialize',[])).catch(()=>{});
      if(session.reconnects++>=3){finish();return}
      session.retry=setTimeout(openSocket,500*session.reconnects);
    };
    socket.onerror=()=>socket.close();
  }
  session.heartbeat=setInterval(()=>{if(session.socket.readyState===WebSocket.OPEN)session.socket.send('{}')},20000);
  openSocket();return session.ready;
}
// A tab-scoped network fence also covers transports cached before page hooks.
// Keep a cancelled bootstrap fenced until an explicit API generation or tab close.
async function initializationFence(tabId,enabled){
  const id=100000+tabId;
  const update={removeRuleIds:[id]};
  if(enabled)update.addRules=[{id,priority:1,action:{type:'block'},condition:{tabIds:[tabId],regexFilter:'^https://alkalimakersuite-pa\\.clients6\\.google\\.com/.*(GenerateContent|ProxyStreamedCall|ProxyUnaryCall|SavePrompt|CreatePrompt)',resourceTypes:['xmlhttprequest','other']}}];
  await chrome.declarativeNetRequest.updateSessionRules(update);
}
async function execute(session,method,args){
  const connection=session.socket;
  const unavailable=()=>session.closed||session.socket!==connection||connection.readyState!==WebSocket.OPEN;
  const tab=await chrome.tabs.get(session.tab);
  if(['start','initialize'].includes(method)&&unavailable())throw Error('disconnected');
  if(method==='show'){await chrome.tabs.update(session.tab,{active:true});await chrome.windows.update(tab.windowId,{focused:true});return true}
  let allowed=false;try{allowed=new URL(tab.url).origin===origins[session.source]}catch{}
  if(!allowed){if(method==='state'||method==='initialize'){let login=false;try{login=new URL(tab.url).origin==='https://accounts.google.com'}catch{}return {status:'waiting',message:login?'正在等待本人完成 Google 登录':'来源标签页已离开对应官方站点，请返回该来源页面',checks:{official_origin:false}};}if(method==='cancel'){session.requestActive=false;return true}if(method==='cancelInitialize')return true;return false}
  if(tab.status!=='complete'){if(method==='state'||method==='initialize')return {status:'waiting',message:'正在等待官方页面加载'};throw Error('page_loading')}
  if(session.source==='aistudio'&&method==='initialize'){
    try{await initializationFence(session.tab,true)}catch{return {status:'waiting',message:'浏览器初始化拦截未安装，请刷新 MIXED 浏览器连接扩展后重试'}}
  }
  await chrome.scripting.executeScript({target:{tabId:session.tab},world:'MAIN',files:['google-session.js']});
  if(['start','initialize'].includes(method)&&unavailable())throw Error('disconnected');
  if(session.source==='aistudio'&&method==='start'){
    const check=await chrome.scripting.executeScript({target:{tabId:session.tab},world:'MAIN',func:invoke,args:['state',[],origins[session.source]]});
    if(check[0]?.result?.status!=='ready')return false;
    if(unavailable())throw Error('disconnected');
    await initializationFence(session.tab,false);
  }
  if(['start','initialize'].includes(method)&&unavailable()){
    if(session.source==='aistudio'&&method==='start')await initializationFence(session.tab,true);
    throw Error('disconnected');
  }
  if(method==='start')session.requestActive=true;
  let results;
  try{results=await chrome.scripting.executeScript({target:{tabId:session.tab},world:'MAIN',func:invoke,args:[method,args,origins[session.source]]})}
  catch(error){if(session.source==='aistudio'&&method==='start')await initializationFence(session.tab,true);throw error}
  const result=results[0]?.result??false;
  if(session.source==='aistudio'&&method==='start'&&result===false)await initializationFence(session.tab,true);
  if(method==='cancel'&&result===true||method==='start'&&result===false)session.requestActive=false;
  return result;
}
// chrome.scripting serializes this function into the page; validate there too.
function invoke(method,args,origin){
  if(location.origin!==origin||!['state','initialize','cancelInitialize','start','read','cancel'].includes(method))return false;
  if(!Array.isArray(args))return false;
  if(['start','read','cancel'].includes(method)&&!/^[a-f0-9]{48}$/.test(args[0]))return false;
  const api=window.__mixedGoogleWeb;
  if(!api)return method==='state'?{status:'waiting',message:'官方页面连接脚本未就绪'}:false;
  return api[method](...args);
}
chrome.tabs.onRemoved.addListener(id=>{initializationFence(id,false).catch(()=>{});for(const s of sessions.values())if(s.tab===id){s.finish();s.socket.close()}});
