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
  scrollOffset: 0, undoStack: null, undoing: false, theme: null,
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
    if (res.status === 401) { State.token = null; location.reload(); throw new Error('Unauthorized'); }
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
      case 'planning_updated': { if (State.undoing) break; const e = msg.data; State.planning[getSlotKey(e.person_id, e.date, e.slot)] = e.data; break; }
      case 'planning_cleared': { if (State.undoing) break; const e = msg.data; delete State.planning[getSlotKey(e.person_id, e.date, e.slot)]; break; }
      case 'planning_range': case 'planning_range_cleared': API.reloadPlanning().then(render); return;
      case 'planning_copied': case 'planning_pruned': case 'data_reset': case 'data_imported': API.reloadPlanning().then(render); return;
      case 'project_added': if (!State.projects.find(p => p.id === msg.data.id)) State.projects.push(msg.data); break;
      case 'project_updated': { const i = State.projects.findIndex(p => p.id === msg.data.id); if (i >= 0) State.projects[i] = msg.data; break; }
      case 'project_deleted': State.projects = State.projects.filter(p => p.id !== msg.data.id); break;
      case 'oncall_changed': { API.get(`/oncall?start=${formatWeekStart(getVisibleWeeks()[0])}&end=${windowEndDate(getVisibleWeeks())}`).then(d => { State.oncall = d || {}; render(); }); return; }
      case 'rotation_changed': { API.get(`/rotation?start=${formatWeekStart(getVisibleWeeks()[0])}&end=${windowEndDate(getVisibleWeeks())}`).then(d => { State.rotation = d || {}; render(); }); return; }
      case 'settings_updated': State.settings = Object.assign({}, State.settings, msg.data); break;
      case 'holidays_imported': API.get('/holidays').then(d => { State.holidays = d || []; render(); }); return;
    }
    render();
  },
};

// ===== UNDO (single-level, client-side) =====
// Single-level undo for planning edits. pushUndo() snapshots the whole
// planning map before a change; undo() diffs that snapshot against the
// current state and writes each changed slot back to the server — so the
// revert is real (persists across refresh / other clients), not just visual.
function pushUndo(keys) {
  State.undoStack = {};
  (keys && keys.length ? keys : Object.keys(State.planning)).forEach(k => State.undoStack[k] = State.planning[k] ? clone(State.planning[k]) : null);
  const b = $('btn-undo'); if (b) b.disabled = false;
}
async function undo() {
  if (!State.undoStack) return;
  const snap = State.undoStack; State.undoStack = null;
  const b = $('btn-undo'); if (b) b.disabled = true;
  // Suppress WS planning echoes while reverting, so a racing echo of the
  // edit being undone can't re-apply it over our authoritative reload.
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
    if (writes.length === 0) return;
    await Promise.all(writes);
    await API.reloadPlanning(); // sync local state to the authoritative server state
  } finally {
    State.undoing = false;
  }
  render();
}

// ===== SLOT HELPERS =====
function getSlotClass(slotData) {
  if (!slotData || slotData.state === 'not_filled') return 'not-filled';
  if (slotData.state === 'undetermined') return 'undetermined';
  if (slotData.away) return 'away';
  if (slotData.run && slotData.projects && slotData.projects.length > 0) return 'project run';
  if (slotData.run) return 'run';
  if (slotData.projects && slotData.projects.length > 0) return 'project';
  return 'not-filled';
}
function getCellLabel(slotData) {
  if (!slotData || slotData.state === 'not_filled') return '?';
  if (slotData.state === 'undetermined') return '?';
  if (slotData.away) { const a = {vacation:'Vac',public_holiday:'Hol',sick_leave:'Sick',training:'Train',conference:'Conf',parental_leave:'Par',sabbatical:'Sab',other:'Other'}; return a[slotData.away.type] || slotData.away.type; }
  let parts = [];
  if (slotData.projects) slotData.projects.forEach(p => parts.push(p.name));
  if (slotData.run) parts.push('R');
  return parts.join('+');
}
function getMergeKey(slotData, weekIndex) {
  const wk = 'w' + weekIndex + ':';
  if (!slotData || slotData.state === 'not_filled') return wk + 'nf';
  if (slotData.state === 'undetermined') return wk + 'und';
  if (slotData.away) return wk + 'away:' + slotData.away.type;
  if (slotData.projects && slotData.projects.length === 1 && !slotData.run) return wk + 'proj:' + slotData.projects[0].name;
  if (slotData.projects && slotData.projects.length === 1 && slotData.run) return wk + 'projrun:' + slotData.projects[0].name;
  if (slotData.run && (!slotData.projects || slotData.projects.length === 0)) return wk + 'run';
  return wk + 'complex:' + JSON.stringify(slotData.projects) + ':' + (slotData.run ? '1' : '0');
}
function getSlotTitle(person, dateStr, slot, slotData) {
  const p = typeof person === 'string' ? getPerson(person) : person;
  const name = p ? p.name : person;
  if (!slotData || slotData.state === 'not_filled') return `${name} · ${dateStr} ${slot} · Not filled`;
  if (slotData.state === 'undetermined') return `${name} · ${dateStr} ${slot} · Undetermined project`;
  if (slotData.away) return `${name} · ${dateStr} ${slot} · Away: ${slotData.away.type}${slotData.away.note ? ' ('+slotData.away.note+')' : ''}`;
  let parts = [];
  if (slotData.projects) slotData.projects.forEach(p => parts.push(`${p.name} ${p.pct}%`));
  if (slotData.run) parts.push('Run');
  return `${name} · ${dateStr} ${slot} · ${parts.join(' + ')}`;
}
function getSlotLabel(slotData) {
  if (!slotData || slotData.state === 'not_filled') return '—';
  if (slotData.state === 'undetermined') return '?';
  if (slotData.away) return slotData.away.type.replace(/_/g,' ');
  let parts = [];
  if (slotData.projects) slotData.projects.forEach(p => parts.push(p.name));
  if (slotData.run) parts.push('🏃');
  return parts.join(' ') || '—';
}

