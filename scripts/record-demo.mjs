import { spawn, spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const args = process.argv.slice(2);
const outputIndex = args.indexOf('--output');
const requestedOutput = outputIndex >= 0 ? args[outputIndex + 1] : null;
const requireLLM = args.includes('--require-llm');
if (outputIndex >= 0 && !requestedOutput) {
  throw new Error('Укажите путь после --output.');
}
if (requireLLM && (!process.env.BUDDY_LLM_URL || !process.env.BUDDY_LLM_MODEL || !process.env.BUDDY_LLM_API_KEY)) {
  throw new Error('Для --require-llm задайте BUDDY_LLM_URL, BUDDY_LLM_MODEL и BUDDY_LLM_API_KEY в окружении.');
}

const ffmpegAvailable = spawnSync('ffmpeg', ['-version'], { stdio: 'ignore' }).status === 0;
const defaultExtension = ffmpegAvailable ? 'mp4' : 'webm';
const timestamp = new Date().toISOString().replaceAll(':', '-').replace(/\.\d{3}Z$/, 'Z');
const outputPath = path.resolve(
  requestedOutput || path.join(projectRoot, 'recordings', `ai-buddy-demo-${timestamp}.${defaultExtension}`),
);
const outputExtension = path.extname(outputPath).toLowerCase();
if (!['.mp4', '.webm'].includes(outputExtension)) {
  throw new Error('Формат видео должен быть .mp4 или .webm.');
}
if (outputExtension === '.mp4' && !ffmpegAvailable) {
  throw new Error('Для MP4 нужен ffmpeg. Установите его или укажите выходной файл с расширением .webm.');
}

const temporaryDirectory = await mkdtemp(path.join(tmpdir(), 'ai-buddy-recording-'));
const statePath = path.join(temporaryDirectory, 'demo-state.json');
const executablePath = path.join(temporaryDirectory, process.platform === 'win32' ? 'buddy.exe' : 'buddy');
const videoDirectory = path.join(temporaryDirectory, 'video');
let serverProcess;
let browser;
let context;

async function findAvailablePort() {
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const { port } = server.address();
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  return port;
}

async function waitForServer(url) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (serverProcess.exitCode !== null) {
      throw new Error('Сервер завершился до запуска страницы.');
    }
    try {
      const response = await fetch(`${url}/api/health`);
      if (response.ok) return;
    } catch {
      // The server may need a moment to bind its port.
    }
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  throw new Error('Сервер не ответил за 30 секунд.');
}

async function pause(page, milliseconds = 1800) {
  await page.waitForTimeout(milliseconds);
}

async function installCursor(page) {
  await page.evaluate(() => {
    const cursor = document.createElement('div');
    cursor.id = 'recording-cursor';
    cursor.setAttribute('aria-hidden', 'true');
    cursor.innerHTML = '<svg width="30" height="38" viewBox="0 0 30 38"><path d="M3 2v27l7-7 5 12 6-3-5-11h11L3 2Z" fill="#172039" stroke="#fff" stroke-width="2.5" stroke-linejoin="round"/></svg>';
    Object.assign(cursor.style, {
      position: 'fixed',
      zIndex: '10000',
      left: '0',
      top: '0',
      width: '30px',
      height: '38px',
      pointerEvents: 'none',
      willChange: 'transform',
      transition: 'transform 460ms cubic-bezier(.22,.75,.2,1)',
      filter: 'drop-shadow(0 2px 3px rgba(0,0,0,.4))',
    });
    const pulse = document.createElement('div');
    pulse.id = 'recording-click-pulse';
    pulse.setAttribute('aria-hidden', 'true');
    Object.assign(pulse.style, {
      position: 'fixed',
      zIndex: '9999',
      left: '0',
      top: '0',
      width: '34px',
      height: '34px',
      borderRadius: '50%',
      border: '3px solid #1678f3',
      background: 'rgba(22,120,243,.22)',
      opacity: '0',
      pointerEvents: 'none',
      transform: 'translate(-50%,-50%) scale(.35)',
      willChange: 'transform, opacity',
    });
    document.body.append(cursor, pulse);
    window.__demoCursor = {
      move(x, y) {
        cursor.style.transform = `translate3d(${Math.round(x)}px, ${Math.round(y)}px, 0)`;
      },
      click() {
        pulse.style.left = `${cursor.getBoundingClientRect().left + 3}px`;
        pulse.style.top = `${cursor.getBoundingClientRect().top + 3}px`;
        pulse.animate([
          { opacity: 0, transform: 'translate(-50%,-50%) scale(.35)' },
          { opacity: .9, transform: 'translate(-50%,-50%) scale(1)' },
          { opacity: 0, transform: 'translate(-50%,-50%) scale(1.55)' },
        ], { duration: 360, easing: 'ease-out' });
      },
    };
    cursor.style.transform = 'translate3d(80px, 105px, 0)';
  });
}

