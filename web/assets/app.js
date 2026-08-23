/* ===================================================================
   TEAM ACTIVITY VISUALIZER — SPA (Go API backend)
   =================================================================== */

// ===== UTILITIES =====
const $ = s => document.querySelector(s);
const $$ = s => document.querySelectorAll(s);
const uid = () => Math.random().toString(36).substr(2,9) + Date.now().toString(36);
const pad2 = n => String(n).padStart(2,'0');
const fmtDate = d => `${d.getFullYear()}/${pad2(d.getMonth()+1)}/${pad2(d.getDate())}`;
const parseDate = s => { const m = s.match(/^(\d{4})\/(\d{2})\/(\d{2})$/); return m ? new Date(+m[1],+m[2]-1,+m[3]) : null; };
const clone = o => JSON.parse(JSON.stringify(o));
const sum = arr => arr.reduce((a,b)=>a+b,0);
const escapeHtml = s => String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
const esc = escapeHtml;
const escTitle = s => escapeHtml(s).replace(/\n/g,'&#10;');

// Safe confirm helpers — look up names from State by id, avoiding interpolation XSS
function confirmArchivePerson(id){ const p=getPerson(id); if(p&&confirm('Archive '+p.name+'?')){ API.post('/people/'+id+'/archive').then(()=>{ if(p){p.status='archived';p.archived_date=fmtDate(new Date());} render();}) } }
function confirmDeletePerson(id){ const p=getPerson(id); if(p&&confirm('Delete '+p.name+'?')){ API.del('/people/'+id).then(()=>{ State.people=State.people.filter(x=>x.id!==id); render();}) } }
function confirmDeleteProject(id){ const proj=getProject(id); if(proj&&confirm('Delete '+proj.name+'?')){ API.del('/projects/'+id).then(()=>{ State.projects=State.projects.filter(x=>x.id!==id); render();}) } }
const fmtDateTime = d => `${d.getFullYear()}-${pad2(d.getMonth()+1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}:00`;
const fmtExportTS = d => `${d.getFullYear()}-${pad2(d.getMonth()+1)}-${pad2(d.getDate())}-${pad2(d.getHours())}-${pad2(d.getMinutes())}`;

function projectColor(name) {
  if (!name) return null;
  let h = 0;
  for (let i = 0; i < name.length; i++) h = ((h<<5)-h+name.charCodeAt(i))|0;
  const hue = Math.abs(h) % 360;
  const sat = 55 + (Math.abs(h>>8) % 20);
  const light = 45 + (Math.abs(h>>16) % 10);
  return `hsl(${hue},${sat}%,${light}%)`;
}
function projectColorBg(slotData) {
  if (!slotData || !slotData.projects || slotData.projects.length === 0) return null;
  if (slotData.projects.length >= 2) {
    return `linear-gradient(135deg,${projectColor(slotData.projects[0].name)} 50%,${projectColor(slotData.projects[1].name)} 50%)`;
  }
  return projectColor(slotData.projects[0].name);
}

// ===== WEEK HELPERS =====
function getWeekStart(date) {
  const d = new Date(date); const day = d.getDay();
  d.setDate(d.getDate() + (day === 0 ? -6 : 1 - day));
  d.setHours(0,0,0,0); return d;
}
function addWeeks(date, n) { const d = new Date(date); d.setDate(d.getDate() + n * 7); return d; }
function formatWeekStart(d) { return fmtDate(d); }
// Last calendar day (Sunday) of the final visible week — used as the inclusive
// upper bound when fetching planning, so Tue–Sun of the last week aren't cut off.
function windowEndDate(weeks) { const d = new Date(weeks[weeks.length-1]); d.setDate(d.getDate()+6); return fmtDate(d); }
function getWeekDays(weekStart) {
  const days = [];
  for (let i = 0; i < 7; i++) { const d = new Date(weekStart); d.setDate(d.getDate() + i); days.push(d); }
  return days;
}
function getWeekNumber(d) {
  const start = getWeekStart(d); const yearStart = new Date(start.getFullYear(), 0, 1);
  return Math.ceil(((start - yearStart) / 86400000 + yearStart.getDay() + 1) / 7);
}

// ===== STATE (replaces localStorage store) =====
const State = {
  user: null, token: null,
  people: [], projects: [], planning: {}, settings: {},
  oncall: {}, rotation: {}, holidays: [],
  scrollOffset: 0, undoStack: null, undoing: false, theme: null, presence: [], myPersonId: '',
};

function getSlotKey(personId, date, slot) { return `${personId}|${date}|${slot}`; }
function getSlot(personId, date, slot) { return State.planning[getSlotKey(personId, date, slot)] || null; }
function getActivePeople() { return State.people.filter(p => p.status === 'active' && !p.is_guest); }
function getActiveGuests() { return State.people.filter(p => p.status === 'active' && p.is_guest); }
function getArchivedPeople() { return State.people.filter(p => p.status === 'archived'); }
function getPerson(id) { return State.people.find(p => p.id === id); }
function getProject(id) { return State.projects.find(p => p.id === id); }
function getProjectByName(name) { return State.projects.find(p => p.name.toLowerCase() === name.toLowerCase()); }

const defaultSettings = { window_weeks:4, prune_weeks:12, week_starts:'monday', run_mode:'ratio', run_target_persons:3, theme:'dracula', export_counter:1 };
const THEMES = ['dracula','monokai','light','nord','solarized_light','solarized_dark','github','github_dark','one_dark','gruvbox','tokyo_night','catppuccin'];

function canEdit() { return State.user && (State.user.role === 'admin' || State.user.role === 'normal'); }
function isAdmin() { return State.user && State.user.role === 'admin'; }

// ===== API CLIENT =====
const API = {
  async call(method, path, body) {
    if (method !== 'GET') showSaving();
    const opts = { method, headers: { 'Authorization': `Bearer ${State.token}` } };
    if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    const res = await fetch(`/api${path}`, opts);
    if (res.status === 401) { State.token = null; window.location.href = '/auth/login'; throw new Error('Unauthorized'); }
    if (res.status === 403) { toast('Permission denied', 'error'); throw new Error('Forbidden'); }
    const text = await res.text();
    return text ? JSON.parse(text) : null;
  },
  get: p => API.call('GET', p),
  post: (p,b) => API.call('POST', p, b),
  put: (p,b) => API.call('PUT', p, b),
  del: (p,b) => API.call('DELETE', p, b),

  async loadAll() {
    const [people, projects, settings, holidays] = await Promise.all([
      API.get('/people'), API.get('/projects'), API.get('/settings'), API.get('/holidays'),
    ]);
    State.people = people || [];
    State.projects = projects || [];
    State.settings = Object.assign({}, defaultSettings, settings || {});
    State.holidays = holidays || [];
    // Load planning for current visible window
    const weeks = getVisibleWeeks();
    const startDate = formatWeekStart(weeks[0]);
    const endDate = windowEndDate(weeks);
    const planningEntries = await API.get(`/planning?start=${startDate}&end=${endDate}`);
    State.planning = {};
    (planningEntries || []).forEach(e => { State.planning[getSlotKey(e.person_id, e.date, e.slot)] = e.data; });
    // Load oncall + rotation
    const [oncall, rotation] = await Promise.all([
      API.get(`/oncall?start=${startDate}&end=${endDate}`),
      API.get(`/rotation?start=${startDate}&end=${endDate}`),
    ]);
    State.oncall = oncall || {};
    State.rotation = rotation || {};
  },

  async reloadPlanning() {
    const weeks = getVisibleWeeks();
    const startDate = formatWeekStart(weeks[0]);
    const endDate = windowEndDate(weeks);
    const planningEntries = await API.get(`/planning?start=${startDate}&end=${endDate}`);
    State.planning = {};
    (planningEntries || []).forEach(e => { State.planning[getSlotKey(e.person_id, e.date, e.slot)] = e.data; });
    const [oncall, rotation] = await Promise.all([
      API.get(`/oncall?start=${startDate}&end=${endDate}`),
      API.get(`/rotation?start=${startDate}&end=${endDate}`),
    ]);
    State.oncall = oncall || {};
    State.rotation = rotation || {};
  },
};

// ===== WEBSOCKET CLIENT =====
const WS = {
  conn: null, reconnectTimer: null,
  connect() {
    if (!State.token) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    this.conn = new WebSocket(`${proto}//${location.host}/api/ws?token=${State.token}`);
    this.conn.onopen = () => { console.log('ws: connected'); updateWSStatus('connected'); if (this.reconnectTimer) { clearTimeout(this.reconnectTimer); this.reconnectTimer = null; } };
    this.conn.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      this.onMessage(msg);
    };
    this.conn.onclose = () => {
      console.log('ws: disconnected, reconnecting in 3s...');
      updateWSStatus('reconnecting');
      this.reconnectTimer = setTimeout(() => this.connect(), 3000);
    };
    this.conn.onerror = () => { this.conn.close(); };
  },
  onMessage(msg) {
    switch (msg.type) {
      case 'person_added': if (!State.people.find(p => p.id === msg.data.id)) State.people.push(msg.data); break;
      case 'person_updated': { const i = State.people.findIndex(p => p.id === msg.data.id); if (i >= 0) State.people[i] = msg.data; break; }
      case 'person_deleted': State.people = State.people.filter(p => p.id !== msg.data.id); break;
      case 'person_archived': case 'person_unarchived': { const p = getPerson(msg.data.id); if (p) { p.status = msg.type === 'person_archived' ? 'archived' : 'active'; p.archived_date = msg.type === 'person_archived' ? fmtDate(new Date()) : ''; } break; }
      case 'planning_updated': { if (State.undoing) break; const e = msg.data; const k = getSlotKey(e.person_id, e.date, e.slot); if (isRecentSelf([k])) return; queueFlash([{pid:e.person_id, date:e.date, slot:e.slot}], 'remote'); State.planning[k] = e.data; break; }
      case 'planning_cleared': { if (State.undoing) break; const e = msg.data; const k = getSlotKey(e.person_id, e.date, e.slot); if (isRecentSelf([k])) return; queueFlash([{pid:e.person_id, date:e.date, slot:e.slot}], 'remote'); delete State.planning[k]; break; }
      case 'planning_range': case 'planning_range_cleared': { const r = rangeSlotKeys(msg.data); if (isRecentSelf(r.keys)) return; queueFlash(r.cells, 'remote'); API.reloadPlanning().then(render); return; }
      case 'planning_copied': case 'planning_pruned': case 'data_reset': case 'data_imported': API.reloadPlanning().then(render); return;
      case 'project_added': if (!State.projects.find(p => p.id === msg.data.id)) State.projects.push(msg.data); break;
      case 'project_updated': { const i = State.projects.findIndex(p => p.id === msg.data.id); if (i >= 0) State.projects[i] = msg.data; break; }
      case 'project_deleted': State.projects = State.projects.filter(p => p.id !== msg.data.id); break;
      case 'oncall_changed': { API.get(`/oncall?start=${formatWeekStart(getVisibleWeeks()[0])}&end=${windowEndDate(getVisibleWeeks())}`).then(d => { State.oncall = d || {}; render(); }); return; }
      case 'rotation_changed': { API.get(`/rotation?start=${formatWeekStart(getVisibleWeeks()[0])}&end=${windowEndDate(getVisibleWeeks())}`).then(d => { State.rotation = d || {}; render(); }); return; }
      case 'settings_updated': State.settings = Object.assign({}, State.settings, msg.data); break;
      case 'holidays_imported': API.get('/holidays').then(d => { State.holidays = d || []; render(); }); return;
      case 'presence': State.presence = msg.data || []; renderPresence(); break;
      case 'activity_new': { if (!Array.isArray(State.activity)) State.activity=[]; State.activity.unshift(msg.data); if (State.activity.length>100) State.activity.pop(); if (currentView==='activity') render(); return; }
    }
    render();
  },
};

// ===== UNDO / REDO (multi-level, depth 20) =====
function pushUndo(keys) {
  const snap = {};
  (keys && keys.length ? keys : Object.keys(State.planning)).forEach(k => snap[k] = State.planning[k] ? clone(State.planning[k]) : null);
  if (!Array.isArray(State.undoStack)) State.undoStack = [];
  State.undoStack.push(snap);
  if (State.undoStack.length > 20) State.undoStack.shift();
  State.redoStack = [];
  const b = $('#btn-undo'); if (b) b.disabled = false;
  const br = $('#btn-redo'); if (br) br.disabled = true;
}
async function undo() {
  if (!Array.isArray(State.undoStack) || State.undoStack.length === 0) return;
  const snap = State.undoStack.pop();
  // Push current state of those keys onto redo stack
  const redoSnap = {};
  Object.keys(snap).forEach(k => redoSnap[k] = State.planning[k] ? clone(State.planning[k]) : null);
  if (!Array.isArray(State.redoStack)) State.redoStack = [];
  State.redoStack.push(redoSnap);
  const b = $('#btn-undo'); if (b) b.disabled = State.undoStack.length === 0;
  const br = $('#btn-redo'); if (br) br.disabled = false;
  State.undoing = true;
  try {
    const writes = [];
    Object.keys(snap).forEach(k => {
      const prev = snap[k], cur = State.planning[k] || null;
      if (JSON.stringify(prev) === JSON.stringify(cur)) return;
      const [pid, date, slot] = k.split('|');
      if (prev === null) writes.push(API.del('/planning/slot', { person_id: pid, date, slot }));
      else writes.push(API.put('/planning/slot', { person_id: pid, date, slot, data: prev }));
    });
    if (writes.length === 0) { State.undoing = false; return; }
    await Promise.all(writes);
    await API.reloadPlanning();
  } finally {
    State.undoing = false;
  }
  // Flash the cells that were reverted by this undo (self-coloured).
  const undoneKeys = Object.keys(snap).filter(k => { const [pid, date, slot] = k.split('|'); return snap[k] !== null || State.planning[k] !== undefined; });
  undoneKeys.forEach(k => { const [pid, date, slot] = k.split('|'); queueFlash([{pid, date, slot}], 'self'); });
  render();
}
async function redo() {
  if (!Array.isArray(State.redoStack) || State.redoStack.length === 0) return;
  const snap = State.redoStack.pop();
  const undoSnap = {};
  Object.keys(snap).forEach(k => undoSnap[k] = State.planning[k] ? clone(State.planning[k]) : null);
  if (!Array.isArray(State.undoStack)) State.undoStack = [];
  State.undoStack.push(undoSnap);
  const b = $('#btn-undo'); if (b) b.disabled = false;
  const br = $('#btn-redo'); if (br) br.disabled = State.redoStack.length === 0;
  State.undoing = true;
  try {
    const writes = [];
    Object.keys(snap).forEach(k => {
      const prev = snap[k], cur = State.planning[k] || null;
      if (JSON.stringify(prev) === JSON.stringify(cur)) return;
      const [pid, date, slot] = k.split('|');
      if (prev === null) writes.push(API.del('/planning/slot', { person_id: pid, date, slot }));
      else writes.push(API.put('/planning/slot', { person_id: pid, date, slot, data: prev }));
    });
    if (writes.length === 0) { State.undoing = false; return; }
    await Promise.all(writes);
    await API.reloadPlanning();
  } finally {
    State.undoing = false;
  }
  // Flash the cells that were re-applied by this redo (self-coloured).
  const redoneKeys = Object.keys(snap).filter(k => { const [pid, date, slot] = k.split('|'); return snap[k] !== null || State.planning[k] !== undefined; });
  redoneKeys.forEach(k => { const [pid, date, slot] = k.split('|'); queueFlash([{pid, date, slot}], 'self'); });
  render();
}

// ===== SLOT HELPERS =====
function getSlotClass(slotData) {
  if (!slotData || slotData.state === 'not_filled') {
    if (slotData && slotData.remote) return 'not-filled remote';
    if (slotData && slotData.offsite) return 'not-filled offsite';
    return 'not-filled';
  }
  const flagCls = slotData.remote ? ' remote' : slotData.offsite ? ' offsite' : '';
  if (slotData.state === 'undetermined') return 'undetermined' + flagCls;
  if (slotData.away) return 'away' + flagCls;
  if (slotData.incident) return 'incident' + flagCls;
  if (slotData.run && slotData.projects && slotData.projects.length > 0) return 'project run' + flagCls;
  if (slotData.run) return 'run' + flagCls;
  if (slotData.projects && slotData.projects.length > 0) return 'project' + flagCls;
  if (slotData.remote) return 'not-filled remote';
  if (slotData.offsite) return 'not-filled offsite';
  return 'not-filled';
}
function getCellLabel(slotData) {
  if (!slotData || slotData.state === 'not_filled') return '?';
  // Note: remote/off-site icons are added via CSS ::before on .cell-label,
  // so we do NOT include them in the text here (avoids double icons).
  if (slotData.state === 'undetermined') return '?';
  if (slotData.away) { const a = {vacation:'Vac',public_holiday:'Hol',sick_leave:'Sick',training:'Train',conference:'Conf',parental_leave:'Par',sabbatical:'Sab',other:'Other'}; return (a[slotData.away.type] || slotData.away.type); }
  if (slotData.incident) return '⚠';
  let parts = [];
  if (slotData.projects) slotData.projects.forEach(p => parts.push(p.name));
  if (slotData.run) parts.push('R' + (slotData.run_note ? '*' : ''));
  return parts.join('/');
}
function getMergeKey(slotData, weekIndex) {
  const wk = 'w' + weekIndex + ':';
  if (!slotData || slotData.state === 'not_filled') return wk + 'nf';
  if (slotData.state === 'undetermined') return wk + 'und';
  if (slotData.away) return wk + 'away:' + slotData.away.type;
  if (slotData.incident) return wk + 'incident:' + (slotData.incident.text || '');
  if (slotData.projects && slotData.projects.length === 1 && !slotData.run) return wk + 'proj:' + slotData.projects[0].name;
  if (slotData.projects && slotData.projects.length === 1 && slotData.run) return wk + 'projrun:' + slotData.projects[0].name + ':' + (slotData.run_note || '');
  if (slotData.run && (!slotData.projects || slotData.projects.length === 0)) return wk + 'run:' + (slotData.run_note || '');
  return wk + 'complex:' + JSON.stringify(slotData.projects) + ':' + (slotData.run ? '1' : '0') + ':' + (slotData.run_note || '');
}
function getSlotTitle(person, dateStr, slot, slotData) {
  const p = typeof person === 'string' ? getPerson(person) : person;
  const name = p ? p.name : person;
  const flagTxt = slotData && slotData.remote ? ' + Remote' : slotData && slotData.offsite ? ' + Off-site' : '';
  if (!slotData || slotData.state === 'not_filled') return `${name} · ${dateStr} ${slot} · Not filled${flagTxt}`;
  if (slotData.state === 'undetermined') return `${name} · ${dateStr} ${slot} · Undetermined project${flagTxt}`;
  if (slotData.away) return `${name} · ${dateStr} ${slot} · Away: ${slotData.away.type}${slotData.away.note ? ' ('+slotData.away.note+')' : ''}${flagTxt}`;
  if (slotData.incident) return `${name} · ${dateStr} ${slot} · Incident${slotData.incident.text ? ': ' + slotData.incident.text : ''}${flagTxt}`;
  // Projects — multi-line with descriptions (Feature 1)
  let lines = [`${name} · ${dateStr} ${slot}`];
  if (slotData.projects) slotData.projects.forEach(pr => {
    const proj = getProjectByName(pr.name);
    const desc = proj && proj.description ? proj.description : '';
    const emoji = proj && proj.emoji ? proj.emoji + ' ' : '';
    lines.push(desc ? `${emoji}${pr.name} (${pr.pct}%): ${desc}` : `${emoji}${pr.name} (${pr.pct}%)`);
  });
  if (slotData.run) lines.push('Run duty' + (slotData.run_note ? ': ' + slotData.run_note : ''));
  if (slotData.remote) lines.push('Remote');
  else if (slotData.offsite) lines.push('Off-site');
  return lines.join('\n');
}
// HTML version of getSlotTitle for the custom tooltip (renders emoji in color)
function getSlotTitleHtml(person, dateStr, slot, slotData) {
  const p = typeof person === 'string' ? getPerson(person) : person;
  const name = p ? p.name : person;
  const flagTxt = slotData && slotData.remote ? ' + Remote' : slotData && slotData.offsite ? ' + Off-site' : '';
  if (!slotData || slotData.state === 'not_filled') return `<div class="tt-header">${esc(name)} · ${dateStr} ${slot} · Not filled${esc(flagTxt)}</div>`;
  if (slotData.state === 'undetermined') return `<div class="tt-header">${esc(name)} · ${dateStr} ${slot} · Undetermined project${esc(flagTxt)}</div>`;
  if (slotData.away) return `<div class="tt-header">${esc(name)} · ${dateStr} ${slot} · Away: ${esc(slotData.away.type)}${slotData.away.note ? ' ('+esc(slotData.away.note)+')' : ''}${esc(flagTxt)}</div>`;
  if (slotData.incident) return `<div class="tt-header">${esc(name)} · ${dateStr} ${slot} · Incident${slotData.incident.text ? ': '+esc(slotData.incident.text) : ''}${esc(flagTxt)}</div>`;
  let html = `<div class="tt-header">${esc(name)} · ${dateStr} ${slot}</div>`;
  if (slotData.projects) slotData.projects.forEach(pr => {
    const proj = getProjectByName(pr.name);
    const desc = proj && proj.description ? proj.description : '';
    const emoji = proj && proj.emoji ? proj.emoji + ' ' : '';
    html += `<div class="tt-line">${emoji}${esc(pr.name)} (${pr.pct}%)${desc ? ': '+esc(desc) : ''}</div>`;
  });
  if (slotData.run) html += `<div class="tt-line">🏃 Run duty${slotData.run_note ? ': ' + esc(slotData.run_note) : ''}</div>`;
  if (slotData.remote) html += '<div class="tt-line">🏠 Remote</div>';
  else if (slotData.offsite) html += '<div class="tt-line">🏢 Off-site</div>';
  return html;
}
function getSlotLabel(slotData) {
  if (!slotData || slotData.state === 'not_filled') return '—';
  const flagIcon = slotData.remote ? '🏠' : slotData.offsite ? '🏢' : '';
  if (slotData.state === 'undetermined') return (flagIcon ? flagIcon + ' ?' : '?');
  if (slotData.away) return (flagIcon ? flagIcon + ' ' : '') + slotData.away.type.replace(/_/g,' ');
  if (slotData.incident) return (flagIcon ? flagIcon + ' ' : '') + 'Incident' + (slotData.incident.text ? ': ' + slotData.incident.text : '');
  let parts = [];
  if (flagIcon) parts.push(flagIcon);
  if (slotData.projects) slotData.projects.forEach(p => parts.push(p.name));
  if (slotData.run) parts.push('🏃' + (slotData.run_note ? ' ' + slotData.run_note : ''));
  return parts.join(' ') || '—';
}