// ===== ON-CALL / ROTATION =====
function isOnCall(personId, weekStart) { return !!(State.oncall[weekStart] || []).includes(personId); }
function isRunPerson(personId, weekStart) { return !!(State.rotation[weekStart] || []).includes(personId); }
function getRunPeople(weekStart) { return getActivePeople().filter(p => isRunPerson(p.id, weekStart)); }
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
let editorPersonId = null, editorDate = null, editorSlot = null;
let _showWeekend = false;

function getVisibleWeeks() {
  const n = State.settings.window_weeks || 4;
  const ws = getWeekStart(new Date()); const weeks = [];
  for (let i = 0; i < n; i++) weeks.push(addWeeks(ws, i + scrollOffset));
  return weeks;
}

// ===== RENDER ENGINE =====
function render() {
  document.documentElement.setAttribute('data-theme', State.theme || State.settings.theme || 'dracula');
  const main = document.getElementById('main');
  $$('.nav-btn[data-view]').forEach(btn => btn.classList.toggle('active', btn.dataset.view === currentView));
  // Role-based UI: the Admin tab is only visible to admins.
  $$('.nav-btn[data-view="admin"]').forEach(b => b.style.display = isAdmin() ? '' : 'none');
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
    case 'individual': renderIndividual(main); break;
    default: renderTeamGrid(main);
  }
}

function updateStatusBar() {
  const left = document.getElementById('status-left');
  const right = document.getElementById('status-right');
  left.textContent = `${getActivePeople().length} team · ${getActiveGuests().length} guests · ${getArchivedPeople().length} archived`;
  right.textContent = `Week ${getWeekNumber(new Date())} · ${fmtDate(new Date())}`;
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
    days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const sep = (di === 0 && wi > 0) ? ' week-start' : ''; html += `<th colspan="2" class="day-header${sep}">${dayNames[di]} <span class="day-num">/${d.getDate()}</span></th>`; });
  });
  html += '</tr></thead><tbody>';
  // Build allSlots
  const allSlots = [];
  weeks.forEach((w, wi) => { const days = getWeekDays(w); days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const ds = fmtDate(d); ['am','pm'].forEach(slot => allSlots.push({date:ds, slot, weekIdx:wi, dayIdx:di})); }); });
  // Sort + group. Guests are always grouped together at the end; under a
  // "Guests" sub-team when grouping by sub-team.
  let peopleSorted = people.slice();
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
    html += `<tr><td class="person-col" onclick="showIndividual('${p.id}')" title="${esc(p.role||'')} · ${esc(p.sub_team||'')}">${esc(p.avatar_emoji)} ${esc(p.name)}`;
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
      html += `<td class="half-day ${cls}${sep}${mergeCls}${onCallCls}" data-person="${p.id}" data-date="${sl.date}" data-slot="${sl.slot}" data-row="${rowIdx}" data-col="${colIdx}"${onmousedown} title="${escapeHtml(getSlotTitle(p, sl.date, sl.slot, sd))}"${bgStyle}>${showLabel ? `<span class="cell-label">${escapeHtml(label)}</span>` : ''}</td>`;
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
  html += `<div style="display:flex;gap:8px;align-items:center;margin-top:8px">
    <button onclick="scrollOffset--;API.reloadPlanning().then(render)">◀ Earlier</button>
    <span>Weeks ${formatWeekStart(weeks[0]).slice(5)} – ${formatWeekStart(weeks[weeks.length-1]).slice(5)}</span>
    <button onclick="scrollOffset++;API.reloadPlanning().then(render)">Later ▶</button>
    <button onclick="scrollOffset=0;API.reloadPlanning().then(render)" style="margin-left:8px">Today</button>
    <span style="margin-left:12px;color:var(--fg-muted)">Group by:</span>
    <button onclick="teamGroupBy='name';render()" style="font-weight:${teamGroupBy==='name'?'700':'400'}">Name</button>
    <button onclick="teamGroupBy='sub_team';render()" style="font-weight:${teamGroupBy==='sub_team'?'700':'400'}">Sub-team</button>
    <label style="margin-left:12px;display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${_showWeekend?'checked':''} onchange="_showWeekend=this.checked;render()" style="width:auto"> Weekends</label>
    <label style="display:flex;align-items:center;gap:4px;cursor:pointer;font-size:.85rem"><input type="checkbox" ${teamShowGuests?'checked':''} onchange="teamShowGuests=this.checked;render()" style="width:auto"> Guests</label>
  </div>`;
  // Legend
  html += `<div style="display:flex;gap:12px;margin-top:8px;font-size:.75rem;flex-wrap:wrap">
    <span><span class="half-day away" style="display:inline-block;width:12px;height:12px"></span> Away</span>
    <span><span class="half-day project" style="display:inline-block;width:12px;height:12px"></span> Project</span>
    <span><span class="half-day run" style="display:inline-block;width:12px;height:12px"></span> Run</span>
    <span><span class="half-day undetermined" style="display:inline-block;width:12px;height:12px"></span> Undetermined</span>
    <span><span class="half-day not-filled" style="display:inline-block;width:12px;height:12px"></span> Not filled</span>
  </div>`;
  container.innerHTML = html;
}

