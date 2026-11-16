(() => {
  const app = document.querySelector('#app');
  const roleGate = document.querySelector('#role-gate');
  const roleSelect = document.querySelector('#role-select');
  const nav = document.querySelector('#main-nav');
  const names = { home: 'Главная', chat: 'Чат', week: 'Моя неделя', services: 'Сервисы', team: 'Команда' };
  const days = [
    { name: 'Пн', title: 'Знакомство', items: [['Познакомиться с командой', 'Кто за что отвечает'], ['Провести 1:1 с наставником', 'Обсудить первые шаги']] },
    { name: 'Вт', title: 'Доступы', items: [['Запросить доступ к репозиторию', 'Заявка через демо-сервис'], ['Войти в нужные чаты и каналы', 'Список уточнить у наставника']] },
    { name: 'Ср', title: 'Инструменты', items: [['Проверить рабочие инструменты', 'Нужные сервисы команды'], ['Открыть материалы команды', 'Инструкции и мануалы']] },
    { name: 'Чт', title: 'План адаптации', items: [['Изучить пространство команды', 'Кластер и зона ответственности'], ['Обсудить план с руководителем', 'Цели, критерии и ближайшие OKR команды']] },
    { name: 'Пт', title: 'Следующие шаги', items: [['Сверить план с наставником', 'Что делать на второй неделе'], ['Собрать открытые вопросы', 'Передать владельцам тем']] }
  ];
  const knowledge = {
    repo: { question: 'Как получить доступ к репозиторию?' },
    cicd: { question: 'Кто отвечает за CI/CD?' },
    staging: { question: 'Как устроен staging?' },
    merge: { question: 'Можно ли мержить без согласования?' }
  };
  const services = [
    { title: 'Отсутствия', note: 'Информация о сервисе', roles: ['intern', 'mentor', 'manager'] },
    { title: 'Командировки', note: 'Информация о сервисе', roles: ['intern', 'mentor', 'manager'] },
    { title: 'Найм', note: 'Доступ руководителя', roles: ['manager'] },
    { title: 'Увольнения', note: 'Доступ руководителя', roles: ['manager'] },
    { title: 'Согласия', note: 'Информация о сервисе', roles: ['intern', 'mentor', 'manager'] },
    { title: 'Бенефиты', note: 'Информация о сервисе', roles: ['intern', 'mentor', 'manager'] },
    { title: 'Чек-лист новичка', note: 'Шаги адаптации', roles: ['intern', 'mentor', 'manager'], action: 'week' },
    { title: 'Учебный сервис доступов', note: 'Мок-сервис: заявка на репозиторий', roles: ['intern', 'mentor'], action: 'access' }
  ];
  const team = [
    { name: 'Никита С.', title: 'Backend-стажёр', team: 'Backend Platform', tag: 'Неделя 1', avatar: 'Н', color: 'blue', scope: 'Первые шаги адаптации' },
    { name: 'Дарья М.', title: 'Backend-стажёр', team: 'Backend Platform', tag: 'Неделя 2', avatar: 'Д', color: 'peach', scope: 'Рабочие инструменты' },
    { name: 'Марина К.', title: 'Наставник · backend-инженер', team: 'Backend Platform', tag: 'Штатный сотрудник', avatar: 'М', color: 'violet', scope: 'Вопросы стажёров и согласование доступов' },
    { name: 'Павел Р.', title: 'Backend-инженер', team: 'Backend Platform', tag: 'Штатный сотрудник', avatar: 'П', color: 'mint', scope: 'Разработка и технический контекст' }
  ];
  const state = { request: null, done: [], plan: null, goalOptions: [], handoffs: [], faq: [], metrics: {} };
  let role = null;
  let apiToken = null;
  let view = 'home';
  let day = 0;
  let currentRequestID = '';
  let chatPending = false;
  let chat = [{ kind: 'bot', text: 'Привет! Я помогу найти проверенную инструкцию, нужный сервис или человека. С чего начнём?' }];
  const events = [];
  const esc = (value) => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  async function api(path, method = 'GET', body = undefined, withToken = true) {
    const response = await fetch(path, { method, headers: { ...(withToken ? { Authorization: `Bearer ${apiToken}` } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, body: body === undefined ? undefined : JSON.stringify(body) });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || `Ошибка ${response.status}`);
    return data;
  }
  async function enterRole(nextRole) {
    try {
      const session = await api('/api/demo/session', 'POST', { role: nextRole }, false);
      apiToken = session.token;
      role = nextRole;
      chat = [{ kind: 'bot', text: 'Привет! Задайте вопрос о первых шагах, сервисах или команде.' }];
      const data = await api('/api/bootstrap');
      state.request = data.request;
      state.done = data.done;
      state.plan = data.plan;
      state.goalOptions = data.goal_options;
      state.handoffs = data.handoffs || [];
      state.faq = data.faq || [];
      state.metrics = data.metrics || {};
      currentRequestID = '';
      emit('session_started');
      view = 'home';
      render();
    } catch (error) { role = null; apiToken = null; render(); toast(`Сервис недоступен: ${error.message}`); }
  }
  const emit = (type, detail = {}) => {
    events.push({ type, at: Date.now(), ...detail });
    if (!apiToken) return;
    const status = type === 'user_feedback' ? (detail.helpful ? 'helpful' : 'not_helpful') : (detail.status || '');
    api('/api/events', 'POST', { type, request_id: currentRequestID, category: detail.category || '', status, source_id: detail.source_id || '', reason: detail.reason || '' }).catch(() => {});
  };
  const toast = (message) => { document.querySelector('.toast')?.remove(); const el = document.createElement('div'); el.className = 'toast'; el.textContent = message; document.body.append(el); setTimeout(() => el.remove(), 3000); };
  const setView = (next) => { view = next; render(); window.scrollTo({ top: 0, behavior: 'smooth' }); };
  const percent = () => Math.round(state.done.length / 10 * 100);
  const isDone = (d, i) => state.done.includes(`${d}-${i}`);
  const requestStatus = () => state.request ? `<div class="flow-status ${state.request.status === 'approved' ? 'approved' : ''}"><strong>${state.request.status === 'approved' ? 'Доступ подтверждён' : state.request.status === 'rejected' ? 'Заявка отклонена' : 'Заявка ждёт наставника'}</strong>Заявка ${esc(state.request.id)} · ${state.request.status === 'approved' ? 'Следующий шаг — настройка окружения.' : state.request.status === 'rejected' ? 'Уточните причину у наставника.' : 'Следующий шаг — согласование наставником.'}<small>Статус получен от учебного API.</small></div>` : '';
  const intro = (eyebrow, title, subtitle) => `<div class="eyebrow">${eyebrow}</div><h1 class="page-title">${title}</h1><p class="subtext">${subtitle}</p>`;
  const person = (p) => `<div class="person-row"><div class="avatar ${p.color}">${p.avatar}</div><div class="row-copy"><strong>${p.name}</strong><small>${p.title} · ${p.team}</small>${p.scope ? `<small>${p.scope}</small>` : ''}</div><span class="pill ${p.tag.startsWith('Неделя') ? 'blue' : 'gray'}">${p.tag}</span></div>`;
  const planLabel = { none: 'Не создан', draft: 'Черновик', awaiting_intern: 'Ждёт стажёра', changes_requested: 'Нужны правки', agreed: 'Согласован' };
  const goalTitle = (id) => state.goalOptions.find(option => option.id === id)?.title || id;
  function managerPlan() {
    const status = state.plan?.status || 'none';
    const chosen = new Set(state.plan?.goal_ids || []);
    return `<section class="card wide-card"><div class="split-title"><h3>План адаптации</h3><span class="pill ${status === 'agreed' ? 'green' : 'blue'}">${planLabel[status]}</span></div><p class="tagline">Выберите цели для обсуждения со стажёром.</p><div class="goal-options">${state.goalOptions.map(option => `<label class="check-item"><input type="checkbox" data-goal="${esc(option.id)}" ${chosen.has(option.id) ? 'checked' : ''}><span><strong>${esc(option.title)}</strong></span></label>`).join('')}</div><div class="inline-actions"><button class="btn primary tiny" data-action="save-plan">Сохранить черновик</button>${status === 'draft' || status === 'changes_requested' ? '<button class="btn tiny" data-action="propose-plan">Отправить стажёру</button>' : ''}</div><p class="tagline">Контекст OKR: синтетический пример команды.</p><div class="manager-progress"><strong>Организационные шаги</strong><span>${state.done.length} из 10 отмечено стажёром</span><div class="progress-track"><div class="progress-fill" style="width:${percent()}%"></div></div></div></section>`;
  }
  function internPlan() {
    const status = state.plan?.status || 'none';
    if (status === 'none') return '<div class="notice">Руководитель ещё готовит план.</div>';
    const goals = (state.plan.goal_ids || []).map(id => `<li>${esc(goalTitle(id))}</li>`).join('');
    return `<div class="plan-summary"><div class="split-title"><h3>Цели адаптации</h3><span class="pill ${status === 'agreed' ? 'green' : 'blue'}">${planLabel[status]}</span></div><ul>${goals}</ul><p class="tagline">Ближайшие OKR команды — синтетический контекст для обсуждения с руководителем.</p>${status === 'awaiting_intern' ? '<div class="inline-actions"><button class="btn primary tiny" data-action="agree-plan">Согласовать</button><button class="btn tiny" data-action="request-plan-changes">Нужны правки</button></div>' : ''}</div>`;
  }
  function homeIntern() {
    return `<div class="content">${intro('ПЕРВАЯ НЕДЕЛЯ', 'Добро пожаловать в команду', 'Backend Platform · демо-план адаптации.')}
      <div class="home-grid"><section class="hero"><div class="hero-badge">СЕГОДНЯ</div><h2>Начните с доступа<br>к репозиторию</h2><p>Учебная инструкция и заявка в одном месте.</p><button class="primary-light" data-action="ask-repo">Открыть сценарий <span aria-hidden="true">→</span></button><div class="hero-art" aria-hidden="true"><i class="art-blue"></i><i class="art-red"></i><i class="art-green"></i><i class="art-purple"></i></div></section>
      <section class="card progress-card"><div><div class="card-top"><h3>План недели</h3><span class="pill blue">В процессе</span></div><div class="week-big">${percent()}<span>%</span></div><div class="progress-track"><div class="progress-fill" style="width:${percent()}%"></div></div><p>${state.done.length} из 10 шагов отмечено</p></div><button class="text-link" data-view="week">Открыть план →</button></section></div>
      <div class="section-heading"><h2>Что важно сейчас</h2><button class="text-link" data-view="week">Все шаги →</button></div><div class="task-list">
        <section class="card task-card"><div class="task-icon">⌘</div><h3>Доступ к репозиторию</h3><p>${state.request?.status === 'approved' ? 'Подтверждён' : state.request?.status === 'rejected' ? 'Отклонён' : state.request ? 'Ждёт наставника' : 'Заявка не создана'}</p><button class="text-link" data-action="ask-repo">${state.request ? 'Статус' : 'Начать'} →</button></section>
        <section class="card task-card"><div class="task-icon green">♧</div><h3>Команда</h3><p>Наставник и владельцы тем</p><button class="text-link" data-view="team">Открыть →</button></section>
        <section class="card task-card"><div class="task-icon purple">▦</div><h3>План адаптации</h3><p>Шаги первой недели</p><button class="text-link" data-view="week">Открыть →</button></section></div>
      <div class="bottom-grid"><section class="card list-card"><div class="section-heading"><h2>Частые вопросы</h2><button class="text-link" data-view="chat">Открыть чат →</button></div>${quickQuestions()}</section><section class="card list-card"><div class="section-heading"><h2>Ближайшие встречи</h2></div><div class="quick-row"><span class="bubble">◷</span><span><strong>1:1 с наставником</strong><small>Пн · 11:00 · демо</small></span></div><div class="quick-row"><span class="bubble">◷</span><span><strong>План с руководителем</strong><small>Чт · 12:00 · демо</small></span></div></section></div></div>`;
  }
  function quickQuestions() {
    return [['Как получить доступ к репозиторию?', 'Инструкция и заявка', 'repo'], ['Кто отвечает за CI/CD?', 'Найти владельца вопроса', 'cicd'], ['Как устроен staging?', 'Проверка актуальности', 'staging']].map(([q, subtitle, key]) => `<div class="quick-row"><span class="bubble">✧</span><span><strong>${q}</strong><small>${subtitle}</small></span><button data-question="${key}" aria-label="Спросить: ${q}">→</button></div>`).join('');
  }
  function homeMentor() {
    const dueSoon = state.faq.filter(f => f.status === 'approved' && new Date(`${f.expires_at}T23:59:59+03:00`).getTime() - Date.now() <= 7 * 86400000).length;
    return `<div class="content">${intro('НАСТАВНИК', 'Подопечные и вопросы', 'Backend Platform · демо-команда.')}
      <section class="card wide-card people-card"><div class="split-title"><h3>Мои стажёры</h3><span class="pill blue">2 человека</span></div>${team.slice(0, 2).map(person).join('')}</section>
      <div class="page-grid"><section class="card wide-card"><div class="split-title"><h3>Очередь наставника</h3><span class="pill amber">Вопросы и заявки</span></div>
      ${state.request ? `<div class="mentor-row"><div class="service-icon">⌘</div><div class="row-copy"><strong>Доступ к учебному backend-репозиторию</strong><small>Заявка ${esc(state.request.id)} · ${state.request.status === 'approved' ? 'подтверждена' : state.request.status === 'rejected' ? 'отклонена' : 'ожидает решения'}</small></div>${state.request.status === 'waiting_mentor' ? '<button class="btn primary tiny" data-action="approve-request">Подтвердить</button>' : `<span class="pill ${state.request.status === 'approved' ? 'green' : 'red'}">${state.request.status === 'approved' ? 'Готово' : 'Отклонено'}</span>`}</div>` : '<div class="empty">Заявок на доступ пока нет. Создайте одну в режиме стажёра.</div>'}
      ${state.handoffs.filter(h => h.owner === 'Наставник команды').map(h => `<div class="mentor-row"><div class="service-icon">?</div><div class="row-copy"><strong>${h.topic === 'merge' ? 'Решение по изменению' : 'Вопрос без подтверждённого источника'}</strong><small>Передача ${esc(h.id)} · ${h.status === 'accepted' ? 'принято' : 'ожидает принятия'}</small></div>${h.status === 'pending' ? `<button class="btn primary tiny" data-action="accept-handoff" data-id="${esc(h.id)}">Принять</button>` : '<span class="pill green">Принято</span>'}</div>`).join('')}
      ${!state.request && !state.handoffs.some(h => h.owner === 'Наставник команды') ? '<div class="empty">Новых вопросов пока нет.</div>' : ''}</section>
      <section class="card wide-card"><h3>Память наставника</h3><p class="subtext" style="margin-top:8px">Новый ответ сначала станет черновиком. Похожие знания покажем для проверки.</p>${dueSoon ? `<div class="notice amber" style="margin-top:12px">${dueSoon} ответ(а) нужно проверить в ближайшие 7 дней.</div>` : ''}
      <form id="faq-form" class="faq-form"><input name="question" maxlength="300" required placeholder="Типовой вопрос" aria-label="Типовой вопрос"><textarea name="answer" maxlength="1500" required placeholder="Краткий проверенный ответ" aria-label="Ответ наставника"></textarea><button class="btn primary" type="submit">Добавить в FAQ</button></form>
      ${state.faq.length ? state.faq.map(f => `<div class="knowledge-card"><strong>${esc(f.question)}</strong><p>${esc(f.answer)}</p><div class="knowledge-meta"><span class="pill ${f.status === 'approved' ? 'green' : f.status === 'expired' ? 'red' : 'amber'}">${esc(f.status)}</span><span class="pill gray">TTL 60 дней</span>${f.duplicate_ids?.length ? `<span class="pill amber">Похожие: ${esc(f.duplicate_ids.join(', '))}</span>` : ''}</div>${f.expires_at ? `<p>Проверено ${esc(f.checked_at)} · до ${esc(f.expires_at)}</p>` : ''}${f.status === 'approved' ? `<button class="btn tiny" data-action="approve-faq" data-id="${esc(f.id)}">Подтвердить актуальность</button> <button class="text-link" data-action="revoke-faq" data-id="${esc(f.id)}">Отозвать</button>` : `<button class="btn tiny primary" data-action="approve-faq" data-id="${esc(f.id)}">Проверить и утвердить</button>`}</div>`).join('') : '<div class="empty">Проверенных ответов наставника пока нет.</div>'}</section></div></div>`;
  }
  function homeManager() {
    return `<div class="content">${intro('РУКОВОДИТЕЛЬ', 'Команда Backend Platform', 'Стажёры и штатные сотрудники · демо-состав.')}
      <div class="page-grid"><section class="card wide-card"><div class="split-title"><h3>Состав команды</h3><span class="pill blue">4 человека</span></div>${team.map(person).join('')}</section>${managerPlan()}<section class="card wide-card"><h3>Обращения к AI-buddy</h3><div class="mini-stat"><span>Вопросов</span><strong>${state.metrics.requests || 0}</strong></div><div class="mini-stat"><span>Ответов с источником</span><strong>${state.metrics.answers || 0}</strong></div><div class="mini-stat"><span>Передано человеку</span><strong>${state.metrics.handoffs || 0}</strong></div><div class="mini-stat"><span>Принято человеком</span><strong>${state.metrics.accepted || 0}</strong></div><div class="mini-stat"><span>Отмечено полезным</span><strong>${state.metrics.helpful || 0}</strong></div><p class="subtext">Только обезличенные события демо.</p></section></div></div>`;
  }
  function homeEmployee() {
    return `<div class="content">${intro('ОБЫЧНЫЙ РЕЖИМ', 'Контекст под рукой', 'После адаптации AI-buddy остаётся точкой входа в знания, сервисы и контакты.')}
      <div class="mode-banner">Этот режим показывает направление развития продукта: быстрый поиск рабочего контекста без недельного плана адаптации.</div><div class="home-grid"><section class="hero"><div class="hero-badge">РАБОЧИЙ ПОМОЩНИК</div><h2>Нужный контекст<br>за пару шагов</h2><p>Вопросы о процессах, инструментах и владельцах — с источником и датой проверки.</p><button class="primary-light" data-view="chat">Спросить AI-buddy <span aria-hidden="true">→</span></button><div class="hero-art" aria-hidden="true"><i class="art-blue"></i><i class="art-red"></i><i class="art-green"></i><i class="art-purple"></i></div></section><section class="card progress-card"><div><h3>Что можно найти</h3><div class="resource-row"><span class="bubble">⌘</span><div class="row-copy"><strong>Инструменты</strong><small>Инструкции и доступы</small></div></div><div class="resource-row"><span class="bubble">♧</span><div class="row-copy"><strong>Ответственные</strong><small>Команды и зоны ответственности</small></div></div><div class="resource-row"><span class="bubble">✧</span><div class="row-copy"><strong>Знания</strong><small>Проверенные ответы наставников</small></div></div></div></section></div><div class="section-heading"><h2>Начать с вопроса</h2></div><section class="card list-card">${quickQuestions()}</section></div>`;
  }
  function chatMessage(m) {
    if (m.kind === 'user') return `<div class="message user">${esc(m.text)}</div>`;
    const sourceLink = m.source?.uri?.startsWith('https://') ? ` <a href="${esc(m.source.uri)}" target="_blank" rel="noopener noreferrer">Открыть источник ↗</a>` : '';
    let source = m.source ? `<div class="source">Источник: ${esc(m.source.source)} · ${esc(m.source.id)}${sourceLink}<br>Владелец: ${esc(m.source.owner)} · проверено ${esc(m.source.checked)} · действует до ${esc(m.source.expires)}<br>${m.source.origin === 'synthetic' ? 'Учебные данные для демонстрации' : 'Публичный источник или факт из кейса'}${m.mode === 'bm25_baseline' ? ' · локальный поиск' : m.mode === 'llm_source_selection' ? ' · источник выбран моделью' : ''}</div>` : '';
    let actions = m.actions?.length ? `<div class="inline-actions">${m.actions.map(a => `<button class="btn ${a.primary ? 'primary' : ''} tiny" data-action="${a.action}" ${a.topic ? `data-topic="${esc(a.topic)}"` : ''}>${esc(a.label)}</button>`).join('')}</div>` : '';
    return `<div class="message bot"><strong>${esc(m.title || 'AI-buddy')}</strong><p>${esc(m.text)}</p>${source}${actions}</div>`;
  }
  function chatView() {
    return `<div class="content">${intro('ЧАТ', 'Спросите AI-buddy', 'Ответ с источником или передача человеку.')}
    <div class="chat-layout"><section class="card chat-panel"><div class="chat-head"><div class="bot-avatar">✧</div><span><strong>AI-buddy</strong><small>Демо-чат</small></span><span class="pill green" style="margin-left:auto">На связи</span></div>
    <div class="chat-body" id="chat-body">${chat.map(chatMessage).join('')}</div><form class="chat-compose" id="chat-form"><div class="suggestions"><button type="button" class="suggestion" data-question="repo">Доступ к репозиторию</button><button type="button" class="suggestion" data-question="cicd">Кто отвечает за CI/CD?</button><button type="button" class="suggestion" data-question="merge">Нужно согласование?</button><button type="button" class="suggestion" data-question="staging">Staging</button></div><div class="compose-row"><input id="chat-input" autocomplete="off" maxlength="300" placeholder="Напишите вопрос…" aria-label="Ваш вопрос"><button type="submit" aria-label="Отправить вопрос">↑</button></div></form></section>
    <div class="aside-stack"><section class="card aside-card"><h3>Статус заявки</h3>${state.request ? requestStatus() : '<div class="notice">Заявки пока нет.</div>'}</section><div class="notice amber">Нет проверенного источника — нет ответа от AI.</div></div></div></div>`;
  }
  function weekView() {
    return `<div class="content">${intro('НЕДЕЛЯ 1', 'План погружения', 'Шаги по фрагменту чек-листа. Даты и команда — демо.')}
      <div class="page-grid"><section class="card wide-card"><div class="split-title"><h3>${days[day].title}</h3><span class="pill blue">День ${day + 1} из 5</span></div><div class="day-tabs">${days.map((d, i) => `<button class="day-tab ${day === i ? 'active' : ''}" data-day="${i}">${d.name}</button>`).join('')}</div><div>${days[day].items.map(([title, desc], i) => `<label class="check-item"><input type="checkbox" data-check="${day}-${i}" ${isDone(day, i) ? 'checked' : ''}><span><strong>${title}</strong><small>${desc}</small></span></label>`).join('')}</div>${day === 1 ? `<button class="btn primary" data-action="ask-repo" style="margin-top:15px">${state.request ? 'Посмотреть заявку' : 'Запросить доступ'} →</button>${requestStatus()}` : ''}</section><section class="card wide-card"><h3>Отмечено</h3><div class="week-big">${state.done.length}<span> / 10</span></div><div class="progress-track"><div class="progress-fill" style="width:${percent()}%"></div></div><div style="margin-top:22px"><h3>Встречи</h3><div class="mini-stat"><span>1:1 с наставником</span><strong>Пн · 11:00</strong></div><div class="mini-stat"><span>План с руководителем</span><strong>Чт · 12:00</strong></div></div><div style="margin-top:25px">${internPlan()}</div></section></div></div>`;
  }
  function servicesView() {
    const rows = services.map((s, i) => {
      const allowed = s.roles.includes(role);
      let control = `<span class="pill ${allowed ? 'gray' : 'red'}">${allowed ? 'Справка' : 'Нет доступа'}</span>`;
      if (allowed && s.action === 'access') control = role === 'intern' ? '<button class="btn primary tiny" data-action="ask-repo">Открыть</button>' : '<button class="btn tiny" data-view="home">Заявки</button>';
      if (allowed && s.action === 'week') control = role === 'intern' ? '<button class="btn tiny" data-view="week">Открыть</button>' : '<span class="pill gray">Справка</span>';
      return `<div class="service-row"><div class="service-icon">${['◷','↗','♧','−','✓','✦','▦','⌘'][i]}</div><div class="row-copy"><strong>${s.title}</strong><small>${allowed ? s.note : 'Действия для этой роли недоступны'}</small></div>${control}</div>`;
    }).join('');
    return `<div class="content">${intro('СЕРВИСЫ', 'Каталог сервисов', 'Сервисы из кейса. Работает только учебная заявка на доступ.')}
      <div class="page-grid"><section class="card wide-card"><h3>Сервисы</h3>${rows}</section><section class="card wide-card"><h3>Демо-действие</h3><p class="subtext" style="margin-top:10px">Запрос доступа к учебному репозиторию.</p>${requestStatus() || '<div class="notice" style="margin-top:18px">Заявки пока нет.</div>'}</section></div></div>`;
  }
  function teamView() {
    const visibleTeam = team;
    return `<div class="content">${intro('КОМАНДА', role === 'manager' ? 'Состав команды' : 'К кому обратиться', 'Backend Platform · вымышленные участники.')}
      <div class="page-grid"><section class="card wide-card"><h3>${role === 'mentor' ? 'Стажёры и команда' : 'Backend Platform'}</h3>${visibleTeam.map(person).join('')}<div class="person-row"><div class="avatar blue">Р</div><div class="row-copy"><strong>Руководитель команды</strong><small>План адаптации и согласования</small></div><span class="pill gray">Руководитель</span></div></section><section class="card wide-card"><h3>Владелец темы</h3><div class="person-row"><div class="avatar mint">P</div><div class="row-copy"><strong>Platform Tools</strong><small>CI/CD и тестовая инфраструктура</small></div></div><button class="btn primary" data-question="cicd" style="margin-top:16px">Спросить про CI/CD →</button></section></div></div>`;
  }
  function renderRoleGate() {
    return `<div class="role-panel"><div class="role-header"><span class="avito-mark" aria-hidden="true"><i></i><i></i><i></i><i></i></span><span><strong>авито</strong><small>AI-buddy · демо</small></span></div><div class="eyebrow">ВЫБОР РОЛИ</div><h1>Выберите роль</h1><p>У каждой роли свой рабочий экран.</p><div class="role-grid"><button class="role-card" data-role="intern"><span class="role-icon blue">▦</span><strong>Стажёр</strong><small>План недели, чат и доступы</small><b>Открыть →</b></button><button class="role-card" data-role="mentor"><span class="role-icon purple">♧</span><strong>Наставник</strong><small>Подопечные, вопросы и FAQ</small><b>Открыть →</b></button><button class="role-card" data-role="manager"><span class="role-icon green">⌘</span><strong>Руководитель</strong><small>Состав команды и сервисы</small><b>Открыть →</b></button></div><div class="gate-note">Это выбор тестовой роли, а не вход в аккаунт. В Авито контекст пользователя уже предоставляют существующие системы.</div></div>`;
  }
  function render() {
    document.body.classList.toggle('role-entry', !role);
    roleGate.hidden = Boolean(role);
    if (!role) { roleGate.innerHTML = renderRoleGate(); return; }
    roleGate.innerHTML = '';
    roleSelect.value = role;
    if (role !== 'intern' && view === 'week') view = 'home';
    nav.querySelector('[data-view="week"]').hidden = role !== 'intern';
    document.querySelector('#page-crumb').textContent = names[view];
    nav.querySelectorAll('[data-view]').forEach(el => el.classList.toggle('active', el.dataset.view === view));
    app.innerHTML = view === 'home' ? (role === 'mentor' ? homeMentor() : role === 'manager' ? homeManager() : homeIntern()) : view === 'chat' ? chatView() : view === 'week' ? weekView() : view === 'services' ? servicesView() : teamView();
    if (view === 'chat') { const body = document.querySelector('#chat-body'); body.scrollTop = body.scrollHeight; }
  }
  async function ask(q) {
    if (chatPending) return toast('Дождитесь ответа на предыдущий вопрос');
    chatPending = true;
    const requestRole = role;
    currentRequestID = globalThis.crypto?.randomUUID?.() || `demo-${Date.now()}`;
    setView('chat');
    chat.push({ kind: 'user', text: q });
    emit('request_created', { role });
    chat.push({ kind: 'bot', title: 'Ищу источник…', text: 'Проверяю доступные знания.' });
    render();
    try {
      const result = await api('/api/chat', 'POST', { question: q });
      if (role !== requestRole) return;
      chat.pop();
      emit('intent_detected', { category: result.intent || 'other' });
      if (result.outcome === 'answer') {
        emit('knowledge_search', { source_id: result.source.id, category: result.intent, status: 'found' });
        emit('answer_shown', { source_id: result.source.id, category: result.intent });
        const actions = result.source.id === 'K-101' && role === 'intern'
          ? [{ label: state.request ? 'Статус заявки' : 'Создать заявку', action: state.request ? 'show-request' : 'create-request', primary: true }]
          : [];
        actions.push({ label: 'Полезно', action: 'feedback' }, { label: 'Не помогло', action: 'feedback-no' });
        chat.push({ kind: 'bot', title: 'Ответ с источником', text: result.text, source: result.source, mode: result.mode, actions });
      } else if (result.outcome === 'human_gate') {
        const topic = result.reason === 'expired_source' ? 'staging' : 'merge';
        emit('human_gate_triggered', { reason: result.reason, category: result.intent, owner: result.owner });
        chat.push({ kind: 'bot', title: 'Нужно участие человека', text: result.text, actions: [{ label: 'Передать вопрос', action: 'handoff', topic }] });
      } else {
        emit('fallback_triggered', { reason: result.reason, category: result.intent || 'other' });
        const restricted = ['sensitive_data', 'financial_operation', 'excluded_scope', 'invalid_question'].includes(result.reason);
        chat.push({ kind: 'bot', title: 'Нет подтверждённого ответа', text: result.text, actions: restricted ? [] : [{ label: 'Передать наставнику', action: 'handoff', topic: 'unknown' }] });
      }
    } catch (error) {
      if (role !== requestRole) return;
      chat.pop();
      emit('fallback_triggered', { reason: 'api_error' });
      chat.push({ kind: 'bot', title: 'Сервис недоступен', text: 'Ответ не получен. Попробуйте ещё раз позже.' });
    } finally {
      chatPending = false;
    }
    render();
  }
  async function createRequest() {
    if (role !== 'intern') return toast('Заявку создаёт стажёр');
    if (!state.request) {
      try {
        toast('Создаём заявку…');
        state.request = await api('/api/access-requests', 'POST');
        emit('action_completed', { action: 'access_request_created', status: state.request.status });
        chat.push({ kind: 'bot', title: 'Заявка создана', text: `Сервис вернул ${state.request.id}. Статус: ждёт наставника. Следующий шаг — согласование.` });
      } catch (error) { chat.push({ kind: 'bot', title: 'Заявка не создана', text: `Сервис не выполнил действие: ${error.message}` }); }
    } else chat.push({ kind: 'bot', title: 'Заявка уже есть', text: `${state.request.id}: ${state.request.status === 'approved' ? 'подтверждена' : 'ждёт наставника'}. Повторную заявку создавать не нужно.` });
    setView('chat');
  }
  async function decideAccess() {
    try {
      toast('Передаём решение…');
      state.request = await api(`/api/access-requests/${encodeURIComponent(state.request.id)}/decision`, 'POST', { decision: 'approve' });
      emit('human_handoff_completed', { status: state.request.status });
      render(); toast('Заявка подтверждена');
    } catch (error) { toast(`Ошибка: ${error.message}`); }
  }
  async function savePlan() {
    const goalIDs = [...document.querySelectorAll('[data-goal]:checked')].map(el => el.dataset.goal);
    try { state.plan = await api('/api/plan', 'PUT', { goal_ids: goalIDs }); render(); toast('Черновик сохранён'); }
    catch (error) { toast(`Ошибка: ${error.message}`); }
  }
  async function proposePlan() {
    try { state.plan = await api('/api/plan/propose', 'POST'); render(); toast('План передан стажёру'); }
    catch (error) { toast(`Ошибка: ${error.message}`); }
  }
  async function respondPlan(decision) {
    try { state.plan = await api('/api/plan/respond', 'POST', { decision }); render(); toast(decision === 'agree' ? 'План согласован' : 'Запрос правок передан руководителю'); }
    catch (error) { toast(`Ошибка: ${error.message}`); }
  }
  async function toggleStep(id, done) {
    try {
      const result = await api(`/api/steps/${encodeURIComponent(id)}`, 'POST', { done });
      state.done = Object.entries(result.done).filter(([, checked]) => checked).map(([step]) => step);
      render();
    } catch (error) { render(); toast(`Шаг не сохранён: ${error.message}`); }
  }
  async function sendHandoff(topic) {
    try {
      const handoff = await api('/api/handoffs', 'POST', { topic });
      const index = state.handoffs.findIndex(item => item.id === handoff.id);
      if (index >= 0) state.handoffs[index] = handoff;
      else state.handoffs.push(handoff);
      emit('human_gate_triggered', { reason: 'user_handoff', category: topic, owner: handoff.owner });
      chat.push({ kind: 'bot', title: 'Передано человеку', text: `Передача ${handoff.id} для ${handoff.owner}. Статус: ${handoff.status === 'accepted' ? 'принято' : 'ожидает принятия'}. Текст вопроса не сохраняется.` });
      render();
    } catch (error) { toast(`Передача не создана: ${error.message}`); }
  }
  async function acceptHandoff(id) {
    try {
      const handoff = await api(`/api/handoffs/${encodeURIComponent(id)}/accept`, 'POST');
      state.handoffs = state.handoffs.map(item => item.id === id ? handoff : item);
      emit('human_handoff_completed', { status: 'accepted', category: handoff.topic });
      render(); toast('Вопрос принят');
    } catch (error) { toast(`Ошибка: ${error.message}`); }
  }
  async function createFAQ(form) {
    const question = form.elements.question.value.trim();
    const answer = form.elements.answer.value.trim();
    try {
      const faq = await api('/api/faq', 'POST', { question, answer });
      state.faq.unshift(faq);
      render(); toast(faq.duplicate_ids?.length ? 'Черновик сохранён. Проверьте похожие знания.' : 'Черновик FAQ сохранён. Теперь проверьте и утвердите его.');
    } catch (error) { toast(`FAQ не сохранён: ${error.message}`); }
  }
  async function changeFAQ(id, action) {
    try {
      const faq = await api(`/api/faq/${encodeURIComponent(id)}/${action}`, 'POST');
      state.faq = state.faq.map(item => item.id === id ? faq : item);
      render(); toast(action === 'approve' ? 'Ответ утверждён на 60 дней' : 'Ответ отозван');
    } catch (error) { toast(`Ошибка: ${error.message}`); }
  }
  document.addEventListener('click', (event) => {
    const target = event.target.closest('[data-role], [data-view], [data-action], [data-question], [data-day]');
    if (!target) return;
    if (target.dataset.role) return enterRole(target.dataset.role);
    if (target.dataset.view) { if (target.tagName === 'A') event.preventDefault(); return setView(target.dataset.view); }
    if (target.dataset.day !== undefined) { day = Number(target.dataset.day); return render(); }
    if (target.dataset.question) { const k = knowledge[target.dataset.question]; return ask(k.question); }
    switch (target.dataset.action) {
      case 'ask-repo': return ask(knowledge.repo.question);
      case 'create-request': return createRequest();
      case 'show-request': chat.push({ kind: 'bot', title: 'Статус заявки', text: `${state.request.id}: ${state.request.status === 'approved' ? 'подтверждена. Можно настраивать окружение.' : state.request.status === 'rejected' ? 'отклонена. Уточните причину у наставника.' : 'ждёт решения наставника.'}` }); return render();
      case 'approve-request': if (role !== 'mentor' || !state.request) return; return decideAccess();
      case 'save-plan': if (role !== 'manager') return; return savePlan();
      case 'propose-plan': if (role !== 'manager') return; return proposePlan();
      case 'agree-plan': if (role !== 'intern') return; return respondPlan('agree');
      case 'request-plan-changes': if (role !== 'intern') return; return respondPlan('request_changes');
      case 'accept-handoff': if (role !== 'mentor') return; return acceptHandoff(target.dataset.id);
      case 'approve-faq': if (role !== 'mentor') return; return changeFAQ(target.dataset.id, 'approve');
      case 'revoke-faq': if (role !== 'mentor') return; return changeFAQ(target.dataset.id, 'revoke');
      case 'handoff': return sendHandoff(target.dataset.topic || 'unknown');
      case 'feedback': emit('user_feedback', { helpful: true }); return toast('Спасибо, отметка учтена в демо-сессии');
      case 'feedback-no': emit('user_feedback', { helpful: false }); return toast('Спасибо, вопрос можно передать наставнику');
    }
  });
  document.addEventListener('change', (event) => {
    if (event.target.id === 'role-select') return enterRole(event.target.value);
    if (event.target.dataset.check) return toggleStep(event.target.dataset.check, event.target.checked);
  });
  document.addEventListener('submit', (event) => {
    if (event.target.id === 'faq-form') { event.preventDefault(); return createFAQ(event.target); }
    if (event.target.id === 'chat-form') {
      event.preventDefault(); const input = document.querySelector('#chat-input'); const q = input.value.trim(); if (q) ask(q);
    }
  });
  render();
})();
