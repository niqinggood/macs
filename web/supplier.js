// ==================== Model Provider Management ====================
function supplierEscHtml(s) {
    if (!s) return '';
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function supplierEscJs(s) {
    return String(s || '').replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\n/g, '\\n').replace(/\r/g, '');
}

function supplierRequiresAPIKey(p) {
    if (!p) return true;
    if (typeof p.requires_api_key === 'boolean') return p.requires_api_key;
    var value = [p.type, p.base_url, p.name].join(' ').toLowerCase();
    return !(value.includes('local') || value.includes('ollama') || value.includes('vllm') || value.includes('localhost') || value.includes('127.0.0.1'));
}

function supplierUnavailableReason(p) {
    if (!p) return 'provider missing';
    if (p.unavailable_reason) return p.unavailable_reason;
    if (!String(p.base_url || '').trim()) return 'base_url missing';
    if (!p.models || p.models.length === 0) return 'models missing';
    if (supplierRequiresAPIKey(p)) {
        var key = String(p.api_key || '').trim();
        if (!key || key.indexOf('YOUR_') === 0 || key.toUpperCase().indexOf('PLACEHOLDER') >= 0) {
            return 'api_key missing or placeholder';
        }
    }
    return '';
}

function providerCapabilityOptions(selected) {
    var options = [
        ['text-to-text', 'Text'],
        ['multi-modal', 'Vision'],
        ['text-to-image', 'Image'],
        ['text-to-video', 'Video'],
        ['audio-to-text', 'Audio']
    ];
    var known = options.some(function(item) { return item[0] === selected; });
    var html = options.map(function(item) {
        return '<option value="' + supplierEscHtml(item[0]) + '"' + (item[0] === selected ? ' selected' : '') + '>' + supplierEscHtml(item[1] + ' (' + item[0] + ')') + '</option>';
    }).join('');
    html += '<option value="__custom__"' + (!known && selected ? ' selected' : '') + '>Custom</option>';
    return html;
}

function providerModelRowHtml(model) {
    model = model || {};
    var capability = String(model.capability || 'text-to-text').trim();
    var known = ['text-to-text', 'multi-modal', 'text-to-image', 'text-to-video', 'audio-to-text'].indexOf(capability) >= 0;
    var customStyle = known ? 'display:none;' : '';
    var customValue = known ? '' : capability;
    var thinkingMode = String(model.thinking_mode || '');
    return '<div class="provider-model-row" style="display:grid;grid-template-columns:minmax(160px,1.5fr) minmax(150px,1fr) minmax(120px,.7fr) minmax(125px,.8fr) auto;gap:8px;align-items:start;margin-bottom:8px;">' +
        '<input class="prov-model-name" placeholder="deepseek-chat" value="' + supplierEscHtml(model.name || '') + '">' +
        '<div>' +
            '<select class="prov-model-capability" onchange="syncProviderCustomCapability(this)" style="width:100%;padding:8px 12px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));">' + providerCapabilityOptions(capability) + '</select>' +
            '<input class="prov-model-capability-custom" placeholder="custom capability" value="' + supplierEscHtml(customValue) + '" style="' + customStyle + 'width:100%;margin-top:6px;">' +
        '</div>' +
        '<input class="prov-model-max" type="number" min="1" step="1" placeholder="8192" value="' + supplierEscHtml(model.max_tokens || 4096) + '">' +
        '<select class="prov-model-thinking" title="Thinking mode" style="width:100%;padding:8px 10px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));">' +
            '<option value=""' + (thinkingMode === '' ? ' selected' : '') + '>Off (safe)</option>' +
            '<option value="disabled"' + (thinkingMode === 'disabled' ? ' selected' : '') + '>Force off</option>' +
            '<option value="enabled"' + (thinkingMode === 'enabled' ? ' selected' : '') + '>Enabled</option>' +
            '<option value="auto"' + (thinkingMode === 'auto' ? ' selected' : '') + '>Auto (legacy)</option>' +
        '</select>' +
        '<button type="button" onclick="removeProviderModelRow(this)" title="Remove model" style="height:34px;padding:0 10px;background:#fef2f2;color:#b91c1c;border:1px solid #fecaca;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">Remove</button>' +
    '</div>';
}

