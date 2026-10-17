import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp,writeFile,rm,mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join,resolve,sep } from 'node:path';
import { spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import { chromium } from 'playwright';

const root=await mkdtemp(join(tmpdir(),'mixed-ui-'));
let child,browser;
const mock=createServer(async(req,res)=>{
  if(req.url==='/v1/models'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({data:[{id:'test-chat'},{id:'test-reasoning'}]}));return;}
  const chunks=[];for await(const chunk of req)chunks.push(chunk);
  const input=JSON.parse(Buffer.concat(chunks).toString());assert.equal(input.model,'test-chat');
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({model:input.model,choices:[{message:{role:'assistant',content:'Alias routing works'}}]}));
});
try {
  await new Promise(done=>mock.listen(0,'127.0.0.1',done));
  const portProbe=createServer();await new Promise(done=>portProbe.listen(0,'127.0.0.1',done));const port=portProbe.address().port;await new Promise(done=>portProbe.close(done));
  const base=`http://127.0.0.1:${port}`;
  const config={listen:`127.0.0.1:${port}`,api_key:'ui-test-client',admin_key:'ui-test-admin',backends:[
    {id:'codex',name:'Codex',url:'http://127.0.0.1:8901/v1',kind:'http',auth:'none',enabled:false},
    {id:'vertex',name:'Vertex',url:`http://127.0.0.1:${mock.address().port}/v1`,auth:'none',enabled:false},
    ...['opencode','workbuddy','aps','aistudio'].map(id=>({id,name:id,url:'http://127.0.0.1:1/v1',auth:'none',enabled:false}))
  ]};
  const path=join(root,'config.json');await writeFile(path,JSON.stringify(config));
  child=spawn(resolve(process.env.MIXED_TEST_BINARY||'.git/MIXED-preview.exe'),['--no-browser','--config',path],{windowsHide:true,stdio:'ignore',env:{...process.env,APPDATA:join(root,'profile')}});
  child.on('error',error=>console.error(error.message));
  let ready=false;for(let i=0;i<60;i++){try{ready=(await fetch(base+'/health')).ok;if(ready)break}catch{}await delay(100)}assert(ready,'gateway did not start');
  browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1000}});
  const output=resolve(process.env.MIXED_UI_OUTPUT||'.git/ui-evidence');await mkdir(output,{recursive:true});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(base+'/#admin=ui-test-admin');await page.locator('#app').waitFor({state:'visible'});
  assert.equal(new URL(page.url()).hash,'','bootstrap credential fragment remains visible');
  await page.locator('#providers .card').nth(1).getByRole('button').click();
  await page.locator('#manual-settings').evaluate(n=>n.open=true);await page.locator('#backend-enabled').check();await page.locator('#save-provider').click();
  await page.locator('#next-provider').click();await page.locator('#verify-provider').click();await page.locator('#provider-progress').filter({hasText:'已读取 2 个模型'}).waitFor();
  await page.getByRole('button',{name:'关闭',exact:true}).first().click();
  // Reopen completion, then fail the next real HTTP refresh. Old model data
  // must never turn a failed verification into a successful completion.
  await page.locator('#providers .card').nth(1).getByRole('button').click();
  await page.route('**/v1/models',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{message:'fixture unavailable'}})}));
  await page.locator('#verify-provider').click();await page.locator('#provider-progress').filter({hasText:'本次读取失败'}).waitFor();
  assert(await page.locator('#provider-models').isHidden(),'stale models were accepted');
  await page.locator('#back-provider').click();await page.locator('#back-provider').click();
  assert(await page.locator('#manual-settings').isVisible(),'user cannot return to fix connection settings');
  await page.unroute('**/v1/models');await page.keyboard.press('Escape');await page.locator('#refresh').click();
  // A fresh session exercises the normal managed 快速V path. Only the
  // installation/activation boundary is faked; config and discovery are real.
  await page.route('**/api/assist/status',route=>route.fulfill({contentType:'application/json',body:'{"platform":"windows","native_codex_supported":true,"tools":{}}'}));
  await page.reload();await page.locator('#app').waitFor({state:'visible'});
  await page.route('**/api/assist/vertex/prepare',route=>route.fulfill({contentType:'application/json',body:'{"ok":true}'}));
  await page.route('**/api/assist/vertex/activate',route=>route.fulfill({contentType:'application/json',body:'{"ok":true}'}));
  await page.route('**/api/status',route=>route.fulfill({contentType:'application/json',body:'{"processes":{"setup:vertex":"installed: fixture prepared"}}'}));
  await page.locator('#providers .card').nth(1).getByRole('button').click();
  assert(await page.locator('#official-field').isHidden(),'credentials shown before the authorization step');
  assert(await page.locator('#verify-provider').isHidden(),'preparation can skip to verification');
  await page.locator('#prepare-provider').click();await page.locator('#official-field').waitFor({state:'visible'});
  await page.locator('#next-provider').click();await page.locator('#toast').filter({hasText:'Express API Key'}).waitFor();
  assert(await page.locator('#official-field').isVisible(),'快速V continued without Express API Key');
  await page.locator('#toast').waitFor({state:'hidden'});await page.screenshot({path:join(output,'onboarding-fast-v.png'),fullPage:true});
  await page.locator('#official-key').fill('fixture-express-key');await page.locator('#next-provider').click();
  await page.locator('#verify-provider').click();await page.locator('#provider-progress').filter({hasText:'已读取 2 个模型'}).waitFor();
  assert.equal(await page.locator('.client-summary').textContent(),`所有来源共用这个客户端地址：\n${base}/v1`);
  await page.keyboard.press('Escape');await page.unroute('**/api/status');await page.unroute('**/api/assist/vertex/prepare');await page.unroute('**/api/assist/vertex/activate');
  await page.locator('[data-page="models"]').click();await page.locator('#model-search').fill('test-chat');assert.equal(await page.locator('.model-row').count(),1);
  await page.locator('#open-alias').click();await page.locator('#alias-name').fill('daily-chat');await page.locator('#alias-target').selectOption('vertex/test-chat');await page.locator('#alias-form').getByRole('button',{name:'保存别名'}).click();
  await page.locator('#alias-dialog').waitFor({state:'hidden'});await page.locator('#aliases').getByText('daily-chat',{exact:true}).waitFor();
  const response=await fetch(base+'/v1/chat/completions',{method:'POST',headers:{Authorization:'Bearer ui-test-client','Content-Type':'application/json'},body:JSON.stringify({model:'daily-chat',messages:[{role:'user',content:'hello'}]})});assert.equal(response.status,200);assert.equal((await response.json()).choices[0].message.content,'Alias routing works');
  await page.locator('[data-page="diagnostics"]').click();await page.locator('#run-diagnostics').click();await page.locator('#diagnostics').getByText('2 个模型',{exact:false}).waitFor();
  await page.locator('[data-page="home"]').click();
  await page.locator('#toast').waitFor({state:'hidden'});await page.screenshot({path:join(output,'console-desktop.png'),fullPage:true});
  await page.locator('[data-page="admin"]').click();
  await page.locator('#admin-engines tr').nth(5).waitFor();
  assert.equal(await page.locator('#admin-enabled').textContent(),'1');
  assert.equal(await page.locator('#admin-engines tr').count(),6);
  assert.equal(await page.locator('#admin-engines').getByText(/http:\/\//).count(),0,'internal addresses are exposed in the admin list');
  // Native lifecycle is real, including persistence; no child process or
  // upstream network request is needed to enable/disable OpenCode.
  const prepared=await fetch(base+'/api/assist/opencode/prepare',{method:'POST',headers:{Authorization:'Bearer ui-test-admin'}});assert.equal(prepared.status,200);
  await page.locator('#refresh-admin').click();
  const nativeRow=page.locator('#admin-engines tr').filter({hasText:'OpenCode'});
  await nativeRow.getByRole('button',{name:'启用',exact:true}).click();
  await nativeRow.getByRole('button',{name:'停用',exact:true}).waitFor();
  assert.equal(await page.locator('#admin-running').textContent(),'1');
  await nativeRow.getByRole('button',{name:'停用',exact:true}).click();
  await nativeRow.getByRole('button',{name:'启用',exact:true}).waitFor();
  const savedNative=await(await fetch(base+'/api/config',{headers:{Authorization:'Bearer ui-test-admin'}})).json();
  const savedOpenCode=savedNative.backends.find(b=>b.id==='opencode');assert.equal(savedOpenCode.kind,'builtin');assert.equal(savedOpenCode.enabled,false);
  // Switching an existing-service setup to the integrated core in the same
  // dialog must clear manual mode and keep the server's newly prepared URL.
  await page.locator('[data-page="home"]').click();
  await page.locator('#providers .card').filter({hasText:'OpenCode'}).getByRole('button').click();
  await page.locator('#manual-settings').evaluate(n=>n.open=true);
  await page.locator('#backend-url').fill('http://127.0.0.1:1/v1');
  await page.locator('#save-provider').click();await page.locator('#next-provider').waitFor();
  await page.locator('#back-provider').click();await page.locator('#prepare-provider').click();
  await page.locator('#official-field').waitFor({state:'visible',timeout:10000});
  await page.locator('#next-provider').click();await page.locator('#verify-provider').waitFor();
  const switched=await(await fetch(base+'/api/config',{headers:{Authorization:'Bearer ui-test-admin'}})).json();
  assert.equal(switched.backends.find(b=>b.id==='opencode').kind,'builtin');
  assert.equal(switched.backends.find(b=>b.id==='opencode').url,'https://opencode.ai/inference/v1');
  await page.keyboard.press('Escape');await page.locator('[data-page="admin"]').click();
  await page.screenshot({path:join(output,'console-admin.png'),fullPage:true});
  await page.locator('[data-page="home"]').click();
  await page.setViewportSize({width:390,height:844});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile page overflows horizontally');
  await page.screenshot({path:join(output,'console-mobile.png'),fullPage:true});
  await page.locator('#providers .card').nth(1).getByRole('button').click();
  assert(await page.locator('#provider-dialog').isVisible(),'mobile onboarding does not open');await page.keyboard.press('Escape');
  // Android capability negotiation shares the real preparation/settings APIs.
  // Only native window opening/state is substituted; no login is performed.
  await page.unroute('**/api/assist/status');
  await page.route('**/api/assist/status',route=>route.fulfill({contentType:'application/json',body:'{"platform":"android","builtin_backends":["aps","aistudio"]}'}));
  await page.route('**/api/accounts/**',route=>route.fulfill({contentType:'application/json',body:'{"status":"waiting","message":"请在官方页面完成本人授权，并发送一条测试消息"}'}));
  await page.reload();await page.locator('#app').waitFor({state:'visible'});
  for(const [id,name] of [['aps','无头V'],['aistudio','AIS']]){
    await page.locator('#providers .card').filter({has:page.getByRole('heading',{name,exact:true})}).getByRole('button').click();
    await page.locator('#prepare-provider').click();await page.locator('#authorize-provider').waitFor({state:'visible'});
    assert(await page.locator('#official-field').isHidden(),'Google source incorrectly asks for Express key');
    await page.locator('#authorize-provider').click();
    await page.locator('#provider-progress').filter({hasText:'测试消息'}).waitFor();
    await page.locator('#next-provider').click();await page.locator('#toast').filter({hasText:'测试消息'}).waitFor();
    assert(await page.locator('#authorize-provider').isVisible(),'unready source bypassed authorization');
    const state=await(await fetch(base+'/api/config',{headers:{Authorization:'Bearer ui-test-admin'}})).json();
    assert.equal(state.backends.find(b=>b.id===id).enabled,false);
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile Google wizard overflows');
    await page.screenshot({path:join(output,`android-${id}-setup.png`),fullPage:true});
    await page.keyboard.press('Escape');
  }
  await page.locator('[data-page="admin"]').click();await page.locator('#load-engine-settings').click();
  await page.locator('#engine-settings-form').waitFor({state:'visible'});await page.locator('#engine-retries').fill('1');await page.locator('#engine-timeout').fill('60');
  await page.locator('#engine-settings-form').getByRole('button').click();await page.locator('#engine-settings-message').filter({hasText:'已保存'}).waitFor();
  await page.locator('#logout').click();await page.locator('#login').waitFor({state:'visible'});assert.equal(await page.evaluate(()=>sessionStorage.getItem('mixed-admin')),null);
  assert.deepEqual(errors,[],'browser JavaScript errors');
  console.log('UI passed: authenticated bootstrap, provider setup, model search, alias inference, diagnostics, mobile layout and logout.');
} finally {
  await browser?.close();child?.kill();await new Promise(done=>mock.close(done));
  // Only our mkdtemp-owned fixture is removed, never an application profile.
  assert(resolve(root).startsWith(resolve(tmpdir())+sep),'unexpected temporary fixture path');
  await rm(root,{recursive:true,force:true,maxRetries:5,retryDelay:100});
}
