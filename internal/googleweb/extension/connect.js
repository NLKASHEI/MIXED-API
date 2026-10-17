(() => {
  if(location.pathname!=='/mixed-browser/connect'||!/^#[a-f0-9]{64}$/.test(location.hash))return;
  chrome.runtime.sendMessage({kind:'connect'}, result=>{
    const status=document.getElementById('status');if(!status)return;
    status.textContent=chrome.runtime.lastError||!result?.ok?'连接失败。请保持 MIXED 运行，在配置步骤重新点击打开浏览器。':'正在打开官方页面。完成本人登录后，MIXED 会自动连接会话。';
  });
})();