async function moveCursorTo(page, locator) {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  if (!box) throw new Error('Не удалось определить позицию элемента для видеозаписи.');
  await page.evaluate(({ x, y }) => window.__demoCursor.move(x, y), {
    x: box.x + box.width / 2,
    y: box.y + box.height / 2,
  });
  await pause(page, 520);
}

async function clickWithCursor(page, locator) {
  await moveCursorTo(page, locator);
  await page.evaluate(() => window.__demoCursor.click());
  await pause(page, 120);
  await locator.click();
}

async function selectWithCursor(page, locator, value) {
  await moveCursorTo(page, locator);
  await page.evaluate(() => window.__demoCursor.click());
  await pause(page, 120);
  await locator.selectOption(value);
}

async function askFromSuggestion(page, name, { llm = false, outcome = 'Ответ с источником' } = {}) {
  const suggestion = page.getByRole('button', { name });
  const previousMessages = await page.locator('.message.bot').count();
  await clickWithCursor(page, suggestion);
  const message = await waitForAssistantMessage(page, previousMessages);
  const title = await message.locator('strong').innerText();
  if (!title.includes(outcome)) {
    const text = await message.locator('p').innerText().catch(() => '');
    throw new Error(`Ожидался исход «${outcome}», получен «${title}»${text ? `: ${text}` : ''}`);
  }
  if (llm) {
    await message.locator('.source').filter({ hasText: 'источник выбран моделью' }).waitFor();
  }
  await pause(page, 1900);
  return message;
}

async function waitForAssistantMessage(page, previousMessages = 0) {
  await page.waitForFunction((previousCount) => {
    const messages = [...document.querySelectorAll('.message.bot')];
    if (messages.length <= previousCount) return false;
    const titles = ['Ответ с источником', 'Нужно участие человека', 'Нет подтверждённого ответа', 'Сервис недоступен'];
    const latest = messages.at(-1);
    return titles.some(value => latest.querySelector('strong')?.textContent?.includes(value));
  }, previousMessages, { timeout: 60_000 });
  return page.locator('.message.bot').last();
}

async function showCaption(page, text) {
  await page.evaluate((caption) => {
    let node = document.querySelector('#recording-caption');
    if (!node) {
      node = document.createElement('div');
      node.id = 'recording-caption';
      node.setAttribute('aria-hidden', 'true');
      Object.assign(node.style, {
        position: 'fixed',
        zIndex: '9999',
        left: '50%',
        bottom: '20px',
        transform: 'translateX(-50%)',
        maxWidth: '90vw',
        padding: '12px 20px',
        borderRadius: '12px',
        background: 'rgba(23, 32, 57, .92)',
        color: '#fff',
        font: '600 16px/1.35 Arial, sans-serif',
        boxShadow: '0 8px 30px rgba(0, 0, 0, .18)',
        textAlign: 'center',
        transition: 'opacity .2s ease',
      });
      document.body.append(node);
    }
    node.textContent = caption;
  }, text);
}