function renderProviderModels(models) {
    var list = document.getElementById('provModelsList');
    if (!list) return;
    models = models && models.length ? models : [{capability: 'text-to-text', max_tokens: 4096}];
    list.innerHTML = models.map(providerModelRowHtml).join('');
}

function addProviderModelRow(model) {
    var list = document.getElementById('provModelsList');
    if (!list) return;
    list.insertAdjacentHTML('beforeend', providerModelRowHtml(model || {capability: 'text-to-text', max_tokens: 4096}));
}

function removeProviderModelRow(btn) {
    var row = btn && btn.closest ? btn.closest('.provider-model-row') : null;
    if (row) row.remove();
    var list = document.getElementById('provModelsList');
    if (list && list.querySelectorAll('.provider-model-row').length === 0) {
        addProviderModelRow();
    }
}

function syncProviderCustomCapability(select) {
    var row = select && select.closest ? select.closest('.provider-model-row') : null;
    if (!row) return;
    var custom = row.querySelector('.prov-model-capability-custom');
    if (!custom) return;
    custom.style.display = select.value === '__custom__' ? 'block' : 'none';
    if (select.value !== '__custom__') custom.value = '';
}

function collectProviderModels() {
    var rows = document.querySelectorAll('#provModelsList .provider-model-row');
    var models = [];
    rows.forEach(function(row) {
        var name = row.querySelector('.prov-model-name')?.value.trim() || '';
        var capabilitySelect = row.querySelector('.prov-model-capability');
        var capability = capabilitySelect ? capabilitySelect.value : 'text-to-text';
        if (capability === '__custom__') {
            capability = row.querySelector('.prov-model-capability-custom')?.value.trim() || '';
        }
        var maxTokens = parseInt(row.querySelector('.prov-model-max')?.value, 10) || 4096;
        var thinkingMode = row.querySelector('.prov-model-thinking')?.value || '';
        if (name) {
            models.push({
                name: name,
                capability: capability || 'text-to-text',
                max_tokens: maxTokens,
                temperature: 0.7,
                thinking_mode: thinkingMode
            });
        }
    });
    return models;
}

function openSupplierPanel() {
    var overlay = document.getElementById('pipelinePanel');
    if (!overlay) return;
    overlay.style.display = 'flex';
    var views = overlay.querySelectorAll('.pipeline-view');
    views.forEach(function(v) { v.style.display = 'none'; });
    var detailView = document.getElementById('pipelineDetailView');
    if (!detailView) return;
    detailView.style.display = 'block';
    var backBtn = document.getElementById('detailBackBtn');
    if (backBtn) backBtn.style.display = 'none';
    var titleH2 = overlay.querySelector('.pipeline-title h2');
    if (titleH2) titleH2.textContent = '模型供应商管理';
    var tabs = overlay.querySelector('.pipeline-tabs');
    if (tabs) tabs.style.display = 'none';

    var html = '<div style="display:flex;align-items:center;gap:8px;margin-bottom:16px;">' +
        '<button onclick="showProviderForm()" style="padding:8px 18px;background:#6366f1;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;">+ 新增供应商</button>' +
        '<button onclick="loadProviders()" style="padding:8px 14px;background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">刷新</button>' +
        '</div>' +
        '<div id="providerList"></div>';
    document.getElementById('detailContent').innerHTML = html;
    loadProviders();
}

