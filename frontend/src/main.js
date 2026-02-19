/* ============================================
   Android ADB Network Inspector — Main JS
   Vanilla JS — HTTP API + Server-Sent Events
   ============================================ */

(function () {
    'use strict';

    // ---- State ----
    const state = {
        devices: [],
        selectedDevice: null,
        selectedPacket: null,
        selectedRowId: null,
        activeTab: 'packets',
        filter: '',
        captures: {},
        autoScroll: true,
        maxTableRows: 2000,
        packetCount: 0,
        connectionCount: 0,
    };

    // ---- DOM Refs ----
    const $ = (sel) => document.querySelector(sel);
    const $$ = (sel) => document.querySelectorAll(sel);

    const dom = {
        deviceList: $('#device-list'),
        deviceCountBadge: $('#device-count-badge'),
        captureBadge: $('#capture-badge'),
        packetsBody: $('#packets-body'),
        connectionsBody: $('#connections-body'),
        packetsEmpty: $('#packets-empty'),
        connectionsEmpty: $('#connections-empty'),
        detailPanel: $('#detail-panel'),
        detailContent: $('#detail-content'),
        searchInput: $('#search-input'),
        statusAdb: $('#status-adb'),
        statusPackets: $('#status-packets'),
        statusConnections: $('#status-connections'),
        statusPool: $('#status-pool'),
        statusMemory: $('#status-memory'),
        tabPacketsCount: $('#tab-packets-count'),
        tabConnectionsCount: $('#tab-connections-count'),
        btnAutoscroll: $('#btn-autoscroll'),
    };

    // ---- Toast Notifications ----
    let toastContainer = null;
    function showToast(message, type = '') {
        if (!toastContainer) {
            toastContainer = document.createElement('div');
            toastContainer.className = 'toast-container';
            document.body.appendChild(toastContainer);
        }
        const toast = document.createElement('div');
        toast.className = 'toast' + (type ? ` toast-${type}` : '');
        toast.textContent = message;
        toastContainer.appendChild(toast);
        setTimeout(() => toast.remove(), 2000);
    }

    // ---- Clipboard ----
    async function copyToClipboard(text) {
        try {
            await navigator.clipboard.writeText(text);
            showToast('Copied: ' + (text.length > 40 ? text.slice(0, 40) + '…' : text), 'success');
            return true;
        } catch {
            showToast('Copy failed', 'error');
            return false;
        }
    }

    // ---- HTTP API helpers ----
    async function api(path, opts = {}) {
        const resp = await fetch('/api' + path, {
            headers: { 'Content-Type': 'application/json' },
            ...opts,
        });
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({ error: resp.statusText }));
            throw new Error(err.error || resp.statusText);
        }
        return resp.json();
    }

    function apiGet(path) { return api(path); }
    function apiPost(path) { return api(path, { method: 'POST' }); }

    // ---- Server-Sent Events ----
    let eventSource = null;

    function connectSSE() {
        if (eventSource) eventSource.close();

        eventSource = new EventSource('/api/events');

        eventSource.addEventListener('device:connected', (e) => {
            const evt = JSON.parse(e.data);
            if (evt.device) addOrUpdateDevice(evt.device);
        });

        eventSource.addEventListener('device:disconnected', (e) => {
            const evt = JSON.parse(e.data);
            removeDevice(evt.serial);
        });

        eventSource.addEventListener('device:state_changed', (e) => {
            const evt = JSON.parse(e.data);
            if (evt.device) addOrUpdateDevice(evt.device);
        });

        eventSource.addEventListener('packet:new', (e) => {
            const pkt = JSON.parse(e.data);
            addPacketRow(pkt);
        });

        eventSource.addEventListener('connection:new', (e) => {
            const conn = JSON.parse(e.data);
            addConnectionRow(conn);
        });

        eventSource.addEventListener('capture:stopped', (e) => {
            const data = JSON.parse(e.data);
            delete state.captures[data.serial];
            renderDeviceList();
            updateCaptureBadge();
        });

        eventSource.addEventListener('devices:refreshed', (e) => {
            const devices = JSON.parse(e.data);
            state.devices = devices || [];
            renderDeviceList();
        });

        eventSource.addEventListener('store:cleared', () => {
            dom.packetsBody.innerHTML = '';
            dom.connectionsBody.innerHTML = '';
            dom.packetsEmpty.classList.remove('hidden');
            dom.connectionsEmpty.classList.remove('hidden');
            state.packetCount = 0;
            state.connectionCount = 0;
            updateTabBadges();
        });

        eventSource.addEventListener('ping', () => {});

        eventSource.onerror = () => {
            console.warn('SSE connection lost, reconnecting...');
        };
    }

    // ---- Initialization ----
    async function init() {
        setupEventListeners();
        connectSSE();

        try {
            const data = await apiGet('/adb/version');
            dom.statusAdb.textContent = `ADB: v${data.version}`;
        } catch (e) {
            dom.statusAdb.textContent = 'ADB: not connected';
        }

        await refreshDevices();
        setInterval(updateStatus, 2000);
    }

    // ---- Event Listeners ----
    function setupEventListeners() {
        $('#btn-refresh').addEventListener('click', refreshDevices);
        $('#btn-start-all').addEventListener('click', startAllCaptures);
        $('#btn-stop-all').addEventListener('click', stopAllCaptures);
        $('#btn-clear').addEventListener('click', clearData);
        $('#btn-close-detail').addEventListener('click', closeDetail);
        $('#btn-export').addEventListener('click', exportData);

        dom.btnAutoscroll.addEventListener('click', () => {
            state.autoScroll = !state.autoScroll;
            dom.btnAutoscroll.classList.toggle('active', state.autoScroll);
            showToast(state.autoScroll ? 'Auto-scroll ON' : 'Auto-scroll OFF');
        });

        dom.searchInput.addEventListener('input', (e) => {
            state.filter = e.target.value.toLowerCase();
            applyFilterToTable(dom.packetsBody);
            applyFilterToTable(dom.connectionsBody);
        });

        $$('.tab').forEach(tab => {
            tab.addEventListener('click', () => switchTab(tab.dataset.tab));
        });

        // Keyboard shortcuts
        document.addEventListener('keydown', (e) => {
            // Escape — close detail panel
            if (e.key === 'Escape') {
                closeDetail();
                return;
            }
            // Ctrl+K — focus search
            if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
                e.preventDefault();
                dom.searchInput.focus();
                dom.searchInput.select();
                return;
            }
            // Ctrl+J — toggle auto-scroll
            if ((e.ctrlKey || e.metaKey) && e.key === 'j') {
                e.preventDefault();
                dom.btnAutoscroll.click();
                return;
            }
            // Ctrl+L — clear data
            if ((e.ctrlKey || e.metaKey) && e.key === 'l') {
                e.preventDefault();
                clearData();
                return;
            }
            // 1/2 — switch tabs
            if (!e.ctrlKey && !e.metaKey && !e.altKey && document.activeElement !== dom.searchInput) {
                if (e.key === '1') switchTab('packets');
                if (e.key === '2') switchTab('connections');
            }
        });
    }

    // ---- Device Management ----
    async function refreshDevices() {
        try {
            const devices = await apiPost('/devices/refresh');
            state.devices = devices || [];
            renderDeviceList();
        } catch (e) {
            console.error('Failed to refresh devices:', e);
        }
    }

    function addOrUpdateDevice(device) {
        if (!device) return;
        const idx = state.devices.findIndex(d => d.serial === device.serial);
        if (idx >= 0) {
            state.devices[idx] = device;
        } else {
            state.devices.push(device);
        }
        renderDeviceList();
    }

    function removeDevice(serial) {
        state.devices = state.devices.filter(d => d.serial !== serial);
        delete state.captures[serial];
        if (state.selectedDevice === serial) {
            state.selectedDevice = null;
        }
        renderDeviceList();
    }

    function renderDeviceList() {
        if (state.devices.length === 0) {
            dom.deviceList.innerHTML = '<div class="empty-state">No devices connected</div>';
            dom.deviceCountBadge.textContent = '0 devices';
            return;
        }

        dom.deviceCountBadge.textContent = `${state.devices.length} device${state.devices.length !== 1 ? 's' : ''}`;

        dom.deviceList.innerHTML = state.devices.map(d => {
            const isCapturing = !!state.captures[d.serial];
            const statusClass = isCapturing ? 'capturing' :
                d.state === 'device' ? 'online' :
                d.state === 'unauthorized' ? 'unauthorized' : 'offline';
            const selected = state.selectedDevice === d.serial ? 'selected' : '';
            const model = d.model || d.product || 'Unknown';
            const btnLabel = isCapturing ? '&#9632;' : '&#9654;';
            const btnClass = isCapturing ? 'active' : '';

            return `
                <div class="device-item ${selected}" data-serial="${d.serial}">
                    <div class="device-status ${statusClass}"></div>
                    <div class="device-info">
                        <div class="device-serial">${escapeHtml(d.serial)}</div>
                        <div class="device-model">${escapeHtml(model)} · ${d.state}</div>
                    </div>
                    <button class="device-capture-btn ${btnClass}" data-serial="${d.serial}" title="${isCapturing ? 'Stop' : 'Start'} Capture">
                        ${btnLabel}
                    </button>
                </div>
            `;
        }).join('');

        dom.deviceList.querySelectorAll('.device-item').forEach(el => {
            el.addEventListener('click', (e) => {
                if (e.target.closest('.device-capture-btn')) return;
                state.selectedDevice = el.dataset.serial;
                renderDeviceList();
            });
        });

        dom.deviceList.querySelectorAll('.device-capture-btn').forEach(btn => {
            btn.addEventListener('click', (e) => {
                e.stopPropagation();
                toggleCapture(btn.dataset.serial);
            });
        });

        updateCaptureBadge();
    }

    async function toggleCapture(serial) {
        if (state.captures[serial]) {
            await apiPost('/capture/stop/' + encodeURIComponent(serial));
            delete state.captures[serial];
        } else {
            try {
                await apiPost('/capture/start/' + encodeURIComponent(serial));
                state.captures[serial] = true;
            } catch (e) {
                console.error('Failed to start capture:', e);
                showToast('Capture failed: ' + e.message, 'error');
            }
        }
        renderDeviceList();
        updateCaptureBadge();
    }

    async function startAllCaptures() {
        try {
            await apiPost('/capture/start-all');
            state.devices.forEach(d => {
                if (d.state === 'device') state.captures[d.serial] = true;
            });
            renderDeviceList();
            updateCaptureBadge();
            showToast('Capture started on all devices', 'success');
        } catch (e) {
            console.error('Failed to start all captures:', e);
            showToast('Failed to start captures', 'error');
        }
    }

    async function stopAllCaptures() {
        await apiPost('/capture/stop-all');
        state.captures = {};
        renderDeviceList();
        updateCaptureBadge();
        showToast('All captures stopped');
    }

    function updateCaptureBadge() {
        const count = Object.keys(state.captures).length;
        if (count > 0) {
            dom.captureBadge.textContent = `Capturing: ${count}`;
            dom.captureBadge.className = 'badge badge-active';
        } else {
            dom.captureBadge.textContent = 'Idle';
            dom.captureBadge.className = 'badge badge-inactive';
        }
    }

    // ---- Tab Badge Counts ----
    function updateTabBadges() {
        dom.tabPacketsCount.textContent = state.packetCount;
        dom.tabConnectionsCount.textContent = state.connectionCount;
    }

    // ---- Row Selection ----
    function selectRow(tbody, rowId) {
        // Remove previous selection in this tbody
        const prev = tbody.querySelector('tr.selected');
        if (prev) prev.classList.remove('selected');

        // Mark new selection
        if (rowId) {
            const row = tbody.querySelector(`tr[data-id="${rowId}"]`);
            if (row) row.classList.add('selected');
        }
        state.selectedRowId = rowId;
    }

    // ---- Packet Table ----
    function addPacketRow(pkt) {
        if (!pkt || !pkt.id) return;

        dom.packetsEmpty.classList.add('hidden');
        state.packetCount++;
        updateTabBadges();

        if (state.filter && !matchesFilter(pkt)) return;

        const tr = document.createElement('tr');
        tr.dataset.id = pkt.id;

        const time = formatTime(pkt.timestamp);
        const proto = pkt.protocol || 'TCP';
        const protoClass = `proto-${proto.toLowerCase()}`;
        const method = pkt.http_method || '';
        const methodClass = method ? `method-${method.toLowerCase()}` : '';
        const isLogcat = (pkt.flags || '').startsWith('logcat:');
        const hostPath = pkt.http_host
            ? `${pkt.http_host}${pkt.http_path || ''}`
            : '';
        const flagsLabel = (!method && pkt.flags && !isLogcat) ? pkt.flags : '';
        const sourceTag = isLogcat ? '<span class="source-logcat" title="Captured from logcat">LC</span> ' : '';

        tr.innerHTML = `
            <td class="col-time">${time}</td>
            <td class="col-device truncate">${escapeHtml(pkt.serial || '')}</td>
            <td class="col-proto ${protoClass}">${proto}</td>
            <td class="col-src truncate">${escapeHtml(pkt.src_ip || '')}${pkt.src_port ? ':' + pkt.src_port : ''}</td>
            <td class="col-dst truncate">${escapeHtml(pkt.dst_ip || '')}${pkt.dst_port ? ':' + pkt.dst_port : ''}</td>
            <td class="col-method ${methodClass}">${sourceTag}${method || flagsLabel}</td>
            <td class="col-host truncate" title="${escapeHtml(hostPath)}">${escapeHtml(hostPath)}</td>
            <td class="col-len">${pkt.length || (isLogcat ? '—' : 0)}</td>
        `;

        if (isLogcat) {
            tr.classList.add('logcat-row');
        }

        tr.addEventListener('click', () => {
            selectRow(dom.packetsBody, pkt.id);
            showPacketDetail(pkt);
        });

        dom.packetsBody.appendChild(tr);

        while (dom.packetsBody.children.length > state.maxTableRows) {
            dom.packetsBody.removeChild(dom.packetsBody.firstChild);
        }

        if (state.autoScroll) {
            const container = dom.packetsBody.closest('.table-container');
            if (container) container.scrollTop = container.scrollHeight;
        }
    }

    function addConnectionRow(conn) {
        if (!conn || !conn.id) return;

        dom.connectionsEmpty.classList.add('hidden');
        state.connectionCount++;
        updateTabBadges();

        if (state.filter && !matchesConnectionFilter(conn)) return;

        const tr = document.createElement('tr');
        tr.dataset.id = conn.id;

        const proto = conn.protocol || 'TCP';
        const protoClass = `proto-${proto.toLowerCase()}`;
        const stateClass = `state-${(conn.state || '').toLowerCase().replace(/ /g, '_')}`;
        const seen = formatTime(conn.first_seen);
        const appName = conn.app_name || '';
        const hostname = conn.hostname || '';

        tr.innerHTML = `
            <td class="col-device truncate">${escapeHtml(conn.serial || '')}</td>
            <td class="col-proto ${protoClass}">${proto}</td>
            <td class="col-state ${stateClass}">${conn.state || ''}</td>
            <td class="col-local truncate">${escapeHtml(conn.local_ip || '')}:${conn.local_port || ''}</td>
            <td class="col-remote truncate">${escapeHtml(conn.remote_ip || '')}:${conn.remote_port || ''}</td>
            <td class="col-host truncate" title="${escapeHtml(hostname)}">${escapeHtml(hostname)}</td>
            <td class="col-app truncate" title="${escapeHtml(appName)}">${escapeHtml(shortPkg(appName))}</td>
            <td class="col-seen">${seen}</td>
        `;

        tr.addEventListener('click', () => {
            selectRow(dom.connectionsBody, conn.id);
            showConnectionDetail(conn);
        });

        dom.connectionsBody.appendChild(tr);

        while (dom.connectionsBody.children.length > state.maxTableRows) {
            dom.connectionsBody.removeChild(dom.connectionsBody.firstChild);
        }
    }

    // ---- Detail Panel ----
    function showPacketDetail(pkt) {
        state.selectedPacket = pkt;
        dom.detailPanel.classList.remove('collapsed');

        dom.detailContent.innerHTML = `
            <div class="detail-section">
                <h4>Packet</h4>
                ${detailRow('ID', pkt.id, true)}
                ${detailRow('Time', new Date(pkt.timestamp).toLocaleTimeString())}
                ${detailRow('Device', pkt.serial, true)}
                ${detailRow('Protocol', pkt.protocol)}
                ${detailRow('Length', pkt.length)}
                ${detailRow('Flags', pkt.flags || '-')}
            </div>
            <div class="detail-section">
                <h4>Source</h4>
                ${detailRow('IP', pkt.src_ip, true)}
                ${detailRow('Port', pkt.src_port, true)}
                ${detailRow('Full', (pkt.src_ip || '') + ':' + (pkt.src_port || ''), true)}
            </div>
            <div class="detail-section">
                <h4>Destination</h4>
                ${detailRow('IP', pkt.dst_ip, true)}
                ${detailRow('Port', pkt.dst_port, true)}
                ${detailRow('Full', (pkt.dst_ip || '') + ':' + (pkt.dst_port || ''), true)}
            </div>
            ${pkt.http_method ? `
            <div class="detail-section">
                <h4>HTTP</h4>
                ${detailRow('Method', pkt.http_method)}
                ${detailRow('Path', pkt.http_path || '-', true)}
                ${detailRow('Host', pkt.http_host || '-', true)}
                ${detailRow('URL', (pkt.http_host || '') + (pkt.http_path || ''), true)}
                ${pkt.http_status ? detailRow('Status', pkt.http_status) : ''}
            </div>
            ` : ''}
            ${pkt.raw ? `
            <div class="detail-section">
                <h4>Raw</h4>
                <div class="detail-value" style="word-break:break-all;white-space:pre-wrap;max-width:100%;font-size:10px;padding:6px;background:var(--bg-tertiary);border-radius:4px;cursor:pointer;" onclick="navigator.clipboard.writeText(this.textContent)">${escapeHtml(pkt.raw)}</div>
            </div>
            ` : ''}
        `;

        bindCopyButtons();
    }

    function showConnectionDetail(conn) {
        dom.detailPanel.classList.remove('collapsed');

        dom.detailContent.innerHTML = `
            <div class="detail-section">
                <h4>Connection</h4>
                ${detailRow('ID', conn.id, true)}
                ${detailRow('Device', conn.serial, true)}
                ${detailRow('Protocol', conn.protocol)}
                ${detailRow('State', conn.state)}
                ${detailRow('UID', conn.uid)}
                ${conn.app_name ? detailRow('App', conn.app_name, true) : ''}
                ${conn.hostname ? detailRow('Host', conn.hostname, true) : ''}
            </div>
            <div class="detail-section">
                <h4>Local</h4>
                ${detailRow('IP', conn.local_ip, true)}
                ${detailRow('Port', conn.local_port, true)}
                ${detailRow('Full', (conn.local_ip || '') + ':' + (conn.local_port || ''), true)}
            </div>
            <div class="detail-section">
                <h4>Remote</h4>
                ${detailRow('IP', conn.remote_ip, true)}
                ${detailRow('Port', conn.remote_port, true)}
                ${detailRow('Full', (conn.remote_ip || '') + ':' + (conn.remote_port || ''), true)}
            </div>
            <div class="detail-section">
                <h4>Timing</h4>
                ${detailRow('First Seen', new Date(conn.first_seen).toLocaleTimeString())}
                ${detailRow('Last Seen', new Date(conn.last_seen).toLocaleTimeString())}
            </div>
        `;

        bindCopyButtons();
    }

    function closeDetail() {
        dom.detailPanel.classList.add('collapsed');
        state.selectedPacket = null;
        state.selectedRowId = null;
        // Clear row selection
        const prev = document.querySelector('tbody tr.selected');
        if (prev) prev.classList.remove('selected');
    }

    function detailRow(label, value, copyable) {
        const val = escapeHtml(String(value ?? '-'));
        const copyBtn = copyable
            ? `<button class="copy-btn" data-copy="${val}" title="Copy to clipboard">&#x1F5CE;</button>`
            : '';
        return `<div class="detail-row">
            <span class="detail-label">${label}</span>
            <div class="detail-value-wrap">
                <span class="detail-value" title="${val}">${val}</span>
                ${copyBtn}
            </div>
        </div>`;
    }

    function bindCopyButtons() {
        dom.detailContent.querySelectorAll('.copy-btn').forEach(btn => {
            btn.addEventListener('click', async (e) => {
                e.stopPropagation();
                const text = btn.dataset.copy;
                if (!text || text === '-') return;
                const ok = await copyToClipboard(text);
                if (ok) {
                    btn.classList.add('copied');
                    btn.innerHTML = '&#10003;';
                    setTimeout(() => {
                        btn.classList.remove('copied');
                        btn.innerHTML = '&#x1F5CE;';
                    }, 1200);
                }
            });
        });
    }

    // ---- Export ----
    function exportData() {
        const rows = [];
        const activeBody = state.activeTab === 'packets' ? dom.packetsBody : dom.connectionsBody;
        activeBody.querySelectorAll('tr').forEach(tr => {
            if (tr.style.display === 'none') return;
            const cells = [];
            tr.querySelectorAll('td').forEach(td => cells.push(td.textContent.trim()));
            rows.push(cells);
        });

        if (rows.length === 0) {
            showToast('Nothing to export', 'error');
            return;
        }

        const blob = new Blob([JSON.stringify(rows, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `adb-inspector-${state.activeTab}-${Date.now()}.json`;
        a.click();
        URL.revokeObjectURL(url);
        showToast(`Exported ${rows.length} rows`, 'success');
    }

    // ---- Tab Switching ----
    function switchTab(tab) {
        state.activeTab = tab;
        $$('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
        $$('.table-container').forEach(tc => tc.classList.remove('active'));

        if (tab === 'packets') {
            $('#packets-view').classList.add('active');
        } else {
            $('#connections-view').classList.add('active');
        }
    }

    // ---- Clear ----
    async function clearData() {
        await apiPost('/clear');
        dom.packetsBody.innerHTML = '';
        dom.connectionsBody.innerHTML = '';
        dom.packetsEmpty.classList.remove('hidden');
        dom.connectionsEmpty.classList.remove('hidden');
        state.packetCount = 0;
        state.connectionCount = 0;
        updateTabBadges();
        closeDetail();
        showToast('Data cleared');
    }

    // ---- Status Updates ----
    async function updateStatus() {
        try {
            const [storeStats, poolStats] = await Promise.all([
                apiGet('/store/stats'),
                apiGet('/pool/stats'),
            ]);

            dom.statusPackets.textContent = `Packets: ${storeStats.packet_count || 0}`;
            dom.statusConnections.textContent = `Connections: ${storeStats.connection_count || 0}`;
            dom.statusPool.textContent = `Workers: ${poolStats.active || 0}/${poolStats.max_workers || 100}`;
            dom.statusMemory.textContent = `Buffer: ${storeStats.packet_count || 0}/${storeStats.packet_capacity || 50000}`;
        } catch (e) {
            // Silently ignore status update failures.
        }
    }

    // ---- Helpers ----
    function formatTime(ts) {
        if (!ts) return '';
        const d = new Date(ts);
        if (isNaN(d.getTime())) return '';
        return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
    }

    function escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    function matchesFilter(pkt) {
        const f = state.filter;
        if (!f) return true;
        return (
            (pkt.src_ip && pkt.src_ip.includes(f)) ||
            (pkt.dst_ip && pkt.dst_ip.includes(f)) ||
            (pkt.http_host && pkt.http_host.toLowerCase().includes(f)) ||
            (pkt.http_method && pkt.http_method.toLowerCase().includes(f)) ||
            (pkt.http_path && pkt.http_path.toLowerCase().includes(f)) ||
            (pkt.serial && pkt.serial.toLowerCase().includes(f)) ||
            (pkt.protocol && pkt.protocol.toLowerCase().includes(f)) ||
            (pkt.raw && pkt.raw.toLowerCase().includes(f)) ||
            (pkt.flags && pkt.flags.toLowerCase().includes(f))
        );
    }

    function matchesConnectionFilter(conn) {
        const f = state.filter;
        if (!f) return true;
        return (
            (conn.local_ip && conn.local_ip.includes(f)) ||
            (conn.remote_ip && conn.remote_ip.includes(f)) ||
            (conn.serial && conn.serial.toLowerCase().includes(f)) ||
            (conn.state && conn.state.toLowerCase().includes(f)) ||
            (conn.protocol && conn.protocol.toLowerCase().includes(f)) ||
            (conn.hostname && conn.hostname.toLowerCase().includes(f)) ||
            (conn.app_name && conn.app_name.toLowerCase().includes(f)) ||
            (String(conn.remote_port).includes(f)) ||
            (String(conn.local_port).includes(f))
        );
    }

    function applyFilterToTable(tbody) {
        const rows = tbody.querySelectorAll('tr');
        const f = state.filter;
        rows.forEach(row => {
            if (!f) {
                row.style.display = '';
                return;
            }
            const text = row.textContent.toLowerCase();
            row.style.display = text.includes(f) ? '' : 'none';
        });
    }

    // Shorten package name: "com.google.android.apps.maps" → "c.g.a.a.maps"
    function shortPkg(pkg) {
        if (!pkg) return '';
        const parts = pkg.split('.');
        if (parts.length <= 2) return pkg;
        return parts.slice(0, -1).map(p => p[0]).join('.') + '.' + parts[parts.length - 1];
    }

    // ---- Boot ----
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
