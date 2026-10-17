(() => {
  if(location.pathname!=='/mixed-browser/connect'||!/^#[a-f0-9]{64}$/.test(location.hash))return;
  chrome.runtime.sendMessage({kind:'connect'}, result=>{
    const status=document.getElementById('status');if(!status)return;
    status.textContent=chrome.runtime.lastError||!result?.ok?'连接失败。请保持 MIXED 运行，在配置步骤重新点击打开浏览器。':'扩展已连接。请在打开的官方页面完成本人登录，然后回 MIXED 点击“连接会话”。';
  });
})();
