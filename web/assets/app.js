const state = { servers: [], users: [], activeView: 'servers' };

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];

document.addEventListener('DOMContentLoaded', () => {
  $$('.tab').forEach(button => button.addEventListener('click', () => switchView(button.dataset.view)));
  $('#add-server').addEventListener('click', () => $('#server-dialog').showModal());
  $('#add-user').addEventListener('click', () => $('#user-dialog').showModal());
  $('#password-auth').addEventListener('change', togglePasswordFields);
  $('#server-form').addEventListener('submit', submitServer);
  $('#user-form').addEventListener('submit', submitUser);
  $('#sync-all').addEventListener('click', syncAll);
  $('#delete-with-uninstall').addEventListener('click', () => performServerDelete('uninstall'));
  $('#delete-forget').addEventListener('click', () => performServerDelete('forget'));
  $('#job-close').addEventListener('click', () => $('#job-dialog').close());
  $$('.close-dialog').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));
  loadOverview();
});

function switchView(view) {
  state.activeView = view;
  $$('.tab').forEach(button => button.classList.toggle('active', button.dataset.view === view));
  $$('.view').forEach(section => section.classList.toggle('active', section.id === `${view}-view`));
}

async function api(path, options = {}) {
  const response = await fetch(path, { headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }, ...options });
  const payload = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) throw new Error(payload?.error || `HTTP ${response.status}`);
  return payload;
}

async function loadOverview() {
  try {
    const overview = await api('/api/overview');
    state.servers = overview.servers || [];
    state.users = overview.users || [];
    $('#server-count').textContent = state.servers.length;
    $('#user-count').textContent = state.users.length;
    renderServers();
    renderUsers();
  } catch (error) {
    toast(`Не удалось загрузить данные: ${error.message}`);
  }
}

function renderServers() {
  const list = $('#servers-list');
  if (!state.servers.length) {
    list.innerHTML = '<div class="empty">Пока нет серверов. Добавьте чистую Ubuntu 24.04 VPS.</div>';
    return;
  }
  list.innerHTML = state.servers.map(server => `
    <article class="card">
      <div class="card-main">
        <div class="card-title"><strong>${escapeHTML(server.duckdns_url)}</strong><span class="status ${statusClass(server.status)}">${statusLabel(server.status)}</span></div>
        <div class="meta">SSH: ${escapeHTML(server.address)}<br>Host key: ${escapeHTML(server.ssh_host_fingerprint || 'не сохранён')}${server.last_error ? `<br><span class="danger-text">${escapeHTML(server.last_error)}</span>` : ''}</div>
      </div>
      <div class="card-actions"><button class="icon-button danger" data-delete-server="${server.id}" title="Удалить сервер">×</button></div>
    </article>`).join('');
  $$('[data-delete-server]', list).forEach(button => button.addEventListener('click', () => deleteServer(button.dataset.deleteServer)));
}

function renderUsers() {
  const list = $('#users-list');
  if (!state.users.length) {
    list.innerHTML = '<div class="empty">Пользователей пока нет. Создайте первый профиль доступа.</div>';
    return;
  }
  list.innerHTML = state.users.map(user => {
    const chips = state.servers.map(server => {
      const link = user.links?.[server.id];
      return `<span class="link-chip ${link?.status || 'pending'}" title="${escapeHTML(link?.last_error || '')}">${escapeHTML(server.duckdns_url)} · ${linkLabel(link)}</span>`;
    }).join('');
    return `<article class="card">
      <div class="card-main"><div class="card-title"><strong>${escapeHTML(user.email)}</strong></div><div class="meta">${escapeHTML(user.subscription_url)}</div><div class="link-statuses">${chips || '<span class="link-chip">Нет серверов</span>'}</div></div>
      <div class="card-actions"><button class="icon-button" data-retry-user="${user.id}" title="Повторить синхронизацию">↻</button><button class="icon-button" data-copy-user="${user.id}" title="Скопировать подписку">⧉</button><button class="icon-button danger" data-delete-user="${user.id}" title="Удалить пользователя">×</button></div>
    </article>`;
  }).join('');
  $$('[data-copy-user]', list).forEach(button => button.addEventListener('click', () => copySubscription(button.dataset.copyUser)));
  $$('[data-retry-user]', list).forEach(button => button.addEventListener('click', () => retryUser(button.dataset.retryUser)));
  $$('[data-delete-user]', list).forEach(button => button.addEventListener('click', () => deleteUser(button.dataset.deleteUser)));
}

function togglePasswordFields() {
  const enabled = $('#password-auth').checked;
  $('#password-fields').classList.toggle('visible', enabled);
  $('#server-form').elements.password.required = enabled;
  $('#server-form').elements.public_key.required = enabled;
}

async function submitServer(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const file = form.elements.private_key_file.files[0];
  if (!file) return;
  try {
    const input = {
      address: form.elements.address.value,
      private_key: await file.text(),
      passphrase: form.elements.passphrase.value,
      password_auth: form.elements.password_auth.checked,
      password: form.elements.password.value,
      public_key: form.elements.public_key.value,
      duckdns_url: form.elements.duckdns_url.value,
      duckdns_token: form.elements.duckdns_token.value
    };
    const result = await api('/api/servers', { method: 'POST', body: JSON.stringify(input) });
    $('#server-dialog').close();
    form.reset(); togglePasswordFields();
    followJob(result.job_id, 'Установка Slave', true);
  } catch (error) { toast(error.message); }
}