// ===== INDIVIDUAL VIEW =====
function showIndividual(personId) { currentPersonId = personId; currentView = 'individual'; render(); }
function renderIndividual(container) {
  const p = getPerson(currentPersonId);
  if (!p) { currentView = 'team'; render(); return; }
  const weeks = getVisibleWeeks();
  const wsStart = formatWeekStart(weeks[0]), wsEnd = formatWeekStart(weeks[weeks.length-1]);
  let html = `<div class="indiv-header"><div class="person-info"><div class="avatar" style="background:${p.avatar_color}20;color:${p.avatar_color}">${esc(p.avatar_emoji)}</div><div><h2>${esc(p.name)}</h2><span style="color:var(--fg-muted);font-size:.85rem">${esc(p.role||'')}${p.sub_team?' · '+esc(p.sub_team):''}${p.is_guest?' · Guest':''}</span></div></div><div class="spacer" style="flex:1"></div><button onclick="downloadICS('${p.id}')" title="Period: ${wsStart} → ${wsEnd}">📅 Export ICS</button>`;
  if (canEdit()) html += `<button onclick="copyLastWeek('${p.id}')">📋 Copy Last Week</button>`;
  html += `<button onclick="currentView='team';render()">← Back</button></div>`;
  html += '<div style="display:flex;gap:16px;margin-bottom:12px;flex-wrap:wrap">';
  weeks.forEach(w => { const ws = formatWeekStart(w); const r = calcRunRatio(p.id, ws); html += `<div style="background:var(--surface);border:1px solid var(--border);border-radius:4px;padding:6px 10px;font-size:.8rem"><strong>W${getWeekNumber(w)}</strong>: Run ${r.run}/${r.working} half-days</div>`; });
  html += '</div>';
  html += '<table class="indiv-grid"><thead><tr><th>Date</th><th>Day</th><th>AM</th><th>PM</th></tr></thead><tbody>';
  weeks.forEach(w => { const days = getWeekDays(w); const dayNames = ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'];
    days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; const ds = fmtDate(d);
      html += `<tr><td>${ds}</td><td>${dayNames[di]}</td>`;
      ['am','pm'].forEach(slot => { const sd = getSlot(p.id, ds, slot); const cls = getSlotClass(sd); const label = getSlotLabel(sd);
        const click = canEdit() ? ` onclick="openCellEditor('${p.id}','${ds}','${slot}')"` : '';
        html += `<td class="slot-cell ${cls}"${click}>${label}</td>`; });
      html += '</tr>';
    });
  });
  html += '</tbody></table>';
  container.innerHTML = html;
}

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
      html += `<div style="font-size:.8rem;margin-top:4px">On run: ${runPeople.length > 0 ? runPeople.map(p=>esc(p.name)).join(', ') : 'None'}</div>`;
      if (canEdit()) html += `<button onclick="showRotationModal('${ws}')">Assign Run Person</button>`;
    } else {
      const people = getActivePeople();
      const runPeople = people.filter(p => calcRunRatio(p.id, ws).run > 0).map(p => `${esc(p.avatar_emoji)} ${esc(p.name)} (${calcRunRatio(p.id, ws).run}h)`);
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
  let html = `<h2>Where is everyone — ${dayName} ${selDate}</h2><div style="display:flex;gap:8px;align-items:center;margin-bottom:12px">
    <button onclick="availabilityDayOffset--;render()">◀ Previous day</button><button onclick="availabilityDayOffset=0;render()">Today</button><button onclick="availabilityDayOffset++;render()">Next day ▶</button></div>`;
  html += '<div class="avail-grid">';
  people.forEach(p => {
    const amSd = getSlot(p.id, selDate, 'am'), pmSd = getSlot(p.id, selDate, 'pm');
    const amCls = getSlotClass(amSd), pmCls = getSlotClass(pmSd);
    const amBg = amCls.includes('project') && projectColorBg(amSd) ? ` style="background:${projectColorBg(amSd)}"` : '';
    const pmBg = pmCls.includes('project') && projectColorBg(pmSd) ? ` style="background:${projectColorBg(pmSd)}"` : '';
    function detail(sd) { if (!sd || sd.state==='not_filled') return 'Available'; if (sd.state==='undetermined') return 'Project (TBD)'; if (sd.away) return `Away: ${sd.away.type.replace(/_/g,' ')}`; const pn = sd.projects ? sd.projects.map(p=>p.name).join(', ') : ''; return sd.run ? `${pn} + Run` : (pn||'Available'); }
    const amHl = isToday && currentSlot === 'am' ? ';outline:2px solid var(--accent)' : '';
    const pmHl = isToday && currentSlot === 'pm' ? ';outline:2px solid var(--accent)' : '';
    html += `<div class="avail-card"><div class="name">${p.avatar_emoji} ${p.name}</div><div style="font-size:.75rem;color:var(--fg-muted);margin-bottom:4px">${p.role||''}</div><div style="display:flex;gap:6px;margin-top:4px"><div style="flex:1;min-width:0"><div style="font-size:.7rem;color:var(--fg-muted);margin-bottom:2px">AM${isToday&&currentSlot==='am'?' ●':''}</div><div class="status ${amCls}"${amBg} style="${amBg?'':amHl}">${escapeHtml(detail(amSd))}</div></div><div style="flex:1;min-width:0"><div style="font-size:.7rem;color:var(--fg-muted);margin-bottom:2px">PM${isToday&&currentSlot==='pm'?' ●':''}</div><div class="status ${pmCls}"${pmBg} style="${pmBg?'':pmHl}">${escapeHtml(detail(pmSd))}</div></div></div></div>`;
  });
  html += '</div>';
  let away=0,run=0,proj=0,nf=0,und=0;
  people.forEach(p => ['am','pm'].forEach(slot => { const sd = getSlot(p.id, selDate, slot); if (!sd||sd.state==='not_filled') nf++; else if (sd.state==='undetermined') und++; else if (sd.away) away++; else if (sd.run) run++; else proj++; }));
  html += `<h3 style="margin-top:20px">Day Summary — ${dayName} ${selDate}</h3><div style="display:flex;gap:12px;flex-wrap:wrap;font-size:.85rem"><span style="color:var(--slot-away)">${away} away</span><span style="color:var(--slot-run)">${run} run</span><span style="color:var(--slot-project)">${proj} project</span><span style="color:var(--fg-muted)">${und} undetermined</span><span style="color:var(--fg-muted)">${nf} unassigned</span></div>`;
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
    days.forEach((d, di) => { if (di >= 5 && !_showWeekend) return; html += `<th colspan="2" class="day-header${di===0&&wi>0?' week-start':''}">${dayNames[di]} <span class="day-num">/${d.getDate()}</span></th>`; });
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
      html += `<td class="half-day ${cls}${sep}${mergeCls}" data-person="${p.id}" data-date="${sl.date}" data-slot="${sl.slot}" data-row="${rowIdx}" data-col="${colIdx}"${onmousedown} title="${escapeHtml(getSlotTitle(p, sl.date, sl.slot, sd))}"${bgStyle}>${showLabel?`<span class="cell-label">${escapeHtml(label)}</span>`:''}</td>`;
      prevMergeKey = mk;
    });
    html += '</tr>';
  });
  html += `</tbody></table></div><div style="display:flex;gap:8px;align-items:center;margin-top:8px"><button onclick="scrollOffset--;API.reloadPlanning().then(render)">◀ Earlier</button><button onclick="scrollOffset=0;API.reloadPlanning().then(render)">Today</button><button onclick="scrollOffset++;API.reloadPlanning().then(render)">Later ▶</button></div>`;
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
    html += `</span></div>${proj.description?`<div class="proj-desc">${escapeHtml(proj.description)}</div>`:''}<div class="proj-meta">${proj.start_date?`<span>📅 ${proj.start_date}</span>`:''}${proj.end_date?`<span>→ ${proj.end_date}</span>`:''}${urlOk?`<a href="${escapeHtml(proj.url)}" target="_blank" style="font-size:.8rem">🔗 Link</a>`:proj.url?`<span style="font-size:.8rem;color:var(--fg-muted)">${escapeHtml(proj.url)}</span>`:''}</div><div class="proj-people">👥 ${escapeHtml(peopleNames)}</div></div>`;
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
  if (!wasDrag) { const td = document.querySelector(`td.half-day[data-row="${loRow}"][data-col="${loCol}"]`); if (td) openCellEditor(td.dataset.person, td.dataset.date, td.dataset.slot); return; }
  // Re-gather from the current DOM state
  const cells = [];
  document.querySelectorAll('td.half-day').forEach(td => { const r = parseInt(td.dataset.row), c = parseInt(td.dataset.col); if (!isNaN(r)&&!isNaN(c)&&r>=loRow&&r<=hiRow&&c>=loCol&&c<=hiCol) cells.push({personId:td.dataset.person, date:td.dataset.date, slot:td.dataset.slot}); });
  if (cells.length > 0) openRangeEditor(cells);
}
document.addEventListener('mousemove', extendDrag);