// ===== HOLIDAY HELPER =====
function isHoliday(dateStr) {
  const country = State.settings.holiday_country;
  if (!country) return null;
  const h = State.holidays.find(h => h.date === dateStr && h.country === country);
  return h ? h.label : null;
}

// ===== ON-CALL / ROTATION =====
function isOnCall(personId, weekStart) { return !!(State.oncall[weekStart] || []).includes(personId); }
function isRunPerson(personId, weekStart) { return !!(State.rotation[weekStart] || []).includes(personId); }
function getRunPeople(weekStart) { return getActivePeople().filter(p => isRunPerson(p.id, weekStart)); }
// collectRunNotes compiles every run-note for a person across a week into a
// single newline-joined string (for the Run Coverage hover tooltip). Returns
// '' if the person has no run slots with notes that week.
function collectRunNotes(personId, weekStart) {
  const days = getWeekDays(parseDate(weekStart)); const bits = [];
  days.forEach(d => { const ds = fmtDate(d); ['am','pm'].forEach(slot => { const sd = getSlot(personId, ds, slot); if (sd && sd.run && sd.run_note) bits.push(`${ds} ${slot.toUpperCase()}: ${sd.run_note}`); }); });
  return bits.join('\n');
}
async function toggleOnCall(personId, weekStart) {
  if (isOnCall(personId, weekStart)) { await API.del('/oncall', { person_id: personId, week_start: weekStart }); State.oncall[weekStart] = (State.oncall[weekStart]||[]).filter(id => id !== personId); }
  else { await API.put('/oncall', {person_id:personId, week_start:weekStart}); if (!State.oncall[weekStart]) State.oncall[weekStart] = []; State.oncall[weekStart].push(personId); }
  render();
}

// ===== RUN COVERAGE =====
function calcRunRatio(personId, weekStart) {
  const days = getWeekDays(parseDate(weekStart)); let working = 0, run = 0;
  days.forEach(d => { const ds = fmtDate(d);
    ['am','pm'].forEach(slot => { const sd = getSlot(personId, ds, slot);
      if (!sd || sd.state === 'not_filled') working++; else if (sd.state === 'undetermined') working++;
      else if (sd.away) {} else { working++; if (sd.run) run++; }
    });
  });
  return { working, run, ratio: working > 0 ? (run/working)*100 : 0 };
}
function calcRunCoveragePerSlot(weekStart) {
  const people = getActivePeople(); const days = getWeekDays(parseDate(weekStart));
  const dayNames = ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'];
  const result = [];
  days.forEach((d, di) => {
    if (di >= 5 && !_showWeekend) return;
    const ds = fmtDate(d);
    if (isHoliday(ds)) return; // skip holiday days from coverage
    const dr = { dayName: dayNames[di], date: ds, am: {onRun:0,away:0,avail:0}, pm: {onRun:0,away:0,avail:0} };
    ['am','pm'].forEach(slot => {
      people.forEach(p => { const sd = getSlot(p.id, ds, slot);
        if (!sd || sd.state === 'not_filled' || sd.state === 'undetermined') dr[slot].avail++;
        else if (sd.away) dr[slot].away++; else { dr[slot].avail++; if (sd.run) dr[slot].onRun++; }
      });
    });
    result.push(dr);
  });
  return result;
}

// ===== VIEW STATE =====
let currentView = 'team', currentPersonId = null;
let scrollOffset = 0, availabilityDayOffset = 0;
let teamGroupBy = 'name', projectsViewMode = 'general', projectsSortBy = 'name', projectsHideDone = true, teamShowGuests = false;
let dragState = null, rangeEditorCells = null, rangeEditorPersonIds = [], rangeProjCount = 1;
let _showWeekend = false;
let teamFilter = '';
// Activity tab filters (persist across re-renders while the tab is open)
let activityFilter = { type: 'all', person: 'all', actor: 'all', search: '' };
// Cell-edit flash: queue of {pid,date,slot,origin} to glow after the next render.
// origin is 'self' (local edit / undo / redo) or 'remote' (WS change from
// another client). The author's own WS echo is suppressed via recentSelfFlash.
let pendingFlash = [];
let recentSelfFlash = new Map(); // slotKey -> expiry timestamp (ms)
const FLASH_SELF_SUPPRESS_MS = 2500;

// Persistent view prefs (localStorage)
const PREFS_KEYS = ['scrollOffset','teamGroupBy','_showWeekend','teamShowGuests','currentView','availabilityDayOffset','teamFilter'];
function loadPrefs() {
  PREFS_KEYS.forEach(k => {
    const v = localStorage.getItem('teamviz_'+k);
    if (v !== null) {
      if (k === '_showWeekend' || k === 'teamShowGuests') window[k] = v === 'true';
      else if (k === 'scrollOffset' || k === 'availabilityDayOffset') window[k] = parseInt(v,10) || 0;
      else window[k] = v;
    }
  });
}
function savePrefs() {
  PREFS_KEYS.forEach(k => {
    const v = window[k];
    localStorage.setItem('teamviz_'+k, String(v));
  });
}

function getVisibleWeeks() {
  const n = State.settings.window_weeks || 4;
  const ws = getWeekStart(new Date()); const weeks = [];
  for (let i = 0; i < n; i++) weeks.push(addWeeks(ws, i + scrollOffset));
  return weeks;
}

function jumpToDate(val) {
  if (!val) return;
  const d = new Date(val + 'T00:00:00');
  const targetWS = getWeekStart(d);
  const currentWS = getWeekStart(new Date());
  const diffWeeks = Math.round((targetWS - currentWS) / (7 * 86400000));
  scrollOffset = diffWeeks;
  savePrefs();
  API.reloadPlanning().then(render);
}

// ===== RENDER ENGINE =====
function render() {
  document.documentElement.setAttribute('data-theme', State.theme || State.settings.theme || 'dracula');
  const main = document.getElementById('main');
  $$('.nav-btn[data-view]').forEach(btn => btn.classList.toggle('active', btn.dataset.view === currentView));
  // Role-based UI: the Admin and Users tabs are only visible to admins.
  $$('.nav-btn[data-view="admin"],.nav-btn[data-view="users"]').forEach(b => b.style.display = isAdmin() ? '' : 'none');
  updateStatusBar();
  switch(currentView) {
    case 'team': renderTeamGrid(main); break;
    case 'availability': renderAvailability(main); break;
    case 'run': renderRunCoverage(main); break;
    case 'guests': renderGuests(main); break;
    case 'archived': renderArchived(main); break;
    case 'people': renderPeople(main); break;
    case 'projects': renderProjects(main); break;
    case 'settings': renderSettings(main); break;
    case 'admin': if (isAdmin()) renderAdmin(main); else renderSettings(main); break;
    case 'workload': renderWorkload(main); break;
    case 'activity': renderActivity(main); break;
    case 'incidents': renderIncidents(main); break;
    case 'myweek': renderMyWeek(main); break;
    case 'users': if (isAdmin()) renderUsers(main); else renderSettings(main); break;
    default: renderTeamGrid(main);
  }
  renderPresence();
  renderMePicker();
  applyPendingFlash();
}

// ===== CELL-EDIT FLASH =====
// Queue slot keys to glow after the next render. cells may be either
// {pid,date,slot} objects or 'pid|date|slot' strings.
function queueFlash(cells, origin) {
  if (!cells) return;
  const arr = Array.isArray(cells) ? cells : [cells];
  arr.forEach(c => {
    const pid = c.pid || c.personId || c.person_id;
    const date = c.date;
    const slot = c.slot;
    if (!pid || !date || !slot) return;
    pendingFlash.push({ pid, date, slot, origin });
    if (origin === 'self') recentSelfFlash.set(getSlotKey(pid, date, slot), Date.now() + FLASH_SELF_SUPPRESS_MS);
  });
}
// Returns true if any of the given slot keys were flashed as 'self' recently
// (used to suppress the author's own WS echo so they don't see a second,
// remote-coloured flash).
function isRecentSelf(keys) {
  const now = Date.now();
  for (const k of keys) {
    const exp = recentSelfFlash.get(k);
    if (exp && exp > now) return true;
  }
  return false;
}
// Compute the slot keys for a planning_range WS payload.
function rangeSlotKeys(data) {
  const pids = data.person_ids || [];
  const slots = generateSlotsInRange(data.start_date, data.start_slot || 'am', data.end_date, data.end_slot || 'pm');
  const keys = [];
  pids.forEach(pid => slots.forEach(sl => keys.push(getSlotKey(pid, sl.date, sl.slot))));
  return { keys, cells: pids.flatMap(pid => slots.map(sl => ({ pid, date: sl.date, slot: sl.slot }))) };
}
// After render(), find the freshly created cells matching pendingFlash and
// start the glow animation. Clears the queue.
function applyPendingFlash() {
  if (!pendingFlash.length) return;
  const items = pendingFlash;
  pendingFlash = [];
  // Use requestAnimationFrame so the new DOM is painted first.
  requestAnimationFrame(() => {
    items.forEach(it => {
      const sel = `[data-person="${CSS.escape(it.pid)}"][data-date="${CSS.escape(it.date)}"][data-slot="${CSS.escape(it.slot)}"]`;
      document.querySelectorAll(sel).forEach(el => {
        const cls = it.origin === 'self' ? 'flash-self' : 'flash-remote';
        // Re-trigger the animation by removing then adding on next frame.
        el.classList.remove('flash-self', 'flash-remote');
        el.offsetWidth; // force reflow
        el.classList.add(cls);
        const cleanup = () => { el.classList.remove('flash-self', 'flash-remote'); el.removeEventListener('animationend', cleanup); };
        el.addEventListener('animationend', cleanup);
        // Safety: also clear after the animation duration in case animationend
        // doesn't fire (e.g. element removed by a later render).
        setTimeout(cleanup, 5300);
      });
    });
  });
}

function updateStatusBar() {
  const left = document.getElementById('status-left');
  const right = document.getElementById('status-right');
  left.textContent = `${getActivePeople().length} team · ${getActiveGuests().length} guests · ${getArchivedPeople().length} archived`;
  // Coverage shortfall badge
  const weeks = getVisibleWeeks();
  const runTarget = State.settings.run_target_persons || 3;
  let totalBelow = 0;
  weeks.forEach(w => {
    const coverage = calcRunCoveragePerSlot(formatWeekStart(w));
    coverage.forEach(day => { ['am','pm'].forEach(slot => { if (day[slot].onRun < runTarget) totalBelow++; }); });
  });
  right.textContent = `Week ${getWeekNumber(new Date())} · ${fmtDate(new Date())}${totalBelow > 0 ? ` ⚠ ${totalBelow} slots below run target` : ''}`;
}

// ===== PRESENCE AVATARS =====
function renderPresence() {
  const el = document.getElementById('presence');
  if (!el) return;
  const max = 8;
  const users = (State.presence || []).slice(0, max);
  el.innerHTML = users.map(u => {
    const p = u.person_id ? getPerson(u.person_id) : null;
    const emoji = p ? p.avatar_emoji : '👤';
    const personName = p ? ' — ' + p.name : '';
    return `<span class="pv-badge" title="${esc(u.username)} (${u.role})${esc(personName)}">${emoji}</span>`;
  }).join('') + (State.presence.length > max ? `<span class="pv-badge" title="${State.presence.length - max} more">+${State.presence.length - max}</span>` : '');
}

// ===== ME PICKER =====
function renderMePicker() {
  const el = document.getElementById('me-picker');
  if (!el) return;
  const people = getActivePeople();
  let html = '<select onchange="API.put(\'/me/person\',{person_id:this.value}).then(()=>{State.myPersonId=this.value;render()})" style="font-size:.75rem;width:auto;max-width:120px">';
  html += '<option value="">I am…</option>';
  people.forEach(p => html += `<option value="${p.id}" ${State.myPersonId === p.id ? 'selected' : ''}>${esc(p.name)}</option>`);
  html += '</select>';
  el.innerHTML = html;
}

// ===== DATE PICKER HELPER =====
function datePickerToText(picker, textId) { if (picker.value) document.getElementById(textId).value = picker.value.replace(/-/g, '/'); }
function projectNamesDatalist() {
  const names = State.projects.filter(p => p.status !== 'done').map(p => p.name).filter(Boolean);
  return '<datalist id="project-names">' + names.map(n => `<option value="${escapeHtml(n)}">`).join('') + '</datalist>';
}