async function submitUser(event) {
  event.preventDefault();
  const form = event.currentTarget;
  try {
    const result = await api('/api/users', { method: 'POST', body: JSON.stringify({ email: form.elements.email.value }) });
    $('#user-dialog').close(); form.reset();
    followJob(result.job_id, 'Добавление пользователя');
  } catch (error) { toast(error.message); }
}

async function retryUser(id) {
  try {
    const result = await api(`/api/users/${id}/retry`, { method: 'POST', body: '{}' });
    followJob(result.job_id, 'Синхронизация пользователя');
  } catch (error) { toast(error.message); }
}

async function deleteUser(id) {
  const user = state.users.find(item => item.id === id);
  if (!user || !confirm(`Удалить пользователя ${user.email}? Subscription-ссылка перестанет работать сразу.`)) return;
  try {
    const result = await api(`/api/users/${id}`, { method: 'DELETE', body: '{}' });
    followJob(result.job_id, 'Удаление пользователя');
  } catch (error) { toast(error.message); }
}

async function deleteServer(id) {
  const server = state.servers.find(item => item.id === id);
  if (!server || !confirm(`Удалить сервер ${server.duckdns_url}?`)) return;
  $('#server-delete-dialog').dataset.serverId = id;
  $('#server-delete-dialog').showModal();
}

async function performServerDelete(mode) {
  const dialog = $('#server-delete-dialog');
  const id = dialog.dataset.serverId;
  dialog.close();
  if (!id) return;
  try {
    const result = await api(`/api/servers/${id}`, { method: 'DELETE', body: JSON.stringify({ mode }) });
    followJob(result.job_id, mode === 'uninstall' ? 'Удаление Slave' : 'Удаление записи', mode === 'uninstall');
  } catch (error) { toast(error.message); }
}

async function syncAll() {
  if (!confirm('Полная синхронизация удалит со Slave всех клиентов, которых нет в Master. Продолжить?')) return;
  try {
    const result = await api('/api/sync', { method: 'POST', body: '{}' });
    followJob(result.job_id, 'Полная синхронизация');
  } catch (error) { toast(error.message); }
}

async function copySubscription(id) {
  const user = state.users.find(item => item.id === id);
  if (!user) return;
  try {
    if (!navigator.clipboard?.writeText) throw new Error('Clipboard API unavailable');
    await navigator.clipboard.writeText(user.subscription_url);
    toast('Subscription-ссылка скопирована.');
  } catch (_) {
    $('#copy-fallback').value = user.subscription_url;
    $('#copy-dialog').showModal();
    $('#copy-fallback').focus(); $('#copy-fallback').select();
  }
}

function followJob(id, title, manualCleanupOnError = false) {
  const dialog = $('#job-dialog');
  const log = $('#job-log');
  const status = $('#job-status');
  $('#job-title').textContent = title;
  log.textContent = '';
  status.textContent = 'В процессе'; status.className = 'status running';
  $('#job-close').disabled = true;
  dialog.showModal();
  const source = new EventSource(`/api/jobs/${id}/events`);
  source.onmessage = event => {
    const item = JSON.parse(event.data);
    if (item.type === 'log' || item.type === 'warning') {
      log.textContent += `${item.type === 'warning' ? '⚠ ' : ''}${item.message}\n`;
      log.scrollTop = log.scrollHeight;
    }
    if (item.type === 'status') {
      source.close();
      const failed = item.status === 'error';
      status.textContent = failed ? 'Error' : item.status === 'success_with_warnings' ? 'Success · warning' : 'Success';
      status.className = `status ${item.status}`;
      if (failed && item.message) log.textContent += `\nERROR: ${item.message}\n`;
      $('#job-close').disabled = false;
      loadOverview();
      if (failed && manualCleanupOnError) alert('Операция завершилась с ошибкой. Зайдите на VPS вручную и выполните /opt/vps-reality/uninstall.sh --yes.');
    }
  };
  source.onerror = () => {
    if (!$('#job-close').disabled) return;
    source.close();
    pollJob(id, manualCleanupOnError);
  };
}

async function pollJob(id, manualCleanupOnError) {
  try {
    const job = await api(`/api/jobs/${id}`);
    $('#job-log').textContent = job.events.filter(event => event.message).map(event => `${event.type === 'warning' ? '⚠ ' : ''}${event.message}`).join('\n');
    if (job.status === 'running') return setTimeout(() => pollJob(id, manualCleanupOnError), 1000);
    $('#job-status').textContent = job.status === 'error' ? 'Error' : 'Success';
    $('#job-status').className = `status ${job.status}`;
    $('#job-close').disabled = false;
    loadOverview();
    if (job.status === 'error' && manualCleanupOnError) alert('Операция завершилась с ошибкой. Выполните uninstall.sh на VPS вручную.');
  } catch (error) { toast(error.message); }
}

function statusClass(status) { return ['error', 'partial', 'success_with_warnings'].includes(status) ? status : ''; }
function statusLabel(status) { return ({ ready: 'Готов', partial: 'Частично', error: 'Ошибка' })[status] || status; }
function linkLabel(link) { return !link ? 'ожидает' : ({ ready: 'готов', error: 'ошибка' })[link.status] || link.status; }
function escapeHTML(value = '') { const element = document.createElement('span'); element.textContent = value; return element.innerHTML; }
function toast(message) { const node = $('#toast'); node.textContent = message; node.classList.add('visible'); clearTimeout(toast.timer); toast.timer = setTimeout(() => node.classList.remove('visible'), 3500); }
