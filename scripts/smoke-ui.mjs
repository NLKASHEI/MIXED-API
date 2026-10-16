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
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(base+'/#admin=ui-test-admin');await page.locator('#app').waitFor({state:'visible'});
  assert.equal(new URL(page.url()).hash,'','bootstrap credential fragment remains visible');
  await page.locator('#providers .card').nth(1).getByRole('button').click();
  await page.locator('#manual-settings').evaluate(n=>n.open=true);await page.locator('#backend-enabled').check();await page.locator('#save-provider').click();
  await page.locator('#next-provider').click();await page.locator('#verify-provider').click();await page.locator('#provider-progress').filter({hasText:'已读取 2 个模型'}).waitFor();
  await page.getByRole('button',{name:'关闭',exact:true}).first().click();
  await page.locator('[data-page="models"]').click();await page.locator('#model-search').fill('test-chat');assert.equal(await page.locator('.model-row').count(),1);
  await page.locator('#open-alias').click();await page.locator('#alias-name').fill('daily-chat');await page.locator('#alias-target').selectOption('vertex/test-chat');await page.locator('#alias-form').getByRole('button',{name:'保存别名'}).click();
  await page.locator('#alias-dialog').waitFor({state:'hidden'});await page.locator('#aliases').getByText('daily-chat',{exact:true}).waitFor();
  const response=await fetch(base+'/v1/chat/completions',{method:'POST',headers:{Authorization:'Bearer ui-test-client','Content-Type':'application/json'},body:JSON.stringify({model:'daily-chat',messages:[{role:'user',content:'hello'}]})});assert.equal(response.status,200);assert.equal((await response.json()).choices[0].message.content,'Alias routing works');
  await page.locator('[data-page="diagnostics"]').click();await page.locator('#run-diagnostics').click();await page.locator('#diagnostics').getByText('2 个模型',{exact:false}).waitFor();
  await page.locator('[data-page="home"]').click();
  const output=resolve(process.env.MIXED_UI_OUTPUT||'.git/ui-evidence');await mkdir(output,{recursive:true});
  await page.locator('#toast').waitFor({state:'hidden'});await page.screenshot({path:join(output,'console-desktop.png'),fullPage:true});
  await page.locator('[data-page="admin"]').click();
  await page.locator('#admin-engines tr').nth(5).waitFor();
  assert.equal(await page.locator('#admin-enabled').textContent(),'1');
  assert.equal(await page.locator('#admin-engines tr').count(),6);
  await page.screenshot({path:join(output,'console-admin.png'),fullPage:true});
  await page.locator('[data-page="home"]').click();
  await page.setViewportSize({width:390,height:844});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile page overflows horizontally');
  await page.screenshot({path:join(output,'console-mobile.png'),fullPage:true});
  await page.locator('#providers .card').nth(1).getByRole('button').click();
  assert(await page.locator('#provider-dialog').isVisible(),'mobile onboarding does not open');await page.keyboard.press('Escape');
  await page.locator('#logout').click();await page.locator('#login').waitFor({state:'visible'});assert.equal(await page.evaluate(()=>sessionStorage.getItem('mixed-admin')),null);
  assert.deepEqual(errors,[],'browser JavaScript errors');
  console.log('UI passed: authenticated bootstrap, provider setup, model search, alias inference, diagnostics, mobile layout and logout.');
} finally {
  await browser?.close();child?.kill();await new Promise(done=>mock.close(done));
  // Only our mkdtemp-owned fixture is removed, never an application profile.
  assert(resolve(root).startsWith(resolve(tmpdir())+sep),'unexpected temporary fixture path');
  await rm(root,{recursive:true,force:true,maxRetries:5,retryDelay:100});
}
