const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  const shots = 'D:/UGit/atomix-demo/docs/ui-check';
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text().slice(0, 120)); });

  // 登录
  await page.goto('http://101.32.28.8/login');
  await page.fill('input[type="email"]', 'ui-test@atomix.dev');
  await page.fill('input[type="password"]', 'uitest123');
  await page.click('button:has-text("开始")');
  await page.waitForURL('**/dashboard**', { timeout: 15000 });
  await page.waitForTimeout(1200);
  await page.screenshot({ path: shots + '/7-dashboard.png' });

  // 首页核心元素
  const greet = await page.locator('.greet').innerText().catch(() => 'MISS');
  console.log('greet:', JSON.stringify(greet));

  // 打开功能菜单
  await page.click('.menu-btn');
  await page.waitForTimeout(400);
  const menuItems = await page.locator('.menu-item').allInnerTexts();
  console.log('menu items:', JSON.stringify(menuItems));
  await page.screenshot({ path: shots + '/8-menu.png' });
  await page.keyboard.press('Escape');
  await page.click('body', { position: { x: 10, y: 400 } });

  // 上传文本附件
  const fs = require('fs');
  fs.writeFileSync('D:/UGit/_att_test.txt', '功能需求：\n1. 记录每天心情\n2. 按日历视图展示\n3. 支持导出');
  await page.setInputFiles('input[type="file"]', 'D:/UGit/_att_test.txt');
  await page.waitForTimeout(1000);
  const atts = await page.locator('.menu ~ * .att, .atts .att, .att').count();
  console.log('attachments on dashboard:', atts);

  // 输入需求并发送（走 dashboard → workspace 全链路）
  await page.fill('.ask textarea', '根据附件需求做一个心情日历应用');
  await page.screenshot({ path: shots + '/9-dashboard-filled.png' });
  await page.click('.go');
  await page.waitForURL('**/workspace**', { timeout: 15000 });
  await page.waitForTimeout(2000);
  await page.screenshot({ path: shots + '/10-workspace-from-dash.png' });

  console.log('JS errors:', errors.length ? JSON.stringify(errors.slice(0, 5)) : 'none');
  await browser.close();
  console.log('VERIFY4_DONE');
})().catch(e => { console.error('FAIL:', e.message); process.exit(1); });