async function run() {
  const port = await findAvailablePort();
  const build = spawnSync('go', ['build', '-o', executablePath, './cmd/buddy'], {
    cwd: projectRoot,
    encoding: 'utf8',
    timeout: 120_000,
  });
  if (build.error || build.status !== 0) {
    throw new Error(`Не удалось собрать Go-сервер:\n${build.stderr || build.error?.message || 'go build завершился с ошибкой'}`);
  }

  serverProcess = spawn(executablePath, [], {
    cwd: projectRoot,
    env: {
      ...process.env,
      PORT: String(port),
      BUDDY_DATA_FILE: statePath,
    },
    stdio: 'ignore',
  });
  serverProcess.once('error', error => {
    console.error(`Не удалось запустить Go-сервер: ${error.message}`);
  });
  await waitForServer(`http://127.0.0.1:${port}`);

  await mkdir(path.dirname(outputPath), { recursive: true });
  await mkdir(videoDirectory, { recursive: true });

  try {
    browser = await chromium.launch({ headless: true });
  } catch (error) {
    throw new Error(`Не удалось запустить Chromium. Выполните «npx playwright install chromium».\n${error.message}`);
  }
  context = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 1,
    reducedMotion: 'reduce',
    recordVideo: { dir: videoDirectory, size: { width: 1600, height: 900 } },
  });
  const page = await context.newPage();
  page.setDefaultTimeout(requireLLM ? 60_000 : 15_000);
  await page.goto(`http://127.0.0.1:${port}`, { waitUntil: 'domcontentloaded' });
  await page.locator('[data-role="intern"]').waitFor({ state: 'visible' });
  await installCursor(page);

  await showCaption(page, requireLLM
    ? 'AI-buddy · демонстрация с внешней моделью'
    : 'AI-buddy · локальный прототип · без внешней модели');
  await pause(page, 2200);

  await showCaption(page, '01 · Стажёр открывает рабочее пространство');
  await clickWithCursor(page, page.locator('[data-role="intern"]'));
  await page.getByRole('heading', { name: 'Добро пожаловать в команду' }).waitFor();
  await pause(page);

  const initialMessageCount = await page.locator('.message.bot').count();
  await clickWithCursor(page, page.getByRole('button', { name: /Открыть сценарий/ }));
  await page.locator('#chat-form').waitFor();
  await showCaption(page, '02 · Задайте вопрос о доступе к репозиторию');
  const answer = await waitForAssistantMessage(page, initialMessageCount);
  const firstTitle = await answer.locator('strong').innerText();
  if (!firstTitle.includes('Ответ с источником')) {
    const text = await answer.locator('p').innerText().catch(() => '');
    throw new Error(`Модель не вернула ответ с источником: ${firstTitle}${text ? ` — ${text}` : ''}`);
  }
  if (requireLLM) {
    await answer.locator('.source').filter({ hasText: 'источник выбран моделью' }).waitFor();
  }
  await answer.locator('.source').filter({ hasText: 'K-101' }).waitFor();
  await showCaption(page, requireLLM
    ? '03 · DeepSeek выбрал разрешённый источник K-101; ответ взят из карточки'
    : '03 · Локальный поиск выбрал карточку K-101; ответ взят из неё');
  await pause(page, 3200);

  await clickWithCursor(page, page.locator('[data-action="create-request"]'));
  await page.getByText('Заявка создана', { exact: true }).waitFor();
  await page.locator('.flow-status').filter({ hasText: 'ждёт наставника' }).waitFor();
  await showCaption(page, '04 · Заявка создана мок-сервисом; статус — ожидание наставника');
  await pause(page, 2800);

  await showCaption(page, '05 · Запрос, требующий решения, проходит через Human Gate');
  const mergeGate = await askFromSuggestion(page, 'Нужно согласование?', {
    llm: false,
    outcome: 'Нужно участие человека',
  });
  await clickWithCursor(page, mergeGate.getByRole('button', { name: 'Передать вопрос' }));
  await page.getByText('Передано человеку', { exact: true }).waitFor();
  await showCaption(page, '06 · AI не принимает решение за наставника и создаёт передачу');
  await pause(page, 1800);

  await showCaption(page, '07 · Неизвестный вопрос приводит к честному fallback');
  await clickWithCursor(page, page.locator('#chat-input'));
  await page.locator('#chat-input').fill('Где находится офис команды в этом демо?');
  await clickWithCursor(page, page.locator('#chat-form button[type="submit"]'));
  await page.locator('.message.bot').filter({ hasText: 'Нет подтверждённого ответа' }).last().waitFor();
  await pause(page, 1900);

  await selectWithCursor(page, page.locator('#role-select'), 'mentor');
  await showCaption(page, '08 · Наставник видит запрос доступа и передачу в своей очереди');
  const acceptHandoff = page.locator('[data-action="accept-handoff"]').first();
  await acceptHandoff.waitFor({ state: 'visible' });
  await clickWithCursor(page, acceptHandoff);
  await page.locator('.mentor-row').filter({ hasText: 'принято' }).waitFor();
  const approveButton = page.locator('[data-action="approve-request"]');
  await approveButton.waitFor({ state: 'visible' });
  await pause(page, 2300);
  await clickWithCursor(page, approveButton);
  await page.locator('.mentor-row').getByText('подтверждена').waitFor();
  await showCaption(page, '09 · Наставник принимает решение по заявке на доступ');
  await pause(page, 1800);

  await selectWithCursor(page, page.locator('#role-select'), 'intern');
  const finalMessageCount = await page.locator('.message.bot').count();
  await clickWithCursor(page, page.getByRole('button', { name: /Открыть сценарий/ }));
  const finalAnswer = await waitForAssistantMessage(page, finalMessageCount);
  const finalTitle = await finalAnswer.locator('strong').innerText();
  if (!finalTitle.includes('Ответ с источником')) {
    const text = await finalAnswer.locator('p').innerText().catch(() => '');
    throw new Error(`Модель не вернула повторный ответ с источником: ${finalTitle}${text ? ` — ${text}` : ''}`);
  }
  if (requireLLM) {
    await finalAnswer.locator('.source').filter({ hasText: 'источник выбран моделью' }).waitFor();
  }
  await finalAnswer.locator('.source').filter({ hasText: 'K-101' }).waitFor();
  await clickWithCursor(page, page.locator('[data-action="show-request"]').last());
  await page.locator('.message.bot').filter({ hasText: 'подтверждена. Можно настраивать окружение.' }).waitFor();
  await showCaption(page, '10 · Стажёр видит фактический статус и следующий шаг');
  await pause(page, 2500);

  await showCaption(page, '11 · Проверка устаревшей информации тоже направляется человеку');
  await askFromSuggestion(page, 'Staging', { outcome: 'Нужно участие человека' });
  await pause(page, 1000);

  await selectWithCursor(page, page.locator('#role-select'), 'mentor');
  await showCaption(page, '12 · Наставник добавляет ответ в базу знаний');
  const faqQuestion = 'Как запросить доступ к учебному backend-репозиторию?';
  const faqAnswer = 'Стажёр отправляет заявку на доступ через AI-buddy; наставник проверяет и подтверждает её.';
  await page.locator('#faq-form input[name="question"]').fill(faqQuestion);
  await page.locator('#faq-form textarea[name="answer"]').fill(faqAnswer);
  await clickWithCursor(page, page.locator('#faq-form button[type="submit"]'));
  const faqCard = page.locator('.knowledge-card').filter({ hasText: faqQuestion }).first();
  await faqCard.waitFor();
  await showCaption(page, '13 · Новый ответ получает pending; похожие знания показываются для проверки');
  await pause(page, 1800);
  await clickWithCursor(page, faqCard.getByRole('button', { name: 'Проверить и утвердить' }));
  await faqCard.getByText('approved', { exact: true }).waitFor();
  await showCaption(page, '14 · Только наставник утверждает FAQ и задаёт его срок действия');
  await pause(page, 1800);

  await selectWithCursor(page, page.locator('#role-select'), 'intern');
  await clickWithCursor(page, page.locator('#main-nav [data-view="chat"]'));
  await page.locator('#chat-form').waitFor();
  await clickWithCursor(page, page.locator('#chat-input'));
  await page.locator('#chat-input').fill(faqQuestion);
  await clickWithCursor(page, page.locator('#chat-form button[type="submit"]'));
  const faqAnswerMessage = page.locator('.message.bot').filter({ hasText: 'Ответ с источником' }).last();
  await faqAnswerMessage.waitFor();
  if (requireLLM) {
    await faqAnswerMessage.locator('.source').filter({ hasText: 'источник выбран моделью' }).waitFor();
  }
  await faqAnswerMessage.locator('.source').filter({ hasText: 'FAQ-' }).waitFor();
  await showCaption(page, '15 · После утверждения ответ найден и повторно использован в чате');
  await pause(page, 2200);

  await selectWithCursor(page, page.locator('#role-select'), 'manager');
  await showCaption(page, '16 · Руководитель задаёт цели адаптации и видит обезличенные метрики');
  const firstGoal = page.locator('[data-goal]').first();
  await clickWithCursor(page, firstGoal);
  await clickWithCursor(page, page.locator('[data-action="save-plan"]'));
  await page.locator('[data-action="propose-plan"]').waitFor({ state: 'visible' });
  await clickWithCursor(page, page.locator('[data-action="propose-plan"]'));
  await page.getByText('Ждёт стажёра', { exact: true }).waitFor();
  await pause(page, 1700);

  await selectWithCursor(page, page.locator('#role-select'), 'intern');
  await clickWithCursor(page, page.locator('#main-nav [data-view="week"]'));
  await page.getByRole('heading', { name: 'План погружения' }).waitFor();
  await clickWithCursor(page, page.getByRole('button', { name: 'Чт' }));
  const agreePlan = page.locator('[data-action="agree-plan"]');
  await agreePlan.waitFor({ state: 'visible' });
  await showCaption(page, '17 · Стажёр получает предложенный план и согласует его');
  await clickWithCursor(page, agreePlan);
  await page.getByText('Согласован', { exact: true }).waitFor();
  await pause(page, 2400);

  const recordedVideo = page.video();
  await context.close();
  context = null;
  const webmPath = outputExtension === '.webm' ? outputPath : path.join(temporaryDirectory, 'demo.webm');
  await recordedVideo.saveAs(webmPath);
  if (outputExtension === '.mp4') {
    const conversion = spawnSync('ffmpeg', [
      '-y', '-i', webmPath, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', outputPath,
    ], { encoding: 'utf8', timeout: 120_000 });
    if (conversion.error || conversion.status !== 0) {
      throw new Error(`Видео записано, но не удалось преобразовать его в MP4:\n${conversion.stderr || conversion.error?.message}`);
    }
  }
  console.log(`Видео готово: ${outputPath}`);
  console.log(`Режим записи: ${requireLLM ? 'внешняя модель обязательна; источник проверен в UI' : 'локальный прототип без внешней модели'}`);
}

try {
  await run();
} finally {
  if (context) await context.close();
  if (browser) await browser.close();
  if (serverProcess && serverProcess.exitCode === null) {
    serverProcess.kill('SIGTERM');
    await Promise.race([
      new Promise(resolve => serverProcess.once('exit', resolve)),
      new Promise(resolve => setTimeout(resolve, 3000)),
    ]);
    if (serverProcess.exitCode === null) serverProcess.kill('SIGKILL');
  }
  await rm(temporaryDirectory, { recursive: true, force: true });
}