// ===== TEAM GRID VIEW =====
function renderTeamGrid(container) {
  let people = getActivePeople();
  if (teamShowGuests) people = people.concat(getActiveGuests());
  if (people.length === 0) { container.innerHTML = canEdit() ? '<div id="onboarding"><h1>👋 Welcome</h1><p>Add team members to start planning.</p><div class="actions"><button onclick="showAddPersonModal(false)">➕ Add Person</button></div></div>' : '<p>No team members yet.</p>'; return; }
  const weeks = getVisibleWeeks();
  const slotsPerWeek = _showWeekend ? 14 : 10;
  let html = '<div class="grid-container"><table class="grid-table"><thead><tr><th class="person-col">Person</th>';
  weeks.forEach((w, wi) => { const ws = formatWeekStart(w); const wn = getWeekNumber(w); const sep = wi > 0 ? ' week-start' : ''; html += `<th colspan="${slotsPerWeek}" class="${sep}">W${wn} ${ws.slice(5)}</th>`; });
  html += '</tr><tr><th class="person-col"></th>';
  weeks.forEach((w, wi) => { const days = getWeekDays(w); const dayNames = _showWeekend ? ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'] : ['Mon','Tue','Wed','Thu','Fri'];
    days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const sep = (di === 0 && wi > 0) ? ' week-start' : ''; const hl = isHoliday(fmtDate(d)); html += `<th colspan="2" class="day-header${sep}">${dayNames[di]} <span class="day-num">/${d.getDate()}</span>${hl ? `<span class="holiday-badge" title="${esc(hl)}">${esc(hl)}</span>` : ''}</th>`; });
  });
  html += '</tr></thead><tbody>';
  // Build allSlots
  const allSlots = [];
  weeks.forEach((w, wi) => { const days = getWeekDays(w); days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const ds = fmtDate(d); ['am','pm'].forEach(slot => allSlots.push({date:ds, slot, weekIdx:wi, dayIdx:di})); }); });
  // Sort + group. Guests are always grouped together at the end; under a
  // "Guests" sub-team when grouping by sub-team.
  let peopleSorted = people.slice();
  // Apply filter
  if (teamFilter) {
    const f = teamFilter.toLowerCase();
    peopleSorted = peopleSorted.filter(p => {
      if (p.name.toLowerCase().includes(f)) return true;
      if ((p.sub_team||'').toLowerCase().includes(f)) return true;
      // Check if any project in visible weeks matches
      for (const w of weeks) {
        const days = getWeekDays(w);
        for (const d of days) {
          for (const slot of ['am','pm']) {
            const sd = getSlot(p.id, fmtDate(d), slot);
            if (sd && sd.projects && sd.projects.some(pr => pr.name.toLowerCase().includes(f))) return true;
          }
        }
      }
      return false;
    });
  }
  if (teamGroupBy === 'sub_team') {
    peopleSorted.sort((a,b) => {
      if (a.is_guest !== b.is_guest) return a.is_guest ? 1 : -1;
      const sa=(a.is_guest?'guests':(a.sub_team||'other')).toLowerCase(), sb=(b.is_guest?'guests':(b.sub_team||'other')).toLowerCase();
      if (sa!==sb) return sa.localeCompare(sb);
      return a.name.localeCompare(b.name);
    });
  } else {
    peopleSorted.sort((a,b) => (a.is_guest?1:0)-(b.is_guest?1:0) || a.name.localeCompare(b.name));
  }
  let lastSubTeam = null;
  const runTarget = State.settings.run_target_persons || 3;
  peopleSorted.forEach((p, rowIdx) => {
    if (teamGroupBy === 'sub_team') { const st = p.is_guest ? 'Guests' : (p.sub_team || 'Other'); if (st !== lastSubTeam) { lastSubTeam = st; html += `<tr class="subteam-row"><td colspan="${allSlots.length+1}">${escapeHtml(st)}</td></tr>`; } }
    const firstWeekStart = formatWeekStart(weeks[0]);
    const onCallAnyWeek = weeks.some(w => isOnCall(p.id, formatWeekStart(w)));
    const onCallWeeks = weeks.filter(w => isOnCall(p.id, formatWeekStart(w))).map(w => 'W'+getWeekNumber(w));
    const onCallTip = onCallWeeks.length > 0 ? 'On-call: '+onCallWeeks.join(', ') : 'Click to toggle on-call W'+getWeekNumber(weeks[0]);
    // Conflict warning: away + on-call/run in same week
    let conflictWarn = '';
    weeks.forEach(w => {
      const ws = formatWeekStart(w);
      if (isOnCall(p.id, ws) || isRunPerson(p.id, ws)) {
        const days = getWeekDays(w);
        for (const d of days) {
          for (const slot of ['am','pm']) {
            const sd = getSlot(p.id, fmtDate(d), slot);
            if (sd && sd.away) { conflictWarn = `<span class="warn-icon" title="Away but on-call/run in W${getWeekNumber(w)}">⚠</span>`; break; }
          }
          if (conflictWarn) break;
        }
      }
    });
    html += `<tr><td class="person-col" onclick="showIndividual('${p.id}')" title="${esc(p.role||'')} · ${esc(p.sub_team||'')}">${esc(p.avatar_emoji)} ${esc(p.name)}${conflictWarn}`;
    if (canEdit()) html += `<button class="oncall-btn${onCallAnyWeek?' active':''}" onclick="event.stopPropagation();toggleOnCall('${p.id}','${firstWeekStart}')" title="${onCallTip}">📞</button>`;
    html += '</td>';
    let prevMergeKey = null;
    allSlots.forEach((sl, colIdx) => {
      const sd = getSlot(p.id, sl.date, sl.slot); const cls = getSlotClass(sd); const label = getCellLabel(sd);
      const mk = getMergeKey(sd, sl.weekIdx); const isMergedCont = (mk === prevMergeKey);
      const sep = (sl.dayIdx === 0 && sl.slot === 'am' && sl.weekIdx > 0) ? ' week-start' : '';
      const mergeCls = isMergedCont ? ' merged-cont' : '';
      const showLabel = !isMergedCont || cls.includes('not-filled');
      const onCallCls = isOnCall(p.id, formatWeekStart(weeks[sl.weekIdx])) ? ' on-call' : '';
      let bgStyle = '';
      if (cls.includes('project')) { const bg = projectColorBg(sd); if (bg) bgStyle = ` style="background:${bg}"`; }
      const onmousedown = canEdit() ? ` onmousedown="startDrag(event,'${p.id}','${sl.date}','${sl.slot}',${rowIdx},${colIdx})"` : '';
      html += `<td class="half-day ${cls}${sep}${mergeCls}${onCallCls}" data-person="${p.id}" data-date="${sl.date}" data-slot="${sl.slot}" data-row="${rowIdx}" data-col="${colIdx}"${onmousedown}${bgStyle}>${showLabel ? `<span class="cell-label">${escapeHtml(label)}</span>` : ''}</td>`;
      prevMergeKey = mk;
    });
    html += '</tr>';
  });
  // Summary row
  html += `<tr class="summary-row"><td class="person-col"><strong>Summary</strong></td>`;
  allSlots.forEach((sl) => {
    let awayCount=0, runCount=0, total=0;
    people.forEach(p => { const sd = getSlot(p.id, sl.date, sl.slot); if (sd && sd.away) awayCount++; if (sd && sd.run) runCount++; total++; });
    const availPct = total > 0 ? Math.round(((total-awayCount)/total)*100) : 0;
    const sep = (sl.dayIdx === 0 && sl.slot === 'am' && sl.weekIdx > 0) ? ' week-start' : '';
    const runWarn = runCount < runTarget ? ' style="color:var(--danger);font-weight:600"' : '';
    html += `<td class="${sep}" style="font-size:.65rem;padding:2px 4px">${availPct}%<br><span ${runWarn}>${runCount}/${runTarget}r</span></td>`;
  });
  html += '</tr></tbody></table></div>';
  // Nav controls
  html += `<div style="display:flex;gap:8px;align-items:center;margin-top:8px;flex-wrap:wrap">
    <button onclick="scrollOffset--;savePrefs();API.reloadPlanning().then(render)">◀ Earlier</button>
    <span>Weeks ${formatWeekStart(weeks[0]).slice(5)} – ${formatWeekStart(weeks[weeks.length-1]).slice(5)}</span>
    <button onclick="scrollOffset++;savePrefs();API.reloadPlanning().then(render)">Later ▶</button>
    <button onclick="scrollOffset=0;savePrefs();API.reloadPlanning().then(render)" style="margin-left:8px">Today</button>
    <input type="search" id="team-filter" placeholder="Filter name / team / project" value="${esc(teamFilter)}" oninput="teamFilter=this.value;savePrefs();render()" style="width:160px;font-size:.8rem;margin-left:8px">
    <label style="display:flex;align-items:center;gap:2px;font-size:.8rem">Jump to <input type="date" id="team-jump" onchange="jumpToDate(this.value)" style="width:auto;font-size:.8rem"></label>
    <span style="margin-left:12px;color:var(--fg-muted)">Group by:</span>
    <button onclick="teamGroupBy='name';savePrefs();render()" style="font-weight:${teamGroupBy==='name'?'700':'400'}">Name</button>
    <button onclick="teamGroupBy='sub_team';savePrefs();render()" style="font-weight:${teamGroupBy==='sub_team'?'700':'400'}">Sub-team</button>
    <label style="margin-left:12px;display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${_showWeekend?'checked':''} onchange="_showWeekend=this.checked;savePrefs();render()" style="width:auto"> Weekends</label>
    <label style="display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${teamShowGuests?'checked':''} onchange="teamShowGuests=this.checked;savePrefs();render()" style="width:auto"> Guests</label>
  </div>`;
  // Legend
  html += `<div style="display:flex;gap:12px;margin-top:8px;font-size:.75rem;flex-wrap:wrap">
    <span><span class="half-day away" style="display:inline-block;width:12px;height:12px"></span> Away</span>
    <span><span class="half-day project" style="display:inline-block;width:12px;height:12px"></span> Project</span>
    <span><span class="half-day run" style="display:inline-block;width:12px;height:12px"></span> Run</span>
    <span><span class="half-day undetermined" style="display:inline-block;width:12px;height:12px"></span> Undetermined</span>
    <span><span class="half-day incident" style="display:inline-block;width:12px;height:12px"></span> Incident</span>
    <span><span class="half-day not-filled" style="display:inline-block;width:12px;height:12px"></span> Not filled</span>
    <span><span class="half-day not-filled remote" style="display:inline-block;width:12px;height:12px"></span> 🏠 Remote</span>
    <span><span class="half-day not-filled offsite" style="display:inline-block;width:12px;height:12px"></span> 🏢 Off-site</span>
  </div>`;
  container.innerHTML = html;
}

// Clicking a person name in the team grid opens the My Week view for that person.
function showIndividual(personId) { currentPersonId = personId; currentView = 'myweek'; render(); }

// ===== RUN COVERAGE VIEW =====
function renderRunCoverage(container) {
  const weeks = getVisibleWeeks(); const mode = State.settings.run_mode; const runTarget = State.settings.run_target_persons || 3;
  let html = `<h2>Run Coverage — Target: ${runTarget} persons per half-day</h2><div style="margin-bottom:8px;display:flex;gap:8px;align-items:center">`;
  if (isAdmin()) html += `<label>Mode: <select onchange="API.put('/settings',{run_mode:this.value}).then(()=>{State.settings.run_mode=this.value;render()})"><option value="ratio" ${mode==='ratio'?'selected':''}>Ratio</option><option value="rotation" ${mode==='rotation'?'selected':''}>Rotation</option></select></label>`;
  html += '</div><div class="run-view">';
  weeks.forEach(w => {
    const ws = formatWeekStart(w); const coverage = calcRunCoveragePerSlot(ws); let anyBelow = false;
    html += `<div class="week-card"><div class="week-title">Week ${getWeekNumber(w)} — ${ws}</div>`;
    html += '<table class="slot-coverage-table"><thead><tr><th>Day</th><th>AM</th><th>PM</th></tr></thead><tbody>';
    coverage.forEach(day => {
      ['am','pm'].forEach(slot => { if (day[slot].onRun < runTarget) anyBelow = true; });
      const amCls = day.am.onRun < runTarget ? 'below' : 'met'; const pmCls = day.pm.onRun < runTarget ? 'below' : 'met';
      html += `<tr><td>${day.dayName}</td><td class="${amCls}">${day.am.onRun}/${runTarget}</td><td class="${pmCls}">${day.pm.onRun}/${runTarget}</td></tr>`;
    });
    html += '</table>';
    if (anyBelow) html += `<div class="warning-banner">⚠️ Some slots below target of ${runTarget}</div>`;
    if (mode === 'rotation') {
      const runPeople = getRunPeople(ws);
      const names = runPeople.map(p => { const notes = collectRunNotes(p.id, ws); return notes ? `<span title="${esc(notes)}">${esc(p.name)}*</span>` : esc(p.name); }).join(', ');
      html += `<div style="font-size:.8rem;margin-top:4px">On run: ${names || 'None'}</div>`;
      if (canEdit()) html += `<button onclick="showRotationModal('${ws}')">Assign Run Person</button>`;
    } else {
      const people = getActivePeople();
      const runPeople = people.filter(p => calcRunRatio(p.id, ws).run > 0).map(p => { const notes = collectRunNotes(p.id, ws); const tip = notes ? ` title="${esc(notes)}"` : ''; return `<span${tip}>${esc(p.avatar_emoji)} ${esc(p.name)} (${calcRunRatio(p.id, ws).run}h)${notes?'*':''}</span>`; });
      if (runPeople.length > 0) html += `<div style="font-size:.75rem;margin-top:4px">On run: ${runPeople.join(', ')}</div>`;
    }
    html += '</div>';
  });
  html += '</div>';
  container.innerHTML = html;
}

function showRotationModal(weekStart) {
  const people = getActivePeople(); const current = getRunPeople(weekStart); const currentIds = current.map(p => p.id);
  let html = `<h2>Assign Run Person — Week ${getWeekNumber(parseDate(weekStart))}</h2><div style="display:flex;flex-direction:column;gap:6px">`;
  people.forEach(p => { const checked = currentIds.includes(p.id) ? 'checked' : '';
    html += `<label style="display:flex;align-items:center;gap:8px;cursor:pointer"><input type="checkbox" ${checked} onchange="if(this.checked){API.post('/rotation/assign',{person_id:'${p.id}',week_start:'${weekStart}'}).then(()=>{API.reloadPlanning().then(render)})}else{API.del('/rotation',{person_id:'${p.id}',week_start:'${weekStart}'}).then(()=>{API.reloadPlanning().then(render)})}">${p.avatar_emoji} ${p.name}</label>`;
  });
  html += '</div><div class="form-actions"><button onclick="closeModal()">Done</button></div>';
  showModal(html);
}

// ===== AVAILABILITY VIEW =====
function renderAvailability(container) {
  const people = getActivePeople(); const now = new Date();
  const selDay = new Date(now); selDay.setDate(selDay.getDate() + availabilityDayOffset);
  const selDate = fmtDate(selDay); const dayNames = ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'];
  const dayIdx = (selDay.getDay() + 6) % 7; const dayName = dayNames[dayIdx];
  const isToday = availabilityDayOffset === 0; const currentSlot = now.getHours() < 12 ? 'am' : 'pm';
  const hl = isHoliday(selDate);
  let html = `<h2>Where is everyone — ${dayName} ${selDate}${hl ? ` <span class="holiday-badge" title="${esc(hl)}">${esc(hl)}</span>` : ''}</h2><div style="display:flex;gap:8px;align-items:center;margin-bottom:12px">
    <button onclick="availabilityDayOffset--;savePrefs();render()">◀ Previous day</button><button onclick="availabilityDayOffset=0;savePrefs();render()">Today</button><button onclick="availabilityDayOffset++;savePrefs();render()">Next day ▶</button></div>`;
  html += '<div class="avail-grid">';
  people.forEach(p => {
    const amSd = getSlot(p.id, selDate, 'am'), pmSd = getSlot(p.id, selDate, 'pm');
    const amCls = getSlotClass(amSd), pmCls = getSlotClass(pmSd);
    const amBg = amCls.includes('project') && projectColorBg(amSd) ? ` style="background:${projectColorBg(amSd)}"` : '';
    const pmBg = pmCls.includes('project') && projectColorBg(pmSd) ? ` style="background:${projectColorBg(pmSd)}"` : '';
    function detail(sd) { if (!sd || sd.state==='not_filled') return 'Available'; const rem = sd.remote ? '🏠 ' : sd.offsite ? '🏢 ' : ''; if (sd.state==='undetermined') return rem+'Project (TBD)'; if (sd.away) return `Away: ${sd.away.type.replace(/_/g,' ')}`; if (sd.incident) return `Incident${sd.incident.text?': '+sd.incident.text:''}`; const pn = sd.projects ? sd.projects.map(p=>p.name).join(', ') : ''; return sd.run ? `${rem}${pn} + Run${sd.run_note?': '+sd.run_note:''}` : (rem+pn||'Available'); }
    const amHl = isToday && currentSlot === 'am' ? ';outline:2px solid var(--accent)' : '';
    const pmHl = isToday && currentSlot === 'pm' ? ';outline:2px solid var(--accent)' : '';
    html += `<div class="avail-card"><div class="name">${p.avatar_emoji} ${p.name}</div><div style="font-size:.75rem;color:var(--fg-muted);margin-bottom:4px">${p.role||''}</div><div style="display:flex;gap:6px;margin-top:4px"><div style="flex:1;min-width:0"><div style="font-size:.7rem;color:var(--fg-muted);margin-bottom:2px">AM${isToday&&currentSlot==='am'?' ●':''}</div><div class="status ${amCls}" data-person="${p.id}" data-date="${selDate}" data-slot="am"${amBg} style="${amBg?'':amHl}">${escapeHtml(detail(amSd))}</div></div><div style="flex:1;min-width:0"><div style="font-size:.7rem;color:var(--fg-muted);margin-bottom:2px">PM${isToday&&currentSlot==='pm'?' ●':''}</div><div class="status ${pmCls}" data-person="${p.id}" data-date="${selDate}" data-slot="pm"${pmBg} style="${pmBg?'':pmHl}">${escapeHtml(detail(pmSd))}</div></div></div></div>`;
  });
  html += '</div>';
  let away=0,run=0,proj=0,nf=0,und=0,inc=0;
  people.forEach(p => ['am','pm'].forEach(slot => { const sd = getSlot(p.id, selDate, slot); if (!sd||sd.state==='not_filled') nf++; else if (sd.state==='undetermined') und++; else if (sd.away) away++; else if (sd.incident) inc++; else if (sd.run) run++; else proj++; }));
  html += `<h3 style="margin-top:20px">Day Summary — ${dayName} ${selDate}</h3><div style="display:flex;gap:12px;flex-wrap:wrap;font-size:.85rem"><span style="color:var(--slot-away)">${away} away</span><span style="color:var(--slot-run)">${run} run</span><span style="color:var(--slot-project)">${proj} project</span><span style="color:var(--slot-incident-line)">${inc} incident</span><span style="color:var(--fg-muted)">${und} undetermined</span><span style="color:var(--fg-muted)">${nf} unassigned</span></div>`;
  container.innerHTML = html;
}

// ===== GUESTS VIEW =====
function renderGuests(container) {
  const guests = getActiveGuests();
  if (guests.length === 0) { container.innerHTML = '<h2>Guests</h2><p style="color:var(--fg-muted)">No guests.</p>' + (canEdit()?'<button onclick="showAddPersonModal(true)">Add a Guest</button>':''); return; }
  const weeks = getVisibleWeeks(); const slotsPerWeek = _showWeekend ? 14 : 10;
  let html = '<h2>Guests</h2><div class="grid-container"><table class="grid-table"><thead><tr><th class="person-col">Guest</th>';
  weeks.forEach((w, wi) => { html += `<th colspan="${slotsPerWeek}" class="${wi>0?'week-start':''}">W${getWeekNumber(w)} ${formatWeekStart(w).slice(5)}</th>`; });
  html += '</tr><tr><th class="person-col"></th>';
  weeks.forEach((w, wi) => { const days = getWeekDays(w); const dayNames = _showWeekend ? ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'] : ['Mon','Tue','Wed','Thu','Fri'];
    days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const hl = isHoliday(fmtDate(d)); html += `<th colspan="2" class="day-header${di===0&&wi>0?' week-start':''}">${dayNames[di]} <span class="day-num">/${d.getDate()}</span>${hl ? `<span class="holiday-badge" title="${esc(hl)}">${esc(hl)}</span>` : ''}</th>`; });
  });
  html += '</tr></thead><tbody>';
  const allSlots = [];
  weeks.forEach((w, wi) => { const days = getWeekDays(w); days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const ds = fmtDate(d); ['am','pm'].forEach(slot => allSlots.push({date:ds, slot, weekIdx:wi, dayIdx:di})); }); });
  guests.forEach((p, rowIdx) => {
    html += `<tr><td class="person-col" onclick="showIndividual('${p.id}')">${esc(p.avatar_emoji)} ${esc(p.name)}</td>`;
    let prevMergeKey = null;
    allSlots.forEach((sl, colIdx) => {
      const sd = getSlot(p.id, sl.date, sl.slot); const cls = getSlotClass(sd); const label = getCellLabel(sd);
      const mk = getMergeKey(sd, sl.weekIdx); const isMergedCont = (mk === prevMergeKey);
      const sep = (sl.dayIdx === 0 && sl.slot === 'am' && sl.weekIdx > 0) ? ' week-start' : '';
      const mergeCls = isMergedCont ? ' merged-cont' : '';
      const showLabel = !isMergedCont || cls.includes('not-filled');
      let bgStyle = ''; if (cls.includes('project')) { const bg = projectColorBg(sd); if (bg) bgStyle = ` style="background:${bg}"`; }
      const onmousedown = canEdit() ? ` onmousedown="startDrag(event,'${p.id}','${sl.date}','${sl.slot}',${rowIdx},${colIdx})"` : '';
      html += `<td class="half-day ${cls}${sep}${mergeCls}" data-person="${p.id}" data-date="${sl.date}" data-slot="${sl.slot}" data-row="${rowIdx}" data-col="${colIdx}"${onmousedown}${bgStyle}>${showLabel?`<span class="cell-label">${escapeHtml(label)}</span>`:''}</td>`;
      prevMergeKey = mk;
    });
    html += '</tr>';
  });
  html += `</tbody></table></div><div style="display:flex;gap:8px;align-items:center;margin-top:8px"><button onclick="scrollOffset--;savePrefs();API.reloadPlanning().then(render)">◀ Earlier</button><button onclick="scrollOffset=0;savePrefs();API.reloadPlanning().then(render)">Today</button><button onclick="scrollOffset++;savePrefs();API.reloadPlanning().then(render)">Later ▶</button></div>`;
  container.innerHTML = html;
}