// ===== CELL EDITOR =====
function openCellEditor(personId, date, slot) {
  editorPersonId = personId; editorDate = date; editorSlot = slot;
  const slotData = getSlot(personId, date, slot) || { state: 'not_filled', away: null, projects: [], run: false };
  const p = getPerson(personId); const name = p ? p.name : personId;
  const isAway = slotData && slotData.away; const isUndetermined = slotData && slotData.state === 'undetermined';
  const projects = (slotData && slotData.projects) ? clone(slotData.projects) : [{ name: '', pct: 100 }];
  const runChecked = slotData && slotData.run;
  let html = `<div style="font-weight:600;margin-bottom:8px">${name} · ${date} ${slot.toUpperCase()}</div>
  <div class="tabs"><button class="active" data-tab="project" onclick="switchEditorTab('project')">Project</button><button data-tab="away" onclick="switchEditorTab('away')">Away</button><button data-tab="undetermined" onclick="setUndetermined()">Undetermined</button><button data-tab="clear" onclick="clearSlot()">Clear</button></div>`;
  html += `<div id="editor-tab-project" class="tab-content ${isAway||isUndetermined?'hidden':''}"><div id="project-list">`;
  projects.forEach((proj, i) => { html += `<div class="project-row"><input type="text" value="${proj.name}" placeholder="Project name" list="project-names" onchange="updatePctTotal()"><input type="number" min="0" max="100" value="${proj.pct}" onchange="updatePctTotal()"><span>%</span>${projects.length>1?`<button onclick="removeProject(${i})">✕</button>`:''}</div>`; });
  html += `</div><button onclick="addEditorProject()" style="font-size:.8rem;margin-top:4px">+ Add Project</button><div class="pct-total" id="pct-total">Total: ${sum(projects.map(p=>p.pct))}%</div><div style="margin-top:8px"><label style="display:flex;align-items:center;gap:6px;cursor:pointer"><input type="checkbox" ${runChecked?'checked':''} onchange="window.editorRunToggle=this.checked"> 🏃 Run duty</label></div></div>`;
  const awayTypes = ['vacation','public_holiday','sick_leave','training','conference','parental_leave','sabbatical','other'];
  const currentAway = isAway ? slotData.away.type : ''; const currentNote = isAway ? (slotData.away.note || '') : '';
  html += `<div id="editor-tab-away" class="tab-content ${isAway?'':'hidden'}"><div class="form-row"><label>Type</label><select id="away-type">${awayTypes.map(t=>`<option value="${t}" ${currentAway===t?'selected':''}>${t.replace(/_/g,' ')}</option>`).join('')}</select></div><div class="form-row"><label>Note (optional)</label><input type="text" id="away-note" value="${currentNote}" placeholder="e.g. Family vacation"></div></div>`;
  html += `<div class="form-actions"><button onclick="closeCellEditor()">Cancel</button><button onclick="saveCellEditor()" class="primary">Save</button></div>`;
  html += projectNamesDatalist();
  const editor = document.getElementById('cell-editor');
  editor.innerHTML = html; editor.classList.remove('hidden');
  editor.style.top = '50%'; editor.style.left = '50%'; editor.style.transform = 'translate(-50%,-50%)';
  window.editorRunToggle = runChecked;
}
function switchEditorTab(tab) { const editor = document.getElementById('cell-editor'); editor.querySelectorAll('.tabs button').forEach(b => b.classList.remove('active')); editor.querySelectorAll('.tab-content').forEach(c => c.classList.add('hidden')); const btn = editor.querySelector(`.tabs button[data-tab="${tab}"]`); if (btn) btn.classList.add('active'); const tc = document.getElementById(`editor-tab-${tab}`); if (tc) tc.classList.remove('hidden'); }
function addEditorProject() { const list = document.getElementById('project-list'); const div = document.createElement('div'); div.className = 'project-row'; div.innerHTML = `<input type="text" value="" placeholder="Project name" list="project-names" onchange="updatePctTotal()"><input type="number" min="0" max="100" value="50" onchange="updatePctTotal()"><span>%</span><button onclick="removeProject(Array.from(this.parentElement.parentElement.children).indexOf(this.parentElement))">✕</button>`; list.appendChild(div); updatePctTotal(); }
function removeProject(idx) { const rows = document.getElementById('project-list').children; if (rows.length <= 1) return; rows[idx].remove(); updatePctTotal(); }
function updatePctTotal() { const rows = document.getElementById('project-list').children; let total = 0; Array.from(rows).forEach(row => { const i = row.querySelector('input[type="number"]'); if (i) total += +i.value || 0; }); const el = document.getElementById('pct-total'); if (el) { el.textContent = `Total: ${total}%`; el.classList.toggle('over', total > 100); } }
async function saveCellEditor() {
  const awayTab = document.getElementById('editor-tab-away');
  if (awayTab && !awayTab.classList.contains('hidden')) {
    const type = document.getElementById('away-type').value; const note = document.getElementById('away-note').value;
    const data = { state: 'filled', away: { type, note }, projects: [], run: false };
    pushUndo([getSlotKey(editorPersonId, editorDate, editorSlot)]);
    await API.put('/planning/slot', { person_id: editorPersonId, date: editorDate, slot: editorSlot, data });
    State.planning[getSlotKey(editorPersonId, editorDate, editorSlot)] = data;
  } else {
    const rows = document.getElementById('project-list').children; const projects = []; let total = 0;
    Array.from(rows).forEach(row => { const name = row.querySelector('input[type="text"]').value.trim(); const pct = +row.querySelector('input[type="number"]').value || 0; if (name) { projects.push({ name, pct }); total += pct; } });
    if (total > 100) { toast('Total percentage exceeds 100%', 'error'); return; }
    pushUndo([getSlotKey(editorPersonId, editorDate, editorSlot)]);
    const data = { state: projects.length > 0 || window.editorRunToggle ? 'filled' : 'not_filled', away: null, projects, run: !!window.editorRunToggle };
    await API.put('/planning/slot', { person_id: editorPersonId, date: editorDate, slot: editorSlot, data });
    State.planning[getSlotKey(editorPersonId, editorDate, editorSlot)] = data;
  }
  closeCellEditor(); render();
}
async function setUndetermined() { pushUndo([getSlotKey(editorPersonId, editorDate, editorSlot)]); const data = { state: 'undetermined', away: null, projects: [], run: false }; await API.put('/planning/slot', { person_id: editorPersonId, date: editorDate, slot: editorSlot, data }); State.planning[getSlotKey(editorPersonId, editorDate, editorSlot)] = data; closeCellEditor(); render(); }
async function clearSlot() { pushUndo([getSlotKey(editorPersonId, editorDate, editorSlot)]); await API.del('/planning/slot', { person_id: editorPersonId, date: editorDate, slot: editorSlot }); delete State.planning[getSlotKey(editorPersonId, editorDate, editorSlot)]; closeCellEditor(); render(); }
function closeCellEditor() { document.getElementById('cell-editor').classList.add('hidden'); editorPersonId = null; editorDate = null; editorSlot = null; }

