/* Cairn Dashboard client JS application */

document.addEventListener('DOMContentLoaded', () => {
  // Navigation & Router
  const navItems = document.querySelectorAll('.nav-item');
  const panels = document.querySelectorAll('.tab-panel');
  const pageTitle = document.getElementById('page-title');
  const pageSubtitle = document.getElementById('page-subtitle');
  
  // Cache data
  let servicesCache = [];
  let selectedVolumeName = null;
  let activePollingInterval = null;

  // Router matching hash changes
  function handleRoute() {
    const hash = window.location.hash || '#/overview';
    
    // Deactivate current nav and panel
    navItems.forEach(nav => nav.classList.remove('active'));
    panels.forEach(panel => panel.classList.add('hidden'));

    // Reset details if needed
    if (hash !== '#/volumes') {
      selectedVolumeName = null;
      document.getElementById('backup-inspector-card').classList.add('hidden');
    }

    if (hash === '#/overview') {
      document.getElementById('nav-overview').classList.add('active');
      document.getElementById('panel-overview').classList.remove('panel-overview');
      document.getElementById('panel-overview').classList.remove('hidden');
      pageTitle.textContent = 'Overview';
      pageSubtitle.textContent = 'Control plane metrics and cluster status';
      loadOverviewData();
    } else if (hash === '#/services') {
      document.getElementById('nav-services').classList.add('active');
      document.getElementById('panel-services').classList.remove('hidden');
      pageTitle.textContent = 'Services';
      pageSubtitle.textContent = 'Run, scale, and monitor active service deployments';
      loadServicesData();
    } else if (hash === '#/volumes') {
      document.getElementById('nav-volumes').classList.add('active');
      document.getElementById('panel-volumes').classList.remove('hidden');
      pageTitle.textContent = 'Volumes & Backups';
      pageSubtitle.textContent = 'Persistent state partitions and logical database snapshots';
      loadVolumesData();
    } else if (hash === '#/events') {
      document.getElementById('nav-events').classList.add('active');
      document.getElementById('panel-events').classList.remove('hidden');
      pageTitle.textContent = 'Events Timeline';
      pageSubtitle.textContent = 'Audit log timeline of daemon state changes and deployments';
      loadEventsTimeline();
    }
  }

  window.addEventListener('hashchange', handleRoute);

  // Status Polling Loop
  async function pollDaemonStatus() {
    try {
      const res = await fetch('/status');
      if (!res.ok) throw new Error('Daemon status failed');
      const data = await res.json();
      
      // Update header widgets
      document.getElementById('stat-uptime').textContent = data.uptime;
      document.getElementById('stat-active').textContent = data.active_services;
      document.getElementById('stat-storage').textContent = data.storage_usage;
      document.getElementById('stat-version').textContent = data.version;

      // Sidebar connection
      const ind = document.querySelector('.status-indicator');
      ind.className = 'status-indicator online';
      document.getElementById('daemon-connection-text').textContent = 'Connected';
    } catch (err) {
      console.error('Connection to cairnd lost:', err);
      const ind = document.querySelector('.status-indicator');
      ind.className = 'status-indicator offline';
      document.getElementById('daemon-connection-text').textContent = 'Disconnected';
    }
  }

  // Initial daemon stats call
  pollDaemonStatus();
  // Poll every 5 seconds
  activePollingInterval = setInterval(pollDaemonStatus, 5000);

  // API Call Wrapper
  async function apiCall(url, method = 'GET', body = null) {
    const options = { method };
    if (body) {
      options.headers = { 'Content-Type': 'application/json' };
      options.body = JSON.stringify(body);
    }
    const res = await fetch(url, options);
    
    if (res.status === 409) {
      // Return a special conflict status for safety prompt
      const data = await res.json();
      return { conflict: true, status: 409, message: data.error };
    }
    
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error || `HTTP error ${res.status}`);
    }
    
    if (res.status === 204) return null;
    return await res.json();
  }

  // OVERVIEW PANELS
  async function loadOverviewData() {
    try {
      const [services, events] = await Promise.all([
        apiCall('/services'),
        apiCall('/events')
      ]);

      // Update counters
      document.getElementById('overview-services-count').textContent = services.length;
      
      // Volumes counter calculation
      let volCount = 0;
      services.forEach(s => {
        // we can fetch volumes list separately if needed, but let's query volumes directly
      });
      const volumes = await apiCall('/volumes');
      document.getElementById('overview-volumes-count').textContent = volumes.length;

      // Backups count total
      let totalBackups = 0;
      for (const vol of volumes) {
        try {
          const backups = await apiCall(`/volumes/${vol.name}/backups`);
          totalBackups += backups.length;
        } catch (e) {}
      }
      document.getElementById('overview-backups-count').textContent = totalBackups;

      // Render Recent Services
      const tbody = document.querySelector('#table-recent-services tbody');
      tbody.innerHTML = '';
      if (services.length === 0) {
        tbody.innerHTML = `<tr><td colspan="4" class="text-center">No services deployed yet.</td></tr>`;
      } else {
        services.slice(0, 5).forEach(s => {
          const tr = document.createElement('tr');
          tr.innerHTML = `
            <td><span class="font-bold">${s.name}</span></td>
            <td><span class="badge badge-secondary" style="background-color:rgba(255,255,255,0.05);color:var(--text-muted);border:none;">${s.kind}</span></td>
            <td><span class="badge ${s.actual_state}">${s.actual_state}</span></td>
            <td><span class="text-mono">${s.route || 'N/A'}</span></td>
          `;
          tr.style.cursor = 'pointer';
          tr.addEventListener('click', () => showServiceDetail(s.name));
          tbody.appendChild(tr);
        });
      }

      // Render Recent Events
      const evList = document.getElementById('overview-events-list');
      evList.innerHTML = '';
      if (events.length === 0) {
        evList.innerHTML = `<p class="text-muted text-center py-4">No recent events registered.</p>`;
      } else {
        events.slice(0, 5).forEach(e => {
          const div = document.createElement('div');
          const typeClass = e.type.toLowerCase();
          div.className = `recent-event-item ${typeClass.includes('deploy') ? 'deploy' : typeClass.includes('backup') ? 'backup' : typeClass.includes('restore') ? 'restore' : typeClass.includes('crash') ? 'crash' : ''}`;
          div.innerHTML = `
            <span class="event-time">${formatTime(e.created_at)}</span>
            <span class="event-msg"><strong>[${e.type}]</strong> ${e.message}</span>
          `;
          evList.appendChild(div);
        });
      }
    } catch (err) {
      console.error('Error loading overview data:', err);
    }
  }

  // SERVICES TAB
  async function loadServicesData() {
    const container = document.getElementById('services-container');
    const searchVal = document.getElementById('input-search-services').value.toLowerCase();
    
    try {
      const services = await apiCall('/services');
      servicesCache = services;
      
      container.innerHTML = '';
      const filtered = services.filter(s => s.name.toLowerCase().includes(searchVal) || s.kind.toLowerCase().includes(searchVal));

      if (filtered.length === 0) {
        container.innerHTML = `
          <div class="glass-card text-center py-5" style="grid-column: 1/-1;">
            <p class="text-muted">No services found matching the criteria.</p>
          </div>
        `;
        return;
      }

      filtered.forEach(s => {
        const card = document.createElement('div');
        card.className = 'glass-card service-card';
        card.innerHTML = `
          <div class="service-card-header">
            <div class="service-card-title">
              <h3>${s.name}</h3>
              <p>${s.id.slice(0, 8)}</p>
            </div>
            <span class="badge ${s.actual_state}">${s.actual_state}</span>
          </div>
          <div class="service-info-row">
            <span class="service-info-label">Kind:</span>
            <span>${s.kind}</span>
          </div>
          <div class="service-info-row">
            <span class="service-info-label">Route:</span>
            <span class="text-mono">${s.route || 'N/A'}</span>
          </div>
          <div class="service-card-footer">
            <button class="btn btn-secondary btn-sm" id="btn-inspect-${s.name}">Inspect Details</button>
          </div>
        `;
        
        card.querySelector(`#btn-inspect-${s.name}`).addEventListener('click', () => showServiceDetail(s.name));
        container.appendChild(card);
      });
    } catch (err) {
      console.error('Failed to load services:', err);
      container.innerHTML = `<div class="glass-card text-center py-4" style="grid-column: 1/-1;"><p class="text-danger">Failed to load services: ${err.message}</p></div>`;
    }
  }

  // Service search listener
  document.getElementById('input-search-services').addEventListener('input', loadServicesData);

  // SERVICE DETAILS DRAWER/MODAL
  let activeLogsInterval = null;

  async function showServiceDetail(serviceName) {
    const modal = document.getElementById('modal-service-detail');
    modal.classList.remove('hidden');

    // Clean active intervals
    if (activeLogsInterval) clearInterval(activeLogsInterval);

    try {
      const s = await apiCall(`/services/${serviceName}`);
      
      document.getElementById('detail-service-name').textContent = s.name;
      const statusBadge = document.getElementById('detail-service-status');
      statusBadge.textContent = s.actual_state;
      statusBadge.className = `badge ${s.actual_state}`;

      document.getElementById('detail-service-id').textContent = s.id;
      document.getElementById('detail-service-kind').textContent = s.kind;
      document.getElementById('detail-service-runtime').textContent = s.runtime_backend;
      document.getElementById('detail-service-runtime-id').textContent = s.runtime_id || 'N/A';
      document.getElementById('detail-service-route').textContent = s.route || 'N/A';

      // Load Deploys list
      loadDeployHistory(s.name);

      // Load Logs first time
      loadConsoleLogs(s.name);
      
      // Auto refresh logs every 3 seconds
      activeLogsInterval = setInterval(() => {
        loadConsoleLogs(s.name);
      }, 3000);

      // Wire service action buttons
      setupServiceActions(s.name);

    } catch (err) {
      console.error('Failed to inspect service:', err);
    }
  }

  // Setup service actions (start/stop/restart)
  function setupServiceActions(serviceName) {
    const btnStart = document.getElementById('btn-action-start');
    const btnStop = document.getElementById('btn-action-stop');
    const btnRestart = document.getElementById('btn-action-restart');
    const btnRefresh = document.getElementById('btn-logs-refresh');
    const btnClear = document.getElementById('btn-logs-clear');
    
    // Clear old event listeners
    const newStart = btnStart.cloneNode(true);
    const newStop = btnStop.cloneNode(true);
    const newRestart = btnRestart.cloneNode(true);
    const newRefresh = btnRefresh.cloneNode(true);
    
    btnStart.parentNode.replaceChild(newStart, btnStart);
    btnStop.parentNode.replaceChild(newStop, btnStop);
    btnRestart.parentNode.replaceChild(newRestart, btnRestart);
    btnRefresh.parentNode.replaceChild(newRefresh, btnRefresh);

    newStart.addEventListener('click', async () => {
      newStart.disabled = true;
      try {
        await apiCall(`/services/${serviceName}/start`, 'POST');
        showServiceDetail(serviceName);
      } catch (e) { alert(e.message); }
      newStart.disabled = false;
    });

    newStop.addEventListener('click', async () => {
      newStop.disabled = true;
      try {
        await apiCall(`/services/${serviceName}/stop`, 'POST');
        showServiceDetail(serviceName);
      } catch (e) { alert(e.message); }
      newStop.disabled = false;
    });

    newRestart.addEventListener('click', async () => {
      newRestart.disabled = true;
      try {
        await apiCall(`/services/${serviceName}/restart`, 'POST');
        showServiceDetail(serviceName);
      } catch (e) { alert(e.message); }
      newRestart.disabled = false;
    });

    newRefresh.addEventListener('click', () => {
      loadConsoleLogs(serviceName);
    });

    btnClear.onclick = () => {
      document.getElementById('service-logs-console').innerHTML = '<div class="log-line text-muted">Console cleared.</div>';
    };
  }

  // Load Deploy History
  async function loadDeployHistory(serviceName) {
    const historyList = document.getElementById('detail-deploys-list');
    historyList.innerHTML = '<p class="text-muted">Loading deploy history...</p>';

    try {
      const deploys = await apiCall(`/services/${serviceName}/deploys`);
      historyList.innerHTML = '';
      if (deploys.length === 0) {
        historyList.innerHTML = '<p class="text-muted">No deployments found.</p>';
        return;
      }

      deploys.forEach(d => {
        const item = document.createElement('div');
        item.className = 'deploy-history-item';
        
        // Show status badge
        const badge = `<span class="badge ${d.status === 'success' ? 'running' : d.status === 'failed' ? 'stopped' : 'starting'}">${d.status}</span>`;
        
        // Rollback button if it is a completed deploy and not current
        const svc = servicesCache.find(x => x.name === serviceName);
        const isCurrent = svc && svc.current_deploy_id === d.id;
        const rollbackBtn = (!isCurrent && d.status === 'success') 
          ? `<button class="btn btn-secondary btn-sm" id="btn-rollback-${d.id}" style="padding:4px 8px;font-size:11px;">Rollback</button>`
          : (isCurrent ? '<span class="text-muted text-sm font-bold">Active</span>' : '');

        item.innerHTML = `
          <div class="deploy-history-meta">
            <span class="deploy-version">Deploy ${d.id.slice(0, 8)} (v${d.version})</span>
            <span class="deploy-date">${formatDateTime(d.created_at)}</span>
            ${d.failure_reason ? `<span class="text-danger" style="font-size:11px;">Reason: ${d.failure_reason}</span>` : ''}
          </div>
          <div style="display:flex;align-items:center;gap:12px;">
            ${badge}
            ${rollbackBtn}
          </div>
        `;

        const btn = item.querySelector(`#btn-rollback-${d.id}`);
        if (btn) {
          btn.addEventListener('click', () => triggerRollback(serviceName, d.id));
        }

        historyList.appendChild(item);
      });
    } catch (err) {
      console.error('Failed to load history:', err);
      historyList.innerHTML = `<p class="text-danger">Failed to load history: ${err.message}</p>`;
    }
  }

  // Rollback Action
  async function triggerRollback(serviceName, deployID) {
    try {
      const res = await apiCall(`/services/${serviceName}/rollback`, 'POST', { deploy_id: deployID, force: false });
      
      if (res.conflict) {
        // Rollback is unsafe because database schema or state has changed!
        promptDangerousAction({
          title: 'Unsafe Rollback Detected',
          message: res.message,
          forceTextRequired: true,
          onProceed: async () => {
            const finalRes = await apiCall(`/services/${serviceName}/rollback`, 'POST', { deploy_id: deployID, force: true });
            if (finalRes.error) {
              alert('Rollback failed: ' + finalRes.error);
            } else {
              alert('Force Rollback started successfully!');
              closeServiceModal();
              loadOverviewData();
            }
          }
        });
      } else {
        alert('Rollback initiated successfully!');
        closeServiceModal();
        loadOverviewData();
      }
    } catch (err) {
      alert('Rollback failed: ' + err.message);
    }
  }

  // Load logs
  async function loadConsoleLogs(serviceName) {
    const consolePane = document.getElementById('service-logs-console');
    try {
      const res = await fetch(`/services/${serviceName}/logs`);
      if (!res.ok) throw new Error('Logs failed');
      const text = await res.text();
      
      if (text.trim() === '') {
        consolePane.innerHTML = '<div class="log-line text-muted">No logs recorded yet.</div>';
        return;
      }

      // Convert text to clean lines
      const lines = text.split('\n');
      consolePane.innerHTML = '';
      lines.forEach(l => {
        if (!l.trim()) return;
        
        const lineDiv = document.createElement('div');
        lineDiv.className = 'log-line';
        
        // Parse minidocker prefix time if matches format YYYY-MM-DDTHH:MM:SS.mmm
        const match = l.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z?)\s(.*)$/);
        if (match) {
          lineDiv.innerHTML = `<span class="log-timestamp">${formatTime(match[1])}</span><span>${escapeHtml(match[2])}</span>`;
        } else {
          lineDiv.textContent = l;
        }
        consolePane.appendChild(lineDiv);
      });
      
      // scroll to bottom
      consolePane.scrollTop = consolePane.scrollHeight;

    } catch (err) {
      consolePane.innerHTML = `<div class="log-line text-danger">Failed to fetch logs: ${err.message}</div>`;
    }
  }

  // Log Wrap button toggle
  const btnWrap = document.getElementById('btn-logs-wrap');
  btnWrap.addEventListener('click', () => {
    const pane = document.getElementById('service-logs-console');
    pane.classList.toggle('nowrap');
    btnWrap.classList.toggle('active');
  });

  // Close service modal
  function closeServiceModal() {
    document.getElementById('modal-service-detail').classList.add('hidden');
    if (activeLogsInterval) {
      clearInterval(activeLogsInterval);
      activeLogsInterval = null;
    }
  }

  document.getElementById('btn-close-service-modal').addEventListener('click', closeServiceModal);

  // VOLUMES & BACKUPS TAB
  async function loadVolumesData() {
    const tbody = document.getElementById('volumes-list-body');
    tbody.innerHTML = '<tr><td colspan="5" class="text-center">Loading volumes...</td></tr>';
    
    try {
      const volumes = await apiCall('/volumes');
      tbody.innerHTML = '';

      if (volumes.length === 0) {
        tbody.innerHTML = '<tr><td colspan="5" class="text-center">No persistent volumes defined.</td></tr>';
        return;
      }

      volumes.forEach(v => {
        const tr = document.createElement('tr');
        tr.id = `volume-row-${v.name}`;
        tr.innerHTML = `
          <td><span class="font-bold">${v.name}</span></td>
          <td><span class="badge badge-secondary" style="background-color:rgba(255,255,255,0.05);color:var(--text-muted);border:none;">${v.attached_service_id ? v.attached_service_id.slice(0, 8) : 'Unattached'}</span></td>
          <td><span class="text-mono">${v.mount_path || 'N/A'}</span></td>
          <td><span class="text-muted text-sm">${v.host_path}</span></td>
          <td>
            <button class="btn btn-secondary btn-sm" id="btn-inspect-vol-${v.name}">Inspect Backups</button>
          </td>
        `;
        
        tr.querySelector(`#btn-inspect-vol-${v.name}`).addEventListener('click', () => selectVolume(v.name));
        tbody.appendChild(tr);
      });

      // Restore highlight if selected previously
      if (selectedVolumeName) {
        selectVolume(selectedVolumeName);
      }
    } catch (err) {
      console.error('Failed to load volumes:', err);
      tbody.innerHTML = `<tr><td colspan="5" class="text-danger text-center">Error: ${err.message}</td></tr>`;
    }
  }

  // Inspect Backups for selected volume
  async function selectVolume(volumeName) {
    selectedVolumeName = volumeName;
    
    // Highlight table row
    const rows = document.querySelectorAll('#volumes-list-body tr');
    rows.forEach(r => r.classList.remove('volume-row-selected'));
    
    const selectedRow = document.getElementById(`volume-row-${volumeName}`);
    if (selectedRow) selectedRow.classList.add('volume-row-selected');

    // Show backups card
    const card = document.getElementById('backup-inspector-card');
    card.classList.remove('hidden');

    document.getElementById('backup-subtitle').textContent = `Volume: ${volumeName}`;
    
    const btnCreate = document.getElementById('btn-create-backup');
    btnCreate.classList.remove('hidden');
    
    // Rebind create backup button
    const newBtn = btnCreate.cloneNode(true);
    btnCreate.parentNode.replaceChild(newBtn, btnCreate);
    newBtn.addEventListener('click', () => triggerCreateBackup(volumeName));

    loadBackupsList(volumeName);
  }

  // Load backups list
  async function loadBackupsList(volumeName) {
    const listBody = document.getElementById('backups-list-body');
    listBody.innerHTML = '<p class="text-muted text-center py-4">Loading backups...</p>';

    try {
      const backups = await apiCall(`/volumes/${volumeName}/backups`);
      listBody.innerHTML = '';

      if (backups.length === 0) {
        listBody.innerHTML = '<p class="text-muted text-center py-4">No backups created for this volume.</p>';
        return;
      }

      backups.forEach(b => {
        const item = document.createElement('div');
        item.className = 'backup-item';
        
        const sizeMb = (b.size_bytes / (1024 * 1024)).toFixed(2);
        
        item.innerHTML = `
          <div class="backup-meta-info">
            <span class="backup-id">${b.id.slice(0, 12)}...</span>
            <span class="backup-subtext">Status: <strong>${b.status}</strong> | Size: ${sizeMb} MB</span>
            <span class="backup-subtext">Created: ${formatDateTime(b.created_at)}</span>
            <span class="backup-subtext text-mono" style="font-size:10px;">SHA256: ${b.checksum.slice(0, 16)}</span>
          </div>
          <div>
            <button class="btn btn-secondary btn-sm" id="btn-restore-${b.id}" style="padding:4px 8px;font-size:11px;">Restore</button>
          </div>
        `;

        item.querySelector(`#btn-restore-${b.id}`).addEventListener('click', () => triggerRestoreBackup(volumeName, b.id));
        listBody.appendChild(item);
      });
    } catch (err) {
      listBody.innerHTML = `<p class="text-danger text-center py-4">Error loading backups: ${err.message}</p>`;
    }
  }

  // Create Backup Action
  async function triggerCreateBackup(volumeName) {
    const btnCreate = document.getElementById('btn-create-backup');
    btnCreate.disabled = true;
    
    try {
      alert(`Creating volume snapshot backup for '${volumeName}'...`);
      const backup = await apiCall(`/volumes/${volumeName}/backups`, 'POST');
      alert(`Backup '${backup.id}' created successfully! Checksum: ${backup.checksum.slice(0, 12)}`);
      loadBackupsList(volumeName);
    } catch (err) {
      alert('Backup failed: ' + err.message);
    }
    btnCreate.disabled = false;
  }

  // Restore Backup Action
  async function triggerRestoreBackup(volumeName, backupID) {
    promptDangerousAction({
      title: 'Destructive Volume Restore',
      message: `You are about to restore backup '${backupID.slice(0, 8)}' into volume '${volumeName}'. This replaces ALL existing files in the volume. The connected container service will be stopped during restoration. This is destructive and cannot be undone.`,
      forceTextRequired: false,
      onProceed: async () => {
        try {
          alert(`Initiating restore path for '${volumeName}'...`);
          await apiCall(`/volumes/${volumeName}/restore`, 'POST', { backup_id: backupID });
          alert(`Volume '${volumeName}' restored successfully!`);
          loadOverviewData();
          if (selectedVolumeName === volumeName) loadBackupsList(volumeName);
        } catch (e) {
          alert('Restore failed: ' + e.message);
        }
      }
    });
  }

  // EVENTS TIMELINE
  async function loadEventsTimeline() {
    const body = document.getElementById('timeline-events-body');
    body.innerHTML = '<p class="text-muted text-center py-5">Loading events timeline...</p>';

    try {
      const events = await apiCall('/events');
      body.innerHTML = '';

      if (events.length === 0) {
        body.innerHTML = '<p class="text-muted text-center py-5">No events logged in system database.</p>';
        return;
      }

      events.forEach(e => {
        const node = document.createElement('div');
        const typeClass = e.type.toLowerCase();
        node.className = `timeline-node ${typeClass.includes('deploy') ? 'deploy' : typeClass.includes('volume') ? 'volume' : typeClass.includes('backup') ? 'backup' : typeClass.includes('restore') ? 'restore' : typeClass.includes('crash') ? 'crash' : ''}`;
        
        node.innerHTML = `
          <div class="timeline-meta">${formatDateTime(e.created_at)}</div>
          <div class="timeline-title">${e.type}</div>
          <div class="timeline-desc">${e.message}</div>
          ${e.metadata_json && e.metadata_json !== '{}' ? `
            <a href="#" class="btn-link text-sm mt-2 d-inline-block toggle-metadata" id="btn-meta-${e.id}">Inspect Event Metadata &raquo;</a>
            <pre class="timeline-json hidden" id="json-meta-${e.id}">${escapeHtml(JSON.stringify(JSON.parse(e.metadata_json), null, 2))}</pre>
          ` : ''}
        `;
        
        const toggle = node.querySelector(`#btn-meta-${e.id}`);
        if (toggle) {
          toggle.addEventListener('click', (ev) => {
            ev.preventDefault();
            const pre = node.querySelector(`#json-meta-${e.id}`);
            pre.classList.toggle('hidden');
            toggle.innerHTML = pre.classList.contains('hidden') ? 'Inspect Event Metadata &raquo;' : 'Hide Metadata &laquo;';
          });
        }

        body.appendChild(node);
      });
    } catch (err) {
      body.innerHTML = `<p class="text-danger text-center py-5">Failed to load events timeline: ${err.message}</p>`;
    }
  }

  document.getElementById('btn-refresh-events').addEventListener('click', loadEventsTimeline);

  // GLOBAL DANGEROUS ACTION MODAL CONFIRMATION
  let currentConfirmCallback = null;

  function promptDangerousAction({ title, message, forceTextRequired = false, onProceed }) {
    const modal = document.getElementById('modal-confirm-action');
    document.getElementById('confirm-title').textContent = title;
    document.getElementById('confirm-message').textContent = message;

    const chk = document.getElementById('chk-confirm-understand');
    chk.checked = false;

    const forceInputWrapper = document.getElementById('confirm-force-text-wrapper');
    const forceInput = document.getElementById('input-confirm-force');
    forceInput.value = '';

    if (forceTextRequired) {
      forceInputWrapper.classList.remove('hidden');
    } else {
      forceInputWrapper.classList.add('hidden');
    }

    const btnProceed = document.getElementById('btn-confirm-proceed');
    btnProceed.disabled = true;

    // Checkbox and text validation
    function validateInput() {
      const isChecked = chk.checked;
      const textMatch = !forceTextRequired || (forceInput.value.trim().toUpperCase() === 'FORCE');
      btnProceed.disabled = !(isChecked && textMatch);
    }

    chk.onchange = validateInput;
    forceInput.oninput = validateInput;

    modal.classList.remove('hidden');
    currentConfirmCallback = onProceed;
  }

  function closeConfirmModal() {
    document.getElementById('modal-confirm-action').classList.add('hidden');
    currentConfirmCallback = null;
  }

  document.getElementById('btn-confirm-cancel').onclick = closeConfirmModal;
  document.getElementById('btn-close-confirm-modal').onclick = closeConfirmModal;
  
  document.getElementById('btn-confirm-proceed').onclick = () => {
    if (currentConfirmCallback) {
      currentConfirmCallback();
    }
    closeConfirmModal();
  };

  // Helper Utility functions
  function formatTime(isoString) {
    const d = new Date(isoString);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }

  function formatDateTime(isoString) {
    const d = new Date(isoString);
    const date = d.toLocaleDateString([], { month: 'short', day: 'numeric' });
    const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    return `${date} ${time}`;
  }

  function escapeHtml(unsafe) {
    return unsafe
         .replace(/&/g, "&amp;")
         .replace(/</g, "&lt;")
         .replace(/>/g, "&gt;")
         .replace(/"/g, "&quot;")
         .replace(/'/g, "&#039;");
  }

  // Init Route
  handleRoute();
});
