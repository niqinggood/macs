(function () {
    'use strict';

    var state = {
        templateFile: null,
        templateUpload: null,
        dataFile: null,
        dataUpload: null,
        sourceData: null,
        records: [],
        inspection: null,
        mapping: null,
        mappingRequestID: 0,
        fieldMapping: {},
        tableCells: [],
        tableCellsOverride: false,
        repeatRows: [],
        profiles: [],
        matchedProfile: null,
        outputs: [],
        previewReady: false,
        generatedVerified: false,
        repeatHelpOpen: false,
        busy: false,
        saveProfile: true,
        zoom: 90,
        toastTimer: null
    };

    var icons = {
        arrowLeft: '<path d="m15 18-6-6 6-6"/><path d="M9 12h10"/>',
        close: '<path d="m18 6-12 12M6 6l12 12"/>',
        file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>',
        upload: '<path d="M12 16V4m0 0-4 4m4-4 4 4"/><path d="M4 15v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4"/>',
        database: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/>',
        check: '<path d="m5 12 4 4L19 6"/>',
        checkCircle: '<circle cx="12" cy="12" r="9"/><path d="m8 12 2.5 2.5L16 9"/>',
        alert: '<circle cx="12" cy="12" r="9"/><path d="M12 8v5m0 3h.01"/>',
        xCircle: '<circle cx="12" cy="12" r="9"/><path d="m9 9 6 6m0-6-6 6"/>',
        info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5m0-8h.01"/>',
        history: '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5m4-1v5l3 2"/>',
        chevronRight: '<path d="m9 18 6-6-6-6"/>',
        chevronLeft: '<path d="m15 18-6-6 6-6"/>',
        edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L8 18l-4 1 1-4Z"/>',
        download: '<path d="M12 3v12m0 0 4-4m-4 4-4-4"/><path d="M5 21h14"/>',
        external: '<path d="M14 3h7v7m0-7-9 9"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
        reset: '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/>'
    };

    function icon(name) {
        return '<svg class="ds-icon" viewBox="0 0 24 24" aria-hidden="true">' + (icons[name] || icons.file) + '</svg>';
    }

    function studioMarkup() {
        return '' +
            '<section id="documentAutomationStudio" class="ds-shell" role="dialog" aria-modal="true" aria-labelledby="dsTitle">' +
              '<header class="ds-topbar">' +
                '<div class="ds-title-group">' +
                  '<button class="ds-icon-button" type="button" data-ds-action="close" aria-label="返回">' + icon('arrowLeft') + '</button>' +
                  '<div class="ds-title-copy"><h2 id="dsTitle">文档自动化</h2><p>企业模板 · LIMS 数据填报</p></div>' +
                '</div>' +
                '<div class="ds-stepper" id="dsStepper" aria-label="文档生成步骤"></div>' +
                '<div class="ds-top-actions">' +
                  '<button class="ds-text-button" type="button" data-ds-action="history">' + icon('history') + ' 生成记录</button>' +
                  '<button class="ds-icon-button" type="button" data-ds-action="close" aria-label="关闭">' + icon('close') + '</button>' +
                  '<div class="ds-history-popover" id="dsHistoryPopover" hidden></div>' +
                '</div>' +
              '</header>' +
              '<div class="ds-main">' +
                '<aside class="ds-pane ds-left"><div class="ds-pane-scroll">' +
                  '<h3 class="ds-pane-title">本次任务</h3>' +
                  '<span class="ds-section-label">Word 模板</span><div id="dsTemplateSlot"></div>' +
                  '<span class="ds-section-label">实验数据</span><div id="dsDataSlot"></div>' +
                  '<span class="ds-section-label">复用方案</span><div id="dsProfileSlot"></div>' +
                  '<div class="ds-switch-row"><span>生成后保存此方案</span><label class="ds-switch"><input id="dsSaveProfile" type="checkbox" checked><span class="ds-switch-track"></span></label></div>' +
                  '<button class="ds-ghost-button" type="button" data-ds-action="reset">' + icon('reset') + ' 重新选择文件</button>' +
                '</div></aside>' +
                '<main class="ds-pane ds-center">' +
                  '<div class="ds-center-head"><div class="ds-center-title-row"><h3 class="ds-pane-title">字段映射</h3><div class="ds-summary" id="dsSummary"></div></div><div id="dsMappingNote"></div></div>' +
                  '<div class="ds-mapping-wrap" id="dsMappingBody"></div>' +
                '</main>' +
                '<aside class="ds-pane ds-right">' +
                  '<div class="ds-right-head"><h3 class="ds-pane-title">文档预览</h3><div class="ds-preview-tools">' +
                    '<button class="ds-icon-button" type="button" disabled aria-label="上一页">' + icon('chevronLeft') + '</button><span class="ds-page-count">1 / 1</span><button class="ds-icon-button" type="button" disabled aria-label="下一页">' + icon('chevronRight') + '</button>' +
                    '<select id="dsZoom" class="ds-zoom-select" aria-label="预览缩放"><option value="75">75%</option><option value="90" selected>90%</option><option value="100">100%</option><option value="110">110%</option></select>' +
                  '</div></div>' +
                  '<div class="ds-preview-stage"><article class="ds-paper" id="dsPaper"></article></div>' +
                  '<div class="ds-preview-validation idle" id="dsValidation">' + icon('info') + '<span>上传 Word 模板后检查版式</span></div>' +
                '</aside>' +
              '</div>' +
              '<footer class="ds-footer">' +
                '<div class="ds-save-state" id="dsSaveState">' + icon('checkCircle') + '<span>工作台已就绪</span></div>' +
                '<div class="ds-footer-actions">' +
                  '<button class="ds-secondary-button" id="dsPreviewButton" type="button" data-ds-action="preview" disabled>生成预览</button>' +
                  '<button class="ds-primary-button" id="dsGenerateButton" type="button" data-ds-action="generate" disabled>' + icon('file') + '<span>生成文档</span></button>' +
                  '<button class="ds-secondary-button" id="dsDownloadButton" type="button" data-ds-action="download" hidden>' + icon('download') + ' 下载结果</button>' +
                '</div>' +
              '</footer>' +
              '<input id="dsTemplateInput" type="file" accept=".docx,application/vnd.openxmlformats-officedocument.wordprocessingml.document" hidden>' +
              '<input id="dsDataInput" type="file" accept=".json,.csv,application/json,text/csv" hidden>' +
              '<div class="ds-toast" id="dsToast" role="status" aria-live="polite"></div>' +
            '</section>';
    }

    function injectStudio() {
        if (document.getElementById('documentAutomationStudio')) return;
        document.body.insertAdjacentHTML('beforeend', studioMarkup());
        injectMenuItem();
        bindEvents();
        renderAll();
    }

    function injectMenuItem() {
        var menu = document.getElementById('toolMenu');
        if (!menu || document.getElementById('documentAutomationMenuItem')) return;
        var item = document.createElement('a');
        item.id = 'documentAutomationMenuItem';
        item.href = 'javascript:void(0)';
        item.innerHTML = icon('file') + '<span>文档自动化</span>';
        item.addEventListener('click', function (event) {
            event.preventDefault();
            openStudio();
        });
        var insertionPoint = Array.prototype.find.call(menu.querySelectorAll('a'), function (link) {
            return /openDocAnalyzer|openMarketplace|shutdownServer/.test(String(link.getAttribute('onclick') || ''));
        });
        if (insertionPoint) menu.insertBefore(item, insertionPoint);
        else menu.appendChild(item);
    }

    function bindEvents() {
        var root = getRoot();
        root.addEventListener('click', function (event) {
            var actionNode = event.target.closest('[data-ds-action]');
            if (!actionNode) return;
            var action = actionNode.getAttribute('data-ds-action');
            if (action === 'close') closeStudio();
            else if (action === 'pick-template') document.getElementById('dsTemplateInput').click();
            else if (action === 'pick-data') document.getElementById('dsDataInput').click();
            else if (action === 'reset') resetStudio();
            else if (action === 'preview') generateDocuments(true);
            else if (action === 'generate') generateDocuments(false);
            else if (action === 'download') downloadOutputs();
            else if (action === 'history') toggleHistory();
            else if (action === 'download-output') downloadOutput(Number(actionNode.getAttribute('data-output-index')));
            else if (action === 'repeat-help') {
                state.repeatHelpOpen = !state.repeatHelpOpen;
                renderMapping();
            }
        });
        root.addEventListener('change', function (event) {
            if (event.target.id === 'dsTemplateInput' && event.target.files[0]) handleTemplateFile(event.target.files[0]);
            else if (event.target.id === 'dsDataInput' && event.target.files[0]) handleDataFile(event.target.files[0]);
            else if (event.target.id === 'dsSaveProfile') state.saveProfile = event.target.checked;
            else if (event.target.id === 'dsZoom') setZoom(Number(event.target.value));
            else if (event.target.matches('[data-mapping-target]')) {
                var target = event.target.getAttribute('data-mapping-target');
                if (event.target.value) state.fieldMapping[target] = event.target.value;
                else delete state.fieldMapping[target];
                inspectMapping(false);
            } else if (event.target.matches('[data-cell-address]')) {
                updateTableCellPlan(event.target.getAttribute('data-cell-address'), event.target.value);
                state.tableCellsOverride = true;
                renderMapping();
            }
        });
        bindDropZone('dsTemplateSlot', function (file) { handleTemplateFile(file); });
        bindDropZone('dsDataSlot', function (file) { handleDataFile(file); });
        document.addEventListener('keydown', function (event) {
            if (event.key === 'Escape' && getRoot().classList.contains('is-open')) closeStudio();
        });
        document.addEventListener('click', function (event) {
            if (event.target.closest('#themeToggle')) setTimeout(syncTheme, 0);
        });
    }

    function bindDropZone(slotID, handler) {
        var slot = document.getElementById(slotID);
        slot.addEventListener('dragover', function (event) {
            event.preventDefault();
            var card = slot.querySelector('.ds-upload-card');
            if (card) card.classList.add('is-dragging');
        });
        slot.addEventListener('dragleave', function () {
            var card = slot.querySelector('.ds-upload-card');
            if (card) card.classList.remove('is-dragging');
        });
        slot.addEventListener('drop', function (event) {
            event.preventDefault();
            var card = slot.querySelector('.ds-upload-card');
            if (card) card.classList.remove('is-dragging');
            if (event.dataTransfer.files[0]) handler(event.dataTransfer.files[0]);
        });
    }

    function getRoot() { return document.getElementById('documentAutomationStudio'); }

    function openStudio() {
        var root = getRoot();
        if (!root) return;
        syncTheme();
        root.classList.add('is-open');
        root.removeAttribute('aria-hidden');
        if (window.closeDropdownMenus) window.closeDropdownMenus();
        else {
            var menu = document.getElementById('toolMenu');
            if (menu) menu.style.display = 'none';
        }
        loadProfiles();
        setTimeout(function () { root.querySelector('[data-ds-action="close"]').focus(); }, 0);
    }

    function closeStudio() {
        var root = getRoot();
        if (!root) return;
        root.classList.remove('is-open');
        root.setAttribute('aria-hidden', 'true');
        document.getElementById('dsHistoryPopover').hidden = true;
    }

    function syncTheme() {
        var root = getRoot();
        if (root) root.classList.toggle('ds-dark', localStorage.getItem('kitty-dark') === '1');
    }

    function resetStudio() {
        state.templateFile = null;
        state.templateUpload = null;
        state.dataFile = null;
        state.dataUpload = null;
        state.sourceData = null;
        state.records = [];
        state.inspection = null;
        state.mapping = null;
        state.mappingRequestID++;
        state.fieldMapping = {};
        state.tableCells = [];
        state.tableCellsOverride = false;
        state.repeatRows = [];
        state.matchedProfile = null;
        state.outputs = [];
        state.previewReady = false;
        state.generatedVerified = false;
        state.repeatHelpOpen = false;
        state.busy = false;
        document.getElementById('dsTemplateInput').value = '';
        document.getElementById('dsDataInput').value = '';
        renderAll();
        showToast('已清空本次任务');
    }

    async function handleTemplateFile(file) {
        if (!/\.docx$/i.test(file.name)) {
            showToast('请选择 .docx Word 模板', true);
            return;
        }
        state.templateFile = file;
        state.templateUpload = { filename: file.name, uploading: true };
        state.inspection = null;
        state.mapping = null;
        state.mappingRequestID++;
        state.fieldMapping = {};
        state.tableCells = [];
        state.tableCellsOverride = false;
        state.repeatRows = [];
        state.outputs = [];
        state.generatedVerified = false;
        renderAll();
        setBusy(true, '正在识别 Word 模板…');
        try {
            var upload = await uploadWorkspaceFile(file);
            state.templateUpload = upload;
            await renderDocxBuffer(await file.arrayBuffer());
            await inspectTemplateOnly();
            if (state.sourceData) await inspectMapping(true);
            else setSaveState('模板已识别 · 等待实验数据');
            showToast('Word 模板识别完成');
        } catch (error) {
            state.templateUpload = { filename: file.name, error: error.message };
            showToast(error.message || '模板上传失败', true);
        } finally {
            setBusy(false);
            renderAll();
        }
    }

    async function handleDataFile(file) {
        if (!/\.(json|csv)$/i.test(file.name)) {
            showToast('当前工作台支持 JSON 或 CSV 数据；Excel 可先导出 CSV', true);
            return;
        }
        state.dataFile = file;
        state.dataUpload = { filename: file.name, uploading: true };
        state.mapping = null;
        state.mappingRequestID++;
        state.fieldMapping = {};
        state.tableCells = [];
        state.tableCellsOverride = false;
        state.outputs = [];
        state.generatedVerified = false;
        renderAll();
        setBusy(true, '正在读取实验数据…');
        try {
            var parsed = await parseDataFile(file);
            state.sourceData = parsed.sourceData;
            state.records = parsed.records;
            state.dataUpload = await uploadWorkspaceFile(file);
            if (state.inspection) {
                state.repeatRows = deriveRepeatRows(state.inspection, documentAutomationPreviewSource());
                await inspectMapping(false);
            }
            showToast('实验数据已载入');
        } catch (error) {
            state.sourceData = null;
            state.records = [];
            state.dataUpload = { filename: file.name, error: error.message };
            showToast(error.message || '数据文件读取失败', true);
        } finally {
            setBusy(false);
            renderAll();
        }
    }

    async function uploadWorkspaceFile(file) {
        var form = new FormData();
        form.append('file', file, file.name);
        var response = await fetch('/api/upload', { method: 'POST', body: form });
        var payload = await response.json().catch(function () { return {}; });
        if (!response.ok || !payload.success || !payload.path) throw new Error(payload.error || ('上传失败（HTTP ' + response.status + '）'));
        return payload;
    }

    async function parseDataFile(file) {
        var text = (await file.text()).replace(/^\uFEFF/, '');
        if (/\.csv$/i.test(file.name)) {
            var rows = parseCSV(text);
            if (!rows.length) throw new Error('CSV 中没有可用数据行');
            return { sourceData: { samples: rows }, records: [] };
        }
        var value;
        try { value = JSON.parse(text); }
        catch (error) { throw new Error('JSON 格式错误：' + error.message); }
        if (Array.isArray(value)) {
            var records = value.filter(isPlainObject);
            if (!records.length) throw new Error('JSON 数组中没有对象记录');
            return { sourceData: records[0], records: records };
        }
        if (!isPlainObject(value)) throw new Error('JSON 顶层应为对象或对象数组');
        return { sourceData: value, records: [] };
    }

    function parseCSV(text) {
        var matrix = [], row = [], field = '', quoted = false;
        for (var i = 0; i < text.length; i++) {
            var char = text[i];
            if (quoted) {
                if (char === '"' && text[i + 1] === '"') { field += '"'; i++; }
                else if (char === '"') quoted = false;
                else field += char;
            } else if (char === '"') quoted = true;
            else if (char === ',') { row.push(field); field = ''; }
            else if (char === '\n') { row.push(field.replace(/\r$/, '')); matrix.push(row); row = []; field = ''; }
            else field += char;
        }
        if (field.length || row.length) { row.push(field.replace(/\r$/, '')); matrix.push(row); }
        if (matrix.length < 2) return [];
        var headers = matrix.shift().map(function (header, index) { return header.trim() || ('column_' + (index + 1)); });
        return matrix.filter(function (cells) { return cells.some(function (cell) { return cell.trim() !== ''; }); }).map(function (cells) {
            var result = {};
            headers.forEach(function (header, index) { result[header] = (cells[index] || '').trim(); });
            return result;
        });
    }

    async function inspectTemplateOnly() {
        if (!state.templateUpload || !state.templateUpload.path) return;
        var payload = await postJSON('/api/document-automation/inspect', { template_path: state.templateUpload.path });
        var preview = payload.preview || {};
        state.inspection = preview.inspection || preview;
        matchProfile();
        state.repeatRows = state.sourceData ? deriveRepeatRows(state.inspection, documentAutomationPreviewSource()) : [];
    }

    async function inspectMapping(allowDerive) {
        if (!state.templateUpload || !state.templateUpload.path || !documentAutomationPreviewSource()) return;
        var requestID = ++state.mappingRequestID;
        setBusy(true, 'AI 正在匹配字段…');
        try {
            var request = buildRequest(false);
            var payload = await postJSON('/api/document-automation/inspect', request);
            if (requestID !== state.mappingRequestID) return;
            var preview = payload.preview || {};
            state.inspection = preview.inspection || state.inspection;
            state.mapping = preview.mapping || null;
            matchProfile();
            if (!state.tableCellsOverride && !state.matchedProfile) {
                state.tableCells = deriveTableCells(state.mapping);
            }
            if (allowDerive && !state.repeatRows.length) {
                var derived = deriveRepeatRows(state.inspection, documentAutomationPreviewSource());
                if (derived.length) {
                    state.repeatRows = derived;
                    await inspectMapping(false);
                    return;
                }
            }
            setSaveState('字段映射已自动保存');
        } catch (error) {
            if (requestID !== state.mappingRequestID) return;
            state.mapping = null;
            showToast(error.message || '字段映射失败', true);
        } finally {
            if (requestID === state.mappingRequestID) {
                setBusy(false);
                renderAll();
            }
        }
    }

    function buildRequest(forGenerate) {
        var request = {
            template_path: state.templateUpload.path,
            field_mapping: state.fieldMapping,
            repeat_rows: state.repeatRows,
            mapping_threshold: 0.88
        };
        if (!state.matchedProfile || state.tableCellsOverride) request.table_cells = state.tableCells;
        if (state.records.length) request.records = state.records;
        else request.source_data = state.sourceData;
        if (state.matchedProfile) request.use_template_profile = true;
        if (forGenerate) {
            request.save_template_profile = state.saveProfile;
            request.template_profile_name = profileDisplayName();
            request.output_name = outputBaseName();
        }
        return request;
    }

    async function generateDocuments(previewOnly) {
        if (state.busy || !canGenerate()) return;
        setBusy(true, previewOnly ? '正在生成可核对预览…' : '正在生成 Word 文档…');
        state.generatedVerified = false;
        renderAll();
        try {
            var request = buildRequest(true);
            request.strict = !previewOnly;
            request.verify_complete = !previewOnly;
            request.save_template_profile = previewOnly ? false : state.saveProfile;
            if (previewOnly) request.output_name += '-预览';
            var payload = await postJSON('/api/document-automation/generate', request);
            state.outputs = payload.files || [];
            if (!state.outputs.length) throw new Error('生成接口没有返回文档');
            await renderDownloadURL(state.outputs[0].download_url);
            state.generatedVerified = !previewOnly;
            if (!previewOnly && state.saveProfile) await loadProfiles();
            setSaveState((previewOnly ? '预览' : '文档') + '已生成 · 刚刚');
            showToast(previewOnly ? '预览已更新，可核对版式和数据' : ('已生成 ' + state.outputs.length + ' 份 Word 文档'));
        } catch (error) {
            showToast(error.message || '文档生成失败', true);
        } finally {
            setBusy(false);
            renderAll();
        }
    }

    async function postJSON(path, body) {
        var response = await fetch(path, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
        var payload = await response.json().catch(function () { return {}; });
        if (!response.ok || payload.ok === false || payload.error) throw new Error(payload.error || ('请求失败（HTTP ' + response.status + '）'));
        return payload;
    }

    async function loadProfiles() {
        try {
            var response = await fetch('/api/document-automation/profiles');
            var payload = await response.json();
            state.profiles = payload.profiles || [];
            matchProfile();
            renderLeft();
            renderHistory();
        } catch (_) {
            state.profiles = [];
        }
    }

    function matchProfile() {
        var fingerprint = state.inspection && state.inspection.fingerprint;
        state.matchedProfile = fingerprint ? state.profiles.find(function (profile) { return profile.fingerprint === fingerprint; }) || null : null;
    }

    function deriveRepeatRows(inspection, source) {
        if (!inspection || !source) return [];
        var arrays = findObjectArrays(source);
        if (!arrays.length) return [];
        var candidate = arrays[0];
        var tables = inspection.tables || [];
        var best = null;
        for (var i = 0; i < tables.length; i++) {
            var rows = tables[i].rows || [];
            for (var j = 0; j < rows.length; j++) {
                if (rows[j].likely_repeatable && (rows[j].placeholder_keys || []).length) {
                    var placeholderCount = rows[j].placeholder_keys.length;
                    var score = placeholderCount * 10;
                    if (placeholderCount > 1) score += 25;
                    if (!best || score > best.score) best = {
                        score: score,
                        part: tables[i].part || 'word/document.xml',
                        table_index: tables[i].index,
                        template_row: rows[j].index,
                        data_path: candidate.path,
                        remove_template_if_empty: false
                    };
                }
            }
        }
        if (!best) return [];
        delete best.score;
        return [best];
    }

    function deriveTableCells(mapping) {
        if (!mapping) return [];
        return (mapping.table_cell_suggestions || []).filter(function (item) {
            return item.status === 'suggested' && item.source_path;
        }).map(function (item) {
            return tableCellPlanFromAddress(item.address, item.source_path);
        }).filter(Boolean);
    }

    function tableCellPlanFromAddress(address, sourcePath) {
        var match = /^(.*)#table\[(\d+)\]\.row\[(\d+)\]\.cell\[(\d+)\]$/.exec(String(address || ''));
        if (!match || !sourcePath) return null;
        return {
            part: match[1] || 'word/document.xml',
            table_index: Number(match[2]),
            row: Number(match[3]),
            cell: Number(match[4]),
            source_path: sourcePath
        };
    }

    function tableCellAddress(plan) {
        return (plan.part || 'word/document.xml') + '#table[' + plan.table_index + '].row[' + plan.row + '].cell[' + plan.cell + ']';
    }

    function updateTableCellPlan(address, sourcePath) {
        state.tableCells = state.tableCells.filter(function (plan) { return tableCellAddress(plan) !== address; });
        var plan = tableCellPlanFromAddress(address, sourcePath);
        if (plan) state.tableCells.push(plan);
    }

    function findObjectArrays(value, prefix, output, depth) {
        prefix = prefix || '';
        output = output || [];
        depth = depth || 0;
        if (!isPlainObject(value) || depth > 5) return output;
        Object.keys(value).forEach(function (key) {
            var path = prefix ? prefix + '.' + key : key;
            var child = value[key];
            if (Array.isArray(child) && child.length && child.some(isPlainObject)) output.push({ path: path, rows: child });
            else if (isPlainObject(child)) findObjectArrays(child, path, output, depth + 1);
        });
        return output;
    }

    function flattenScalarPaths(value, prefix, output, depth) {
        prefix = prefix || '';
        output = output || [];
        depth = depth || 0;
        if (depth > 7) return output;
        if (Array.isArray(value)) return output;
        if (isPlainObject(value)) {
            Object.keys(value).forEach(function (key) {
                var path = prefix ? prefix + '.' + key : key;
                flattenScalarPaths(value[key], path, output, depth + 1);
            });
        } else if (prefix) output.push(prefix);
        return output;
    }

    function getPathValue(root, path) {
        if (!path) return '';
        return path.split('.').reduce(function (current, key) {
            return current !== null && current !== undefined ? current[key] : undefined;
        }, root);
    }

    function documentAutomationPreviewSource() {
        return state.records.length ? state.records[0] : state.sourceData;
    }

    async function renderDocxBuffer(buffer) {
        var paper = document.getElementById('dsPaper');
        if (!window.mammoth || !window.mammoth.convertToHtml) {
            paper.innerHTML = paperEmpty('浏览器预览组件未加载', '生成的 DOCX 仍可正常下载');
            return;
        }
        try {
            var result = await window.mammoth.convertToHtml({ arrayBuffer: buffer });
            paper.innerHTML = result.value || paperEmpty('模板中没有可预览文字', '图片与复杂域将在 Word 中显示');
            state.previewReady = true;
        } catch (error) {
            paper.innerHTML = paperEmpty('暂时无法渲染此模板', '可继续映射并生成 Word 文档');
            state.previewReady = false;
        }
        setZoom(state.zoom);
        renderValidation();
    }

    async function renderDownloadURL(downloadURL) {
        var response = await fetch(downloadURL);
        if (!response.ok) throw new Error('读取生成文档失败');
        await renderDocxBuffer(await response.arrayBuffer());
    }

    function renderAll() {
        syncTheme();
        renderLeft();
        renderStepper();
        renderMapping();
        renderValidation();
        renderActions();
        renderHistory();
        if (!state.previewReady && !state.templateFile) {
            document.getElementById('dsPaper').innerHTML = paperEmpty('Word 页面预览', '上传模板后，这里会显示版式；生成预览后会替换为已填写结果。');
        }
    }

    function renderLeft() {
        var templateSlot = document.getElementById('dsTemplateSlot');
        var dataSlot = document.getElementById('dsDataSlot');
        if (!templateSlot || !dataSlot) return;
        templateSlot.innerHTML = state.templateFile ? fileCard('template') : uploadCard('template');
        dataSlot.innerHTML = state.dataFile ? fileCard('data') : uploadCard('data');

        var profileSlot = document.getElementById('dsProfileSlot');
        if (state.matchedProfile) {
            var profile = state.matchedProfile;
            profileSlot.innerHTML = '<div class="ds-profile-card"><div class="ds-file-main"><span class="ds-file-icon">' + icon('file') + '</span><div class="ds-file-copy"><div class="ds-profile-title">' + escapeHTML(profile.name || profile.template_name || '已保存方案') + '</div><div class="ds-profile-meta">' + profile.field_count + ' 个字段 · ' + (profile.cell_count || 0) + ' 个固定单元格 · ' + profile.repeat_count + ' 个明细规则</div></div>' + icon('chevronRight') + '</div><div class="ds-card-status ok">' + icon('checkCircle') + ' 已按模板指纹自动匹配</div></div>';
        } else if (state.inspection) {
            profileSlot.innerHTML = '<div class="ds-empty-profile">这是一个新模板。首次核对并生成后，会记住字段路径和明细行规则；下次只需换数据。</div>';
        } else {
            profileSlot.innerHTML = '<div class="ds-empty-profile">识别模板后自动查找历史方案，不保存本次样品值和实验结果。</div>';
        }
        var saveToggle = document.getElementById('dsSaveProfile');
        if (saveToggle) saveToggle.checked = state.saveProfile;
    }

    function uploadCard(kind) {
        var template = kind === 'template';
        return '<button type="button" class="ds-upload-card" data-ds-action="pick-' + kind + '"><span class="ds-upload-symbol">' + icon(template ? 'file' : 'database') + '</span><strong>' + (template ? '选择 Word 模板' : '选择实验数据') + '</strong><small>' + (template ? '支持 .docx，可拖放到这里' : '支持 .json / .csv，可拖放到这里') + '</small></button>';
    }

    function fileCard(kind) {
        var template = kind === 'template';
        var file = template ? state.templateFile : state.dataFile;
        var upload = template ? state.templateUpload : state.dataUpload;
        var statusClass = upload && upload.error ? 'warn' : (upload && upload.uploading ? '' : 'ok');
        var statusText;
        if (upload && upload.error) statusText = upload.error;
        else if (upload && upload.uploading) statusText = '上传中…';
        else if (template && state.inspection) statusText = '已识别 · ' + countTemplateFields() + ' 个字段';
        else if (!template && state.sourceData) statusText = dataSummary();
        else statusText = '已上传';
        return '<div class="ds-file-card"><div class="ds-file-main"><span class="ds-file-icon">' + icon(template ? 'file' : 'database') + '</span><div class="ds-file-copy"><div class="ds-file-name" title="' + escapeAttr(file.name) + '">' + escapeHTML(file.name) + '</div><div class="ds-card-status ' + statusClass + '">' + icon(statusClass === 'warn' ? 'alert' : 'checkCircle') + '<span>' + escapeHTML(statusText) + '</span></div></div><button type="button" class="ds-file-change" data-ds-action="pick-' + kind + '" aria-label="更换文件">' + icon('edit') + '</button></div></div>';
    }

    function renderStepper() {
        var hasInput = !!(state.templateFile && state.dataFile);
        var hasMapping = !!state.mapping;
        var generated = state.outputs.length > 0;
        var steps = [
            { label: '导入资料', detail: hasInput ? '已完成' : '进行中', done: hasInput, active: !hasInput },
            { label: '核对映射', detail: hasMapping ? (mappingUnresolved() ? '待确认' : '已完成') : '等待中', done: hasMapping && !mappingUnresolved(), active: hasInput && !generated },
            { label: '生成文档', detail: generated ? '已完成' : '等待中', done: generated, active: generated }
        ];
        document.getElementById('dsStepper').innerHTML = steps.map(function (step, index) {
            var cls = step.done ? 'done' : (step.active ? 'active' : '');
            var dot = step.done ? icon('check') : String(index + 1);
            return (index ? '<span class="ds-step-line"></span>' : '') + '<div class="ds-step ' + cls + '"><span class="ds-step-dot">' + dot + '</span><span><b>' + step.label + '</b><small>' + step.detail + '</small></span></div>';
        }).join('');
    }

    function renderMapping() {
        var summary = document.getElementById('dsSummary');
        var note = document.getElementById('dsMappingNote');
        var body = document.getElementById('dsMappingBody');
        if (!state.templateFile || !state.dataFile) {
            summary.innerHTML = '<span class="muted"><strong>0</strong> 已匹配</span><span class="warn"><strong>0</strong> 待确认</span><span class="muted"><strong>0</strong> 未匹配</span>';
            note.innerHTML = '';
            body.innerHTML = '<div class="ds-empty-state"><div><span class="ds-empty-icon">' + icon('database') + '</span><h3>先导入模板和实验数据</h3><p>系统会识别占位符、内容控件、下划线与重复表格行，然后给出可修正的字段映射。</p></div></div>';
            return;
        }
        if (state.busy && !state.mapping) {
            body.innerHTML = '<div class="ds-empty-state"><div><span class="ds-button-spinner" style="display:inline-block;border-color:#dbe4ff;border-top-color:#3157d5;width:28px;height:28px"></span><h3 style="margin-top:14px">正在建立字段映射</h3><p>匹配字段名、上下文与历史模板方案…</p></div></div>';
            return;
        }
        if (!state.mapping) {
            body.innerHTML = '<div class="ds-empty-state"><div><span class="ds-empty-icon">' + icon('alert') + '</span><h3>尚未得到映射结果</h3><p>请检查文件格式，或重新选择模板与数据。</p></div></div>';
            return;
        }
        var fields = state.mapping.fields || [];
        var cellSuggestions = state.mapping.table_cell_suggestions || [];
        var mapped = fields.filter(function (field) { return field.status === 'mapped'; }).length + state.tableCells.length;
        var pending = fields.filter(function (field) { return field.status === 'ambiguous'; }).length + cellSuggestions.filter(function (cell) { return cell.status === 'ambiguous'; }).length;
        var unmatched = fields.filter(function (field) { return field.status === 'unmatched'; }).length;
        summary.innerHTML = '<span class="ok"><strong>' + mapped + '</strong> 已匹配</span><span class="warn"><strong>' + pending + '</strong> 待确认</span><span class="muted"><strong>' + unmatched + '</strong> 未匹配</span>';
        note.innerHTML = '<div class="ds-note">' + icon('info') + '<span>AI 已根据字段名、上下文' + (state.matchedProfile ? '和历史方案' : '') + '完成映射；生成前可逐项修正。</span></div>';
        var source = documentAutomationPreviewSource();
        var sourcePaths = flattenScalarPaths(source).sort();
        var rows = fields.map(function (field) {
            var cls = field.status === 'ambiguous' ? 'needs-review' : (field.status === 'unmatched' ? 'unmatched' : '');
            var status = field.status === 'mapped' ? ['ok', 'checkCircle', '已确认'] : (field.status === 'ambiguous' ? ['warn', 'alert', '待确认'] : ['bad', 'xCircle', '未匹配']);
            var selectedPath = state.fieldMapping[field.target] || field.source_path || '';
            var options = '<option value="">选择数据字段</option>' + sourcePaths.map(function (path) {
                return '<option value="' + escapeAttr(path) + '"' + (path === selectedPath ? ' selected' : '') + '>' + escapeHTML(path) + '</option>';
            }).join('');
            var value = selectedPath ? getPathValue(source, selectedPath) : '';
            return '<tr class="' + cls + '"><td><div class="ds-field-target">' + escapeHTML(field.target) + '</div></td><td><select class="ds-source-select" data-mapping-target="' + escapeAttr(field.target) + '" aria-label="' + escapeAttr(field.target) + ' 的数据来源">' + options + '</select></td><td class="ds-example" title="' + escapeAttr(formatValue(value)) + '">' + escapeHTML(formatValue(value) || '—') + '</td><td class="ds-confidence">' + Math.round((field.confidence || 0) * 100) + '%</td><td><span class="ds-status ' + status[0] + '">' + icon(status[1]) + status[2] + '</span></td></tr>';
        }).join('');
        rows += cellSuggestions.map(function (cell) {
            var selectedPlan = state.tableCells.find(function (plan) { return tableCellAddress(plan) === cell.address; });
            var selectedPath = selectedPlan ? selectedPlan.source_path : (state.matchedProfile && cell.status === 'suggested' ? cell.source_path : '');
            var options = '<option value="">保留空白</option>' + sourcePaths.map(function (path) {
                return '<option value="' + escapeAttr(path) + '"' + (path === selectedPath ? ' selected' : '') + '>' + escapeHTML(path) + '</option>';
            }).join('');
            var confirmed = !!selectedPath;
            var cls = confirmed ? '' : (cell.status === 'ambiguous' ? 'needs-review' : 'unmatched');
            var status = confirmed ? ['ok', 'checkCircle', '已确认'] : (cell.status === 'ambiguous' ? ['warn', 'alert', '待确认'] : ['bad', 'xCircle', '未启用']);
            var label = escapeHTML(cell.label || '表格单元格') + '<small style="display:block;color:var(--ds-muted);font-weight:400">' + escapeHTML(cell.address) + '</small>';
            var value = selectedPath ? getPathValue(source, selectedPath) : '';
            return '<tr class="' + cls + '"><td><div class="ds-field-target">' + label + '</div></td><td><select class="ds-source-select" data-cell-address="' + escapeAttr(cell.address) + '" aria-label="' + escapeAttr(cell.label || cell.address) + ' 的数据来源">' + options + '</select></td><td class="ds-example" title="' + escapeAttr(formatValue(value)) + '">' + escapeHTML(formatValue(value) || '—') + '</td><td class="ds-confidence">' + Math.round((cell.confidence || 0) * 100) + '%</td><td><span class="ds-status ' + status[0] + '">' + icon(status[1]) + status[2] + '</span></td></tr>';
        }).join('');
        var table = '<table class="ds-mapping-table"><colgroup><col style="width:19%"><col style="width:28%"><col style="width:24%"><col style="width:12%"><col style="width:17%"></colgroup><thead><tr><th>模板字段</th><th>数据来源</th><th>示例值</th><th>置信度</th><th>状态</th></tr></thead><tbody>' + (rows || '<tr><td colspan="5" style="padding:18px;color:#667085;text-align:center">模板没有需要映射的单值字段</td></tr>') + '</tbody></table>';
        body.innerHTML = table + renderRepeatBlock();
    }

    function renderRepeatBlock() {
        if (!state.repeatRows.length) return '<div class="ds-repeat-block"><div class="ds-repeat-head"><div><h3>明细表行</h3><p>当前未识别到带占位符的重复明细行</p></div></div></div>';
        var source = documentAutomationPreviewSource();
        return state.repeatRows.map(function (plan) {
            var rows = getPathValue(source, plan.data_path) || [];
            var keys = rows.length && isPlainObject(rows[0]) ? Object.keys(rows[0]) : [];
            var chips = ['序号 = $index'].concat(keys.map(function (key) { return key + ' = ' + key; }));
            var help = state.repeatHelpOpen ? '<div class="ds-note" style="margin-top:8px">' + icon('info') + '<span>模板明细行可使用 <b>{{$index}}</b> 和 <b>{{字段名}}</b>。字段名不完全一致时，后端会按 LIMS 常见别名匹配；无占位符的表格可由 Agent 使用 <b>column_keys</b> 声明列规则。</span></div>' : '';
            return '<div class="ds-repeat-block"><div class="ds-repeat-head"><div><h3>明细表行</h3><p>根据 <b>' + escapeHTML(plan.data_path) + '[]</b> 自动重复生成 ' + rows.length + ' 行</p></div><button class="ds-mini-button" type="button" data-ds-action="repeat-help">' + icon('edit') + ' 占位符规则</button></div><div class="ds-repeat-table"><div class="ds-repeat-chips" style="padding:10px">' + chips.map(function (chip) { return '<span class="ds-code-token">' + escapeHTML(chip) + '</span>'; }).join('') + '</div></div>' + help + '</div>';
        }).join('');
    }

    function renderValidation() {
        var node = document.getElementById('dsValidation');
        if (!node) return;
        if (state.generatedVerified) {
            node.className = 'ds-preview-validation';
            node.innerHTML = icon('checkCircle') + '<span>版式结构完整 · 无未填字段</span>';
        } else if (state.outputs.length) {
            node.className = 'ds-preview-validation';
            node.innerHTML = icon('checkCircle') + '<span>已生成预览 · 请核对版式与数据</span>';
        } else if (state.mapping && mappingUnresolved()) {
            node.className = 'ds-preview-validation warn';
            node.innerHTML = icon('alert') + '<span>仍有字段待确认，修正后再生成</span>';
        } else if (state.templateFile && state.mapping) {
            node.className = 'ds-preview-validation';
            node.innerHTML = icon('checkCircle') + '<span>字段映射完整 · 可以生成预览</span>';
        } else if (state.templateFile) {
            node.className = 'ds-preview-validation idle';
            node.innerHTML = icon('info') + '<span>模板版式已载入 · 等待实验数据</span>';
        } else {
            node.className = 'ds-preview-validation idle';
            node.innerHTML = icon('info') + '<span>上传 Word 模板后检查版式</span>';
        }
    }

    function renderActions() {
        var can = canGenerate() && !state.busy;
        var preview = document.getElementById('dsPreviewButton');
        var generate = document.getElementById('dsGenerateButton');
        var download = document.getElementById('dsDownloadButton');
        preview.disabled = !can;
        generate.disabled = !can;
        var count = state.records.length || 1;
        generate.innerHTML = state.busy ? '<span class="ds-button-spinner"></span><span>处理中…</span>' : icon('file') + '<span>' + (count > 1 ? ('生成 ' + count + ' 份报告') : '生成文档') + '</span>';
        download.hidden = state.outputs.length === 0;
        download.innerHTML = icon('download') + (state.outputs.length > 1 ? (' 下载 ' + state.outputs.length + ' 份结果') : ' 下载结果');
    }

    function renderHistory() {
        var popover = document.getElementById('dsHistoryPopover');
        if (!popover) return;
        var items = state.outputs.map(function (file, index) {
            return '<button type="button" class="ds-history-item" data-ds-action="download-output" data-output-index="' + index + '"><b>' + escapeHTML(file.name) + '</b><small>本次生成 · 点击下载</small></button>';
        }).join('');
        var profileItems = state.profiles.slice(0, 5).map(function (profile) {
            return '<div class="ds-history-item"><b>' + escapeHTML(profile.name || profile.template_name) + '</b><small>复用方案 · ' + profile.field_count + ' 个字段 · ' + (profile.cell_count || 0) + ' 个固定单元格 · ' + escapeHTML(formatDate(profile.updated_at)) + '</small></div>';
        }).join('');
        popover.innerHTML = '<div class="ds-history-head"><strong>最近生成与方案</strong><span style="font-size:calc(11px * var(--macs-font-scale, 1));color:var(--ds-muted)">' + (state.outputs.length + state.profiles.length) + ' 项</span></div>' + (items + profileItems || '<div class="ds-history-empty">还没有生成记录</div>');
    }

    function toggleHistory() {
        var popover = document.getElementById('dsHistoryPopover');
        popover.hidden = !popover.hidden;
    }

    function canGenerate() {
        return !!(state.templateUpload && state.templateUpload.path && documentAutomationPreviewSource() && state.mapping && !mappingUnresolved());
    }

    function mappingUnresolved() {
        if (!state.mapping) return true;
        return (state.mapping.unmatched_fields || []).length + (state.mapping.ambiguous_fields || []).length > 0;
    }

    function countTemplateFields() {
        if (!state.inspection) return 0;
        return (state.inspection.placeholders || []).length + (state.inspection.content_controls || []).length + (state.inspection.underlined_fields || []).length;
    }

    function dataSummary() {
        if (state.records.length) return state.records.length + ' 份报告数据';
        var arrays = findObjectArrays(state.sourceData || {});
        return arrays.length ? arrays[0].rows.length + ' 条明细数据' : (flattenScalarPaths(state.sourceData || {}).length + ' 个数据字段');
    }

    function profileDisplayName() {
        if (state.matchedProfile) return state.matchedProfile.name || state.matchedProfile.template_name;
        return state.templateFile ? state.templateFile.name.replace(/\.docx$/i, '') : '企业文档模板';
    }

    function outputBaseName() {
        return (state.templateFile ? state.templateFile.name.replace(/\.docx$/i, '') : '自动化文档') + '-已填写';
    }

    function setBusy(busy, label) {
        state.busy = busy;
        if (label) setSaveState(label, true);
        renderActions();
    }

    function setSaveState(text, loading) {
        var node = document.getElementById('dsSaveState');
        if (!node) return;
        node.innerHTML = loading ? '<span class="ds-button-spinner" style="border-color:#ccd8ff;border-top-color:#3157d5"></span><span>' + escapeHTML(text) + '</span>' : icon('checkCircle') + '<span>' + escapeHTML(text) + '</span>';
    }

    function setZoom(value) {
        state.zoom = value || 90;
        var paper = document.getElementById('dsPaper');
        if (paper) {
            paper.style.transform = 'scale(' + (state.zoom / 100) + ')';
            paper.style.marginBottom = '-' + Math.round(paper.offsetHeight * (1 - state.zoom / 100)) + 'px';
        }
    }

    function downloadOutputs() {
        state.outputs.forEach(function (_, index) { setTimeout(function () { downloadOutput(index); }, index * 180); });
    }

    function downloadOutput(index) {
        var file = state.outputs[index];
        if (!file) return;
        var link = document.createElement('a');
        link.href = file.download_url;
        link.download = file.name;
        document.body.appendChild(link);
        link.click();
        link.remove();
    }

    function paperEmpty(title, detail) {
        return '<div class="ds-paper-empty"><div><div>' + icon('file') + '</div><strong>' + escapeHTML(title) + '</strong><div style="margin-top:5px">' + escapeHTML(detail) + '</div></div></div>';
    }

    function formatValue(value) {
        if (value === undefined || value === null) return '';
        if (typeof value === 'object') return JSON.stringify(value);
        return String(value);
    }

    function formatDate(value) {
        if (!value) return '未记录时间';
        var date = new Date(value);
        return isNaN(date.getTime()) ? value : date.toLocaleDateString('zh-CN');
    }

    function isPlainObject(value) {
        return value !== null && typeof value === 'object' && !Array.isArray(value);
    }

    function escapeHTML(value) {
        return String(value === undefined || value === null ? '' : value).replace(/[&<>"']/g, function (char) {
            return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char];
        });
    }

    function escapeAttr(value) { return escapeHTML(value); }

    function showToast(message, error) {
        var toast = document.getElementById('dsToast');
        if (!toast) return;
        clearTimeout(state.toastTimer);
        toast.textContent = message;
        toast.className = 'ds-toast show' + (error ? ' error' : '');
        state.toastTimer = setTimeout(function () { toast.classList.remove('show'); }, 3200);
    }

    window.openDocumentAutomationStudio = openStudio;
    window.closeDocumentAutomationStudio = closeStudio;

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', injectStudio);
    else injectStudio();
}());
