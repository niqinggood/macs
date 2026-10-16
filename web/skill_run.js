(function () {
    'use strict';

    var catalog = [];
    var catalogLoaded = false;
    var catalogLoading = false;
    var configuredSkill = null;
    var selectedRequest = null;
    var selectedDisplayName = '';
    var autoFilledPrompt = '';

    function byId(id) { return document.getElementById(id); }
    function text(value) { return value == null ? '' : String(value); }
    function html(value) {
        return text(value).replace(/&/g, '&amp;').replace(/</g, '&lt;')
            .replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }
    function clone(value) {
        if (value == null) return value;
        return JSON.parse(JSON.stringify(value));
    }
    function skillDisplayName(skill) {
        return text(skill && skill.display_name).trim() || text(skill && skill.name).trim();
    }
    function skillDescription(skill) {
        return text(skill && skill.short_description).trim() || text(skill && skill.description).trim();
    }
    function dispatchMessageInput(input) {
        if (input) input.dispatchEvent(new Event('input', {bubbles: true}));
    }
    function applyDefaultPrompt(skill) {
        var input = byId('messageInput');
        if (!input) return;
        var prompt = text(skill && skill.default_prompt).trim();
        var ownsCurrentValue = !!autoFilledPrompt && input.value === autoFilledPrompt;
        if (input.value.trim() && !ownsCurrentValue) {
            autoFilledPrompt = '';
            return;
        }
        if (input.value === prompt) {
            autoFilledPrompt = prompt;
            return;
        }
        input.value = prompt;
        autoFilledPrompt = prompt;
        dispatchMessageInput(input);
    }
    function clearAutoFilledPrompt() {
        var input = byId('messageInput');
        if (input && autoFilledPrompt && input.value === autoFilledPrompt) {
            input.value = '';
            dispatchMessageInput(input);
        }
        autoFilledPrompt = '';
    }
    function redrawIcons() {
        if (window.lucide && typeof window.lucide.createIcons === 'function') {
            window.lucide.createIcons();
        }
    }
    function showError(message) {
        var error = byId('skillRunParameterError');
        if (!error) return;
        error.textContent = message || '';
        error.hidden = !message;
    }

    function setOpen(open) {
        var popover = byId('skillRunPopover');
        var trigger = byId('skillRunBtn');
        if (!popover || !trigger) return;
        popover.hidden = !open;
        trigger.setAttribute('aria-expanded', open ? 'true' : 'false');
        if (open) {
            showListView();
            loadCatalog();
            window.setTimeout(function () {
                var search = byId('skillRunSearch');
                if (search) search.focus();
            }, 0);
        }
    }

    function showListView() {
        var listView = byId('skillRunListView');
        var configureView = byId('skillRunConfigureView');
        if (listView) listView.hidden = false;
        if (configureView) configureView.hidden = true;
        configuredSkill = null;
        showError('');
    }

    function sourceLabel(source) {
        var normalized = text(source).toLowerCase();
        if (normalized === 'builtin') return '内置';
        if (normalized === 'user') return '本地';
        if (normalized === 'tencent') return 'SkillHub';
        if (normalized === 'clawhub') return 'ClawHub';
        return source || '已安装';
    }

    function renderCatalog() {
        var container = byId('skillRunList');
        if (!container) return;
        var query = text(byId('skillRunSearch') && byId('skillRunSearch').value).trim().toLowerCase();
        var filtered = catalog.filter(function (skill) {
            if (!query) return true;
            return [skill.name, skill.display_name, skill.description, skill.short_description, skill.author, skill.source, skill.type]
                .some(function (value) { return text(value).toLowerCase().indexOf(query) >= 0; });
        });
        if (catalogLoading) {
            container.innerHTML = '<div class="skill-run-empty">正在读取已安装的 Skill…</div>';
            return;
        }
        if (!filtered.length) {
            container.innerHTML = '<div class="skill-run-empty">' +
                (catalogLoaded ? '没有匹配的 Skill' : 'Skill 列表暂时不可用') + '</div>';
            return;
        }
        container.innerHTML = filtered.map(function (skill) {
            var version = text(skill.version).trim();
            var displayName = skillDisplayName(skill);
            var description = skillDescription(skill) || '此 Skill 未提供简介';
            var canonicalName = displayName !== text(skill.name).trim() ? ' · ' + text(skill.name).trim() : '';
            return '<button type="button" class="skill-run-item" role="option" aria-selected="' +
                (!!selectedRequest && selectedRequest.skill_name === skill.name) + '" title="' + html(displayName + '\n' + description) +
                '" data-skill-name="' + html(skill.name) + '">' +
                '<span class="skill-run-item-top"><span class="skill-run-item-name">' + html(displayName) + '</span>' +
                (version ? '<span class="skill-run-version">v' + html(version) + '</span>' : '') + '</span>' +
                '<span class="skill-run-item-description">' + html(description) + '</span>' +
                '<span class="skill-run-item-meta">' + html(sourceLabel(skill.source) + canonicalName) +
                (skill.requires_runtime ? ' · 带运行资源' : '') + '</span></button>';
        }).join('');
        Array.prototype.forEach.call(container.querySelectorAll('[data-skill-name]'), function (button) {
            button.addEventListener('click', function () {
                var name = button.getAttribute('data-skill-name');
                var skill = catalog.find(function (candidate) { return candidate.name === name; });
                if (skill) showConfigureView(skill);
            });
        });
    }

    async function loadCatalog() {
        if (catalogLoading) return;
        if (catalogLoaded) {
            renderCatalog();
            return;
        }
        catalogLoading = true;
        renderCatalog();
        try {
            var response = await fetch('/api/skills', {cache: 'no-store'});
            var payload = await response.json().catch(function () { return {}; });
            if (!response.ok || payload.ok === false) throw new Error(payload.error || ('HTTP ' + response.status));
            catalog = Array.isArray(payload.skills) ? payload.skills.slice() : [];
            catalog.sort(function (left, right) { return skillDisplayName(left).localeCompare(skillDisplayName(right)); });
            catalogLoaded = true;
        } catch (error) {
            catalog = [];
            catalogLoaded = false;
            if (typeof window.showToast === 'function') {
                window.showToast('读取 Skill 列表失败：' + (error.message || error), 'error');
            }
        } finally {
            catalogLoading = false;
            renderCatalog();
        }
    }

    function parameterInput(definition, index) {
        var name = text(definition.name).trim();
        var type = text(definition.type).trim().toLowerCase() || 'string';
        var id = 'skillRunParameter_' + index;
        var description = text(definition.description).trim();
        var value = definition.default;
        var label = '<label class="skill-run-parameter-label" for="' + id + '">' + html(name) +
            (definition.required ? '<span class="skill-run-required">必填</span>' : '') + '</label>';
        var hint = description ? '<div class="skill-run-parameter-hint">' + html(description) + '</div>' : '';
        if (type === 'bool' || type === 'boolean') {
            return '<div class="skill-run-parameter" data-param-index="' + index + '">' + label +
                '<select id="' + id + '" data-param-type="bool"><option value="">未设置</option>' +
                '<option value="true"' + (value === true ? ' selected' : '') + '>是</option>' +
                '<option value="false"' + (value === false ? ' selected' : '') + '>否</option></select>' + hint + '</div>';
        }
        var inputType = (type === 'int' || type === 'integer' || type === 'number' || type === 'float' || type === 'double') ? 'number' : 'text';
        var step = (type === 'int' || type === 'integer') ? '1' : 'any';
        var display = value == null ? '' : ((type === 'array' || type === 'list' || type === 'object' || type === 'map') ? JSON.stringify(value) : text(value));
        var placeholder = (type === 'array' || type === 'list') ? '["item"]' : ((type === 'object' || type === 'map') ? '{"key":"value"}' : '');
        return '<div class="skill-run-parameter" data-param-index="' + index + '">' + label +
            '<input id="' + id + '" type="' + inputType + '" step="' + step + '" value="' + html(display) + '" placeholder="' + html(placeholder) + '" data-param-type="' + html(type) + '">' + hint + '</div>';
    }

    function showConfigureView(skill) {
        configuredSkill = skill;
        var listView = byId('skillRunListView');
        var configureView = byId('skillRunConfigureView');
        if (listView) listView.hidden = true;
        if (configureView) configureView.hidden = false;
        byId('skillRunConfigureTitle').textContent = skillDisplayName(skill) + (skill.version ? ' · v' + skill.version : '');
        byId('skillRunConfigureDescription').textContent = skillDescription(skill) || '确认后，此 Skill 将只用于下一次发送。';
        var definitions = Array.isArray(skill.parameters) ? skill.parameters : [];
        byId('skillRunParameterFields').innerHTML = definitions.length
            ? definitions.map(parameterInput).join('')
            : '<div class="skill-run-no-parameters">此 Skill 不需要额外参数。</div>';
        showError('');
        redrawIcons();
    }

    function collectParameters() {
        var definitions = configuredSkill && Array.isArray(configuredSkill.parameters) ? configuredSkill.parameters : [];
        var parameters = {};
        definitions.forEach(function (definition, index) {
            var name = text(definition.name).trim();
            if (!name) return;
            var control = byId('skillRunParameter_' + index);
            var raw = control ? text(control.value).trim() : '';
            var type = text(definition.type).trim().toLowerCase() || 'string';
            if (raw === '') {
                if (definition.required && definition.default == null) throw new Error(name + ' 是必填参数');
                if (definition.default != null) parameters[name] = clone(definition.default);
                return;
            }
            if (type === 'bool' || type === 'boolean') parameters[name] = raw === 'true';
            else if (type === 'int' || type === 'integer') {
                var integer = Number(raw);
                if (!Number.isInteger(integer)) throw new Error(name + ' 必须是整数');
                parameters[name] = integer;
            } else if (type === 'number' || type === 'float' || type === 'double') {
                var number = Number(raw);
                if (!Number.isFinite(number)) throw new Error(name + ' 必须是数字');
                parameters[name] = number;
            } else if (type === 'array' || type === 'list' || type === 'object' || type === 'map') {
                try { parameters[name] = JSON.parse(raw); }
                catch (_) { throw new Error(name + ' 必须是有效 JSON'); }
                if ((type === 'array' || type === 'list') && !Array.isArray(parameters[name])) throw new Error(name + ' 必须是 JSON 数组');
                if ((type === 'object' || type === 'map') && (Array.isArray(parameters[name]) || !parameters[name] || typeof parameters[name] !== 'object')) throw new Error(name + ' 必须是 JSON 对象');
            } else parameters[name] = raw;
        });
        return parameters;
    }

    function updateSelection() {
        var wrapper = byId('skillRunSelection');
        var name = byId('skillRunSelectionName');
        var trigger = byId('skillRunBtn');
        if (!wrapper || !name || !trigger) return;
        wrapper.hidden = !selectedRequest;
        trigger.classList.toggle('is-selected', !!selectedRequest);
        if (selectedRequest) {
            name.textContent = (selectedDisplayName || selectedRequest.skill_name) + (selectedRequest.version ? ' · v' + selectedRequest.version : '');
        } else name.textContent = '';
    }

    function chooseConfiguredSkill() {
        if (!configuredSkill) return;
        try {
            selectedRequest = {
                skill_name: configuredSkill.name,
                parameters: collectParameters()
            };
            selectedDisplayName = skillDisplayName(configuredSkill);
            if (configuredSkill.version) selectedRequest.version = configuredSkill.version;
            applyDefaultPrompt(configuredSkill);
            updateSelection();
            setOpen(false);
            var input = byId('messageInput');
            if (input) input.focus();
        } catch (error) {
            showError(error.message || String(error));
        }
    }

    function clearSelection() {
        selectedRequest = null;
        selectedDisplayName = '';
        clearAutoFilledPrompt();
        updateSelection();
    }

    function init() {
        var trigger = byId('skillRunBtn');
        if (!trigger || trigger.dataset.ready === 'true') return;
        trigger.dataset.ready = 'true';
        trigger.addEventListener('click', function (event) {
            event.preventDefault();
            event.stopPropagation();
            setOpen(byId('skillRunPopover').hidden);
        });
        byId('skillRunCloseBtn').addEventListener('click', function () { setOpen(false); });
        byId('skillRunBackBtn').addEventListener('click', showListView);
        byId('skillRunUseBtn').addEventListener('click', chooseConfiguredSkill);
        byId('skillRunClearBtn').addEventListener('click', clearSelection);
        byId('skillRunSearch').addEventListener('input', renderCatalog);
        var messageInput = byId('messageInput');
        if (messageInput) {
            messageInput.addEventListener('input', function () {
                if (autoFilledPrompt && messageInput.value !== autoFilledPrompt) autoFilledPrompt = '';
            });
        }
        document.addEventListener('click', function (event) {
            var popover = byId('skillRunPopover');
            if (!popover || popover.hidden) return;
            if (!popover.contains(event.target) && !trigger.contains(event.target)) setOpen(false);
        });
        document.addEventListener('keydown', function (event) {
            if (event.key === 'Escape' && !byId('skillRunPopover').hidden) setOpen(false);
        });
        updateSelection();
        redrawIcons();
    }

    window.getSelectedSkillRunRequest = function () { return clone(selectedRequest); };
    window.clearSelectedSkillRun = clearSelection;
    window.openSkillRunPicker = function () { setOpen(true); };
    document.addEventListener('DOMContentLoaded', init);
})();