// ===== ARCHIVED VIEW =====
function renderArchived(container) {
  const archived = getArchivedPeople();
  let html = '<h2>Archived People</h2>';
  if (archived.length === 0) html += '<p style="color:var(--fg-muted)">No archived people.</p>';
  else { html += '<div class="people-list">';
    archived.forEach(p => { html += `<div class="person-card" style="opacity:.7"><div class="avatar" style="background:${p.avatar_color}20;color:${p.avatar_color}">${esc(p.avatar_emoji)}</div><div class="info"><div class="name">${esc(p.name)}</div><div class="meta">${esc(p.role||'')} · Archived: ${p.archived_date||'unknown'}${p.is_guest?' · Guest':''}</div></div><div class="actions">`;
      if (canEdit()) html += `<button onclick="API.post('/people/${p.id}/unarchive').then(()=>{const pp=getPerson('${p.id}');if(pp){pp.status='active';pp.archived_date='';}render()})">↩ Restore</button>`;
      if (isAdmin()) html += `<button class="danger" onclick="confirmDeletePerson('${p.id}')">🗑</button>`;
      html += '</div></div>'; });
    html += '</div>'; }
  container.innerHTML = html;
}

// ===== PEOPLE VIEW =====
function renderPeople(container) {
  const active = getActivePeople(), guests = getActiveGuests();
  let html = '<h2>Team Members</h2>';
  if (canEdit()) html += '<div style="margin-bottom:8px"><button onclick="showAddPersonModal(false)">➕ Add Team Member</button></div>';
  html += '<div class="people-list">';
  active.forEach(p => html += personCard(p));
  html += '</div>';
  if (guests.length > 0) { html += '<h3 style="margin-top:16px">Guests</h3><div class="people-list">'; guests.forEach(p => html += personCard(p)); html += '</div>'; }
  container.innerHTML = html;
}
function personCard(p) {
  let html = `<div class="person-card"><div class="avatar" style="background:${p.avatar_color}20;color:${p.avatar_color}">${esc(p.avatar_emoji)}</div><div class="info"><div class="name">${esc(p.name)}</div><div class="meta">${esc(p.role||'No role')}${p.sub_team?' · '+esc(p.sub_team):''}${p.is_guest?' · Guest':''}</div></div><div class="actions">`;
  if (canEdit()) html += `<button onclick="showEditPersonModal('${p.id}')">✏</button><button onclick="confirmArchivePerson('${p.id}')">📦</button><button class="danger" onclick="confirmDeletePerson('${p.id}')">🗑</button>`;
  html += '</div></div>';
  return html;
}

// ===== PROJECTS VIEW =====
const PROJECT_STATUSES = ['unstarted','in_progress','paused','done'];
const STATUS_COLORS = {unstarted:'#6c757d',in_progress:'#4caf50',paused:'#ff9800',done:'#9e9e9e'};
const STATUS_LABELS = {unstarted:'Unstarted',in_progress:'In Progress',paused:'Paused',done:'Done'};
function renderProjects(container) {
  let html = '<h2>Projects</h2><div style="display:flex;gap:8px;align-items:center;margin-bottom:12px;flex-wrap:wrap">';
  html += `<button onclick="projectsViewMode='general';render()" style="font-weight:${projectsViewMode==='general'?'700':'400'}">📋 General</button><button onclick="projectsViewMode='gantt';render()" style="font-weight:${projectsViewMode==='gantt'?'700':'400'}">📊 Gantt</button>`;
  if (canEdit()) html += '<button onclick="showAddProjectModal()">➕ Add Project</button><button onclick="showProjectImportModal()">⬆ Import CSV</button>';
  html += '</div>';
  if (State.projects.length === 0) { html += '<p style="color:var(--fg-muted)">No projects yet.</p>'; container.innerHTML = html; return; }
  if (projectsViewMode === 'gantt') html += renderGanttView(); else html += renderGeneralProjectView();
  container.innerHTML = html;
}
function renderGeneralProjectView() {
  let html = `<div style="margin-bottom:8px"><label style="display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${projectsHideDone?'checked':''} onchange="projectsHideDone=this.checked;render()" style="width:auto"> Hide completed</label></div>`;
  let projects = State.projects.slice().sort((a,b)=>a.name.localeCompare(b.name));
  if (projectsHideDone) projects = projects.filter(p => p.status !== 'done');
  if (projects.length === 0) return '<p style="color:var(--fg-muted)">All done (hidden).</p>';
  projects.forEach(proj => {
    const people = [];
    Object.entries(State.planning).forEach(([key, sd]) => { if (sd.projects && sd.projects.some(p => p.name.toLowerCase() === proj.name.toLowerCase())) { const pid = key.split('|')[0]; const person = getPerson(pid); if (person && !people.find(p => p.id === person.id)) people.push(person); } });
    const statusColor = STATUS_COLORS[proj.status] || '#6c757d';
    const peopleNames = people.map(p => `${p.avatar_emoji} ${p.name}`).join(', ') || 'No one assigned yet';
    html += `<div class="project-card"><div class="proj-header"><span class="proj-emoji" style="font-size:1.3rem">${esc(proj.emoji)}</span><span class="proj-name">${escapeHtml(proj.name)}</span><span class="status-badge" style="background:${statusColor}">${STATUS_LABELS[proj.status]||proj.status}</span><span style="margin-left:auto;display:flex;gap:4px">`;
    if (canEdit()) html += `<button onclick="showEditProjectModal('${proj.id}')" style="font-size:.75rem;padding:2px 8px">✏</button><button class="danger" onclick="confirmDeleteProject('${proj.id}')" style="font-size:.75rem;padding:2px 8px">🗑</button>`;
    const urlOk = proj.url && /^https?:\/\/|^mailto:/i.test(proj.url);
    html += `</span></div>${proj.description?`<div class="proj-desc">${escapeHtml(proj.description)}</div>`:''}<div class="proj-meta">${proj.start_date?`<span>📅 ${proj.start_date}</span>`:''}${proj.end_date?`<span>→ ${proj.end_date}</span>`:''}${urlOk?`<a href="${escapeHtml(proj.url)}" target="_blank" style="font-size:.8rem">🔗 Link</a>`:proj.url?`<span style="font-size:.8rem;color:var(--fg-muted)">${escapeHtml(proj.url)}</span>`:''}</div><div class="proj-people">${(() => { let h=''; if (proj.team_lead) { const lead = getPerson(proj.team_lead); if (lead) { const ls = lead.is_guest ? 'color:var(--fg-muted);font-style:italic' : 'font-weight:600'; h += '<div style="margin-bottom:2px;'+ls+'">⭐ '+esc(lead.avatar_emoji)+' '+esc(lead.name)+(lead.is_guest?' <span style="font-size:.7rem">(guest)</span>':'')+'</div>'; } } h += '👥 '+escapeHtml(peopleNames); return h; })()}</div></div>`;
  });
  return html;
}
function renderGanttView() {
  let projects = State.projects.slice();
  const withDates = projects.filter(p => p.start_date || p.end_date);
  const noDates = projects.filter(p => !p.start_date && !p.end_date);
  if (projectsSortBy === 'start') withDates.sort((a,b)=>(a.start_date||'9999').localeCompare(b.start_date||'9999'));
  else if (projectsSortBy === 'end') withDates.sort((a,b)=>(a.end_date||'9999').localeCompare(b.end_date||'9999'));
  else withDates.sort((a,b)=>a.name.localeCompare(b.name));
  noDates.sort((a,b)=>a.name.localeCompare(b.name));
  projects = withDates.concat(noDates);
  const now = new Date();
  let minDate = fmtDate(now), maxDate = fmtDate(now);
  projects.forEach(p => { if (p.start_date && p.start_date < minDate) minDate = p.start_date; if (p.end_date && p.end_date > maxDate) maxDate = p.end_date; });
  const minD = parseDate(minDate) || now, maxD = parseDate(maxDate) || now;
  minD.setDate(minD.getDate() - 7); maxD.setDate(maxD.getDate() + 7);
  const weeks = []; const w = new Date(minD); w.setHours(0,0,0,0); w.setDate(w.getDate() - ((w.getDay() + 6) % 7));
  while (w <= maxD) { weeks.push(new Date(w)); w.setDate(w.getDate() + 7); }
  const colCount = weeks.length; const colWidth = 60;
  let html = `<div style="margin-bottom:8px;display:flex;gap:8px;align-items:center"><span style="font-size:.85rem;color:var(--fg-muted)">Sort by:</span><button onclick="projectsSortBy='name';render()" style="font-weight:${projectsSortBy==='name'?'700':'400'}">Name</button><button onclick="projectsSortBy='start';render()" style="font-weight:${projectsSortBy==='start'?'700':'400'}">Start date</button><button onclick="projectsSortBy='end';render()" style="font-weight:${projectsSortBy==='end'?'700':'400'}">End date</button></div>`;
  if (projects.length === 0) return '<p style="color:var(--fg-muted)">No projects.</p>';
  html += '<div class="gantt-container"><table class="gantt-table"><thead><tr><th style="text-align:left;min-width:140px;position:sticky;left:0;z-index:3;background:var(--surface)">Project</th>';
  weeks.forEach(wk => html += `<th style="min-width:${colWidth}px">W${getWeekNumber(wk)} ${fmtDate(wk).slice(5)}</th>`);
  html += '</tr></thead><tbody>';
  projects.forEach(proj => {
    const statusColor = STATUS_COLORS[proj.status] || '#6c757d';
    const projStart = proj.start_date ? parseDate(proj.start_date) : null;
    const projEnd = proj.end_date ? parseDate(proj.end_date) : null;
    let startCol = 0, endCol = colCount - 1;
    if (projStart) { const dayDiff = Math.round((projStart - weeks[0]) / 86400000); startCol = Math.max(0, Math.floor(dayDiff / 7)); }
    if (projEnd) { const dayDiff = Math.round((projEnd - weeks[0]) / 86400000); endCol = Math.min(colCount - 1, Math.ceil(dayDiff / 7)); }
    if (startCol > endCol) endCol = startCol;
    const span = endCol - startCol + 1;
    const peopleNow = [];
    Object.entries(State.planning).forEach(([key, sd]) => { if (sd.projects && sd.projects.some(p => p.name.toLowerCase() === proj.name.toLowerCase())) { const pid = key.split('|')[0]; const person = getPerson(pid); if (person && !peopleNow.find(p => p.id === person.id)) peopleNow.push(person); } });
    const hasNoPeopleNow = proj.status === 'in_progress' && peopleNow.length === 0;
    const nowDayDiff = Math.round((now - weeks[0]) / 86400000); const nowCol = nowDayDiff / 7;
    const nowInBar = nowCol >= startCol && nowCol <= endCol + 1;
    const peopleText = peopleNow.map(p => esc(p.name)).join(', ') || 'No one currently assigned';
    const tooltip = `${escapeHtml(proj.name)} — ${STATUS_LABELS[proj.status]||proj.status}\nPeople now: ${escapeHtml(peopleText)}`;
    html += `<tr class="gantt-row"><td style="text-align:left;position:sticky;left:0;background:var(--surface);z-index:1">${esc(proj.emoji)} ${escapeHtml(proj.name)}</td>`;
    for (let c = 0; c < colCount; c++) {
      if (c === startCol) {
        let barStyle = `background:${statusColor};`;
        if (hasNoPeopleNow && nowInBar) barStyle += 'border:2px dashed var(--danger);';
        if (proj.status === 'paused') barStyle += 'opacity:.6;background:#ff9800;';
        if (proj.status === 'done') barStyle += 'opacity:.4;';
        if (proj.status === 'unstarted') barStyle += 'opacity:.5;';
        html += `<td colspan="${span}" style="padding:3px;position:relative"><div class="gantt-bar ${hasNoPeopleNow?'no-people':''}" style="${barStyle}" title="${tooltip}">${escapeHtml(proj.name)}</div>${nowInBar?`<div class="gantt-now-line" style="left:${((nowCol-c)/span)*100}%"></div>`:''}</td>`;
        c += span - 1;
      } else html += '<td></td>';
    }
    html += '</tr>';
  });
  html += '</tbody></table></div>';
  return html;
}

// ===== SETTINGS VIEW =====
function renderSettings(container) {
  const s = State.settings;
  let html = '<h2>Settings</h2><div class="settings-grid">';
  html += `<div class="field"><label>Window weeks (≥3)</label><input type="number" min="3" value="${s.window_weeks}" onchange="API.put('/settings',{window_weeks:String(Math.max(3,+this.value))}).then(()=>{State.settings.window_weeks=Math.max(3,+this.value);render()})"></div>`;
  html += `<div class="field"><label>Run mode</label><select onchange="API.put('/settings',{run_mode:this.value}).then(()=>{State.settings.run_mode=this.value;render()})"><option value="ratio" ${s.run_mode==='ratio'?'selected':''}>Ratio</option><option value="rotation" ${s.run_mode==='rotation'?'selected':''}>Rotation</option></select></div>`;
  html += `<div class="field"><label>Run target (persons)</label><input type="number" min="0" value="${s.run_target_persons}" onchange="API.put('/settings',{run_target_persons:String(+this.value)}).then(()=>{State.settings.run_target_persons=+this.value;render()})"></div>`;
  html += `<div class="field"><label>Theme</label><select onchange="State.theme=this.value; localStorage.setItem('teamviz_theme',this.value); render()">${THEMES.map(t=>`<option value="${t}" ${State.theme===t?'selected':''}>${t.split('_').map(w=>w.charAt(0).toUpperCase()+w.slice(1)).join(' ')}</option>`).join('')}</select></div>`;
  html += '</div>';
  html += '<p style="margin-top:12px;color:var(--fg-muted);font-size:.8rem">Theme is stored in your browser only; the rest apply app-wide.</p>';
  container.innerHTML = html;
}

// ===== ADMIN VIEW (admin only) =====
function renderAdmin(container) {
  const s = State.settings;
  let html = '<h2>Admin</h2><div class="settings-grid">';
  html += `<div class="field"><label>Prune threshold (weeks)</label><input type="number" min="1" value="${s.prune_weeks}" onchange="API.put('/settings',{prune_weeks:String(Math.max(1,+this.value))}).then(()=>{State.settings.prune_weeks=Math.max(1,+this.value)})"></div>`;
  html += '</div>';
  html += '<div style="margin-top:16px;display:flex;gap:8px;flex-wrap:wrap">';
  html += `<button onclick="if(confirm('Prune old data?')){API.post('/planning/prune',{}).then(r=>{alert('Pruned '+r.deleted+' entries');API.reloadPlanning().then(render)})}">🧹 Prune Old Data</button>`;
  html += `<button class="danger" onclick="if(confirm('Reset ALL data?')){API.post('/reset',{}).then(()=>{API.loadAll().then(render)})}">⚠️ Reset All Data</button>`;
  html += `<button onclick="showImportModal()">⬆ Import TOML</button>`;
  html += '</div>';
  html += '<p style="margin-top:12px;color:var(--fg-muted);font-size:.8rem">Import replaces/plans data and can wipe the database — admin only.</p>';
  container.innerHTML = html;
}

// ===== WORKLOAD VIEW (all roles) =====

// Half-day workload value per the spec:
// not filled / away / incident = 0; project(s) = sum of pct; project(s)+run = sum of pct; run only = 100; undetermined = 100.
function halfDayValue(sd) {
  if (!sd || sd.state === 'not_filled') return 0;
  if (sd.away) return 0;
  if (sd.state === 'undetermined') return 100;
  if (sd.projects && sd.projects.length > 0) return sd.projects.reduce((a,pr) => a + (pr.pct||0), 0);
  if (sd.run) return 100;
  return 0;
}
// Half-day presence: away = 0, everything else (not filled, project, run, undetermined, incident) = 100.
function halfDayPresence(sd) {
  if (sd && sd.away) return 0;
  return 100;
}

function renderWorkload(container) {
  const people = getActivePeople();
  const weeks = getVisibleWeeks();
  const dayCount = _showWeekend ? 7 : 5;
  let html = '<h2>Workload</h2><p style="color:var(--fg-muted);font-size:.85rem;margin-bottom:8px">Average daily allocation (AM+PM / 2). <span style="color:var(--danger)">Red</span> = over 100% in a half-day. Away/not-filled = 0, run-only/undetermined = 100, projects = sum of %.</p>';
  html += '<div class="grid-container"><table class="grid-table"><thead><tr><th class="person-col">Person</th>';
  weeks.forEach((w, wi) => { const ws = formatWeekStart(w); const wn = getWeekNumber(w); html += `<th colspan="${dayCount}" class="${wi>0?'week-start':''}">W${wn} ${ws.slice(5)}</th>`; });
  html += '</tr><tr><th class="person-col"></th>';
  weeks.forEach((w, wi) => { const days = getWeekDays(w); const dayNames = _showWeekend ? ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'] : ['Mon','Tue','Wed','Thu','Fri'];
    days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; html += `<th class="${di===0&&wi>0?'week-start':''}">${dayNames[di]} / ${d.getDate()}</th>`; });
  });
  html += '</tr></thead><tbody>';
  // Per-person rows
  people.forEach(p => {
    html += `<tr><td class="person-col">${esc(p.avatar_emoji)} ${esc(p.name)}</td>`;
    weeks.forEach((w, wi) => {
      const days = getWeekDays(w);
      days.forEach((d, di) => {
        if (di >= 5 && !_showWeekend) return;
        const ds = fmtDate(d);
        const amSd = getSlot(p.id, ds, 'am'), pmSd = getSlot(p.id, ds, 'pm');
        const amVal = halfDayValue(amSd), pmVal = halfDayValue(pmSd);
        const dayAvg = Math.round((amVal + pmVal) / 2);
        const over = amVal > 100 || pmVal > 100;
        let tipParts = [];
        [amSd, pmSd].forEach(sd => { if (sd && sd.projects) sd.projects.forEach(pr => { if (!tipParts.includes(pr.name)) tipParts.push(pr.name); }); if (sd && sd.state === 'undetermined' && !tipParts.includes('TBD')) tipParts.push('TBD'); if (sd && sd.run && (!sd.projects || sd.projects.length === 0) && !tipParts.includes('Run')) tipParts.push('Run'); });
        const cls = over ? ' style="color:var(--danger);font-weight:700"' : '';
        html += `<td${cls} title="${esc(tipParts.join(', '))}">${dayAvg > 0 ? dayAvg + '%' : '\u2014'}</td>`;
      });
    });
    html += '</tr>';
  });
  // Remote percentage summary row (per day)
  html += '<tr class="summary-row"><td class="person-col" style="font-size:.7rem">\ud83c\udf68 Remote</td>';
  weeks.forEach((w, wi) => {
    const days = getWeekDays(w);
    days.forEach((d, di) => {
      if (di >= 5 && !_showWeekend) return;
      const ds = fmtDate(d);
      let remoteCount = 0;
      people.forEach(p => { const amSd = getSlot(p.id, ds, 'am'), pmSd = getSlot(p.id, ds, 'pm'); if ((amSd && amSd.remote) || (pmSd && pmSd.remote)) remoteCount++; });
      const pct = people.length > 0 ? Math.round(remoteCount / people.length * 100) : 0;
      const sep = (di === 0 && wi > 0) ? ' week-start' : '';
      const color = pct > 0 ? 'var(--remote-border)' : 'var(--fg-muted)';
      html += `<td class="${sep}" style="font-size:.65rem;padding:2px 4px;color:${color}">${pct > 0 ? pct + '%' : '\u2014'}</td>`;
    });
  });
  html += '</tr>';
  // Off-site percentage summary row (per day)
  html += '<tr class="summary-row"><td class="person-col" style="font-size:.7rem">🏢 Off-site</td>';
  weeks.forEach((w, wi) => {
    const days = getWeekDays(w);
    days.forEach((d, di) => {
      if (di >= 5 && !_showWeekend) return;
      const ds = fmtDate(d);
      let offsiteCount = 0;
      people.forEach(p => { const amSd = getSlot(p.id, ds, 'am'), pmSd = getSlot(p.id, ds, 'pm'); if ((amSd && amSd.offsite) || (pmSd && pmSd.offsite)) offsiteCount++; });
      const pct = people.length > 0 ? Math.round(offsiteCount / people.length * 100) : 0;
      const sep = (di === 0 && wi > 0) ? ' week-start' : '';
      const color = pct > 0 ? 'var(--offsite-border)' : 'var(--fg-muted)';
      html += `<td class="${sep}" style="font-size:.65rem;padding:2px 4px;color:${color}">${pct > 0 ? pct + '%' : '\u2014'}</td>`;
    });
  });
  html += '</tr>';
  // Weekly presence summary row (per week, team average): away = 0, everything else = 100
  html += '<tr class="summary-row"><td class="person-col" style="font-size:.7rem">Presence</td>';
  weeks.forEach((w, wi) => {
    const days = getWeekDays(w);
    let totalPresence = 0, totalSlots = 0;
    people.forEach(p => {
      days.forEach((d, di) => {
        if (di >= 5 && !_showWeekend) return;
        const ds = fmtDate(d);
        ['am','pm'].forEach(slot => { const sd = getSlot(p.id, ds, slot); totalSlots++; totalPresence += halfDayPresence(sd); });
      });
    });
    const presencePct = totalSlots > 0 ? Math.round(totalPresence / totalSlots) : 0;
    const sep = wi > 0 ? ' week-start' : '';
    html += `<td colspan="${dayCount}" class="${sep}" style="font-size:.65rem;padding:2px 4px;color:var(--accent);font-weight:600">${presencePct}%</td>`;
  });
  html += '</tr>';
  html += '</tbody></table></div>';
  html += `<div style="display:flex;gap:8px;align-items:center;margin-top:8px">
    <button onclick="scrollOffset--;savePrefs();API.reloadPlanning().then(render)">\u25c0 Earlier</button>
    <span>Weeks ${formatWeekStart(weeks[0]).slice(5)} \u2013 ${formatWeekStart(weeks[weeks.length-1]).slice(5)}</span>
    <button onclick="scrollOffset++;savePrefs();API.reloadPlanning().then(render)">Later \u25b6</button>
    <button onclick="scrollOffset=0;savePrefs();API.reloadPlanning().then(render)" style="margin-left:8px">Today</button>
    <label style="display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${_showWeekend?'checked':''} onchange="_showWeekend=this.checked;savePrefs();render()" style="width:auto"> Weekends</label>
  </div>`;
  container.innerHTML = html;
}

