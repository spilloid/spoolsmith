const { chromium } = require('playwright');
const { default: AxeBuilder } = require('@axe-core/playwright');
const fs = require('fs');
(async () => {
 const browser = await chromium.launch({headless:true});
 const context = await browser.newContext();
 const page = await context.newPage();
 const errors=[]; page.on('pageerror', error => errors.push(error.message));
 const base=process.env.SITE_URL || 'http://127.0.0.1:8765/';
 const out=process.env.SITE_QC_OUTPUT || 'dist/web-qc';fs.mkdirSync(out,{recursive:true});
 for (const width of [360,390,768,1440]) {
   await page.setViewportSize({width,height:1000});await page.goto(base);await page.locator('h1').waitFor();
   const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth);
   if(overflow) throw Error(`horizontal overflow at ${width}`);
   for(const tab of await page.getByRole('tab').all()) {
     await tab.click();const selected=await tab.getAttribute('aria-controls');
     if(!await page.locator('#'+selected).isVisible()) throw Error('tab failed');
     if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)) throw Error(`tab overflow at ${width}`);
   }
   await page.getByRole('tab').first().click();
   const axe=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
   console.log(JSON.stringify({width,axeViolations:axe.violations.map(v=>({id:v.id,impact:v.impact,nodes:v.nodes.map(n=>({target:n.target,summary:n.failureSummary}))}))}));
   if(axe.violations.length) errors.push(`axe failed at ${width}`);
   await page.evaluate(async()=>{for(const img of document.images){img.loading='eager';await img.decode();}});
   await page.screenshot({path:`${out}/site-${width}.png`,fullPage:true});
 }
 await page.getByRole('tab').first().focus();await page.keyboard.press('End');
 if(await page.getByRole('tab').last().getAttribute('aria-selected')!=='true') throw Error('End key failed');
 await page.keyboard.press('Home');await page.keyboard.press('ArrowRight');
 if(await page.getByRole('tab').nth(1).getAttribute('aria-selected')!=='true') throw Error('Arrow key failed');
 const missing=await page.evaluate(()=>[...document.querySelectorAll('a[href^="#"]')].map(a=>a.getAttribute('href')).filter(h=>h!=='#'&&!document.getElementById(h.slice(1))));
 if(missing.length) throw Error('broken anchors '+missing);
 const brokenImages=await page.evaluate(()=>[...document.images].filter(i=>!i.complete||!i.naturalWidth).map(i=>i.src));
 if(brokenImages.length) throw Error('broken images '+brokenImages);
 await page.getByRole('tab').first().click();await page.getByText('Prefer PowerShell?',{exact:true}).click();
 await context.grantPermissions(['clipboard-read','clipboard-write']);
 await page.getByRole('button',{name:'Copy source PC commands',exact:true}).click();
 if(!(await page.evaluate(()=>navigator.clipboard.readText())).includes('--include-driver')) throw Error('clipboard failed');
 const nojs=await browser.newContext({javaScriptEnabled:false});const np=await nojs.newPage();await np.goto(base);
 if(await np.getByRole('tabpanel').count()!==5) throw Error('missing no-JS guides');
 for(const panel of await np.getByRole('tabpanel').all()) if(!await panel.isVisible()) throw Error('hidden no-JS guide');
 console.log(JSON.stringify({base,errors,checks:['responsive layout','all tabs','keyboard navigation','anchors','images','clipboard','no-JS guides']}));
 await browser.close();if(errors.length) process.exitCode=1;
})().catch(e=>{console.error(e);process.exit(1)});
