'use strict';
const sessions=new Map();
const origins={aistudio:'https://aistudio.google.com',aps:'https://console.cloud.google.com'};
const pages={aistudio:origins.aistudio+'/prompts/new_chat',aps:origins.aps+'/vertex-ai/studio/multimodal'};
const methods=new Set(['state','initialize','cancelInitialize','start','read','cancel','show']);
chrome.runtime.onMessage.addListener((message,sender,respond)=>{
  if(message?.kind!=='connect'||!sender.tab)return false;
  let url;try{url=new URL(sender.url)}catch{return false}
  if(url.protocol!=='http:'||url.hostname!=='127.0.0.1'||!url.port||url.pathname!=='/mixed-browser/connect'||!/^#[a-f0-9]{64}$/.test(url.hash))return false;
  const endpoint='ws://'+url.host+'/mixed-browser/socket?token='+url.hash.slice(1);
  connect(endpoint).then(()=>respond({ok:true}),()=>respond({ok:false}));return true;
});
function connect(endpoint){
  const existing=sessions.get(endpoint);if(existing)return existing.ready;
  const session={socket:new WebSocket(endpoint),tab:null,source:null,closed:false};sessions.set(endpoint,session);
  session.ready=new Promise((resolve,reject)=>{
    const timeout=setTimeout(()=>{reject(Error('connection_timeout'));session.socket.close()},15000);
    session.socket.onmessage=async event=>{
      let message;try{message=JSON.parse(event.data)}catch{session.socket.close();return}
      if(message.kind==='hello'){
        if(session.source||!pages[message.source]){session.socket.close();return}
        session.source=message.source;
        try{
          const tab=await chrome.tabs.create({url:pages[session.source]});session.tab=tab.id;
          if(session.closed){await chrome.tabs.remove(tab.id);return}
          session.socket.send(JSON.stringify({ready:true}));clearTimeout(timeout);resolve();
        }catch{clearTimeout(timeout);reject(Error('tab_failed'));session.socket.close()}
        return;
      }
      if(!Number.isSafeInteger(message.id)||!methods.has(message.method)||!session.tab)return;
      // Commands run in arrival order. A cancellation cannot overtake an earlier
      // start waiting for the browser API and falsely acknowledge its absence.
      session.commands=(session.commands||Promise.resolve()).then(async()=>{
        if(session.closed)return;
        try{const result=await execute(session,message.method,message.args||[]);if(session.socket.readyState===WebSocket.OPEN)session.socket.send(JSON.stringify({id:message.id,result}))}
        catch{if(session.socket.readyState===WebSocket.OPEN)session.socket.send(JSON.stringify({id:message.id,error:'page_unavailable'}))}
      });
    };
    session.socket.onclose=()=>{clearTimeout(timeout);clearInterval(session.heartbeat);session.closed=true;sessions.delete(endpoint);reject(Error('disconnected'));if(session.tab)chrome.tabs.remove(session.tab).catch(()=>{})};
    session.socket.onerror=()=>session.socket.close();
  });
  session.heartbeat=setInterval(()=>{if(session.socket.readyState===WebSocket.OPEN)session.socket.send('{}')},20000);
  return session.ready;
}
async function execute(session,method,args){
  const tab=await chrome.tabs.get(session.tab);
  if(method==='show'){await chrome.tabs.update(session.tab,{active:true});await chrome.windows.update(tab.windowId,{focused:true});return true}
  let allowed=false;try{allowed=new URL(tab.url).origin===origins[session.source]}catch{}
  if(!allowed){if(method==='state'||method==='initialize')return {status:'waiting',message:'请在当前浏览器的官方页面完成本人登录与页面提示'};if(method==='cancel'||method==='cancelInitialize')return true;return false}
  if(tab.status!=='complete'){if(method==='state'||method==='initialize')return {status:'waiting',message:'官方页面正在加载，请等待后连接会话'};throw Error('page_loading')}
  await chrome.scripting.executeScript({target:{tabId:session.tab},world:'MAIN',files:['google-session.js']});
  const results=await chrome.scripting.executeScript({target:{tabId:session.tab},world:'MAIN',func:invoke,args:[method,args,origins[session.source]]});
  return results[0]?.result??false;
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
chrome.tabs.onRemoved.addListener(id=>{for(const s of sessions.values())if(s.tab===id)s.socket.close()});