// ===== ACTIVITY VIEW (all roles) =====

// Map of action → human-friendly label and category for filtering.
const ACT_LABELS = {
  planning_set:        { label: 'Set slot',          cat: 'planning' },
  planning_clear:      { label: 'Clear slot',        cat: 'planning' },
  planning_range:      { label: 'Set range',         cat: 'planning' },
  planning_range_clear:{ label: 'Clear range',       cat: 'planning' },
  planning_copy:       { label: 'Copy week',         cat: 'planning' },
  oncall_set:          { label: 'On-call set',       cat: 'oncall' },
  oncall_remove:       { label: 'On-call removed',   cat: 'oncall' },
  project_add:         { label: 'Project added',    cat: 'projects' },
  project_update:      { label: 'Project updated',  cat: 'projects' },
  project_delete:      { label: 'Project deleted',  cat: 'projects' },
  project_import_csv:  { label: 'Projects imported', cat: 'projects' },
  person_add:          { label: 'Person added',      cat: 'people' },
  person_update:       { label: 'Person updated',   cat: 'people' },
  person_delete:       { label: 'Person deleted',    cat: 'people' },
  person_archive:      { label: 'Person archived',  cat: 'people' },
  person_unarchive:    { label: 'Person restored',  cat: 'people' },
  prune:               { label: 'Pruned old data',  cat: 'system' },
  reset:               { label: 'Reset all data',   cat: 'system' },
  settings_update:     { label: 'Settings updated',  cat: 'system' },
};
const ACT_CAT_LABELS = {
  all: 'All actions', planning: 'Planning', oncall: 'On-call', projects: 'Projects', people: 'People', system: 'System',
};

function renderActivity(container) {
  if (!Array.isArray(State.activity)) State.activity = [];
  // Show cached data immediately (instant tab switch), then always fetch fresh.
  if (State.activity.length > 0) renderActivityList(container);
  else container.innerHTML = '<h2>Activity</h2><p style="color:var(--fg-muted)">Loading…</p>';
  API.get('/activity?limit=50').then(d => { State.activity = d || []; renderActivityList(container); });
}

function renderActivityList(container) {
  const events = State.activity || [];
  // Build unique actor list from the events for the actor filter.
  const actors = {};
  events.forEach(e => { if (e.actor) actors[e.actor] = true; });

  // Apply filters.
  const q = activityFilter.search.trim().toLowerCase();
  const filtered = events.filter(e => {
    if (activityFilter.type !== 'all') {
      const info = ACT_LABELS[e.action];
      if (!info || info.cat !== activityFilter.type) return false;
    }
    if (activityFilter.actor !== 'all' && e.actor !== activityFilter.actor) return false;
    if (activityFilter.person !== 'all') {
      const meta = e.meta || {};
      const ids = meta.person_ids || (meta.person_id ? [meta.person_id] : []);
      if (!ids.includes(activityFilter.person)) return false;
    }
    if (q) {
      const meta = e.meta || {};
      const info = ACT_LABELS[e.action];
      const hay = [
        e.action, info ? info.label : '', e.target, e.detail, e.actor,
        meta.person_name, meta.project_name, meta.away_type, meta.incident_text,
        Array.isArray(meta.projects) ? meta.projects.join(' ') : '',
        (meta.person_ids || []).map(id => { const p = getPerson(id); return p ? p.name : id; }).join(' '),
      ].join(' ').toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  });

  let html = '<h2>Activity</h2>';
  // Toolbar: refresh + filters + search
  html += '<div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:10px">';
  html += `<button onclick="API.get('/activity?limit=50').then(d=>{State.activity=d||[];render()})">🔄 Refresh</button>`;
  html += `<select id="act-filter-type" onchange="activityFilter.type=this.value;renderActivityList(document.getElementById('main').querySelector('.act-list-wrap')||document.getElementById('main'));" style="width:auto">`;
  Object.keys(ACT_CAT_LABELS).forEach(c => {
    html += `<option value="${c}" ${activityFilter.type===c?'selected':''}>${ACT_CAT_LABELS[c]}</option>`;
  });
  html += '</select>';
  // Person filter
  html += `<select id="act-filter-person" onchange="activityFilter.person=this.value;renderActivityList(document.getElementById('main').querySelector('.act-list-wrap')||document.getElementById('main'));" style="width:auto">`;
  html += `<option value="all" ${activityFilter.person==='all'?'selected':''}>All team members</option>`;
  State.people.forEach(p => html += `<option value="${p.id}" ${activityFilter.person===p.id?'selected':''}>${esc(p.avatar_emoji||'👤')} ${esc(p.name)}</option>`);
  html += '</select>';
  // Actor filter
  html += `<select id="act-filter-actor" onchange="activityFilter.actor=this.value;renderActivityList(document.getElementById('main').querySelector('.act-list-wrap')||document.getElementById('main'));" style="width:auto">`;
  html += `<option value="all" ${activityFilter.actor==='all'?'selected':''}>All users</option>`;
  Object.keys(actors).forEach(a => html += `<option value="${esc(a)}" ${activityFilter.actor===a?'selected':''}>${esc(a)}</option>`);
  html += '</select>';
  // Search box
  html += `<input id="act-filter-search" type="search" placeholder="Search…" value="${esc(activityFilter.search)}" oninput="activityFilter.search=this.value;actSearchDebounce()" style="width:160px">`;
  const active = (activityFilter.type!=='all')||(activityFilter.person!=='all')||(activityFilter.actor!=='all')||(q!=='');
  if (active) html += `<button onclick="activityFilter={type:'all',person:'all',actor:'all',search:''};render()" title="Clear filters">✕ Clear</button>`;
  html += `<span style="color:var(--fg-muted);font-size:.8rem;margin-left:auto">${filtered.length} of ${events.length} event${events.length===1?'':'s'}</span>`;
  html += '</div>';

  html += '<div class="act-list-wrap" style="display:flex;flex-direction:column;gap:4px;max-height:72vh;overflow-y:auto">';
  if (filtered.length === 0) {
    html += '<p style="color:var(--fg-muted)">No activity matching the filters.</p>';
  } else {
    // Group by day (based on event timestamp).
    let lastDay = '';
    filtered.forEach(e => {
      const day = (e.ts || '').slice(0, 10);
      if (day !== lastDay) {
        lastDay = day;
        html += `<div class="act-day-head" style="margin:10px 0 4px;font-size:.75rem;color:var(--fg-muted);font-weight:600;border-bottom:1px solid var(--border);padding-bottom:2px">${actDayLabel(day)}</div>`;
      }
      html += actRow(e);
    });
  }
  html += '</div>';
  container.innerHTML = html;
}

// Debounce search re-render so typing doesn't thrash.
let _actSearchTimer = null;
function actSearchDebounce() {
  clearTimeout(_actSearchTimer);
  _actSearchTimer = setTimeout(() => {
    const main = document.getElementById('main');
    if (currentView === 'activity') renderActivityList(main);
  }, 200);
}

function actDayLabel(dayStr) {
  if (!dayStr) return 'Unknown date';
  const d = new Date(dayStr + 'T00:00:00');
  if (isNaN(d)) return dayStr;
  const today = new Date(); today.setHours(0,0,0,0);
  const diff = Math.round((today - d) / 86400000);
  const nice = d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
  if (diff === 0) return 'Today · ' + nice;
  if (diff === 1) return 'Yesterday · ' + nice;
  return nice;
}

function actRow(e) {
  const info = ACT_LABELS[e.action] || { label: e.action, cat: 'other' };
  const meta = e.meta || {};
  const ts = e.ts ? new Date(e.ts).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) : '';
  let html = `<div class="act-row" style="background:var(--surface);border:1px solid var(--border);border-radius:4px;padding:6px 10px;font-size:.8rem;display:flex;gap:8px;align-items:flex-start;flex-wrap:wrap">`;
  // Time + actor
  html += `<span style="color:var(--fg-muted);font-size:.7rem;min-width:48px;white-space:nowrap">${esc(ts)}</span>`;
  html += `<span class="pv-badge" style="font-size:.7rem;padding:1px 6px;border-radius:3px;background:var(--accent);color:#fff" title="${esc(e.actor||'')}">${esc(e.actor||'system')}</span>`;
  // Action label
  html += `<strong style="min-width:110px">${esc(info.label)}</strong>`;
  // Targeted team members (the key improvement)
  html += actPersonChips(meta);
  // Friendly date / slot / range
  html += actDateLabel(meta);
  // State badge (away / incident / project / run / cleared)
  html += actStateBadge(meta);
  // Extra detail (project names, away note, counts)
  html += actExtra(meta, e);
  html += '</div>';
  return html;
}

function actPersonChips(meta) {
  const ids = meta.person_ids || (meta.person_id ? [meta.person_id] : []);
  if (!ids.length) {
    if (meta.person_name) return `<span style="color:var(--fg)" title="person">👤 ${esc(meta.person_name)}</span>`;
    return '';
  }
  let out = '<span style="display:flex;gap:3px;flex-wrap:wrap;align-items:center">';
  ids.forEach(id => {
    const p = getPerson(id);
    if (p) {
      out += `<span style="background:${(p.avatar_color||'#888')}20;color:${p.avatar_color||'#888'};border-radius:10px;padding:1px 8px;font-size:.72rem;white-space:nowrap" title="${esc(p.role||'')}${p.sub_team?' · '+esc(p.sub_team):''}">${esc(p.avatar_emoji||'👤')} ${esc(p.name)}</span>`;
    } else {
      out += `<span style="color:var(--fg-muted);font-size:.72rem;font-family:var(--font-mono);white-space:nowrap" title="unknown / archived person">${esc(id)}</span>`;
    }
  });
  out += '</span>';
  return out;
}

function actDateLabel(meta) {
  // Single-slot action: date + slot
  if (meta.date) {
    const d = parseDate(meta.date);
    const day = d ? d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' }) : meta.date;
    const slot = meta.slot ? (meta.slot === 'am' ? 'AM' : 'PM') : '';
    return `<span style="color:var(--fg-muted);font-size:.75rem;white-space:nowrap">📅 ${esc(day)}${slot?' · '+esc(slot):''}</span>`;
  }
  // Week-based action: on-call / copy
  if (meta.week_start) {
    const d = parseDate(meta.week_start);
    const day = d ? d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' }) : meta.week_start;
    return `<span style="color:var(--fg-muted);font-size:.75rem;white-space:nowrap">📅 Week of ${esc(day)}</span>`;
  }
  if (meta.from_week || meta.to_week) {
    const f = meta.from_week ? actShortDate(meta.from_week) : '?';
    const t = meta.to_week ? actShortDate(meta.to_week) : '?';
    return `<span style="color:var(--fg-muted);font-size:.75rem;white-space:nowrap">📅 ${esc(f)} → ${esc(t)}</span>`;
  }
  // Range action: start_date/slot → end_date/slot
  if (meta.start_date || meta.end_date) {
    const s = actShortDate(meta.start_date) + (meta.start_slot ? ' ' + (meta.start_slot === 'am' ? 'AM' : 'PM') : '');
    const en = actShortDate(meta.end_date) + (meta.end_slot ? ' ' + (meta.end_slot === 'am' ? 'AM' : 'PM') : '');
    return `<span style="color:var(--fg-muted);font-size:.75rem;white-space:nowrap">📅 ${esc(s)} → ${esc(en)}</span>`;
  }
  return '';
}

function actShortDate(s) {
  if (!s) return '?';
  const d = parseDate(s);
  return d ? d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' }) : s;
}

function actStateBadge(meta) {
  const st = meta.state;
  if (!st || st === 'cleared') return '';
  const styles = {
    away:      'background:var(--slot-away);color:#fff',
    incident:  'background:var(--slot-incident);color:var(--slot-incident-line);border:1px solid var(--slot-incident-line)',
    project:   'background:var(--slot-project);color:#fff',
    run:       'background:var(--slot-run);color:#fff',
    undetermined: 'background:var(--slot-undetermined);color:var(--fg)',
  };
  const s = styles[st] || '';
  if (!s) return '';
  let label = st.charAt(0).toUpperCase() + st.slice(1);
  if (st === 'away' && meta.away_type) label += ': ' + meta.away_type;
  return `<span style="${s};font-size:.7rem;padding:1px 6px;border-radius:3px;white-space:nowrap">${esc(label)}</span>`;
}

function actExtra(meta, e) {
  let bits = [];
  if (meta.remote) bits.push('🌐 remote');
  if (meta.offsite) bits.push('🏢 offsite');
  if (meta.run && meta.state === 'project') bits.push('🏃 +run');
  if (meta.run_note) bits.push('🏃 ' + meta.run_note);
  if (Array.isArray(meta.projects) && meta.projects.length) bits.push('📁 ' + meta.projects.join(', '));
  if (meta.away_note) bits.push('📝 ' + meta.away_note);
  if (meta.incident_text) bits.push('⚠ ' + meta.incident_text);
  if (meta.project_name) bits.push('📁 ' + meta.project_name);
  if (meta.weeks_old !== undefined) bits.push('older than ' + meta.weeks_old + ' weeks');
  if (meta.deleted !== undefined) bits.push('deleted ' + meta.deleted + ' slots');
  if (meta.created !== undefined) bits.push('created ' + meta.created);
  if (meta.updated !== undefined) bits.push('updated ' + meta.updated);
  if (meta.people_count !== undefined && !(meta.person_ids && meta.person_ids.length)) bits.push(meta.people_count + ' people');
  if (meta.keys && meta.keys.length) bits.push('keys: ' + meta.keys.join(', '));
  // Fallback to legacy text fields if nothing structured was recorded.
  if (!bits.length && (e.detail || e.target)) {
    const t = [e.target, e.detail].filter(Boolean).join(' — ');
    if (t) bits.push(esc(t));
  }
  if (!bits.length) return '';
  return `<span style="color:var(--fg-muted);font-size:.75rem;flex:1;min-width:120px">${bits.join(' · ')}</span>`;
}

// ===== INCIDENTS VIEW (all roles) =====
function renderIncidents(container) {
  if (!Array.isArray(State.incidents)) State.incidents = [];
  if (State.incidents.length > 0) renderIncidentsList(container);
  else container.innerHTML = '<h2>Incidents</h2><p style="color:var(--fg-muted)">Loading…</p>';
  API.get('/incidents').then(d => { State.incidents = d || []; renderIncidentsList(container); });
}
function renderIncidentsList(container) {
  let html = '<h2>Incidents</h2><div style="margin-bottom:8px;display:flex;gap:8px;align-items:center"><button onclick="API.get(\'/incidents\').then(d=>{State.incidents=d||[];render()})">🔄 Refresh</button>';
  if (State.incidents.length > 0) html += `<span style="color:var(--fg-muted);font-size:.85rem">${State.incidents.length} incident${State.incidents.length>1?'s':''} recorded</span>`;
  html += '</div>';
  if (State.incidents.length === 0) {
    html += '<p style="color:var(--fg-muted)">No incidents recorded.</p>';
    container.innerHTML = html;
    return;
  }
  html += '<div style="display:flex;flex-direction:column;gap:4px;max-height:70vh;overflow-y:auto">';
  State.incidents.forEach(inc => {
    const slotLabel = inc.slot === 'am' ? 'AM' : 'PM';
    const textDisp = inc.incident_text ? esc(inc.incident_text) : '<span style="color:var(--fg-muted);font-style:italic">(no description)</span>';
    html += `<div style="background:var(--surface);border:1px solid var(--border);border-left:3px solid var(--slot-incident-line);border-radius:4px;padding:6px 10px;font-size:.85rem;display:flex;gap:8px;align-items:center;flex-wrap:wrap">
      <span style="color:var(--fg-muted);font-size:.75rem;min-width:110px;white-space:nowrap">${inc.date} ${slotLabel}</span>
      <span style="min-width:120px;cursor:pointer;color:var(--accent)" onclick="showIndividual('${inc.person_id}')" title="Click to view this person's schedule">${esc(inc.avatar_emoji||'👤')} ${esc(inc.person_name)}</span>
      <span style="color:var(--slot-incident-line);font-weight:700;font-size:1rem">⚠</span>
      <span style="flex:1;min-width:200px">${textDisp}</span>
    </div>`;
  });
  html += '</div>';
  container.innerHTML = html;
}

// ===== USERS VIEW (admin only) =====
function renderUsers(container) {
  if (!Array.isArray(State.users)) State.users = [];
  if (State.users.length === 0) {
    API.get('/users').then(d => { State.users = d || []; render(); });
    container.innerHTML = '<h2>Users</h2><p style="color:var(--fg-muted)">Loading…</p>';
    return;
  }
  let html = '<h2>Users</h2><p style="color:var(--fg-muted);font-size:.85rem;margin-bottom:8px">Roles come from Keycloak groups (read-only).</p>';
  html += '<table class="grid-table" style="width:100%"><thead><tr><th>Username</th><th>Role</th><th>Created</th><th>Last seen</th><th>I am (person)</th></tr></thead><tbody>';
  State.users.forEach(u => {
    const created = u.created_at ? new Date(u.created_at).toLocaleString() : '—';
    const lastSeen = u.last_seen ? new Date(u.last_seen).toLocaleString() : '—';
    html += `<tr><td>${esc(u.username)}</td><td>${esc(u.role)}</td><td>${created}</td><td>${lastSeen}</td><td><select onchange="API.put('/users/'+${u.id}+'/person',{person_id:this.value}).then(()=>{u.person_id=this.value;toast('Updated','success')})" style="width:auto;font-size:.75rem">`;
    html += '<option value="">—</option>';
    State.people.forEach(p => html += `<option value="${p.id}" ${u.person_id===p.id?'selected':''}>${esc(p.name)}</option>`);
    html += '</select></td></tr>';
  });
  html += '</tbody></table>';
  container.innerHTML = html;
}