// ===== RANGE EDITOR =====
function openRangeEditor(cells) {
  rangeEditorCells = cells; rangeProjCount = 1;
  rangeEditorPersonIds = [...new Set(cells.map(c => c.personId))];
  const first = cells[0], last = cells[cells.length-1];
  const names = rangeEditorPersonIds.map(id => { const p = getPerson(id); return p ? p.name : id; });
  const nameText = names.length === 1 ? names[0] : `${names.length} people`;
  let html = `<h2>Edit Range — ${escapeHtml(nameText)}</h2>
    <div style="display:flex;gap:12px;margin-bottom:12px"><div class="form-row" style="flex:1"><label>Start date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="range-start-date" value="${first.date}" style="flex:1"><input type="date" id="range-start-picker" style="width:36px" onchange="datePickerToText(this,'range-start-date')"><select id="range-start-slot" style="width:60px"><option value="am" ${first.slot==='am'?'selected':''}>AM</option><option value="pm" ${first.slot==='pm'?'selected':''}>PM</option></select></div></div><div class="form-row" style="flex:1"><label>End date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="range-end-date" value="${last.date}" style="flex:1"><input type="date" id="range-end-picker" style="width:36px" onchange="datePickerToText(this,'range-end-date')"><select id="range-end-slot" style="width:60px"><option value="am" ${last.slot==='am'?'selected':''}>AM</option><option value="pm" ${last.slot==='pm'?'selected':''}>PM</option></select></div></div></div>
    <div id="range-project-list"><div class="project-row"><input type="text" id="range-proj-0" placeholder="Project name" list="project-names"><input type="number" min="0" max="100" value="100" id="range-pct-0" style="width:60px" onchange="updateRangePctTotal()"><span>%</span></div></div>
    <button onclick="addRangeProject()" style="font-size:.8rem;margin-top:4px">+ Add Project</button>
    <div class="pct-total" id="range-pct-total">Total: 100%</div>
    <div style="margin-top:8px"><label style="display:flex;align-items:center;gap:6px;cursor:pointer"><input type="checkbox" id="range-run" style="width:auto"> 🏃 Run duty</label></div>
    <div style="margin:12px 0;border-top:1px solid var(--border)"></div>
    <div class="form-row"><label>Or set Away type:</label><select id="range-away-type"><option value="">— None —</option><option value="vacation">Vacation</option><option value="public_holiday">Public holiday</option><option value="sick_leave">Sick leave</option><option value="training">Training</option><option value="conference">Conference</option><option value="parental_leave">Parental leave</option><option value="sabbatical">Sabbatical</option><option value="other">Other</option></select></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button onclick="applyRangeUndetermined()">Set Undetermined</button><button onclick="applyRangeClear()">Clear All</button><button class="primary" onclick="applyRangeEditor()">Apply to All</button></div>`;
  html += projectNamesDatalist();
  showModal(html);
}
function addRangeProject() { const list = document.getElementById('range-project-list'); const idx = rangeProjCount++; const div = document.createElement('div'); div.className = 'project-row'; div.innerHTML = `<input type="text" id="range-proj-${idx}" placeholder="Project name" list="project-names"><input type="number" min="0" max="100" value="50" id="range-pct-${idx}" style="width:60px" onchange="updateRangePctTotal()"><span>%</span><button onclick="removeRangeProject(this.parentElement)" style="padding:2px 6px;font-size:.75rem">✕</button>`; list.appendChild(div); updateRangePctTotal(); }
function removeRangeProject(row) { const list = document.getElementById('range-project-list'); if (list.children.length <= 1) return; row.remove(); updateRangePctTotal(); }
function updateRangePctTotal() { const list = document.getElementById('range-project-list'); if (!list) return; let total = 0; Array.from(list.children).forEach(row => { const i = row.querySelector('input[type="number"]'); if (i) total += +i.value || 0; }); const el = document.getElementById('range-pct-total'); if (el) { el.textContent = `Total: ${total}%`; el.classList.toggle('over', total > 100); } }
function getRangeEditorCells() { const sd = document.getElementById('range-start-date').value.trim(); const ss = document.getElementById('range-start-slot').value; const ed = document.getElementById('range-end-date').value.trim(); const es = document.getElementById('range-end-slot').value; const slots = generateSlotsInRange(sd, ss, ed, es); const cells = []; rangeEditorPersonIds.forEach(pid => slots.forEach(sl => cells.push({personId: pid, date: sl.date, slot: sl.slot}))); return cells; }
function generateSlotsInRange(startDate, startSlot, endDate, endSlot) { const result = []; const start = parseDate(startDate), end = parseDate(endDate); if (!start || !end) return result; let cd = new Date(start), cs = startSlot; while (cd < end || (cd.getTime() === end.getTime() && cs <= endSlot)) { result.push({date: fmtDate(cd), slot: cs}); if (cs === 'am') cs = 'pm'; else { cs = 'am'; cd = new Date(cd); cd.setDate(cd.getDate() + 1); } } return result; }
async function applyRangeEditor() { const awayType = document.getElementById('range-away-type').value; const run = document.getElementById('range-run').checked; const projects = []; let total = 0; Array.from(document.getElementById('range-project-list').children).forEach(row => { const name = row.querySelector('input[type="text"]').value.trim(); const pct = +row.querySelector('input[type="number"]').value || 0; if (name) { projects.push({name, pct}); total += pct; } }); if (total > 100) { toast('Total percentage exceeds 100%', 'error'); return; } const cells = getRangeEditorCells(); pushUndo(cells.map(c => getSlotKey(c.personId, c.date, c.slot))); const data = awayType ? {state:'filled',away:{type:awayType,note:''},projects:[],run:false} : projects.length > 0 ? {state:'filled',away:null,projects,run} : run ? {state:'filled',away:null,projects:[],run:true} : {state:'not_filled',away:null,projects:[],run:false}; await API.put('/planning/range', { person_ids: rangeEditorPersonIds, start_date: document.getElementById('range-start-date').value, start_slot: document.getElementById('range-start-slot').value, end_date: document.getElementById('range-end-date').value, end_slot: document.getElementById('range-end-slot').value, data }); await API.reloadPlanning(); rangeEditorCells = null; closeModal(); render(); }
async function applyRangeUndetermined() { const cells = getRangeEditorCells(); pushUndo(cells.map(c => getSlotKey(c.personId, c.date, c.slot))); await API.put('/planning/range', { person_ids: rangeEditorPersonIds, start_date: document.getElementById('range-start-date').value, start_slot: document.getElementById('range-start-slot').value, end_date: document.getElementById('range-end-date').value, end_slot: document.getElementById('range-end-slot').value, data: {state:'undetermined',away:null,projects:[],run:false} }); await API.reloadPlanning(); rangeEditorCells = null; closeModal(); render(); }
async function applyRangeClear() { const cells = getRangeEditorCells(); pushUndo(cells.map(c => getSlotKey(c.personId, c.date, c.slot))); await API.del('/planning/range', { person_ids: rangeEditorPersonIds, start_date: document.getElementById('range-start-date').value, start_slot: document.getElementById('range-start-slot').value, end_date: document.getElementById('range-end-date').value, end_slot: document.getElementById('range-end-slot').value }); await API.reloadPlanning(); rangeEditorCells = null; closeModal(); render(); }

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
function showAddProjectModal() {
  showModal(`<h2>Add Project</h2>
    <div class="form-row"><label>Name *</label><input type="text" id="proj-name" placeholder="e.g. Atlas"></div>
    <div class="form-row"><label>Emoji</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="proj-emoji" value="📁" maxlength="4" style="width:60px"><button type="button" onclick="toggleEmojiPicker('proj-emoji',this)" style="padding:4px 8px">😀</button></div></div>
    <div class="form-row"><label>Description</label><textarea id="proj-desc" rows="2" placeholder="Short description"></textarea></div>
    <div class="form-row"><label>URL</label><input type="text" id="proj-url" placeholder="https://..."></div>
    <div style="display:flex;gap:12px"><div class="form-row" style="flex:1"><label>Start date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="proj-start" placeholder="2025/01/06" style="flex:1"><input type="date" id="proj-start-picker" style="width:36px" onchange="datePickerToText(this,'proj-start')"></div></div><div class="form-row" style="flex:1"><label>End date</label><div style="display:flex;gap:4px;align-items:center"><input type="text" id="proj-end" placeholder="2025/03/28" style="flex:1"><input type="date" id="proj-end-picker" style="width:36px" onchange="datePickerToText(this,'proj-end')"></div></div></div>
    <div class="form-row"><label>Status</label><select id="proj-status">${PROJECT_STATUSES.map(s=>`<option value="${s}">${STATUS_LABELS[s]}</option>`).join('')}</select></div>
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="submitAddProject()">Add</button></div>`);
}
async function submitAddProject() {
  const name = document.getElementById('proj-name').value.trim();
  if (!name) { toast('Name is required', 'error'); return; }
  if (getProjectByName(name)) { toast('Project already exists', 'error'); return; }
  const proj = await API.post('/projects', { name, emoji: document.getElementById('proj-emoji').value.trim() || '📁', description: document.getElementById('proj-desc').value.trim(), url: document.getElementById('proj-url').value.trim(), start_date: document.getElementById('proj-start').value.trim(), end_date: document.getElementById('proj-end').value.trim(), status: document.getElementById('proj-status').value });
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
    <div class="form-actions"><button onclick="closeModal()">Cancel</button><button class="primary" onclick="submitEditProject('${id}')">Save</button></div>`);
}
async function submitEditProject(id) {
  const name = document.getElementById('edit-proj-name').value.trim();
  if (!name) { toast('Name is required', 'error'); return; }
  const updated = { name, emoji: document.getElementById('edit-proj-emoji').value.trim() || '📁', description: document.getElementById('edit-proj-desc').value.trim(), url: document.getElementById('edit-proj-url').value.trim(), start_date: document.getElementById('edit-proj-start').value.trim(), end_date: document.getElementById('edit-proj-end').value.trim(), status: document.getElementById('edit-proj-status').value };
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
      if (sd.projects && sd.projects.length > 0) sd.projects.forEach(proj => { events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Project: '+proj.name)}\r\nDESCRIPTION:${esc(proj.name+' ('+proj.pct+'%)'+(sd.run?' + Run':''))}\r\nCATEGORIES:project\r\nEND:VEVENT\r\n`); });
      else if (sd.run) events.push(`BEGIN:VEVENT\r\nUID:${uid()}@teamviz\r\nDTSTAMP:${fmtICS(new Date())}Z\r\nDTSTART:${fmtICS(dts)}\r\nDTEND:${fmtICS(dte)}\r\nSUMMARY:${esc('Run duty')}\r\nCATEGORIES:run\r\nEND:VEVENT\r\n`);
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
function showHelp() { showModal(`<h2>Keyboard Shortcuts</h2><div class="help-shortcuts"><span><kbd>Tab</kbd></span><span>Move between cells</span><span><kbd>Enter</kbd></span><span>Open cell editor</span><span><kbd>←</kbd></span><span>Move timeframe back (Team / Availability)</span><span><kbd>→</kbd></span><span>Move timeframe forward (Team / Availability)</span><span><kbd>U</kbd></span><span>Mark undetermined</span><span><kbd>R</kbd></span><span>Toggle run</span><span><kbd>Ctrl+Z</kbd></span><span>Undo</span><span><kbd>Escape</kbd></span><span>Close editor / modal</span></div><div class="form-actions"><button onclick="closeModal()">Close</button></div>`); }