function ensureProviderModal() {
    if (document.getElementById('providerModal')) return;
    var overlay = document.createElement('div');
    overlay.id = 'providerModal';
    overlay.className = 'ae-edit-overlay';
    overlay.innerHTML = '<div class="ae-edit-modal" style="max-width:760px;">' +
        '<div class="ae-edit-header">' +
            '<h3 id="providerModalTitle">模型供应商</h3>' +
            '<button class="ae-edit-close" onclick="hideProviderForm()">&times;</button>' +
        '</div>' +
        '<div class="ae-edit-body">' +
            '<div style="display:grid;grid-template-columns:1fr 1fr;gap:10px;">' +
                '<div class="ae-edit-field"><label>Name (ID)</label><input id="provName" placeholder="deepseek"></div>' +
                '<div class="ae-edit-field"><label>Display Name</label><input id="provLabel" placeholder="DeepSeek"></div>' +
                '<div class="ae-edit-field"><label>Type</label><select id="provType" style="width:100%;padding:8px 12px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));"><option value="deepseek">deepseek</option><option value="openai">openai</option><option value="volcengine">volcengine</option><option value="minimax">minimax</option><option value="ollama">ollama</option><option value="openrouter">openrouter</option></select></div>' +
                '<div class="ae-edit-field"><label>Base URL</label><input id="provURL" placeholder="https://api.deepseek.com/v1"></div>' +
                '<div class="ae-edit-field"><label>API Key</label><input id="provKey" type="password" placeholder="sk-xxx"></div>' +
                '<div class="ae-edit-field"><label>Default</label><select id="provDefault" style="width:100%;padding:8px 12px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));"><option value="false">No</option><option value="true">Yes</option></select></div>' +
            '</div>' +
            '<div class="ae-edit-field">' +
                '<label>Models</label>' +
                '<div style="display:grid;grid-template-columns:minmax(160px,1.5fr) minmax(150px,1fr) minmax(120px,.7fr) minmax(125px,.8fr) auto;gap:8px;color:#64748b;font-size:calc(12.1px * var(--macs-font-scale, 1));margin-bottom:6px;">' +
                    '<span>Model name</span><span>Capability</span><span>Max tokens</span><span>Thinking</span><span></span>' +
                '</div>' +
                '<div id="provModelsList"></div>' +
                '<button type="button" onclick="addProviderModelRow()" style="margin-top:2px;padding:6px 10px;background:#f8fafc;border:1px solid #cbd5e1;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">+ Add model</button>' +
            '</div>' +
            '<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:10px;">' +
                '<button onclick="hideProviderForm()" style="padding:8px 14px;background:#f8fafc;border:1px solid #cbd5e1;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">取消</button>' +
                '<button onclick="saveProvider()" style="padding:8px 16px;background:#22c55e;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;">保存</button>' +
            '</div>' +
        '</div>' +
    '</div>';
    overlay.onclick = function(e) {
        if (e.target === overlay) hideProviderForm();
    };
    document.body.appendChild(overlay);
}

function showProviderForm(editData) {
    ensureProviderModal();
    document.getElementById('providerModalTitle').textContent = editData ? '编辑模型供应商' : '新增模型供应商';
    document.getElementById('providerModal').style.display = 'flex';
    if (editData) {
        document.getElementById('provName').value = editData.name || '';
        document.getElementById('provName').readOnly = true;
        document.getElementById('provLabel').value = editData.label || '';
        document.getElementById('provType').value = editData.type || 'deepseek';
        document.getElementById('provURL').value = editData.base_url || '';
        document.getElementById('provKey').value = editData.api_key || '';
        document.getElementById('provDefault').value = editData.is_default ? 'true' : 'false';
        renderProviderModels(editData.models || []);
    } else {
        document.getElementById('provName').readOnly = false;
        document.getElementById('provName').value = '';
        document.getElementById('provLabel').value = '';
        document.getElementById('provType').value = 'deepseek';
        document.getElementById('provURL').value = '';
        document.getElementById('provKey').value = '';
        document.getElementById('provDefault').value = 'false';
        renderProviderModels([]);
    }
}

function hideProviderForm() {
    var modal = document.getElementById('providerModal');
    if (modal) modal.style.display = 'none';
}