// ===== MY WEEK VIEW (all roles, mobile-optimised) =====
function renderMyWeek(container) {
  // Works for both "My Week" (the logged-in user's person) and clicking a
  // specific person in the team grid (via currentPersonId). The nav button
  // clears currentPersonId so the tab shows the logged-in user's person.
  const pid = currentPersonId || State.myPersonId;
  if (!pid) {
    let html = '<h2>My Week</h2><p style="color:var(--fg-muted);margin-bottom:12px">Select who you are from the picker in the top bar, or choose below:</p>';
    html += '<select onchange="API.put(\'/me/person\',{person_id:this.value}).then(()=>{State.myPersonId=this.value;render()})" style="width:auto;font-size:1rem;padding:6px 12px">';
    html += '<option value="">I am…</option>';
    State.people.filter(p => p.status === 'active').forEach(p => html += `<option value="${p.id}">${esc(p.name)}</option>`);
    html += '</select>';
    container.innerHTML = html;
    return;
  }
  const p = getPerson(pid);
  if (!p) { container.innerHTML = '<h2>My Week</h2><p style="color:var(--fg-muted)">Person not found. Please re-select in the top bar.</p>'; return; }
  const weeks = getVisibleWeeks();
  const wsStart = formatWeekStart(weeks[0]), wsEnd = formatWeekStart(weeks[weeks.length-1]);
  // Header with ICS export / copy / subscription (merged from the old individual view)
  let html = `<div class="indiv-header"><div class="person-info"><div class="avatar" style="background:${p.avatar_color}20;color:${p.avatar_color}">${esc(p.avatar_emoji)}</div><div><h2>${esc(p.name)}</h2><span style="color:var(--fg-muted);font-size:.85rem">${esc(p.role||'')}${p.sub_team?' · '+esc(p.sub_team):''}${p.is_guest?' · Guest':''}</span></div></div><div class="spacer" style="flex:1"></div>`;
  html += `<button onclick="downloadICS('${p.id}')" title="Period: ${wsStart} → ${wsEnd}">📅 Export ICS</button>`;
  if (canEdit()) html += `<button onclick="copyLastWeek('${p.id}')">📋 Copy Last Week</button>`;
  if (currentPersonId) html += `<button onclick="currentPersonId=null;currentView='team';render()">← Back</button>`;
  html += `</div>`;
  // ICS subscription
  if (p.ics_token) {
    const url = `${location.origin}/api/ics/public/${p.ics_token}`;
    html += `<div style="margin-top:8px;font-size:.8rem;color:var(--fg-muted)">📅 Subscribe: <code style="font-size:.7rem">${esc(url)}</code> <button onclick="navigator.clipboard.writeText('${url}').then(()=>toast('Copied!','success'))" style="font-size:.7rem;padding:1px 6px">Copy</button></div>`;
  }
  if (isAdmin()) {
    html += `<div style="margin-top:4px;display:flex;gap:4px">`;
    html += `<button onclick="API.post('/people/${p.id}/ics-token').then(r=>{const pp=getPerson('${p.id}');if(pp)pp.ics_token=r.token;render()})" style="font-size:.7rem;padding:1px 6px">${p.ics_token?'Reset':'Generate'} subscription</button>`;
    if (p.ics_token) html += `<button onclick="API.del('/people/${p.id}/ics-token').then(()=>{const pp=getPerson('${p.id}');if(pp)pp.ics_token='';render()})" style="font-size:.7rem;padding:1px 6px">Revoke</button>`;
    html += `</div>`;
  }
  // Run ratio per week
  html += '<div style="display:flex;gap:16px;margin-bottom:12px;flex-wrap:wrap">';
  weeks.forEach(w => { const ws = formatWeekStart(w); const r = calcRunRatio(p.id, ws); html += `<div style="background:var(--surface);border:1px solid var(--border);border-radius:4px;padding:6px 10px;font-size:.8rem"><strong>W${getWeekNumber(w)}</strong>: Run ${r.run}/${r.working} half-days</div>`; });
  html += '</div>';
  // Nav controls
  html += `<div style="display:flex;gap:8px;align-items:center;margin-bottom:12px;flex-wrap:wrap">
    <button onclick="scrollOffset--;savePrefs();API.reloadPlanning().then(render)">◀ Earlier</button>
    <span>Weeks ${formatWeekStart(weeks[0]).slice(5)} – ${formatWeekStart(weeks[weeks.length-1]).slice(5)}</span>
    <button onclick="scrollOffset++;savePrefs();API.reloadPlanning().then(render)">Later ▶</button>
    <button onclick="scrollOffset=0;savePrefs();API.reloadPlanning().then(render)" style="margin-left:8px">Today</button>
    <label style="display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${_showWeekend?'checked':''} onchange="_showWeekend=this.checked;savePrefs();render()" style="width:auto"> Weekends</label>
  </div>`;
  // Day cards (mobile-friendly, large tap targets)
  weeks.forEach(w => {
    const ws = formatWeekStart(w);
    html += `<div style="margin-bottom:12px"><strong>Week ${getWeekNumber(w)} — ${ws}</strong></div>`;
    const days = getWeekDays(w);
    const dayNames = ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'];
    days.forEach((d, di) => {
      if (di >= 5 && !_showWeekend) return;
      const ds = fmtDate(d);
      html += `<div style="background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:10px;margin-bottom:6px">
        <div style="font-weight:600;margin-bottom:6px;font-size:.9rem">${dayNames[di]} ${ds}</div>
        <div style="display:flex;gap:8px">`;
      ['am','pm'].forEach(slot => {
        const sd = getSlot(p.id, ds, slot);
        const cls = getSlotClass(sd);
        const label = getSlotLabel(sd);
        const click = canEdit() ? ` onclick="openUnifiedEditor([{personId:'${p.id}',date:'${ds}',slot:'${slot}'}])"` : '';
        html += `<div class="myweek-slot ${cls}" data-person="${p.id}" data-date="${ds}" data-slot="${slot}"${click} style="flex:1;padding:12px;border-radius:6px;text-align:center;font-size:1rem;cursor:${canEdit()?'pointer':'default'}">
          <div style="font-size:.7rem;color:var(--fg-muted);margin-bottom:4px">${slot.toUpperCase()}</div>
          <div>${escapeHtml(label)}</div>
        </div>`;
      });
      html += '</div></div>';
    });
  });
  container.innerHTML = html;
}

// ===== DRAG SELECTION =====
function startDrag(event, personId, date, slot, row, col) {
  event.preventDefault();
  dragState = { startRow: row, startCol: col, endRow: row, endCol: col, isDragging: false };
  document.addEventListener('mouseup', endDrag);
}
function extendDrag(event) {
  if (!dragState) return;
  const td = event.target.closest('td.half-day'); if (!td) return;
  const endRow = parseInt(td.dataset.row), endCol = parseInt(td.dataset.col);
  if (isNaN(endRow) || isNaN(endCol) || (endRow === dragState.endRow && endCol === dragState.endCol)) return;
  dragState.endRow = endRow; dragState.endCol = endCol; dragState.isDragging = true;
  const loRow = Math.min(dragState.startRow, endRow), hiRow = Math.max(dragState.startRow, endRow);
  const loCol = Math.min(dragState.startCol, endCol), hiCol = Math.max(dragState.startCol, endCol);
  document.querySelectorAll('.half-day.in-range').forEach(t => t.classList.remove('in-range'));
  document.querySelectorAll('td.half-day').forEach(t => { const r = parseInt(t.dataset.row), c = parseInt(t.dataset.col); if (!isNaN(r)&&!isNaN(c)&&r>=loRow&&r<=hiRow&&c>=loCol&&c<=hiCol) t.classList.add('in-range'); });
}
function clearDragHighlight() { document.querySelectorAll('.half-day.in-range').forEach(td => td.classList.remove('in-range')); }
function endDrag(event) {
  document.removeEventListener('mouseup', endDrag);
  if (!dragState) return;
  const wasDrag = dragState.isDragging;
  const loRow = Math.min(dragState.startRow, dragState.endRow), loCol = Math.min(dragState.startCol, dragState.endCol);
  const hiRow = Math.max(dragState.startRow, dragState.endRow), hiCol = Math.max(dragState.startCol, dragState.endCol);
  clearDragHighlight(); dragState = null;
  if (!wasDrag) { const td = document.querySelector(`td.half-day[data-row="${loRow}"][data-col="${loCol}"]`); if (td) openUnifiedEditor([{personId:td.dataset.person, date:td.dataset.date, slot:td.dataset.slot}]); return; }
  // Re-gather from the current DOM state
  const cells = [];
  document.querySelectorAll('td.half-day').forEach(td => { const r = parseInt(td.dataset.row), c = parseInt(td.dataset.col); if (!isNaN(r)&&!isNaN(c)&&r>=loRow&&r<=hiRow&&c>=loCol&&c<=hiCol) cells.push({personId:td.dataset.person, date:td.dataset.date, slot:td.dataset.slot}); });
  if (cells.length > 0) openUnifiedEditor(cells);
}
document.addEventListener('mousemove', extendDrag);

// ===== UNIFIED SLOT EDITOR (single cell + range) =====
// Replaces the old single-cell editor and the range editor. Opening it with
// one cell pre-fills that cell's data and shows its actual flag values; a
// multi-cell drag pre-fills from the first non-empty cell and defaults the
// run/remote/offsite flags to "don't change" (grey) so existing per-slot
// values are preserved unless explicitly set. Run/remote/offsite are
// tri-state toggles (grey=keep, ticked=set on, empty=set off).

function openUnifiedEditor(cells) {
  if (!cells || !cells.length) return;
  rangeEditorCells = cells;
  rangeProjCount = 1;
  rangeEditorPersonIds = [...new Set(cells.map(c => c.personId))];
  const first = cells[0], last = cells[cells.length - 1];
  const names = rangeEditorPersonIds.map(id => { const p = getPerson(id); return p ? p.name : id; });
  const nameText = names.length === 1 ? names[0] : `${names.length} people`;
  const isRange = cells.length > 1;

  // Pre-fill source: first non-empty cell in the selection (decision #2).
  let src = null;
  for (const c of cells) { const sd = getSlot(c.personId, c.date, c.slot); if (sd && sd.state !== 'not_filled') { src = sd; break; } }
  const hasData = cells.some(c => { const sd = getSlot(c.personId, c.date, c.slot); return sd && sd.state !== 'not_filled'; });

  // Determine the active tab from the source.
  let activeTab = 'project';
  if (src) {
    if (src.state === 'undetermined') activeTab = 'undetermined';
    else if (src.away) activeTab = 'away';
    else if (src.incident) activeTab = 'incident';
    else activeTab = 'project';
  }

  // Tri-state pre-fill. Grey ("don't change") is only meaningful when the
  // initial range is multi-cell AND has existing data for that flag; otherwise
  // the toggle is a plain on/off (two-state). Single-cell edits are always
  // two-state (preserving one value == that value).
  const hasFlag = f => cells.some(c => { const sd = getSlot(c.personId, c.date, c.slot); return sd && sd[f]; });
  const hasRun = hasFlag('run'), hasRemote = hasFlag('remote'), hasOffsite = hasFlag('offsite');
  function triInit(flag, has) {
    if (isRange) return has ? 'grey' : 'empty';      // range: grey only if data exists
    return (src && src[flag]) ? 'tick' : 'empty';     // single cell: actual value
  }
  const runTri0 = triInit('run', hasRun);
  const remoteTri0 = triInit('remote', hasRemote);
  const offsiteTri0 = triInit('offsite', hasOffsite);
  const allowGreyRun = isRange && hasRun;
  const allowGreyRemote = isRange && hasRemote;
  const allowGreyOffsite = isRange && hasOffsite;

  // Pre-fill tab-specific fields from the source.
  const srcProjects = (src && src.projects && src.projects.length) ? src.projects : [{ name: '', pct: 100 }];
  const srcAwayType = (src && src.away) ? src.away.type : 'vacation';
  const srcAwayNote = (src && src.away) ? (src.away.note || '') : '';
  const srcIncidentText = (src && src.incident) ? (src.incident.text || '') : '';
  const srcRunNote = (src && src.run_note) ? src.run_note : '';

  const awayTypes = ['vacation','public_holiday','sick_leave','training','conference','parental_leave','sabbatical','other'];

  let html = `<h2>Edit — ${escapeHtml(nameText)} <span style="font-size:.8rem;color:var(--fg-muted);font-weight:400">${cells.length} slot${cells.length>1?'s':''}</span></h2>`;
  // Range date/slot fields (editable so a single click can grow into a range).
  html += `<div style="display:flex;gap:12px;margin-bottom:8px"><div class="form-row" style="flex:1"><label>Start</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="range-start-date" value="${first.date}" style="flex:1" oninput="updateOverwriteWarning()"><input type="date" id="range-start-picker" style="width:36px" onchange="datePickerToText(this,'range-start-date');updateOverwriteWarning()"><select id="range-start-slot" style="width:60px" onchange="updateOverwriteWarning()"><option value="am" ${first.slot==='am'?'selected':''}>AM</option><option value="pm" ${first.slot==='pm'?'selected':''}>PM</option></select></div></div><div class="form-row" style="flex:1"><label>End</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="range-end-date" value="${last.date}" style="flex:1" oninput="updateOverwriteWarning()"><input type="date" id="range-end-picker" style="width:36px" onchange="datePickerToText(this,'range-end-date');updateOverwriteWarning()"><select id="range-end-slot" style="width:60px" onchange="updateOverwriteWarning()"><option value="am" ${last.slot==='am'?'selected':''}>AM</option><option value="pm" ${last.slot==='pm'?'selected':''}>PM</option></select></div></div></div>`;
  html += `<div id="uni-overwrite-warn" class="uni-warn empty"></div>`;
  // Tabs
  html += `<div class="tabs"><button class="${activeTab==='project'?'active':''}" data-tab="project" onclick="uniSwitchTab('project')">Project</button><button class="${activeTab==='away'?'active':''}" data-tab="away" onclick="uniSwitchTab('away')">Away</button><button class="${activeTab==='incident'?'active':''}" data-tab="incident" onclick="uniSwitchTab('incident')">Incident</button><button class="${activeTab==='undetermined'?'active':''}" data-tab="undetermined" onclick="uniSwitchTab('undetermined')">Undetermined</button><button class="${activeTab==='clear'?'active':''}" data-tab="clear" onclick="uniSwitchTab('clear')">Clear</button></div>`;
  // Project tab
  html += `<div id="uni-tab-project" class="tab-content ${activeTab!=='project'?'hidden':''}"><div id="range-project-list">`;
  srcProjects.forEach((proj, i) => { html += `<div class="project-row"><input type="text" value="${escapeHtml(proj.name)}" placeholder="Project name" list="project-names" id="range-proj-${i}"><input type="number" min="0" max="100" value="${proj.pct}" id="range-pct-${i}" style="width:60px" onchange="updateRangePctTotal()"><span>%</span>${srcProjects.length>1?`<button onclick="removeRangeProject(this.parentElement)" style="padding:2px 6px;font-size:.75rem">✕</button>`:''}</div>`; });
  rangeProjCount = srcProjects.length;
  html += `</div><button onclick="addRangeProject()" style="font-size:.8rem;margin-top:4px">+ Add Project</button><div class="pct-total" id="range-pct-total">Total: ${sum(srcProjects.map(p=>p.pct))}%</div>`;
  html += `<div style="margin-top:8px">${triToggleHtml('uni-run', '🏃 Run duty', runTri0, 'run', allowGreyRun)}</div>`;
  html += `<input type="text" id="uni-run-note" value="${escapeHtml(srcRunNote)}" maxlength="2000" placeholder="e.g. INC-1234 — heads-up for colleagues (applies to run slots)" ${runTri0==='tick'?'':'disabled'} style="margin-top:4px;width:100%;font-size:.8rem"></div>`;
  // Away tab
  html += `<div id="uni-tab-away" class="tab-content ${activeTab!=='away'?'hidden':''}"><div class="form-row"><label>Type</label><select id="uni-away-type">${awayTypes.map(t=>`<option value="${t}" ${srcAwayType===t?'selected':''}>${t.replace(/_/g,' ')}</option>`).join('')}</select></div><div class="form-row"><label>Note (optional)</label><input type="text" id="uni-away-note" value="${escapeHtml(srcAwayNote)}" placeholder="e.g. Family vacation"></div><p style="color:var(--fg-muted);font-size:.8rem;padding:4px 0">Mutually exclusive with project/run/incident.</p></div>`;
  // Incident tab
  html += `<div id="uni-tab-incident" class="tab-content ${activeTab!=='incident'?'hidden':''}"><div class="form-row"><label>Text (e.g. ticket number)</label><input type="text" id="uni-incident-text" value="${escapeHtml(srcIncidentText)}" placeholder="e.g. INC-1234"></div><p style="color:var(--fg-muted);font-size:.8rem;padding:4px 0">Mutually exclusive with project/run/away.</p></div>`;
  // Undetermined tab
  html += `<div id="uni-tab-undetermined" class="tab-content ${activeTab!=='undetermined'?'hidden':''}"><p style="color:var(--fg-muted);padding:8px 0">Mark the slot(s) as "Project (TBD)".</p></div>`;
  // Clear tab
  html += `<div id="uni-tab-clear" class="tab-content ${activeTab!=='clear'?'hidden':''}"><p style="color:var(--fg-muted);padding:8px 0">Clear the work state. Use the Remote/Off-site toggles below to keep a remote/off-site-only slot instead of deleting.</p></div>`;
  // Global remote/off-site tri-state (apply on every tab)
  html += `<div style="margin:10px 0;border-top:1px solid var(--border);padding-top:8px">${triToggleHtml('uni-remote', '🏠 Remote', remoteTri0, 'remote', allowGreyRemote)}<div style="height:6px"></div>${triToggleHtml('uni-offsite', '🏢 Off-site', offsiteTri0, 'offsite', allowGreyOffsite)}</div>`;
  html += `<div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="applyUnifiedEditor()">Apply</button></div>`;
  html += projectNamesDatalist();
  showModal(html);
  updateOverwriteWarning();
}

// Build the HTML for a tri-state flag toggle. state: 'grey' | 'tick' | 'empty'.
function triToggleHtml(id, label, state, flag, allowGrey) {
  const grey = allowGrey ? '1' : '0';
  const hint = state === 'grey' ? 'don\u2019t change' : state === 'tick' ? 'set on' : 'set off';
  return `<span class="tri" id="${id}" data-flag="${flag}" data-state="${state}" data-grey="${grey}" onclick="cycleTri('${id}')"><span class="tri-box"></span> ${label}<span class="tri-hint">(${hint})</span></span>`;
}