// ===== KEYBOARD SHORTCUTS =====
function modalOpen() {
  const ov = document.getElementById('overlay');
  const ce = document.getElementById('cell-editor');
  return (ov && !ov.classList.contains('hidden')) || (ce && !ce.classList.contains('hidden'));
}
document.addEventListener('keydown', (e) => {
  const tag = (e.target.tagName || '').toLowerCase();
  if (tag === 'input' || tag === 'select' || tag === 'textarea') return;
  if (e.ctrlKey && e.key === 'z') { e.preventDefault(); undo(); }
  if (e.key === 'Escape') { closeCellEditor(); closeModal(); }
  if (e.key === 'u' && !e.ctrlKey && editorPersonId) setUndetermined();
  if (e.key === 'r' && !e.ctrlKey && editorPersonId) { window.editorRunToggle = !window.editorRunToggle; const cb = document.getElementById('cell-editor')?.querySelector('input[type="checkbox"]'); if (cb) cb.checked = window.editorRunToggle; }
  // Arrow keys move the timeframe in the team grid & availability views
  if (editorPersonId || modalOpen()) return;
  if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
    const dir = e.key === 'ArrowLeft' ? -1 : 1;
    if (currentView === 'team') { scrollOffset += dir; API.reloadPlanning().then(render); e.preventDefault(); }
    else if (currentView === 'availability') { availabilityDayOffset += dir; render(); e.preventDefault(); }
  }
});