async function loadProviders() {
    try {
        var resp = await fetch('/api/providers');
        var data = await resp.json();
        var list = data.providers || [];
        var configPath = data.config_path || 'openkitty-config.yaml';
        var html = '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:8px 10px;margin-bottom:10px;">配置文件：<code style="font-size:calc(13.2px * var(--macs-font-scale, 1));">' + supplierEscHtml(configPath) + '</code></div>';
        if (list.length === 0) {
            html += '<div style="text-align:center;padding:30px;color:#94a3b8;">No providers yet. Add one above.</div>';
        } else {
            list.forEach(function(p) {
                var reason = supplierUnavailableReason(p);
                var ready = !reason;
                var status = ready
                    ? '<span style="background:#dcfce7;color:#166534;padding:1px 6px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">Ready</span>'
                    : '<span title="'+supplierEscHtml(reason)+'" style="background:#fee2e2;color:#991b1b;padding:1px 6px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">Not ready</span>';
                var def = p.is_default
                    ? (ready
                        ? ' <span style="background:#10b981;color:#fff;padding:1px 6px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">Default</span>'
                        : ' <span title="'+supplierEscHtml(reason)+'" style="background:#f97316;color:#fff;padding:1px 6px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">Default invalid</span>')
                    : '';
                var models = (p.models || []).map(function(m) {
                    return '<span style="background:#f1f5f9;padding:2px 8px;border-radius:4px;font-size:calc(12.1px * var(--macs-font-scale, 1));margin:2px;display:inline-block;">' +
                        supplierEscHtml(m.name) + ' <span style="color:#94a3b8;">(' + supplierEscHtml(m.capability) + ')</span></span>';
                }).join(' ');
                html += '<div style="background:#fff;border:1px solid #e2e8f0;border-radius:8px;padding:14px;margin-bottom:8px;">' +
                    '<div style="display:flex;justify-content:space-between;align-items:center;gap:12px;">' +
                        '<div><strong style="font-size:calc(15.4px * var(--macs-font-scale, 1));">' + supplierEscHtml(p.label || p.name) + def + ' ' + status + '</strong> <span style="color:#94a3b8;font-size:calc(12.1px * var(--macs-font-scale, 1));">' + supplierEscHtml(p.type) + ' | ' + supplierEscHtml(p.base_url) + '</span>' +
                        (reason ? '<div style="margin-top:4px;color:#b91c1c;font-size:calc(12.1px * var(--macs-font-scale, 1));">' + supplierEscHtml(reason) + '</div>' : '') + '</div>' +
                        '<div style="display:flex;gap:4px;flex-shrink:0;">' +
                            '<button onclick="editProvider2(\'' + supplierEscJs(p.name) + '\')" style="padding:3px 10px;background:#f1f5f9;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));">修改</button>' +
                            '<button onclick="deleteProvider(\'' + supplierEscJs(p.name) + '\')" style="padding:3px 10px;background:#fef2f2;color:#dc3545;border:1px solid #fecaca;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));">删除</button>' +
                        '</div>' +
                    '</div>' +
                    '<div style="margin-top:8px;">' + models + '</div>' +
                '</div>';
            });
        }
        document.getElementById('providerList').innerHTML = html;
        window._providerCache = list;
    } catch(e) {
        document.getElementById('providerList').innerHTML = '<div style="color:#dc3545;">Load failed: ' + supplierEscHtml(e.message) + '</div>';
    }
}

function editProvider2(name) {
    var list = window._providerCache || [];
    var p = list.find(function(x) { return x.name === name; });
    if (p) showProviderForm(p);
}

async function saveProvider() {
    var models = collectProviderModels();
    var p = {
        name: document.getElementById('provName').value.trim(),
        label: document.getElementById('provLabel').value.trim() || document.getElementById('provName').value.trim(),
        type: document.getElementById('provType').value,
        base_url: document.getElementById('provURL').value.trim(),
        api_key: document.getElementById('provKey').value.trim(),
        is_default: document.getElementById('provDefault').value === 'true',
        models: models
    };
    if (!p.name) {
        alert('Name required');
        return;
    }
    if (!p.models.length) {
        alert('At least one model is required');
        return;
    }
    var unavailableReason = supplierUnavailableReason(p);
    if (p.is_default && unavailableReason) {
        alert('Default provider is not ready: ' + unavailableReason);
        return;
    }
    try {
        var resp = await fetch('/api/provider/save', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(p)
        });
        var data = await resp.json();
        if (data.ok) {
            hideProviderForm();
            loadProviders();
            if (typeof loadAEModelDefaultsBar === 'function') loadAEModelDefaultsBar();
        } else {
            alert('Save failed: ' + (data.error || 'unknown'));
        }
    } catch(e) {
        alert('Save failed: ' + e.message);
    }
}

async function deleteProvider(name) {
    if (!confirm('Delete provider ' + name + '?')) return;
    try {
        var resp = await fetch('/api/provider/delete?name=' + encodeURIComponent(name), {method: 'POST'});
        var data = await resp.json();
        if (data.ok) {
            loadProviders();
            if (typeof loadAEModelDefaultsBar === 'function') loadAEModelDefaultsBar();
        } else {
            alert('Delete failed: ' + (data.error || 'unknown'));
        }
    } catch(e) {
        alert('Delete failed: ' + e.message);
    }
}