// Cycle a toggle. Three-state (grey -> tick -> empty -> grey) when grey is
// allowed, otherwise two-state (tick <-> empty).
function cycleTri(id) {
  const el = document.getElementById(id); if (!el) return;
  const allowGrey = el.dataset.grey === '1';
  const cur = el.dataset.state || 'empty';
  let next;
  if (allowGrey) { const order = ['grey', 'tick', 'empty']; next = order[(order.indexOf(cur) + 1) % 3]; }
  else { next = cur === 'tick' ? 'empty' : 'tick'; }
  el.dataset.state = next;
  const hint = next === 'grey' ? 'don\u2019t change' : next === 'tick' ? 'set on' : 'set off';
  const h = el.querySelector('.tri-hint'); if (h) h.textContent = `(${hint})`;
  // The run note field is only editable when run is set on.
  if (el.dataset.flag === 'run') { const rn = document.getElementById('uni-run-note'); if (rn) rn.disabled = (next !== 'tick'); }
}

function uniSwitchTab(tab) {
  document.querySelectorAll('#modal-content .tabs button').forEach(b => b.classList.toggle('active', b.dataset.tab === tab));
  ['project','away','incident','undetermined','clear'].forEach(t => { const tc = document.getElementById(`uni-tab-${t}`); if (tc) tc.classList.toggle('hidden', t !== tab); });
}

// Categorise an existing slot for the overwrite breakdown.
function uniCategorise(sd) {
  if (!sd || sd.state === 'not_filled') {
    if (sd && sd.remote) return 'remote-only';
    if (sd && sd.offsite) return 'offsite-only';
    return null;
  }
  if (sd.state === 'undetermined') return 'undetermined';
  if (sd.away) return 'away';
  if (sd.incident) return 'incident';
  if (sd.run && sd.projects && sd.projects.length) return 'project+run';
  if (sd.run) return 'run';
  if (sd.projects && sd.projects.length) return 'project';
  return 'other';
}

// Recompute the overwrite warning over the current range (live as the
// date/slot fields change).
function updateOverwriteWarning() {
  const el = document.getElementById('uni-overwrite-warn');
  if (!el) return;
  let cells; try { cells = getRangeEditorCells(); } catch (e) { cells = []; }
  // A single-slot edit overwrites that one slot implicitly; only warn for a
  // range (and recompute live as the date/slot fields change it into one).
  if (cells.length <= 1) { el.classList.add('empty'); el.innerHTML = ''; return; }
  const cats = {}; let total = 0;
  cells.forEach(c => { const cat = uniCategorise(getSlot(c.personId, c.date, c.slot)); if (cat) { total++; cats[cat] = (cats[cat] || 0) + 1; } });
  if (total === 0) { el.classList.add('empty'); el.innerHTML = ''; return; }
  el.classList.remove('empty');
  const parts = Object.entries(cats).map(([k, v]) => `${v} ${k}`);
  el.innerHTML = `⚠ Overwrites ${total} slot${total > 1 ? 's' : ''}: ${parts.join(', ')}`;
}

// resolve a tri-state against an existing boolean value.
function resolveTri(tri, existing) { return tri === 'tick' ? true : tri === 'empty' ? false : existing; }

async function applyUnifiedEditor() {
  const tab = (document.querySelector('#modal-content .tabs button.active')?.dataset.tab) || 'project';
  // Gather inputs.
  let projects = []; let total = 0;
  if (tab === 'project') {
    Array.from(document.getElementById('range-project-list').children).forEach(row => { const name = row.querySelector('input[type="text"]').value.trim(); const pct = +row.querySelector('input[type="number"]').value || 0; if (name) { projects.push({ name, pct }); total += pct; } });
    if (total > 100) { toast('Total percentage exceeds 100%', 'error'); return; }
  }
  const runTri = document.getElementById('uni-run')?.dataset.state || 'grey';
  const remoteTri = document.getElementById('uni-remote')?.dataset.state || 'grey';
  const offsiteTri = document.getElementById('uni-offsite')?.dataset.state || 'grey';
  const editorRunNote = (document.getElementById('uni-run-note')?.value || '').slice(0, 2000);
  const awayType = document.getElementById('uni-away-type')?.value || 'vacation';
  const awayNote = document.getElementById('uni-away-note')?.value || '';
  const incidentText = document.getElementById('uni-incident-text')?.value.trim() || '';

  const cells = getRangeEditorCells();
  pushUndo(cells.map(c => getSlotKey(c.personId, c.date, c.slot)));

  // Per-slot writes so "don't change" (grey) can preserve existing values.
  const writes = cells.map(c => {
    const ex = getSlot(c.personId, c.date, c.slot) || {};
    const exRun = !!ex.run, exRemote = !!ex.remote, exOffsite = !!ex.offsite, exRunNote = ex.run_note || '';
    // run tri-state is only meaningful on the project/clear tabs; on
    // away/incident/undetermined run is cleared (note preserved).
    const run = (tab === 'project' || tab === 'clear') ? resolveTri(runTri, exRun) : false;
    const remote = resolveTri(remoteTri, exRemote);
    const offsite = resolveTri(offsiteTri, exOffsite);
    // run note: tick -> editor value; otherwise preserve the slot's note.
    const runNote = (tab === 'project' || tab === 'clear') && runTri === 'tick' ? editorRunNote : exRunNote;

    let data;
    if (tab === 'away') {
      data = { state: 'filled', away: { type: awayType, note: awayNote }, incident: null, projects: [], run: false, run_note: exRunNote, remote, offsite };
    } else if (tab === 'incident') {
      data = { state: 'filled', away: null, incident: { text: incidentText }, projects: [], run: false, run_note: exRunNote, remote, offsite };
    } else if (tab === 'undetermined') {
      data = { state: 'undetermined', away: null, incident: null, projects: [], run: false, run_note: exRunNote, remote, offsite };
    } else if (tab === 'clear') {
      if (!run && !remote && !offsite) return API.del('/planning/slot', { person_id: c.personId, date: c.date, slot: c.slot });
      data = { state: 'filled', away: null, incident: null, projects: [], run, run_note: runNote, remote, offsite };
    } else { // project
      data = { state: (projects.length > 0 || run || remote || offsite) ? 'filled' : 'not_filled', away: null, incident: null, projects, run, run_note: runNote, remote, offsite };
    }
    return API.put('/planning/slot', { person_id: c.personId, date: c.date, slot: c.slot, data });
  });
  await Promise.all(writes);
  await API.reloadPlanning();
  queueFlash(cells, 'self');
  rangeEditorCells = null;
  closeModal();
  render();
}

// ===== shared range helpers (kept) =====
function addRangeProject() { const list = document.getElementById('range-project-list'); const idx = rangeProjCount++; const div = document.createElement('div'); div.className = 'project-row'; div.innerHTML = `<input type="text" id="range-proj-${idx}" placeholder="Project name" list="project-names"><input type="number" min="0" max="100" value="50" id="range-pct-${idx}" style="width:60px" onchange="updateRangePctTotal()"><span>%</span><button onclick="removeRangeProject(this.parentElement)" style="padding:2px 6px;font-size:.75rem">✕</button>`; list.appendChild(div); updateRangePctTotal(); }
function removeRangeProject(row) { const list = document.getElementById('range-project-list'); if (list.children.length <= 1) return; row.remove(); updateRangePctTotal(); }
function updateRangePctTotal() { const list = document.getElementById('range-project-list'); if (!list) return; let total = 0; Array.from(list.children).forEach(row => { const i = row.querySelector('input[type="number"]'); if (i) total += +i.value || 0; }); const el = document.getElementById('range-pct-total'); if (el) { el.textContent = `Total: ${total}%`; el.classList.toggle('over', total > 100); } }
function isWeekend(dateStr) { const d = parseDate(dateStr); if (!d) return false; return ((d.getDay() + 6) % 7) >= 5; } // Mon=0..Sun=6
function getRangeEditorCells() { const sd = document.getElementById('range-start-date').value.trim(); const ss = document.getElementById('range-start-slot').value; const ed = document.getElementById('range-end-date').value.trim(); const es = document.getElementById('range-end-slot').value; const slots = generateSlotsInRange(sd, ss, ed, es).filter(sl => _showWeekend || !isWeekend(sl.date)); const cells = []; rangeEditorPersonIds.forEach(pid => slots.forEach(sl => cells.push({personId: pid, date: sl.date, slot: sl.slot}))); return cells; }
function generateSlotsInRange(startDate, startSlot, endDate, endSlot) { const result = []; const start = parseDate(startDate), end = parseDate(endDate); if (!start || !end) return result; let cd = new Date(start), cs = startSlot; while (cd < end || (cd.getTime() === end.getTime() && cs <= endSlot)) { result.push({date: fmtDate(cd), slot: cs}); if (cs === 'am') cs = 'pm'; else { cs = 'am'; cd = new Date(cd); cd.setDate(cd.getDate() + 1); } } return result; }

// ===== MODAL SYSTEM =====
function showModal(html) { document.getElementById('modal-content').innerHTML = html; document.getElementById('overlay').classList.remove('hidden'); }
function closeModal() { document.getElementById('overlay').classList.add('hidden'); }

// ===== EMOJI PICKER =====
const EMOJI_SET = ['😀','😃','😄','😁','😆','😅','🤣','😂','🙂','🙃','😉','😊','😇','🥰','😍','🤩','😘','😗','☺️','😚','😙','🥲','😋','😛','😜','🤪','😝','🤑','🤗','🤭','🤫','🤔','🤐','🤨','😐','😑','😶','😏','😒','🙄','😬','🤥','😌','😔','😪','🤤','😴','😷','🤒','🤕','🤢','🤮','🤧','🥵','🥶','🥴','😵','🤯','🤠','🥳','😎','🤓','🧐','😕','😟','🙁','😮','😯','😲','😳','🥺','😦','😧','😨','😰','😥','😢','😭','😱','😖','😣','😞','😓','😩','😫','🥱','😤','😡','😠','🤬','😈','👿','💀','👻','🤖','👽','👾','🤡','👹','👺','🐱','🐶','🦊','🐻','🐼','🐨','🐯','🦁','🐮','🐷','🐸','🐵','🙈','🙉','🙊','🐒','🐔','🐧','🐦','🐤','🦆','🦅','🦉','🐺','🦝','🐮','🐃','🐂','🐄','🦌','🐎','🦄','🐉','🐲','🦕','🦖','🐢','🐊','🦎','🐍','🐙','🦑','🦐','🦞','🦀','🐠','🐟','🐬','🐳','🐋','🦈','🦋','🐌','🐝','🐞','🐜','🦗','🕷️','🦂','🦟','🦠','💐','🌸','💮','🏵️','🌹','🌺','🌻','🌼','🌷','🌱','🌲','🌳','🌴','🌵','🌾','🍀','🍃','🍂','🍁','🌍','🌎','🌏','🌕','🌙','⭐','🌟','✨','⚡','🔥','💥','☀️','⛅','☁️','🌧️','⛈️','❄️','☃️','🌈','🌊','💧','🎨','🎭','🎬','🎤','🎧','🎼','🎹','🥁','🎷','🎺','🎸','🪕','🎻','🎲','🎯','🎳','🎮','🎰','🧩','♟️','⚽','🏀','🏈','⚾','🎾','🏐','🏉','🥏','🎱','🏓','🏸','🥊','🥋','🥅','🏒','🏑','🥌','🛷','⛸️','🪂','🏆','🥇','🥈','🥉','🏅','🎖️','🎪','🚀','🛸','🚁','✈️','🛩️','🚂','🚃','🚄','🚅','🚆','🚇','🚈','🚊','🚉','🚝','🚲','🛵','🏍️','🚗','🚕','🚙','🚌','🚎','🏎️','🚓','🚑','🚒','🚐','🚚','🚛','🛴','🚢','⛵','🚤','🛶','⚓','🚧','🏠','🏡','🏢','🏣','🏤','🏥','🏦','🏨','🏩','🏫','🏬','🏭','🏯','🏰','🏗️','🔧','🔨','🛠️','⚒️','🔩','⚙️','🧰','🧲','🔬','🔭','📡','🧪','🧫','🧬','💻','🖥️','⌨️','🖱️','💽','💾','💿','📀','📱','☎️','📞','📟','📠','🔋','🔌','💡','🔦','🕯️','📔','📕','📖','📚','📓','📒','📃','📜','📰','📑','🔖','🏷️','✏️','🖊️','🖌️','🖍️','📝','✅','❌','⚠️','🔔','📣','📢','💬','💭','🗯️','👁️','🧠','🦴','💪','🦾','🦿','🦵','🦶','👂','🦻','👃','🦷','🤝','👋','🙌','👏','👍','👎','👊','✊','🤛','🤜','🤞','✌️','🤟','🤘','👌','🤌','🤏','👈','👉','👆','👇','☝️','✋','🤚','🖐️','🖖','👋','🤙','💪','☕','🍵','🍶','🍾','🍷','🍸','🍹','🍺','🍻','🥂','🥃','🥤','🧃','🧉','🧊','🥢','🍽️','🍴','🥄','🔪','🍳','🍲','🥘','🍿','🧂','🥫','🍱','🍘','🍚','🍛','🍜','🍝','🍠','🍢','🍣','🍤','🍥','🥮','🍡','🥟','🥠','🥡','🍦','🍧','🍨','🍩','🍪','🎂','🍰','🧁','🥧','🍫','🍬','🍭','🍮','🍯','🍎','🍏','🍐','🍊','🍋','🍌','🍉','🍇','🍓','🫐','🍈','🍒','🍑','🥭','🍍','🥥','🥝','🍅','🍆','🥑','🥦','🥬','🥒','🌶️','🫑','🌽','🥕','🧄','🧅','🥔','🍠','🥐','🥯','🍞','🥖','🧀','🥚','🧇','🥞','🧈','🔑','🗝️','🚪','🛏️','🛋️','🪑','🚽','🚿','🛁','🧴','🧷','🧹','🧺','🧻','🧼','🧽','🛒','⚰️','⚱️','🗿','🏧','🚮','🚇','🚷','🚯','🚳','🚱','🔞','📵','🚭','🚫','✳️','❇️','❓','❕','❗','‼️','⁉️','🔅','🔆','〽️','⚠️','🚸','🔱','⚜️','🔰','♻️','✅'];
function randomEmoji() { return EMOJI_SET[Math.floor(Math.random() * EMOJI_SET.length)]; }
function toggleEmojiPicker(inputId, btn) {
  let picker = document.getElementById('emoji-picker'); if (picker) { picker.remove(); return; }
  picker = document.createElement('div'); picker.id = 'emoji-picker';
  picker.style.cssText = 'position:fixed;z-index:300;background:var(--bg);border:1px solid var(--border);border-radius:8px;padding:8px;box-shadow:0 4px 24px rgba(0,0,0,.3);max-width:380px;max-height:340px;overflow-y:auto;display:grid;grid-template-columns:repeat(10,1fr);gap:3px';
  // Die — the standout first item: picks an emoji at random.
  const die = document.createElement('button');
  die.textContent = '🎲'; die.title = 'Pick a random emoji';
  die.style.cssText = 'font-size:1.5rem;padding:6px;border:2px solid var(--accent);background:var(--accent);color:#fff;cursor:pointer;border-radius:6px;grid-column:span 2;font-weight:700;line-height:1';
  die.onmouseenter = () => die.style.filter = 'brightness(1.1)';
  die.onmouseleave = () => die.style.filter = 'none';
  die.onclick = (e) => { e.preventDefault(); document.getElementById(inputId).value = randomEmoji(); picker.remove(); };
  picker.appendChild(die);
  EMOJI_SET.forEach(em => { const b = document.createElement('button'); b.textContent = em; b.title = em; b.style.cssText = 'font-size:1.2rem;padding:2px;border:none;background:transparent;cursor:pointer;border-radius:4px'; b.onmouseenter = () => b.style.background = 'var(--surface-hover)'; b.onmouseleave = () => b.style.background = 'transparent'; b.onclick = (e) => { e.preventDefault(); document.getElementById(inputId).value = em; picker.remove(); }; picker.appendChild(b); });
  const rect = btn.getBoundingClientRect(); picker.style.top = (rect.bottom + 4) + 'px'; picker.style.left = rect.left + 'px'; document.body.appendChild(picker);
  setTimeout(() => { const close = (ev) => { if (!picker.contains(ev.target) && ev.target !== btn) { picker.remove(); document.removeEventListener('click', close); } }; document.addEventListener('click', close); }, 0);
}

// ===== ADD/EDIT PERSON MODAL =====
function showAddPersonModal(isGuest) {
  showModal(`<h2>${isGuest?'Add Guest':'Add Team Member'}</h2>
    <div class="form-row"><label>Name *</label><input type="text" id="person-name" placeholder="Full name"></div>
    <div class="form-row"><label>Role / Title</label><input type="text" id="person-role" placeholder="e.g. Backend Dev"></div>
    <div class="form-row"><label>Sub-team</label><input type="text" id="person-team" placeholder="e.g. Platform"></div>
    <div class="form-row"><label>Emoji avatar</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="person-emoji" value="👤" maxlength="4" style="width:60px"><button type="button" onclick="toggleEmojiPicker('person-emoji',this)" style="padding:4px 8px">😀</button></div></div>
    <div class="form-row"><label>Default projects (comma separated)</label><input type="text" id="person-projects" placeholder="Atlas, Beacon"></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="submitAddPerson(${isGuest})">Add</button></div>`);
}
async function submitAddPerson(isGuest) {
  const name = document.getElementById('person-name').value.trim();
  if (!name) { toast('Name is required', 'error'); return; }
  const projects = document.getElementById('person-projects').value.split(',').map(s => s.trim()).filter(Boolean);
  const person = await API.post('/people', { name, role: document.getElementById('person-role').value.trim(), sub_team: document.getElementById('person-team').value.trim(), avatar_emoji: document.getElementById('person-emoji').value.trim() || '👤', default_projects: projects, is_guest: !!isGuest, status: 'active', archived_date: '' });
  if (!State.people.find(x => x.id === person.id)) State.people.push(person); closeModal(); render();
}
function showEditPersonModal(personId) {
  const p = getPerson(personId); if (!p) return;
  showModal(`<h2>Edit ${p.name}</h2>
    <div class="form-row"><label>Name</label><input type="text" id="edit-name" value="${p.name}"></div>
    <div class="form-row"><label>Role / Title</label><input type="text" id="edit-role" value="${p.role||''}"></div>
    <div class="form-row"><label>Sub-team</label><input type="text" id="edit-team" value="${p.sub_team||''}"></div>
    <div class="form-row"><label>Emoji avatar</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="edit-emoji" value="${p.avatar_emoji}" maxlength="4" style="width:60px"><button type="button" onclick="toggleEmojiPicker('edit-emoji',this)" style="padding:4px 8px">😀</button></div></div>
    <div class="form-row"><label>Default projects (comma separated)</label><input type="text" id="edit-projects" value="${(p.default_projects||[]).join(', ')}"></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="submitEditPerson('${personId}')">Save</button></div>`);
}
async function submitEditPerson(personId) {
  const name = document.getElementById('edit-name').value.trim();
  if (!name) { toast('Name is required', 'error'); return; }
  const projects = document.getElementById('edit-projects').value.split(',').map(s => s.trim()).filter(Boolean);
  const p = getPerson(personId);
  const updated = Object.assign({}, p, { name, role: document.getElementById('edit-role').value.trim(), sub_team: document.getElementById('edit-team').value.trim(), avatar_emoji: document.getElementById('edit-emoji').value.trim() || '👤', default_projects: projects });
  await API.put(`/people/${personId}`, updated);
  const i = State.people.findIndex(x => x.id === personId); if (i >= 0) State.people[i] = updated;
  closeModal(); render();
}

// ===== ADD/EDIT PROJECT MODAL =====

