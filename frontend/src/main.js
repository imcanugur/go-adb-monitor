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
        activeTab: 'packets',
        filter: '',
        captures: {},
        autoScroll: true,
        maxTableRows: 2000,
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
    };

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
        });

        eventSource.addEventListener('ping', () => {
            // keep-alive, nothing to do
        });

        eventSource.onerror = () => {
            // Browser auto-reconnects EventSource — just log.
            console.warn('SSE connection lost, reconnecting...');
        };
    }

    // ---- Initialization ----
    async function init() {
        setupEventListeners();
        connectSSE();

        // Load ADB version.
        try {
            const data = await apiGet('/adb/version');
            dom.statusAdb.textContent = `ADB: v${data.version}`;
        } catch (e) {
            dom.statusAdb.textContent = 'ADB: not connected';
        }

        await refreshDevices();

        // Periodic status update.
        setInterval(updateStatus, 2000);
    }

    // ---- Event Listeners ----
    function setupEventListeners() {
        $('#btn-refresh').addEventListener('click', refreshDevices);
        $('#btn-start-all').addEventListener('click', startAllCaptures);
        $('#btn-stop-all').addEventListener('click', stopAllCaptures);
        $('#btn-clear').addEventListener('click', clearData);
        $('#btn-close-detail').addEventListener('click', closeDetail);

        dom.searchInput.addEventListener('input', (e) => {
            state.filter = e.target.value.toLowerCase();
            applyFilterToTable(dom.packetsBody);
            applyFilterToTable(dom.connectionsBody);
        });

        // Tab switching.
        $$('.tab').forEach(tab => {
            tab.addEventListener('click', () => switchTab(tab.dataset.tab));
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

        // Bind click handlers.
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
        } catch (e) {
            console.error('Failed to start all captures:', e);
        }
    }

    async function stopAllCaptures() {
        await apiPost('/capture/stop-all');
        state.captures = {};
        renderDeviceList();
        updateCaptureBadge();
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

    // ---- Packet Table ----
    function addPacketRow(pkt) {
        if (!pkt || !pkt.id) return;

        dom.packetsEmpty.classList.add('hidden');

        // Apply filter.
        if (state.filter && !matchesFilter(pkt)) return;

        const tr = document.createElement('tr');
        tr.dataset.id = pkt.id;

        const time = formatTime(pkt.timestamp);
        const proto = pkt.protocol || 'TCP';
        const protoClass = `proto-${proto.toLowerCase()}`;
        const method = pkt.http_method || '';
        const methodClass = method ? `method-${method.toLowerCase()}` : '';
        const hostPath = pkt.http_host ? `${pkt.http_host}${pkt.http_path || ''}` : '';

        tr.innerHTML = `
            <td class="col-time">${time}</td>
            <td class="col-device truncate">${escapeHtml(pkt.serial || '')}</td>
            <td class="col-proto ${protoClass}">${proto}</td>
            <td class="col-src truncate">${escapeHtml(pkt.src_ip || '')}:${pkt.src_port || ''}</td>
            <td class="col-dst truncate">${escapeHtml(pkt.dst_ip || '')}:${pkt.dst_port || ''}</td>
            <td class="col-method ${methodClass}">${method}</td>
            <td class="col-host truncate">${escapeHtml(hostPath)}</td>
            <td class="col-len">${pkt.length || 0}</td>
        `;

        tr.addEventListener('click', () => showPacketDetail(pkt));

        dom.packetsBody.appendChild(tr);

        // Trim old rows.
        while (dom.packetsBody.children.length > state.maxTableRows) {
            dom.packetsBody.removeChild(dom.packetsBody.firstChild);
        }

        // Auto-scroll.
        if (state.autoScroll) {
            const container = dom.packetsBody.closest('.table-container');
            if (container) container.scrollTop = container.scrollHeight;
        }
    }

    function addConnectionRow(conn) {
        if (!conn || !conn.id) return;

        dom.connectionsEmpty.classList.add('hidden');

        // Apply filter.
        if (state.filter && !matchesConnectionFilter(conn)) return;

        const tr = document.createElement('tr');
        tr.dataset.id = conn.id;

        const proto = conn.protocol || 'TCP';
        const protoClass = `proto-${proto.toLowerCase()}`;
        const stateClass = `state-${(conn.state || '').toLowerCase().replace(/ /g, '_')}`;
        const seen = formatTime(conn.first_seen);

        tr.innerHTML = `
            <td class="col-device truncate">${escapeHtml(conn.serial || '')}</td>
            <td class="col-proto ${protoClass}">${proto}</td>
            <td class="col-state ${stateClass}">${conn.state || ''}</td>
            <td class="col-local truncate">${escapeHtml(conn.local_ip || '')}:${conn.local_port || ''}</td>
            <td class="col-remote truncate">${escapeHtml(conn.remote_ip || '')}:${conn.remote_port || ''}</td>
            <td class="col-seen">${seen}</td>
        `;

        tr.addEventListener('click', () => showConnectionDetail(conn));

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
                ${detailRow('ID', pkt.id)}
                ${detailRow('Time', new Date(pkt.timestamp).toLocaleTimeString())}
                ${detailRow('Device', pkt.serial)}
                ${detailRow('Protocol', pkt.protocol)}
                ${detailRow('Length', pkt.length)}
                ${detailRow('Flags', pkt.flags || '-')}
            </div>
            <div class="detail-section">
                <h4>Source</h4>
                ${detailRow('IP', pkt.src_ip)}
                ${detailRow('Port', pkt.src_port)}
            </div>
            <div class="detail-section">
                <h4>Destination</h4>
                ${detailRow('IP', pkt.dst_ip)}
                ${detailRow('Port', pkt.dst_port)}
            </div>
            ${pkt.http_method ? `
            <div class="detail-section">
                <h4>HTTP</h4>
                ${detailRow('Method', pkt.http_method)}
                ${detailRow('Path', pkt.http_path || '-')}
                ${detailRow('Host', pkt.http_host || '-')}
                ${pkt.http_status ? detailRow('Status', pkt.http_status) : ''}
            </div>
            ` : ''}
            ${pkt.raw ? `
            <div class="detail-section">
                <h4>Raw</h4>
                <div class="detail-value" style="word-break:break-all;white-space:pre-wrap;max-width:100%;font-size:10px;padding:6px;background:var(--bg-tertiary);border-radius:4px;">${escapeHtml(pkt.raw)}</div>
            </div>
            ` : ''}
        `;
    }

    function showConnectionDetail(conn) {
        dom.detailPanel.classList.remove('collapsed');

        dom.detailContent.innerHTML = `
            <div class="detail-section">
                <h4>Connection</h4>
                ${detailRow('ID', conn.id)}
                ${detailRow('Device', conn.serial)}
                ${detailRow('Protocol', conn.protocol)}
                ${detailRow('State', conn.state)}
                ${detailRow('UID', conn.uid)}
            </div>
            <div class="detail-section">
                <h4>Local</h4>
                ${detailRow('IP', conn.local_ip)}
                ${detailRow('Port', conn.local_port)}
            </div>
            <div class="detail-section">
                <h4>Remote</h4>
                ${detailRow('IP', conn.remote_ip)}
                ${detailRow('Port', conn.remote_port)}
            </div>
            <div class="detail-section">
                <h4>Timing</h4>
                ${detailRow('First Seen', new Date(conn.first_seen).toLocaleTimeString())}
                ${detailRow('Last Seen', new Date(conn.last_seen).toLocaleTimeString())}
            </div>
        `;
    }

    function closeDetail() {
        dom.detailPanel.classList.add('collapsed');
        state.selectedPacket = null;
    }

    function detailRow(label, value) {
        return `<div class="detail-row"><span class="detail-label">${label}</span><span class="detail-value">${escapeHtml(String(value ?? '-'))}</span></div>`;
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
        closeDetail();
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
            (pkt.protocol && pkt.protocol.toLowerCase().includes(f))
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

    // ---- Boot ----
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