// ===== NAVIGATION =====
function initNav() {
  $$('.nav-btn[data-view]').forEach(btn => btn.addEventListener('click', () => { currentView = btn.dataset.view; currentPersonId = null; render(); }));
  document.getElementById('overlay').addEventListener('click', (e) => { if (e.target === e.currentTarget) closeModal(); });
  if (canEdit()) { document.getElementById('btn-undo').addEventListener('click', undo); }
  else { document.getElementById('btn-undo').style.display = 'none'; }
  document.getElementById('btn-logout').addEventListener('click', logout);
  document.getElementById('btn-export').addEventListener('click', doExport);
  document.getElementById('btn-help').addEventListener('click', showHelp);
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
  // One click clears BOTH sessions so a different user can sign in:
  //  1. /oauth2/sign_out clears the oauth2-proxy session cookie, then
  //     302-redirects the browser to the `rd` target.
  //  2. Keycloak's end_session_endpoint ends the SSO session. The session is
  //     identified via the browser's Keycloak SSO cookie (we deliberately omit
  //     id_token_hint: oauth2-proxy v7.6.0 does NOT substitute {id_token} in
  //     the `rd` URL — only in --backend-logout-url — so a literal {id_token}
  //     would make Keycloak return 400). Without id_token_hint Keycloak shows
  //     a one-click "confirm logout" page, then redirects to post_logout_redirect_uri.
  //  3. The app root now has no session, so oauth2-proxy starts a fresh login.
  const kcLogout = 'http://localhost:8090/realms/teamviz/protocol/openid-connect/logout'
    + '?client_id=teamviz-demo'
    + '&post_logout_redirect_uri=' + encodeURIComponent('http://localhost:8080/');
  window.location.href = '/oauth2/sign_out?rd=' + encodeURIComponent(kcLogout);
}

// ===== INIT =====
async function init() {
  try {
    const sessionRes = await fetch('/api/auth/session');
    if (!sessionRes.ok) { document.getElementById('main').innerHTML = '<div style="text-align:center;padding:60px;color:var(--fg-muted)"><h1>Authentication required</h1><p>Access via the reverse proxy.</p></div>'; return; }
    const session = await sessionRes.json();
    State.user = session.user; State.token = session.token;
    document.getElementById('topbar').classList.remove('hidden');
    document.getElementById('user-info').textContent = `${State.user.username} (${State.user.role})`;
    await API.loadAll();
    State.theme = localStorage.getItem('teamviz_theme') || State.settings.theme || 'dracula';
    initNav();
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