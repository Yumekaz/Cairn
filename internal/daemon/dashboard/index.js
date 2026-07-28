/* Cairn Dashboard — single-node local admin console (vanilla JS) */

document.addEventListener('DOMContentLoaded', () => {
  // ---------------------------------------------------------------------------
  // DOM refs
  // ---------------------------------------------------------------------------
  const navItems = document.querySelectorAll('.nav-item');
  const panels = document.querySelectorAll('.tab-panel');
  const pageTitle = document.getElementById('page-title');
  const pageSubtitle = document.getElementById('page-subtitle');

  // ---------------------------------------------------------------------------
  // App state
  // ---------------------------------------------------------------------------
  let servicesCache = [];
  let eventsCache = [];
  let selectedVolumeName = null;
  let currentRoute = '';
  let activeServiceName = null;
  let activeLogsInterval = null;
  let lastLogsText = '';
  let currentConfirmCallback = null;

  // Connection / refresh
  let isOnline = false;
  let reconnectAttempts = 0;
  let statusTimer = null;
  let panelRefreshTimer = null;
  let statusInFlight = false;
  let panelRefreshInFlight = false;
  let lastSuccessfulSync = null;

  const STATUS_BASE_MS = 5000;
  const STATUS_MAX_BACKOFF_MS = 30000;
  const PANEL_REFRESH_MS = 8000;
  const LOGS_POLL_MS = 3000;

  // ---------------------------------------------------------------------------
  // Connection status (truthful: green only when /status succeeds)
  // ---------------------------------------------------------------------------
  function setConnectionState(online, message, detail, opts) {
    const wasOnline = isOnline;
    isOnline = online;
    opts = opts || {};

    const ind = document.getElementById('status-indicator');
    const text = document.getElementById('daemon-connection-text');
    const detailEl = document.getElementById('daemon-connection-detail');
    const banner = document.getElementById('global-banner');
    const bannerText = document.getElementById('global-banner-text');
    const statusWrap = document.getElementById('connection-status');

    if (online) {
      ind.className = 'status-indicator online';
      text.textContent = message || 'Connected';
      detailEl.textContent = detail || '';
      banner.classList.add('hidden');
      statusWrap.classList.remove('is-offline');
      document.body.classList.remove('daemon-offline');
    } else {
      ind.className = 'status-indicator offline';
      text.textContent = message || 'Disconnected';
      detailEl.textContent = detail || '';
      bannerText.textContent = detail
        ? `Daemon unreachable — ${detail}`
        : 'Daemon unreachable. Actions are disabled until connection is restored.';
      banner.classList.remove('hidden');
      statusWrap.classList.add('is-offline');
      document.body.classList.add('daemon-offline');
    }

    // Enable / disable mutation actions
    document.querySelectorAll('.needs-online').forEach((el) => {
      if (online) {
        if (el.dataset.busy !== '1') el.disabled = false;
      } else {
        el.disabled = true;
      }
    });

    const uptimeHealth = document.getElementById('overview-uptime-text');
    if (uptimeHealth) {
      uptimeHealth.textContent = online ? 'Healthy' : 'Unreachable';
      uptimeHealth.classList.toggle('text-danger', !online);
    }

    if (wasOnline !== online && !online) {
      showToast('Lost connection to cairnd', 'error');
    } else if (wasOnline !== online && online && opts.reconnected) {
      showToast('Reconnected to cairnd', 'success');
      if (activeServiceName) {
        const svc = servicesCache.find((x) => x.name === activeServiceName);
        setupServiceActions(activeServiceName, svc && svc.actual_state);
      }
    }
  }

  function updateLastUpdated(ts) {
    lastSuccessfulSync = ts || new Date();
    const el = document.getElementById('last-updated-value');
    if (!el) return;
    el.textContent = formatClock(lastSuccessfulSync);
    el.title = lastSuccessfulSync.toISOString();
  }

  function nextStatusDelay() {
    if (isOnline) return STATUS_BASE_MS;
    const exp = Math.min(STATUS_MAX_BACKOFF_MS, STATUS_BASE_MS * Math.pow(2, reconnectAttempts));
    // Jitter ±20%
    return Math.round(exp * (0.8 + Math.random() * 0.4));
  }

  function scheduleStatusPoll(delay) {
    if (statusTimer) clearTimeout(statusTimer);
    statusTimer = null;
    if (document.hidden) return;
    statusTimer = setTimeout(pollDaemonStatus, delay);
  }

  async function pollDaemonStatus() {
    if (statusInFlight) {
      scheduleStatusPoll(STATUS_BASE_MS);
      return;
    }
    statusInFlight = true;
    try {
      const res = await fetch('/status', { cache: 'no-store' });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();

      document.getElementById('stat-uptime').textContent = data.uptime ?? '—';
      document.getElementById('stat-active').textContent =
        data.active_services != null ? String(data.active_services) : '—';
      document.getElementById('stat-storage').textContent = data.storage_usage ?? '—';
      document.getElementById('stat-version').textContent = data.version ?? '—';

      const wasOffline = !isOnline;
      const priorAttempts = reconnectAttempts;
      reconnectAttempts = 0;
      setConnectionState(true, 'Connected', data.version ? `v${data.version}` : '', {
        reconnected: wasOffline && priorAttempts > 0,
      });
      updateLastUpdated();

      // If we just came back online, refresh the active panel immediately
      if (wasOffline) {
        refreshActivePanel(true);
      }
    } catch (err) {
      reconnectAttempts += 1;
      const delay = nextStatusDelay();
      const secs = Math.round(delay / 1000);
      setConnectionState(
        false,
        'Disconnected',
        err.message || 'fetch failed' + ` · retry in ${secs}s`
      );
      // Still schedule with backoff; fall through to scheduleStatusPoll below with backoff
      statusInFlight = false;
      scheduleStatusPoll(delay);
      return;
    }
    statusInFlight = false;
    scheduleStatusPoll(STATUS_BASE_MS);
  }

  document.getElementById('btn-retry-connection').addEventListener('click', () => {
    reconnectAttempts = 0;
    const ind = document.getElementById('status-indicator');
    ind.className = 'status-indicator connecting';
    document.getElementById('daemon-connection-text').textContent = 'Connecting…';
    pollDaemonStatus();
  });

  // ---------------------------------------------------------------------------
  // Panel refresh loop (staggered with status; only active route)
  // ---------------------------------------------------------------------------
  function schedulePanelRefresh() {
    if (panelRefreshTimer) clearTimeout(panelRefreshTimer);
    panelRefreshTimer = null;
    if (document.hidden) return;
    panelRefreshTimer = setTimeout(() => {
      refreshActivePanel(false).finally(() => schedulePanelRefresh());
    }, PANEL_REFRESH_MS);
  }

  // Pause status/panel polls while tab is hidden; resume with immediate refresh
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      if (statusTimer) {
        clearTimeout(statusTimer);
        statusTimer = null;
      }
      if (panelRefreshTimer) {
        clearTimeout(panelRefreshTimer);
        panelRefreshTimer = null;
      }
      return;
    }
    pollDaemonStatus();
    refreshActivePanel(true);
    schedulePanelRefresh();
  });

  async function refreshActivePanel(force) {
    if (panelRefreshInFlight && !force) return;
    if (!isOnline && !force) return;
    panelRefreshInFlight = true;
    try {
      switch (currentRoute) {
        case '#/overview':
          await loadOverviewData({ silent: !force });
          break;
        case '#/services':
          await loadServicesData({ silent: !force });
          break;
        case '#/volumes':
          await loadVolumesData({ silent: !force });
          break;
        case '#/events':
          await loadEventsTimeline({ silent: !force });
          break;
        default:
          break;
      }
    } catch (e) {
      /* loaders surface their own errors */
    } finally {
      panelRefreshInFlight = false;
    }
  }

  // ---------------------------------------------------------------------------
  // Router
  // ---------------------------------------------------------------------------
  function setActiveNav(navId) {
    navItems.forEach((nav) => {
      nav.classList.remove('active');
      nav.removeAttribute('aria-current');
    });
    const el = document.getElementById(navId);
    if (el) {
      el.classList.add('active');
      el.setAttribute('aria-current', 'page');
    }
  }

  function handleRoute() {
    const hash = window.location.hash || '#/overview';
    const prev = currentRoute;
    currentRoute = hash;

    panels.forEach((panel) => panel.classList.add('hidden'));

    if (hash === '#/overview') {
      setActiveNav('nav-overview');
      showPanel('panel-overview');
      pageTitle.textContent = 'Overview';
      pageSubtitle.textContent = 'Control plane metrics for this Cairn node';
      loadOverviewData({ silent: prev === hash });
    } else if (hash === '#/services') {
      setActiveNav('nav-services');
      showPanel('panel-services');
      pageTitle.textContent = 'Services';
      pageSubtitle.textContent = 'Run, inspect, and manage service deployments';
      loadServicesData({ silent: prev === hash });
    } else if (hash === '#/volumes') {
      setActiveNav('nav-volumes');
      showPanel('panel-volumes');
      pageTitle.textContent = 'Volumes & Backups';
      pageSubtitle.textContent = 'Persistent state partitions and volume snapshots';
      loadVolumesData({ silent: prev === hash });
    } else if (hash === '#/events') {
      setActiveNav('nav-events');
      showPanel('panel-events');
      pageTitle.textContent = 'Events Timeline';
      pageSubtitle.textContent = 'Audit log of daemon state changes and deployments';
      loadEventsTimeline({ silent: prev === hash });
    } else {
      window.location.hash = '#/overview';
    }
  }

  function showPanel(id) {
    const panel = document.getElementById(id);
    panel.classList.remove('hidden');
    // Retrigger enter animation
    panel.classList.remove('panel-enter');
    // Force reflow
    void panel.offsetWidth;
    panel.classList.add('panel-enter');
  }

  window.addEventListener('hashchange', handleRoute);

  // ---------------------------------------------------------------------------
  // API helpers
  // ---------------------------------------------------------------------------
  async function apiCall(url, method = 'GET', body = null) {
    const options = { method, cache: 'no-store' };
    if (body) {
      options.headers = { 'Content-Type': 'application/json' };
      options.body = JSON.stringify(body);
    }
    const res = await fetch(url, options);

    if (res.status === 409) {
      const data = await res.json().catch(() => ({}));
      return { conflict: true, status: 409, message: data.error || 'Conflict' };
    }

    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || `HTTP error ${res.status}`);
    }

    if (res.status === 204) return null;
    const ct = res.headers.get('content-type') || '';
    if (ct.includes('application/json')) return await res.json();
    return await res.text();
  }

  function setPanelError(id, message) {
    const el = document.getElementById(id);
    if (!el) return;
    if (!message) {
      el.classList.add('hidden');
      el.textContent = '';
      return;
    }
    el.textContent = message;
    el.classList.remove('hidden');
  }

  // ---------------------------------------------------------------------------
  // OVERVIEW
  // ---------------------------------------------------------------------------
  async function loadOverviewData({ silent } = {}) {
    const tbody = document.getElementById('recent-services-body');
    const evList = document.getElementById('overview-events-list');

    if (!silent) {
      tbody.innerHTML = skeletonTableRows(4, 4);
      evList.innerHTML = skeletonStackHtml(3);
    }

    try {
      let [services, events, volumes] = await Promise.all([
        apiCall('/services'),
        apiCall('/events'),
        apiCall('/volumes'),
      ]);
      services = services || [];
      events = events || [];
      volumes = volumes || [];
      servicesCache = services;
      eventsCache = events;

      document.getElementById('overview-services-count').textContent = String(services.length);
      document.getElementById('overview-volumes-count').textContent = String(volumes.length);

      // Backups: sample in parallel, cap concurrent volume backup lists
      let totalBackups = 0;
      const backupResults = await Promise.all(
        volumes.map((vol) =>
          apiCall(`/volumes/${encodeURIComponent(vol.name)}/backups`).catch(() => [])
        )
      );
      backupResults.forEach((b) => {
        totalBackups += (b || []).length;
      });
      document.getElementById('overview-backups-count').textContent = String(totalBackups);

      const healthEl = document.getElementById('overview-uptime-text');
      healthEl.textContent = isOnline ? 'Healthy' : 'Unreachable';
      healthEl.classList.toggle('text-danger', !isOnline);

      // Recent services — stable columns, ellipsis cells
      tbody.innerHTML = '';
      if (services.length === 0) {
        tbody.innerHTML = `
          <tr>
            <td colspan="4">
              <div class="empty-state compact">
                <p class="empty-title">No services yet</p>
                <p class="text-muted">Deploy a service with the Cairn CLI to see it here.</p>
              </div>
            </td>
          </tr>`;
      } else {
        services.slice(0, 5).forEach((s) => {
          const tr = document.createElement('tr');
          const name = escapeHtml(s.name || '');
          const kind = escapeHtml(s.kind || 'unknown');
          const state = escapeHtml(s.actual_state || 'unknown');
          const route = escapeHtml(s.route || 'N/A');
          const stateClass = sanitizeBadgeClass(s.actual_state);
          tr.innerHTML = `
            <td><span class="font-bold service-name-cell" title="${name}">${name}</span></td>
            <td><span class="badge badge-kind">${kind}</span></td>
            <td><span class="badge ${stateClass}">${state}</span></td>
            <td><span class="text-mono route-cell" title="${route}">${route}</span></td>`;
          tr.style.cursor = 'pointer';
          tr.addEventListener('click', () => showServiceDetail(s.name));
          tbody.appendChild(tr);
        });
      }

      // Recent events
      evList.innerHTML = '';
      if (events.length === 0) {
        evList.innerHTML = `
          <div class="empty-state compact">
            <p class="empty-title">No recent events</p>
            <p class="text-muted">Daemon activity will appear here.</p>
          </div>`;
      } else {
        events.slice(0, 6).forEach((e) => {
          const div = document.createElement('div');
          div.className = `recent-event-item ${eventTypeClass(e.type)}`;
          div.innerHTML = `
            <span class="event-time">${escapeHtml(formatTime(e.created_at))}</span>
            <span class="event-msg"><strong class="event-type">[${escapeHtml(e.type || '')}]</strong> ${escapeHtml(e.message || '')}</span>`;
          evList.appendChild(div);
        });
      }

      setPanelError('overview-error', null);
      updateLastUpdated();
    } catch (err) {
      console.error('Error loading overview data:', err);
      setPanelError('overview-error', `Failed to load overview: ${err.message}`);
      if (!silent) {
        tbody.innerHTML = `<tr><td colspan="4" class="text-center text-danger">Failed to load services</td></tr>`;
        evList.innerHTML = `<p class="text-danger text-center py-4">Failed to load events</p>`;
      }
    }
  }

  // ---------------------------------------------------------------------------
  // SERVICES
  // ---------------------------------------------------------------------------
  async function loadServicesData({ silent } = {}) {
    const container = document.getElementById('services-container');
    const searchVal = (document.getElementById('input-search-services').value || '').toLowerCase();
    const countLabel = document.getElementById('services-count-label');

    if (!silent) {
      container.innerHTML = skeletonServiceCards(3);
    }

    try {
      let services = await apiCall('/services');
      services = services || [];
      servicesCache = services;

      const filtered = services.filter((s) => {
        const n = (s.name || '').toLowerCase();
        const k = (s.kind || '').toLowerCase();
        return n.includes(searchVal) || k.includes(searchVal);
      });

      countLabel.textContent =
        services.length === 0
          ? ''
          : searchVal
            ? `${filtered.length} of ${services.length}`
            : `${services.length} service${services.length === 1 ? '' : 's'}`;

      container.innerHTML = '';

      if (filtered.length === 0) {
        container.innerHTML = `
          <div class="glass-card empty-state-card" style="grid-column: 1/-1;">
            <div class="empty-state">
              <p class="empty-title">${services.length === 0 ? 'No services deployed' : 'No matches'}</p>
              <p class="text-muted">${
                services.length === 0
                  ? 'Register a deployment with the Cairn CLI. This dashboard is read/manage only for existing services.'
                  : 'Try a different name or kind filter.'
              }</p>
            </div>
          </div>`;
        setPanelError('services-error', null);
        updateLastUpdated();
        return;
      }

      filtered.forEach((s) => {
        const card = document.createElement('div');
        card.className = 'glass-card service-card';
        const name = escapeHtml(s.name || '');
        const idShort = escapeHtml((s.id || '').slice(0, 8) || '—');
        const kind = escapeHtml(s.kind || 'unknown');
        const state = escapeHtml(s.actual_state || 'unknown');
        const stateClass = sanitizeBadgeClass(s.actual_state);
        const route = escapeHtml(s.route || 'N/A');
        card.innerHTML = `
          <div class="service-card-header">
            <div class="service-card-title">
              <h3 title="${name}">${name}</h3>
              <p class="text-mono">${idShort}</p>
            </div>
            <span class="badge ${stateClass}">${state}</span>
          </div>
          <div class="service-info-row">
            <span class="service-info-label">Kind</span>
            <span>${kind}</span>
          </div>
          <div class="service-info-row">
            <span class="service-info-label">Route</span>
            <span class="text-mono route-cell" title="${route}">${route}</span>
          </div>
          <div class="service-card-footer">
            <button type="button" class="btn btn-secondary btn-sm btn-inspect">Inspect</button>
          </div>`;
        card.querySelector('.btn-inspect').addEventListener('click', () => showServiceDetail(s.name));
        container.appendChild(card);
      });

      setPanelError('services-error', null);
      updateLastUpdated();
    } catch (err) {
      console.error('Failed to load services:', err);
      setPanelError('services-error', `Failed to load services: ${err.message}`);
      if (!silent) {
        container.innerHTML = `
          <div class="glass-card empty-state-card" style="grid-column: 1/-1;">
            <div class="empty-state">
              <p class="empty-title text-danger">Could not load services</p>
              <p class="text-muted">${escapeHtml(err.message)}</p>
            </div>
          </div>`;
      }
    }
  }

  let searchDebounce = null;
  document.getElementById('input-search-services').addEventListener('input', () => {
    clearTimeout(searchDebounce);
    searchDebounce = setTimeout(() => loadServicesData({ silent: true }), 150);
  });

  // ---------------------------------------------------------------------------
  // Modal focus trap (Tab cycle, Escape, restore opener)
  // ---------------------------------------------------------------------------
  const FOCUSABLE_SEL =
    'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';
  /** @type {{ modal: HTMLElement, returnTo: Element | null }[]} */
  const modalFocusStack = [];

  function isFocusableVisible(el) {
    if (el.disabled || el.getAttribute('aria-hidden') === 'true') return false;
    if (el.closest('.hidden')) return false;
    return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  }

  function getFocusable(container) {
    return Array.from(container.querySelectorAll(FOCUSABLE_SEL)).filter(isFocusableVisible);
  }

  function getTopModal() {
    const confirm = document.getElementById('modal-confirm-action');
    const service = document.getElementById('modal-service-detail');
    if (confirm && !confirm.classList.contains('hidden')) return confirm;
    if (service && !service.classList.contains('hidden')) return service;
    return null;
  }

  function activateModalTrap(modal) {
    modalFocusStack.push({
      modal,
      returnTo: document.activeElement instanceof HTMLElement ? document.activeElement : null,
    });
    modal.classList.remove('hidden');
    requestAnimationFrame(() => {
      const focusables = getFocusable(modal);
      if (focusables.length) focusables[0].focus();
      else if (modal.tabIndex < 0) {
        modal.tabIndex = -1;
        modal.focus();
      }
    });
  }

  function deactivateModalTrap(modal) {
    modal.classList.add('hidden');
    let returnTo = null;
    for (let i = modalFocusStack.length - 1; i >= 0; i--) {
      if (modalFocusStack[i].modal === modal) {
        returnTo = modalFocusStack[i].returnTo;
        modalFocusStack.splice(i, 1);
        break;
      }
    }
    if (returnTo && typeof returnTo.focus === 'function' && document.contains(returnTo)) {
      returnTo.focus();
    }
  }

  // ---------------------------------------------------------------------------
  // SERVICE DETAIL
  // ---------------------------------------------------------------------------
  async function showServiceDetail(serviceName) {
    const modal = document.getElementById('modal-service-detail');
    activateModalTrap(modal);
    activeServiceName = serviceName;
    lastLogsText = '';

    if (activeLogsInterval) {
      clearInterval(activeLogsInterval);
      activeLogsInterval = null;
    }

    document.getElementById('detail-service-name').textContent = serviceName;
    document.getElementById('detail-service-status').textContent = '…';
    document.getElementById('detail-service-status').className = 'badge';
    document.getElementById('service-logs-console').innerHTML =
      '<div class="log-line text-muted">Loading logs…</div>';
    document.getElementById('detail-deploys-list').innerHTML =
      '<p class="text-muted">Loading history…</p>';
    document.getElementById('lifecycle-hint').textContent = '';

    try {
      const s = await apiCall(`/services/${encodeURIComponent(serviceName)}`);

      document.getElementById('detail-service-name').textContent = s.name;
      const statusBadge = document.getElementById('detail-service-status');
      statusBadge.textContent = s.actual_state || 'unknown';
      statusBadge.className = `badge ${sanitizeBadgeClass(s.actual_state)}`;

      document.getElementById('detail-service-id').textContent = s.id || '—';
      document.getElementById('detail-service-kind').textContent = s.kind || '—';
      document.getElementById('detail-service-runtime').textContent = s.runtime_backend || '—';
      document.getElementById('detail-service-runtime-id').textContent = s.runtime_id || 'N/A';
      document.getElementById('detail-service-route').textContent = s.route || 'N/A';

      // Cache current_deploy_id for rollback UX
      const idx = servicesCache.findIndex((x) => x.name === serviceName);
      if (idx >= 0) servicesCache[idx] = { ...servicesCache[idx], ...s };
      else servicesCache.push(s);

      loadDeployHistory(s.name);
      loadConsoleLogs(s.name, { force: true });

      activeLogsInterval = setInterval(() => {
        if (activeServiceName === s.name) loadConsoleLogs(s.name, { force: false });
      }, LOGS_POLL_MS);

      setupServiceActions(s.name, s.actual_state);
    } catch (err) {
      console.error('Failed to inspect service:', err);
      showToast(`Failed to load service: ${err.message}`, 'error');
      document.getElementById('lifecycle-hint').textContent = err.message;
    }
  }

  function setupServiceActions(serviceName, actualState) {
    const btnStart = document.getElementById('btn-action-start');
    const btnStop = document.getElementById('btn-action-stop');
    const btnRestart = document.getElementById('btn-action-restart');
    const btnRefresh = document.getElementById('btn-logs-refresh');
    const btnClear = document.getElementById('btn-logs-clear');
    const hint = document.getElementById('lifecycle-hint');

    // Clone to drop old listeners
    const wire = (btn, handler) => {
      const next = btn.cloneNode(true);
      btn.parentNode.replaceChild(next, btn);
      next.addEventListener('click', handler);
      return next;
    };

    const newStart = wire(btnStart, async () => {
      await runLifecycle(serviceName, 'start', newStart);
    });
    const newStop = wire(btnStop, async () => {
      await runLifecycle(serviceName, 'stop', newStop);
    });
    const newRestart = wire(btnRestart, async () => {
      await runLifecycle(serviceName, 'restart', newRestart);
    });
    const newRefresh = wire(btnRefresh, () => loadConsoleLogs(serviceName, { force: true }));

    btnClear.onclick = () => {
      lastLogsText = '';
      document.getElementById('service-logs-console').innerHTML =
        '<div class="log-line text-muted">Console cleared (local view only).</div>';
    };

    // State-aware enablement when online
    const state = (actualState || '').toLowerCase();
    const online = isOnline;
    newStart.disabled = !online || state === 'running' || state === 'starting';
    newStop.disabled = !online || state === 'stopped' || state === 'failed';
    newRestart.disabled = !online;
    newRefresh.disabled = !online;

    if (!online) {
      hint.textContent = 'Daemon offline — lifecycle actions disabled.';
    } else if (state === 'running') {
      hint.textContent = 'Service is running. Stop or restart as needed.';
    } else if (state === 'stopped') {
      hint.textContent = 'Service is stopped. Start to bring it up.';
    } else {
      hint.textContent = '';
    }
  }

  async function runLifecycle(serviceName, action, btn) {
    if (!isOnline) {
      showToast('Daemon offline', 'error');
      return;
    }
    btn.dataset.busy = '1';
    btn.disabled = true;
    try {
      await apiCall(`/services/${encodeURIComponent(serviceName)}/${action}`, 'POST');
      showToast(`${capitalize(action)} requested for ${serviceName}`, 'success');
      await showServiceDetail(serviceName);
      if (currentRoute === '#/services') loadServicesData({ silent: true });
      if (currentRoute === '#/overview') loadOverviewData({ silent: true });
    } catch (e) {
      showToast(e.message, 'error');
    } finally {
      btn.dataset.busy = '0';
      if (isOnline) btn.disabled = false;
    }
  }

  async function loadDeployHistory(serviceName) {
    const historyList = document.getElementById('detail-deploys-list');
    historyList.innerHTML = '<p class="text-muted">Loading deploy history…</p>';

    try {
      let deploys = await apiCall(`/services/${encodeURIComponent(serviceName)}/deploys`);
      deploys = deploys || [];
      historyList.innerHTML = '';

      if (deploys.length === 0) {
        historyList.innerHTML = `
          <div class="empty-state compact">
            <p class="empty-title">No deployments</p>
            <p class="text-muted">Deploy history will appear after the first deploy.</p>
          </div>`;
        return;
      }

      const svc = servicesCache.find((x) => x.name === serviceName);

      deploys.forEach((d) => {
        const item = document.createElement('div');
        item.className = 'deploy-history-item';
        const status = (d.status || 'unknown').toLowerCase();
        const badgeClass =
          status === 'success' || status === 'completed'
            ? 'running'
            : status === 'failed'
              ? 'stopped'
              : 'starting';
        const badge = `<span class="badge ${badgeClass}">${escapeHtml(d.status || 'unknown')}</span>`;
        const isCurrent = svc && svc.current_deploy_id === d.id;
        const canRollback = !isCurrent && status === 'success' && isOnline;

        let actionHtml = '';
        if (isCurrent) {
          actionHtml = '<span class="deploy-active-tag">Active</span>';
        } else if (canRollback) {
          actionHtml = `<button type="button" class="btn btn-secondary btn-sm btn-rollback needs-online" data-deploy-id="${escapeHtml(d.id)}" title="Roll back to this deploy">Rollback</button>`;
        } else if (!isOnline && !isCurrent && status === 'success') {
          actionHtml = `<button type="button" class="btn btn-secondary btn-sm" disabled title="Daemon offline">Rollback</button>`;
        }

        const idShort = escapeHtml((d.id || '').slice(0, 8));
        const ver = d.version != null ? escapeHtml(String(d.version)) : '—';
        const reason = d.failure_reason
          ? `<span class="text-danger deploy-fail-reason" title="${escapeHtml(d.failure_reason)}">Reason: ${escapeHtml(d.failure_reason)}</span>`
          : '';

        item.innerHTML = `
          <div class="deploy-history-meta">
            <span class="deploy-version">Deploy <span class="text-mono">${idShort}</span> <span class="text-muted">(v${ver})</span></span>
            <span class="deploy-date">${escapeHtml(formatDateTime(d.created_at))}</span>
            ${reason}
          </div>
          <div class="deploy-history-actions">
            ${badge}
            ${actionHtml}
          </div>`;

        const rb = item.querySelector('.btn-rollback');
        if (rb) {
          rb.addEventListener('click', () => triggerRollback(serviceName, d.id));
        }
        if (isCurrent) item.classList.add('is-current');
        historyList.appendChild(item);
      });
    } catch (err) {
      console.error('Failed to load history:', err);
      historyList.innerHTML = `<p class="text-danger">Failed to load history: ${escapeHtml(err.message)}</p>`;
    }
  }

  async function triggerRollback(serviceName, deployID) {
    if (!isOnline) {
      showToast('Daemon offline', 'error');
      return;
    }
    try {
      const res = await apiCall(`/services/${encodeURIComponent(serviceName)}/rollback`, 'POST', {
        deploy_id: deployID,
        force: false,
      });

      if (res && res.conflict) {
        promptDangerousAction({
          title: 'Unsafe Rollback Detected',
          message:
            res.message ||
            'This rollback may be unsafe because database schema or volume state has changed since that deploy.',
          forceTextRequired: true,
          onProceed: async () => {
            try {
              const finalRes = await apiCall(
                `/services/${encodeURIComponent(serviceName)}/rollback`,
                'POST',
                { deploy_id: deployID, force: true }
              );
              if (finalRes && finalRes.error) {
                showToast('Rollback failed: ' + finalRes.error, 'error');
              } else {
                showToast('Force rollback started', 'success');
                closeServiceModal();
                refreshActivePanel(true);
              }
            } catch (e) {
              showToast('Rollback failed: ' + e.message, 'error');
            }
          },
        });
      } else {
        showToast('Rollback initiated', 'success');
        closeServiceModal();
        refreshActivePanel(true);
      }
    } catch (err) {
      showToast('Rollback failed: ' + err.message, 'error');
    }
  }

  async function loadConsoleLogs(serviceName, { force } = {}) {
    const consolePane = document.getElementById('service-logs-console');
    const follow = document.getElementById('chk-logs-follow').checked;
    const nearBottom =
      consolePane.scrollHeight - consolePane.scrollTop - consolePane.clientHeight < 48;

    try {
      const res = await fetch(`/services/${encodeURIComponent(serviceName)}/logs`, {
        cache: 'no-store',
      });
      if (!res.ok) throw new Error(`Logs failed (HTTP ${res.status})`);
      const text = await res.text();

      if (!force && text === lastLogsText) return;
      lastLogsText = text;

      if (!text || text.trim() === '') {
        consolePane.innerHTML = '<div class="log-line text-muted">No logs recorded yet.</div>';
        return;
      }

      const lines = text.split('\n');
      const frag = document.createDocumentFragment();
      lines.forEach((l) => {
        if (!l.trim()) return;
        const lineDiv = document.createElement('div');
        lineDiv.className = 'log-line';
        const match = l.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z?)\s(.*)$/);
        if (match) {
          lineDiv.innerHTML = `<span class="log-timestamp">${escapeHtml(formatTime(match[1]))}</span><span class="log-body">${escapeHtml(match[2])}</span>`;
        } else {
          lineDiv.textContent = l;
        }
        frag.appendChild(lineDiv);
      });
      consolePane.innerHTML = '';
      consolePane.appendChild(frag);

      if (follow || nearBottom) {
        consolePane.scrollTop = consolePane.scrollHeight;
      }
    } catch (err) {
      if (force || !lastLogsText) {
        consolePane.innerHTML = `<div class="log-line text-danger">Failed to fetch logs: ${escapeHtml(err.message)}</div>`;
      }
    }
  }

  document.getElementById('btn-logs-wrap').addEventListener('click', () => {
    const pane = document.getElementById('service-logs-console');
    const btn = document.getElementById('btn-logs-wrap');
    pane.classList.toggle('nowrap');
    btn.classList.toggle('active');
  });

  function closeServiceModal() {
    const modal = document.getElementById('modal-service-detail');
    deactivateModalTrap(modal);
    activeServiceName = null;
    if (activeLogsInterval) {
      clearInterval(activeLogsInterval);
      activeLogsInterval = null;
    }
  }

  document.getElementById('btn-close-service-modal').addEventListener('click', closeServiceModal);
  document.getElementById('modal-service-detail').addEventListener('click', (e) => {
    if (e.target.id === 'modal-service-detail') closeServiceModal();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      if (!document.getElementById('modal-confirm-action').classList.contains('hidden')) {
        closeConfirmModal();
      } else if (!document.getElementById('modal-service-detail').classList.contains('hidden')) {
        closeServiceModal();
      }
      return;
    }

    if (e.key !== 'Tab') return;
    const top = getTopModal();
    if (!top) return;
    const focusables = getFocusable(top);
    if (!focusables.length) {
      e.preventDefault();
      return;
    }
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (e.shiftKey) {
      if (document.activeElement === first || !top.contains(document.activeElement)) {
        e.preventDefault();
        last.focus();
      }
    } else if (document.activeElement === last || !top.contains(document.activeElement)) {
      e.preventDefault();
      first.focus();
    }
  });

  // ---------------------------------------------------------------------------
  // VOLUMES & BACKUPS
  // ---------------------------------------------------------------------------
  async function loadVolumesData({ silent } = {}) {
    const tbody = document.getElementById('volumes-list-body');
    if (!silent) {
      tbody.innerHTML = skeletonTableRows(5, 3);
    }

    try {
      let volumes = await apiCall('/volumes');
      volumes = volumes || [];
      tbody.innerHTML = '';

      if (volumes.length === 0) {
        tbody.innerHTML = `
          <tr>
            <td colspan="5">
              <div class="empty-state compact">
                <p class="empty-title">No persistent volumes</p>
                <p class="text-muted">Volumes appear when services declare stateful mounts.</p>
              </div>
            </td>
          </tr>`;
        setPanelError('volumes-error', null);
        updateLastUpdated();
        return;
      }

      volumes.forEach((v) => {
        const tr = document.createElement('tr');
        tr.id = `volume-row-${cssEscape(v.name)}`;
        tr.dataset.volumeName = v.name;
        const name = escapeHtml(v.name || '');
        const attached = v.attached_service_id
          ? escapeHtml(String(v.attached_service_id).slice(0, 8))
          : 'Unattached';
        const mount = escapeHtml(v.mount_path || 'N/A');
        const host = escapeHtml(v.host_path || '—');
        tr.innerHTML = `
          <td><span class="font-bold service-name-cell" title="${name}">${name}</span></td>
          <td><span class="badge badge-secondary">${attached}</span></td>
          <td><span class="text-mono route-cell" title="${mount}">${mount}</span></td>
          <td><span class="text-muted text-sm route-cell" title="${host}">${host}</span></td>
          <td>
            <button type="button" class="btn btn-secondary btn-sm btn-inspect-vol">Inspect</button>
          </td>`;
        tr.querySelector('.btn-inspect-vol').addEventListener('click', () => selectVolume(v.name));
        tr.addEventListener('click', (ev) => {
          if (ev.target.closest('button')) return;
          selectVolume(v.name);
        });
        tr.style.cursor = 'pointer';
        tbody.appendChild(tr);
      });

      if (selectedVolumeName) {
        const stillThere = volumes.some((v) => v.name === selectedVolumeName);
        if (stillThere) selectVolume(selectedVolumeName);
        else {
          selectedVolumeName = null;
          resetBackupInspector();
        }
      }

      setPanelError('volumes-error', null);
      updateLastUpdated();
    } catch (err) {
      console.error('Failed to load volumes:', err);
      setPanelError('volumes-error', `Failed to load volumes: ${err.message}`);
      if (!silent) {
        tbody.innerHTML = `<tr><td colspan="5" class="text-danger text-center">Error: ${escapeHtml(err.message)}</td></tr>`;
      }
    }
  }

  function resetBackupInspector() {
    document.getElementById('backup-subtitle').textContent = 'Select a volume to view backups';
    document.getElementById('btn-create-backup').classList.add('hidden');
    document.getElementById('backups-list-body').innerHTML = `
      <div class="empty-state">
        <p class="empty-title">No volume selected</p>
        <p class="text-muted">Choose a volume on the left to inspect snapshots and restore points.</p>
      </div>`;
  }

  async function selectVolume(volumeName) {
    selectedVolumeName = volumeName;

    document.querySelectorAll('#volumes-list-body tr').forEach((r) => {
      r.classList.toggle('volume-row-selected', r.dataset.volumeName === volumeName);
    });

    document.getElementById('backup-subtitle').textContent = `Volume: ${volumeName}`;

    const btnCreate = document.getElementById('btn-create-backup');
    btnCreate.classList.remove('hidden');
    btnCreate.disabled = !isOnline;

    const newBtn = btnCreate.cloneNode(true);
    btnCreate.parentNode.replaceChild(newBtn, btnCreate);
    newBtn.disabled = !isOnline;
    newBtn.addEventListener('click', () => triggerCreateBackup(volumeName));

    loadBackupsList(volumeName);
  }

  async function loadBackupsList(volumeName) {
    const listBody = document.getElementById('backups-list-body');
    listBody.innerHTML = skeletonStackHtml(2);

    try {
      let backups = await apiCall(`/volumes/${encodeURIComponent(volumeName)}/backups`);
      backups = backups || [];
      listBody.innerHTML = '';

      if (backups.length === 0) {
        listBody.innerHTML = `
          <div class="empty-state compact">
            <p class="empty-title">No backups yet</p>
            <p class="text-muted">Create a snapshot with <strong>Backup Now</strong>.</p>
          </div>`;
        return;
      }

      backups.forEach((b) => {
        const item = document.createElement('div');
        item.className = 'backup-item';
        const sizeMb =
          b.size_bytes != null ? (Number(b.size_bytes) / (1024 * 1024)).toFixed(2) : '—';
        const idShort = escapeHtml((b.id || '').slice(0, 12));
        const status = escapeHtml(b.status || 'unknown');
        const checksum = b.checksum ? escapeHtml(String(b.checksum).slice(0, 16)) : '—';
        item.innerHTML = `
          <div class="backup-meta-info">
            <span class="backup-id text-mono">${idShort}…</span>
            <span class="backup-subtext">Status: <strong>${status}</strong> · ${sizeMb} MB</span>
            <span class="backup-subtext">Created: ${escapeHtml(formatDateTime(b.created_at))}</span>
            <span class="backup-subtext text-mono" style="font-size:10px;">SHA256: ${checksum}</span>
          </div>
          <div>
            <button type="button" class="btn btn-secondary btn-sm btn-restore needs-online" ${isOnline ? '' : 'disabled'} style="padding:4px 8px;font-size:11px;">Restore</button>
          </div>`;
        item.querySelector('.btn-restore').addEventListener('click', () =>
          triggerRestoreBackup(volumeName, b.id)
        );
        listBody.appendChild(item);
      });
    } catch (err) {
      listBody.innerHTML = `<p class="text-danger text-center py-4">Error loading backups: ${escapeHtml(err.message)}</p>`;
    }
  }

  async function triggerCreateBackup(volumeName) {
    if (!isOnline) {
      showToast('Daemon offline', 'error');
      return;
    }
    const btnCreate = document.getElementById('btn-create-backup');
    btnCreate.dataset.busy = '1';
    btnCreate.disabled = true;
    try {
      showToast(`Creating backup for ${volumeName}…`, 'info');
      const backup = await apiCall(`/volumes/${encodeURIComponent(volumeName)}/backups`, 'POST');
      const id = backup && backup.id ? backup.id.slice(0, 12) : '';
      showToast(`Backup created${id ? ': ' + id : ''}`, 'success');
      loadBackupsList(volumeName);
    } catch (err) {
      showToast('Backup failed: ' + err.message, 'error');
    } finally {
      btnCreate.dataset.busy = '0';
      btnCreate.disabled = !isOnline;
    }
  }

  function triggerRestoreBackup(volumeName, backupID) {
    if (!isOnline) {
      showToast('Daemon offline', 'error');
      return;
    }
    promptDangerousAction({
      title: 'Destructive Volume Restore',
      message: `You are about to restore backup '${(backupID || '').slice(0, 8)}' into volume '${volumeName}'. This replaces ALL existing files in the volume. The connected container service will be stopped during restoration. This cannot be undone.`,
      forceTextRequired: false,
      onProceed: async () => {
        try {
          showToast(`Restoring ${volumeName}…`, 'info');
          await apiCall(`/volumes/${encodeURIComponent(volumeName)}/restore`, 'POST', {
            backup_id: backupID,
          });
          showToast(`Volume '${volumeName}' restored`, 'success');
          if (selectedVolumeName === volumeName) loadBackupsList(volumeName);
          refreshActivePanel(true);
        } catch (e) {
          showToast('Restore failed: ' + e.message, 'error');
        }
      },
    });
  }

  // ---------------------------------------------------------------------------
  // EVENTS TIMELINE
  // ---------------------------------------------------------------------------
  async function loadEventsTimeline({ silent } = {}) {
    const body = document.getElementById('timeline-events-body');
    const filterSel = document.getElementById('events-type-filter');
    const prevFilter = filterSel.value || 'all';
    const autoScroll = document.getElementById('chk-events-autoscroll').checked;
    const nearBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 64;

    if (!silent) {
      body.innerHTML = skeletonStackHtml(4);
    }

    try {
      let events = await apiCall('/events');
      events = events || [];
      eventsCache = events;

      // Rebuild type filter options (preserve selection when possible)
      const types = Array.from(new Set(events.map((e) => e.type).filter(Boolean))).sort();
      const currentOptions = Array.from(filterSel.options).map((o) => o.value);
      const nextTypes = ['all', ...types];
      if (currentOptions.join('|') !== nextTypes.join('|')) {
        filterSel.innerHTML = '';
        const allOpt = document.createElement('option');
        allOpt.value = 'all';
        allOpt.textContent = 'All types';
        filterSel.appendChild(allOpt);
        types.forEach((t) => {
          const opt = document.createElement('option');
          opt.value = t;
          opt.textContent = t;
          filterSel.appendChild(opt);
        });
        if (nextTypes.includes(prevFilter)) filterSel.value = prevFilter;
      }

      const typeFilter = filterSel.value || 'all';
      const filtered =
        typeFilter === 'all' ? events : events.filter((e) => e.type === typeFilter);

      document.getElementById('events-subtitle').textContent =
        filtered.length === events.length
          ? `${events.length} event${events.length === 1 ? '' : 's'}`
          : `${filtered.length} of ${events.length} events`;

      body.innerHTML = '';

      if (filtered.length === 0) {
        body.innerHTML = `
          <div class="empty-state">
            <p class="empty-title">${events.length === 0 ? 'No events logged' : 'No events of this type'}</p>
            <p class="text-muted">${
              events.length === 0
                ? 'Daemon state changes and deployments will appear on this timeline.'
                : 'Clear the type filter to see all events.'
            }</p>
          </div>`;
        setPanelError('events-error', null);
        updateLastUpdated();
        return;
      }

      filtered.forEach((e) => {
        const node = document.createElement('div');
        node.className = `timeline-node ${eventTypeClass(e.type)}`;
        let metaBlock = '';
        if (e.metadata_json && e.metadata_json !== '{}') {
          let pretty = e.metadata_json;
          try {
            pretty = JSON.stringify(JSON.parse(e.metadata_json), null, 2);
          } catch (_) {
            /* keep raw */
          }
          metaBlock = `
            <button type="button" class="btn-link text-sm mt-2 toggle-metadata">Inspect metadata »</button>
            <pre class="timeline-json hidden">${escapeHtml(pretty)}</pre>`;
        }
        node.innerHTML = `
          <div class="timeline-meta">${escapeHtml(formatDateTime(e.created_at))}</div>
          <div class="timeline-title"><span class="event-type-pill">${escapeHtml(e.type || 'event')}</span></div>
          <div class="timeline-desc">${escapeHtml(e.message || '')}</div>
          ${metaBlock}`;

        const toggle = node.querySelector('.toggle-metadata');
        if (toggle) {
          toggle.addEventListener('click', () => {
            const pre = node.querySelector('.timeline-json');
            pre.classList.toggle('hidden');
            toggle.textContent = pre.classList.contains('hidden')
              ? 'Inspect metadata »'
              : 'Hide metadata «';
          });
        }
        body.appendChild(node);
      });

      if (autoScroll && (nearBottom || !silent)) {
        body.scrollTop = body.scrollHeight;
      }

      setPanelError('events-error', null);
      updateLastUpdated();
    } catch (err) {
      setPanelError('events-error', `Failed to load events: ${err.message}`);
      if (!silent) {
        body.innerHTML = `<p class="text-danger text-center py-5">Failed to load events timeline: ${escapeHtml(err.message)}</p>`;
      }
    }
  }

  document.getElementById('btn-refresh-events').addEventListener('click', () => {
    loadEventsTimeline({ silent: false });
  });
  document.getElementById('events-type-filter').addEventListener('change', () => {
    // Re-render from cache if available to avoid flash; otherwise fetch
    if (eventsCache.length) {
      // Cheap path: re-run loader silently (still fetches for freshness)
      loadEventsTimeline({ silent: true });
    } else {
      loadEventsTimeline({ silent: false });
    }
  });

  // ---------------------------------------------------------------------------
  // Confirm modal
  // ---------------------------------------------------------------------------
  function promptDangerousAction({ title, message, forceTextRequired = false, onProceed }) {
    const modal = document.getElementById('modal-confirm-action');
    document.getElementById('confirm-title').textContent = title;
    document.getElementById('confirm-message').textContent = message;

    const chk = document.getElementById('chk-confirm-understand');
    chk.checked = false;

    const forceInputWrapper = document.getElementById('confirm-force-text-wrapper');
    const forceInput = document.getElementById('input-confirm-force');
    forceInput.value = '';

    if (forceTextRequired) forceInputWrapper.classList.remove('hidden');
    else forceInputWrapper.classList.add('hidden');

    const btnProceed = document.getElementById('btn-confirm-proceed');
    btnProceed.disabled = true;

    function validateInput() {
      const isChecked = chk.checked;
      const textMatch = !forceTextRequired || forceInput.value.trim().toUpperCase() === 'FORCE';
      btnProceed.disabled = !(isChecked && textMatch);
    }

    chk.onchange = validateInput;
    forceInput.oninput = validateInput;

    activateModalTrap(modal);
    currentConfirmCallback = onProceed;
  }

  function closeConfirmModal() {
    const modal = document.getElementById('modal-confirm-action');
    deactivateModalTrap(modal);
    currentConfirmCallback = null;
  }

  document.getElementById('btn-confirm-cancel').onclick = closeConfirmModal;
  document.getElementById('btn-close-confirm-modal').onclick = closeConfirmModal;
  document.getElementById('btn-confirm-proceed').onclick = () => {
    const cb = currentConfirmCallback;
    closeConfirmModal();
    if (cb) cb();
  };

  // ---------------------------------------------------------------------------
  // Toasts
  // ---------------------------------------------------------------------------
  function showToast(message, kind = 'info') {
    const host = document.getElementById('toast-host');
    const el = document.createElement('div');
    el.className = `toast toast-${kind}`;
    el.textContent = message;
    host.appendChild(el);
    requestAnimationFrame(() => el.classList.add('show'));
    setTimeout(() => {
      el.classList.remove('show');
      setTimeout(() => el.remove(), 280);
    }, 3200);
  }

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------
  function formatTime(isoString) {
    if (!isoString) return '—';
    const d = new Date(isoString);
    if (Number.isNaN(d.getTime())) return String(isoString);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }

  function formatDateTime(isoString) {
    if (!isoString) return '—';
    const d = new Date(isoString);
    if (Number.isNaN(d.getTime())) return String(isoString);
    const date = d.toLocaleDateString([], { month: 'short', day: 'numeric' });
    const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    return `${date} ${time}`;
  }

  function formatClock(d) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }

  function escapeHtml(unsafe) {
    return String(unsafe ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  function cssEscape(s) {
    // Simple id-safe: only used for building ids; prefer data attributes for lookup
    return String(s).replace(/[^a-zA-Z0-9_-]/g, '_');
  }

  function sanitizeBadgeClass(state) {
    const s = String(state || 'unknown')
      .toLowerCase()
      .replace(/[^a-z0-9_-]/g, '');
    return s || 'unknown';
  }

  function eventTypeClass(type) {
    const t = String(type || '').toLowerCase();
    if (t.includes('deploy')) return 'deploy';
    if (t.includes('volume')) return 'volume';
    if (t.includes('backup')) return 'backup';
    if (t.includes('restore')) return 'restore';
    if (t.includes('crash') || t.includes('fail')) return 'crash';
    if (t.includes('start') || t.includes('stop') || t.includes('restart')) return 'lifecycle';
    return '';
  }

  function capitalize(s) {
    return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
  }

  function skeletonTableRows(cols, rows) {
    let html = '';
    for (let i = 0; i < rows; i++) {
      html += `<tr class="skeleton-row"><td colspan="${cols}"><div class="skeleton skeleton-line"></div></td></tr>`;
    }
    return html;
  }

  function skeletonStackHtml(n) {
    let html = '<div class="skeleton-stack" aria-hidden="true">';
    for (let i = 0; i < n; i++) {
      html += `<div class="skeleton skeleton-line${i % 2 ? ' short' : ''}"></div>`;
    }
    html += '</div>';
    return html;
  }

  function skeletonServiceCards(n) {
    let html = '';
    for (let i = 0; i < n; i++) {
      html += `
        <div class="glass-card service-card skeleton-card" aria-hidden="true">
          <div class="skeleton skeleton-line"></div>
          <div class="skeleton skeleton-line short"></div>
          <div class="skeleton skeleton-line"></div>
        </div>`;
    }
    return html;
  }

  // ---------------------------------------------------------------------------
  // Boot
  // ---------------------------------------------------------------------------
  // Connecting state until first /status succeeds (not green until then)
  isOnline = false;
  document.getElementById('status-indicator').className = 'status-indicator connecting';
  document.getElementById('daemon-connection-text').textContent = 'Connecting…';
  document.getElementById('daemon-connection-detail').textContent = 'waiting for /status';
  document.getElementById('global-banner').classList.add('hidden');
  document.body.classList.remove('daemon-offline');
  document.querySelectorAll('.needs-online').forEach((el) => {
    el.disabled = true;
  });

  pollDaemonStatus();
  schedulePanelRefresh();
  handleRoute();
});