function teamLeadOptions(selectedId) {
  let opts = '<option value="">— None —</option>';
  State.people.filter(p => p.status === 'active').forEach(p => {
    opts += `<option value="${p.id}" ${selectedId === p.id ? 'selected' : ''}>${p.is_guest ? 'Guest: ' : ''}${esc(p.name)}</option>`;
  });
  return opts;
}

function showAddProjectModal() {
  showModal(`<h2>Add Project</h2>
    <div class="form-row"><label>Name *</label><input type="text" id="proj-name" placeholder="e.g. Atlas"></div>
    <div class="form-row"><label>Emoji</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="proj-emoji" value="📁" maxlength="4" style="width:60px"><button type="button" onclick="toggleEmojiPicker('proj-emoji',this)" style="padding:4px 8px">😀</button></div></div>
    <div class="form-row"><label>Description</label><textarea id="proj-desc" rows="2" placeholder="Short description"></textarea></div>
    <div class="form-row"><label>URL</label><input type="text" id="proj-url" placeholder="https://..."></div>
    <div style="display:flex;gap:12px"><div class="form-row" style="flex:1"><label>Start date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="proj-start" placeholder="2025/01/06" style="flex:1"><input type="date" id="proj-start-picker" style="width:36px" onchange="datePickerToText(this,'proj-start')"></div></div><div class="form-row" style="flex:1"><label>End date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="proj-end" placeholder="2025/03/28" style="flex:1"><input type="date" id="proj-end-picker" style="width:36px" onchange="datePickerToText(this,'proj-end')"></div></div></div>
    <div class="form-row"><label>Status</label><select id="proj-status">${PROJECT_STATUSES.map(s=>`<option value="${s}">${STATUS_LABELS[s]}</option>`).join('')}</select></div>
    <div class="form-row"><label>Team lead</label><select id="proj-lead">${teamLeadOptions('')}</select></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="submitAddProject()">Add</button></div>`);
}
async function submitAddProject() {
  const name = document.getElementById('proj-name').value.trim();
  if (!name) { toast('Name is required', 'error'); return; }
  if (getProjectByName(name)) { toast('Project already exists', 'error'); return; }
  const proj = await API.post('/projects', { name, emoji: document.getElementById('proj-emoji').value.trim() || '📁', description: document.getElementById('proj-desc').value.trim(), url: document.getElementById('proj-url').value.trim(), start_date: document.getElementById('proj-start').value.trim(), end_date: document.getElementById('proj-end').value.trim(), status: document.getElementById('proj-status').value, team_lead: document.getElementById('proj-lead').value });
  if (!State.projects.find(x => x.id === proj.id)) State.projects.push(proj); closeModal(); render();
}
function showEditProjectModal(id) {
  const proj = getProject(id); if (!proj) return;
  showModal(`<h2>Edit ${proj.name}</h2>
    <div class="form-row"><label>Name *</label><input type="text" id="edit-proj-name" value="${proj.name}"></div>
    <div class="form-row"><label>Emoji</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="edit-proj-emoji" value="${proj.emoji}" maxlength="4" style="width:60px"><button type="button" onclick="toggleEmojiPicker('edit-proj-emoji',this)" style="padding:4px 8px">😀</button></div></div>
    <div class="form-row"><label>Description</label><textarea id="edit-proj-desc" rows="2">${proj.description||''}</textarea></div>
    <div class="form-row"><label>URL</label><input type="text" id="edit-proj-url" value="${proj.url||''}"></div>
    <div style="display:flex;gap:12px"><div class="form-row" style="flex:1"><label>Start date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="edit-proj-start" value="${proj.start_date||''}" style="flex:1"><input type="date" id="edit-proj-start-picker" style="width:36px" onchange="datePickerToText(this,'edit-proj-start')"></div></div><div class="form-row" style="flex:1"><label>End date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="edit-proj-end" value="${proj.end_date||''}" style="flex:1"><input type="date" id="edit-proj-end-picker" style="width:36px" onchange="datePickerToText(this,'edit-proj-end')"></div></div></div>
    <div class="form-row"><label>Status</label><select id="edit-proj-status">${PROJECT_STATUSES.map(s=>`<option value="${s}" ${proj.status===s?'selected':''}>${STATUS_LABELS[s]}</option>`).join('')}</select></div>
    <div class="form-row"><label>Team lead</label><select id="edit-proj-lead">${teamLeadOptions(proj.team_lead)}</select></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="submitEditProject('${id}')">Save</button></div>`);
}
async function submitEditProject(id) {
  const name = document.getElementById('edit-proj-name').value.trim();
  if (!name) { toast('Name is required', 'error'); return; }
  const updated = { name, emoji: document.getElementById('edit-proj-emoji').value.trim() || '📁', description: document.getElementById('edit-proj-desc').value.trim(), url: document.getElementById('edit-proj-url').value.trim(), start_date: document.getElementById('edit-proj-start').value.trim(), end_date: document.getElementById('edit-proj-end').value.trim(), status: document.getElementById('edit-proj-status').value, team_lead: document.getElementById('edit-proj-lead').value };
  await API.put(`/projects/${id}`, updated);
  const i = State.projects.findIndex(p => p.id === id); if (i >= 0) State.projects[i] = Object.assign(State.projects[i], updated);
  closeModal(); render();
}

// ===== PROJECT CSV IMPORT =====
function showProjectImportModal() {
  const csvFormat = 'name,emoji,description,url,start_date,end_date,status\nAtlas,🚀,Platform migration,https://wiki.example.com/atlas,2025/01/06,2025/03/28,in_progress';
  showModal(`<h2>Import Projects (CSV)</h2><p style="color:var(--fg-muted);margin-bottom:12px;font-size:.85rem">CSV: name,emoji,description,url,start_date,end_date,status</p>
    <div class="form-row"><label>Expected format</label><textarea id="csv-format" rows="4" readonly style="font-family:var(--font-mono);font-size:.75rem;color:var(--fg-muted)">${csvFormat}</textarea></div>
    <div class="form-row"><label>Or paste CSV content</label><textarea id="project-csv-paste" rows="6" placeholder="name,emoji,description,..."></textarea></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="doProjectCSVImport()">Import</button></div>`);
}
async function doProjectCSVImport() {
  const csvText = document.getElementById('project-csv-paste').value.trim();
  if (!csvText) { toast('Please paste CSV content', 'error'); return; }
  const res = await fetch('/api/projects/import-csv', { method: 'POST', headers: { 'Authorization': `Bearer ${State.token}`, 'Content-Type': 'text/csv' }, body: csvText });
  const result = await res.json();
  await API.get('/projects').then(d => { State.projects = d || []; });
  closeModal(); render();
  alert(`Import: ${result.created} created, ${result.updated} updated`);
}

// ===== IMPORT/EXPORT TOML =====
function showImportModal() {
  showModal(`<h2>Import TOML</h2><p style="color:var(--fg-muted);margin-bottom:12px">Choose mode and file.</p>
    <div class="form-row"><label>Import mode</label><select id="import-mode"><option value="merge">Merge</option><option value="replace">Replace</option></select></div>
    <div class="form-row"><label>Select file</label><input type="file" id="import-file" accept=".toml"></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="doImport()">Import</button></div>`);
}
async function doImport() {
  const file = document.getElementById('import-file').files[0]; if (!file) { toast('Please select a file', 'error'); return; }
  const mode = document.getElementById('import-mode').value;
  const text = await file.text();
  const res = await fetch(`/api/import?mode=${mode}`, { method: 'POST', headers: { 'Authorization': `Bearer ${State.token}`, 'Content-Type': 'text/toml' }, body: text });
  const result = await res.json();
  if (res.ok) { await API.loadAll(); closeModal(); render(); alert(`Import: ${result.created} created, ${result.updated} updated`); }
  else { alert('Import error: ' + (result.error || 'unknown')); }
}
async function doExport() {
  const res = await fetch('/api/export', { headers: { 'Authorization': `Bearer ${State.token}` } });
  const blob = await res.blob();
  const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url;
  const cd = res.headers.get('Content-Disposition') || ''; const m = cd.match(/filename="?([^"]+)"?/);
  a.download = m ? m[1] : 'export.toml'; document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
}

// ===== ICS EXPORT =====
async function downloadICS(personId) {
  const weeks = getVisibleWeeks();
  const startDate = formatWeekStart(weeks[0]); const endDate = formatWeekStart(weeks[weeks.length-1]);
  const p = getPerson(personId); const name = p ? p.name.replace(/[^a-zA-Z0-9]/g, '_') : personId;
  // Generate ICS client-side from State
  let events = [];
  for (let d = new Date(parseDate(startDate)); d <= parseDate(endDate); d.setDate(d.getDate() + 1)) {
    const ds = fmtDate(d);
    ['am','pm'].forEach((slot, si) => { const sd = getSlot(personId, ds, slot); if (!sd || sd.state === 'not_filled') return;
      const hs = si === 0 ? 9 : 14; const dts = new Date(d); dts.setHours(hs,0,0,0); const dte = new Date(d); dte.setHours(hs+4,0,0,0);
      const fmtICS = d => `${d.getFullYear()}${pad2(d.getMonth()+1)}${pad2(d.getDate())}T${pad2(d.getHours())}${pad2(d.getMinutes())}00`;
      const esc = s => String(s||'').replace(/\\/g,'\\\\').replace(/,/g,'\\,').replace(/;/g,'\\;').replace(/\n/g,'\\n');
      if (sd.state === 'undetermined') { events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Project: undetermined')}\r\nEND:VEVENT\r\n`); return; }
      if (sd.away) { events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Away: '+sd.away.type)}\r\nDESCRIPTION:${esc(sd.away.note||'')}\r\nCATEGORIES:${esc(sd.away.type)}\r\nEND:VEVENT\r\n`); return; }
      if (sd.incident) { events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Incident'+(sd.incident.text?': '+sd.incident.text:'')+(sd.remote?' 🏠':sd.offsite?' 🏢':''))}\r\nCATEGORIES:incident\r\nEND:VEVENT\r\n`); return; }
      if (sd.projects && sd.projects.length > 0) sd.projects.forEach(proj => { events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Project: '+proj.name+(sd.remote?' 🏠':sd.offsite?' 🏢':''))}\r\nDESCRIPTION:${esc(proj.name+' ('+proj.pct+'%)'+(sd.run?' + Run':'')+(sd.remote?' + Remote':sd.offsite?' + Off-site':''))}\r\nCATEGORIES:project\r\nEND:VEVENT\r\n`); });
      else if (sd.run) events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Run duty'+(sd.remote?' 🏠':sd.offsite?' 🏢':''))}\r\n${sd.run_note?`DESCRIPTION:${icsEscape(sd.run_note)}\r\n`:''}CATEGORIES:run\r\nEND:VEVENT\r\n`);
    });
  }
  let ics = 'BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//TeamVisualizer//V1//EN\r\n' + events.join('') + 'END:VCALENDAR\r\n';
  const blob = new Blob([ics], { type: 'text/calendar;charset=utf-8' });
  const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = `${name}_planning.ics`; document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
}

// ===== COPY LAST WEEK =====
async function copyLastWeek(personId) {
  const weeks = getVisibleWeeks(); if (weeks.length < 2) { toast('Need at least 2 weeks visible', 'error'); return; }
  const fromWS = formatWeekStart(addWeeks(weeks[0], -1)); const toWS = formatWeekStart(weeks[0]);
  if (!confirm("Copy last week's assignments? Away entries skipped.")) return;
  const toKeys = getWeekDays(parseDate(toWS)).filter((d,di)=>di<5).flatMap(d => ['am','pm'].map(s => getSlotKey(personId, fmtDate(d), s)));
  pushUndo(toKeys);
  const result = await API.post('/planning/copy-week', { person_id: personId, from_week_start: fromWS, to_week_start: toWS });
  await API.reloadPlanning(); render();
  if (result.copied > 0) { const s = document.getElementById('status-left'); if (s) { s.textContent = `📋 Copied ${result.copied} slots`; setTimeout(updateStatusBar, 2000); } }
  else alert('Nothing copied (all filled or last week empty)');
}

// ===== HELP =====
function showHelp() { showModal(`<h2>Keyboard Shortcuts</h2><div class="help-shortcuts"><span><kbd>Tab</kbd></span><span>Move between cells</span><span><kbd>Enter</kbd></span><span>Open cell editor</span><span><kbd>←</kbd></span><span>Move timeframe back (Team / Availability)</span><span><kbd>→</kbd></span><span>Move timeframe forward (Team / Availability)</span><span><kbd>U</kbd></span><span>Mark undetermined</span><span><kbd>R</kbd></span><span>Toggle run</span><span><kbd>Ctrl+Z</kbd></span><span>Undo</span><span><kbd>Ctrl+Shift+Z</kbd></span><span>Redo</span><span><kbd>Escape</kbd></span><span>Close editor / modal</span></div><div class="form-actions"><button onclick="closeModal()">Close</button></div>`); }

// ===== KEYBOARD SHORTCUTS =====
function modalOpen() {
  const ov = document.getElementById('overlay');
  return ov && !ov.classList.contains('hidden');
}
document.addEventListener('keydown', (e) => {
  const tag = (e.target.tagName || '').toLowerCase();
  if (tag === 'input' || tag === 'select' || tag === 'textarea') return;
  if (e.ctrlKey && e.key === 'z') { e.preventDefault(); undo(); }
  if ((e.ctrlKey && e.key === 'Z') || (e.ctrlKey && e.key === 'y')) { e.preventDefault(); redo(); }
  if (e.key === 'Escape') { closeModal(); }
  if (e.key === 'u' && !e.ctrlKey && modalOpen()) uniSwitchTab('undetermined');
  if (e.key === 'r' && !e.ctrlKey && modalOpen() && document.getElementById('uni-run')) { cycleTri('uni-run'); }
  // Arrow keys move the timeframe in the team grid & availability views
  if (modalOpen()) return;
  if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
    const dir = e.key === 'ArrowLeft' ? -1 : 1;
    if (currentView === 'team') { scrollOffset += dir; savePrefs(); API.reloadPlanning().then(render); e.preventDefault(); }
    else if (currentView === 'availability') { availabilityDayOffset += dir; savePrefs(); render(); e.preventDefault(); }
  }
});

// ===== NAVIGATION =====
function initNav() {
  $$('.nav-btn[data-view]').forEach(btn => btn.addEventListener('click', () => { currentView = btn.dataset.view; currentPersonId = null; savePrefs(); render(); }));
  document.getElementById('overlay').addEventListener('click', (e) => { if (e.target === e.currentTarget) closeModal(); });
  if (canEdit()) { document.getElementById('btn-undo').addEventListener('click', undo); document.getElementById('btn-redo').addEventListener('click', redo); }
  else { document.getElementById('btn-undo').style.display = 'none'; document.getElementById('btn-redo').style.display = 'none'; }
  document.getElementById('btn-logout').addEventListener('click', logout);
  document.getElementById('btn-export').addEventListener('click', doExport);
  document.getElementById('btn-help').addEventListener('click', showHelp);
}

// ===== CUSTOM CELL TOOLTIP (colored emoji) =====
let _tooltipCell = null;
function showCellTooltip(td) {
  const tooltip = document.getElementById('cell-tooltip');
  if (!tooltip) return;
  const personId = td.dataset.person, date = td.dataset.date, slot = td.dataset.slot;
  if (!personId || !date || !slot) { hideCellTooltip(); return; }
  const sd = getSlot(personId, date, slot);
  const person = getPerson(personId);
  tooltip.innerHTML = getSlotTitleHtml(person, date, slot, sd);
  tooltip.classList.remove('hidden');
  // Position below the cell, clamped to viewport
  const rect = td.getBoundingClientRect();
  const ttRect = tooltip.getBoundingClientRect();
  let left = rect.left + rect.width / 2 - ttRect.width / 2;
  let top = rect.bottom + 4;
  if (left < 4) left = 4;
  if (left + ttRect.width > window.innerWidth - 4) left = window.innerWidth - ttRect.width - 4;
  if (top + ttRect.height > window.innerHeight - 4) top = rect.top - ttRect.height - 4;
  if (top < 4) top = 4;
  tooltip.style.left = left + 'px';
  tooltip.style.top = top + 'px';
}
function hideCellTooltip() {
  const tooltip = document.getElementById('cell-tooltip');
  if (tooltip) tooltip.classList.add('hidden');
  _tooltipCell = null;
}
function initCellTooltips() {
  document.addEventListener('mouseover', (e) => {
    const td = e.target.closest('td.half-day');
    if (td === _tooltipCell) return;
    _tooltipCell = td;
    if (td) showCellTooltip(td);
    else hideCellTooltip();
  });
  document.addEventListener('mouseleave', hideCellTooltip);
}

// ===== TOAST NOTIFICATIONS =====
function toast(message, type = 'info', duration = 3000) {
  const container = document.getElementById('toast-container');
  if (!container) return;
  const t = document.createElement('div');
  t.className = `toast ${type}`;
  const icons = { error: '❌', success: '✅', info: 'ℹ️' };
  t.innerHTML = `<span>${icons[type] || ''}</span><span>${escapeHtml(message)}</span>`;
  container.appendChild(t);
  setTimeout(() => { t.classList.add('fade-out'); setTimeout(() => t.remove(), 300); }, duration);
}

// ===== SAVING INDICATOR =====
let saveTimer = null;
function showSaving() {
  let el = document.getElementById('saving-indicator');
  if (!el) { el = document.createElement('div'); el.id = 'saving-indicator'; el.className = 'saving-indicator'; el.textContent = 'Saving…'; document.body.appendChild(el); }
  el.classList.add('visible');
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => el.classList.remove('visible'), 1000);
}

// ===== WS STATUS INDICATOR =====
function updateWSStatus(status) {
  const el = document.getElementById('ws-status');
  if (!el) return;
  const map = { connected: ['ws-connected', 'Connected'], reconnecting: ['ws-reconnecting', 'Reconnecting…'], disconnected: ['ws-disconnected', 'Disconnected'] };
  const [cls, title] = map[status] || ['ws-disconnected', 'Unknown'];
  el.className = cls;
  el.title = title;
}

// ===== LOGOUT (switch user) =====
function logout() {
  // Clears the app session cookie and redirects to Keycloak's end_session
  // endpoint. The Go app handles both: it clears the teamviz_token cookie
  // and 302-redirects to Keycloak. Keycloak shows a one-click "confirm
  // logout" page (no id_token_hint), then redirects back to the app root.
  window.location.href = '/auth/logout';
}

// ===== INIT =====
async function init() {
  try {
    const sessionRes = await fetch('/api/auth/session');
    if (!sessionRes.ok) { window.location.href = '/auth/login'; return; }
    const session = await sessionRes.json();
    State.user = session.user; State.token = session.token;
    document.getElementById('topbar').classList.remove('hidden');
    document.getElementById('user-info').textContent = `${State.user.username} (${State.user.role})`;
    await API.loadAll();
    State.theme = localStorage.getItem('teamviz_theme') || State.settings.theme || 'dracula';
    loadPrefs();
    // Fetch my person mapping
    try {
      const meRes = await API.get('/me/person');
      State.myPersonId = meRes.person_id || '';
    } catch(e) { State.myPersonId = ''; }
    initNav();
    initCellTooltips();
    WS.connect();
    updateWSStatus('reconnecting');
    render();
    console.log('Team Visualizer loaded — user:', State.user.username, 'role:', State.user.role);
  } catch (err) {
    document.getElementById('main').innerHTML = '<div style="text-align:center;padding:60px;color:var(--fg-muted)"><h1>Error</h1><p>' + escapeHtml(err.message) + '</p></div>';
    console.error('Init error:', err);
  }
}

init();